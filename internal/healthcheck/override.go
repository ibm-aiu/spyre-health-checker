/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package healthcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/ibm-aiu/spyre-health-checker/internal/reporter"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// Values that an entry can force on a device.
const (
	ModeHealthy   = "healthy"   // the device is reported ONLINE
	ModeUnhealthy = "unhealthy" // the device is reported IN_ERROR

	// OverrideSource is the DeviceState.Source of a device whose state was forced by the override.
	OverrideSource = "override"

	// targetAll is the word that makes a line apply to every device of the node.
	targetAll = "all"

	// maxProblems limits the warnings logged for one entry, so a large wrong file cannot flood the log.
	maxProblems = 10
)

// forcedState is the state that each value forces on a device.
var forcedState = map[string]pb.DEVICE_STATE{
	ModeHealthy:   pb.DEVICE_STATE_ONLINE,
	ModeUnhealthy: pb.DEVICE_STATE_IN_ERROR,
}

// pciAddress is the full form of a PCI address, which is the form that the reporters publish. It is
// matched against a lower-case word.
var pciAddress = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)

// NodeEntry is the parsed override of one node. The zero value means "no override".
type NodeEntry struct {
	// All is ModeHealthy or ModeUnhealthy when the entry covers every device of the node, else "".
	All string
	// Devices maps the lower-case PCI address of a PF to ModeHealthy or ModeUnhealthy. A line covers the PF
	// and its VFs, and wins over All. A line for a VF is refused when the entry is applied.
	Devices map[string]string
	// Problems are warnings about the entry (at most maxProblems), and Ignored counts the lines that
	// were not used. The valid lines of an entry still apply.
	Problems []string
	Ignored  int
}

func (e NodeEntry) active() bool { return e.All != "" || len(e.Devices) > 0 }

// describe returns the effect of the entry in words, for the log: "none" or, for example,
// "every device -> ONLINE, 0000:41:00.0 and its VFs -> IN_ERROR".
func (e NodeEntry) describe() string {
	var parts []string
	if forced, ok := forcedState[e.All]; ok {
		parts = append(parts, "every device -> "+forced.String())
	}
	for _, a := range slices.Sorted(maps.Keys(e.Devices)) {
		parts = append(parts, a+" and its VFs -> "+forcedState[e.Devices[a]].String())
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// ignore records a line that is not used.
func (e *NodeEntry) ignore(format string, args ...any) {
	e.Ignored++
	switch {
	case len(e.Problems) < maxProblems:
		e.Problems = append(e.Problems, fmt.Sprintf(format, args...))
	case len(e.Problems) == maxProblems:
		e.Problems = append(e.Problems, "node health override: more lines were ignored, not listed here")
	}
}

// parseEntry reads the text of an entry. See enhancements/node-health-override.md for the format:
//
//	healthy                    the whole node (short form of "all healthy")
//	all unhealthy              the whole node
//	0000:1a:00.0 unhealthy     one PF and its VFs; it wins over the line for the whole node
//	# a comment               blank lines and comments are ignored
//
// A line that cannot be used is skipped and reported; the other lines still apply. When a target
// appears twice, the last line wins. Words are not case-sensitive.
func parseEntry(text string) NodeEntry {
	var e NodeEntry
	text = strings.TrimPrefix(text, "\ufeff") // a file saved by some Windows editors starts with a byte-order mark
	for i, raw := range strings.Split(text, "\n") {
		line, _, _ := strings.Cut(raw, "#")
		fields := strings.Fields(line)

		var target, value string
		switch len(fields) {
		case 0:
			continue
		case 1:
			target, value = targetAll, strings.ToLower(fields[0])
		case 2:
			target, value = strings.ToLower(fields[0]), strings.ToLower(fields[1])
		}

		_, validValue := forcedState[value] // false for a line with more than two words, too
		switch {
		case !validValue:
			e.ignore("node health override: line %d: ignoring %.40q: want \"healthy\", \"unhealthy\", "+
				"\"all <value>\" or \"<address> <value>\", where value is healthy or unhealthy", i+1, strings.TrimSpace(line))
		case target == targetAll:
			e.All = value
		case pciAddress.MatchString(target):
			if e.Devices == nil {
				e.Devices = make(map[string]string)
			}
			e.Devices[target] = value
		default:
			e.ignore("node health override: line %d: ignoring %.40q: %.40q is not a full PCI address "+
				"such as 0000:1a:00.0", i+1, strings.TrimSpace(line), fields[0])
		}
	}
	return e
}

// Override reads the node health override: the file <dir>/<node name>, which is the entry of this
// node in the optional ConfigMap spyre-health-override mounted at dir. See
// enhancements/node-health-override.md.
//
// A missing file or directory is the normal case and means "no override".
type Override struct {
	dir  string
	node string
	log  *zap.SugaredLogger

	mu     sync.Mutex
	last   NodeEntry       // entry returned by the previous Read
	warned map[string]bool // problems logged by the previous Apply, so that each is logged once
}

// NewOverride returns an Override reading <dir>/<node>. An empty dir disables the feature.
func NewOverride(dir, node string) *Override {
	return &Override{dir: dir, node: node, log: zap.NewNop().Sugar()}
}

// SetLogger attaches a logger; the default logs nothing.
func (o *Override) SetLogger(l *zap.SugaredLogger) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if l == nil {
		l = zap.NewNop().Sugar()
	}
	o.log = l
}

// validNodeName rejects names that could point outside the directory.
func validNodeName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsRune(n, '/')
}

// Read returns the current entry of the node.
//
//   - A missing file or directory ends any override (the entry or the ConfigMap was removed).
//   - An empty entry, or one with only comments, is no override.
//   - A line that cannot be used is reported in Problems and skipped; the other lines still apply.
//   - Any other read error keeps the previous entry, so that a transient error does not flip the devices.
//
// The start, change and end of an override are logged at warning level, once each. The caller must not
// change the returned NodeEntry.
func (o *Override) Read() NodeEntry {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.dir == "" {
		return NodeEntry{}
	}
	if !validNodeName(o.node) {
		return NodeEntry{Problems: []string{
			fmt.Sprintf("node health override disabled: unusable node name %.40q (is NODE_NAME set?)", o.node)}}
	}

	data, err := os.ReadFile(filepath.Join(o.dir, o.node))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return o.set(NodeEntry{})
	case err != nil:
		e := o.last
		e.Problems = append(slices.Clone(e.Problems), fmt.Sprintf(
			"node health override: cannot read the entry of node %.40q, keeping %q: %v", o.node, e.describe(), err))
		return e
	}
	return o.set(parseEntry(string(data)))
}

// set records e as the current entry and logs the start, change or end of an override.
// The caller holds o.mu.
func (o *Override) set(e NodeEntry) NodeEntry {
	was, now := o.last.describe(), e.describe()
	switch {
	case was == now:
	case e.active():
		o.log.Warnf("node health override ACTIVE for node %.40q: %s (was: %s)", o.node, now, was)
	default:
		o.log.Warnf("node health override for node %.40q no longer applies (was: %s)", o.node, was)
	}
	o.last = e
	return e
}

// OverrideSummary is what the override did in the last check. It feeds the spyre_health_override metric.
type OverrideSummary struct {
	Healthy   int // devices forced to ONLINE
	Unhealthy int // devices forced to IN_ERROR
	Ignored   int // lines of the entry that were not applied
}

// Apply reads the entry and forces the state of the devices it names, then warns once about each line
// that could not be applied. It changes states in place. driverFailed holds the devices whose kernel
// driver binding is missing; see applyOverride.
func (o *Override) Apply(states []types.DeviceState, driverFailed map[string]bool) OverrideSummary {
	e := o.Read()
	res := applyOverride(states, e, driverFailed)
	o.warnOnce(slices.Concat(e.Problems, res.problems))
	return OverrideSummary{Healthy: res.healthy, Unhealthy: res.unhealthy, Ignored: e.Ignored + len(res.problems)}
}

// warnOnce logs each message that was not in the previous call. A problem that goes away and comes back
// is logged again.
func (o *Override) warnOnce(messages []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := make(map[string]bool, len(messages))
	for _, m := range messages {
		now[m] = true
		if !o.warned[m] {
			o.log.Warn(m)
		}
	}
	o.warned = now
}

// applyResult is what applyOverride did.
type applyResult struct {
	healthy, unhealthy int      // devices forced to ONLINE and to IN_ERROR
	problems           []string // lines for devices that could not be forced
}

// modeFor returns the value in effect for a device, and the address of the card line that decided it
// (empty when the whole-node line or nothing applies).
//
// A device follows the line of its card, which is function 0 of its slot (reporter.PFAddressOf). Any
// other function is a VF of that card, whatever type a reporter gave it: the RAS reporter types every
// address it records as a PF. A line for a VF itself is never used, and a VF at function 0 has no PF to
// name (an isolated VF), so only the whole-node line reaches it.
func (e NodeEntry) modeFor(s types.DeviceState) (mode, line string) {
	address := strings.ToLower(s.PciAddress)
	card := reporter.PFAddressOf(address)
	if card == address && s.Type == pb.DEVICE_TYPE_VF {
		return e.All, ""
	}
	if m, ok := e.Devices[card]; ok {
		return m, card
	}
	return e.All, ""
}

// applyOverride forces the state of the devices named by the entry and marks each with Source "override".
// A card line beats the whole-node line (see modeFor for which devices a line reaches).
//
// driverFailed holds the devices whose kernel driver binding is missing. A device forced to healthy that
// is in this set is left as it is: an override cannot hide a card that cannot run work. This is reported
// when a card line caused it, and passes quietly under a whole-node line. The override never adds a
// device. A line that reached no device is reported in problems: it names a VF, or a card that no reporter
// returned.
func applyOverride(states []types.DeviceState, e NodeEntry, driverFailed map[string]bool) applyResult {
	var res applyResult
	if !e.active() {
		return res
	}
	reported := make(map[string]bool, len(states)) // every reported address
	reached := map[string]bool{}                   // card lines that decided a device

	for i := range states {
		reported[strings.ToLower(states[i].PciAddress)] = true

		mode, line := e.modeFor(states[i])
		if line != "" {
			reached[line] = true
		}
		forced, ok := forcedState[mode]
		if !ok {
			continue
		}
		if mode == ModeHealthy && driverFailed[states[i].PciAddress] {
			if line != "" {
				res.problems = append(res.problems, fmt.Sprintf("node health override: device %s has no driver, "+
					"so \"healthy\" is not applied to it", strings.ToLower(states[i].PciAddress)))
			}
			continue
		}
		states[i].State = forced
		states[i].Source = OverrideSource
		if mode == ModeHealthy {
			res.healthy++
		} else {
			res.unhealthy++
		}
	}

	for address := range e.Devices {
		if reached[address] {
			continue
		}
		// A line for a reported address that no device followed can only name a VF: a PF is reached by
		// its own address.
		switch pf := reporter.PFAddressOf(address); {
		case !reported[address]:
			res.problems = append(res.problems, fmt.Sprintf("node health override: device %s is not reported "+
				"on this node, ignoring its line", address))
		case pf != address:
			res.problems = append(res.problems, fmt.Sprintf("node health override: device %s is a VF, so its "+
				"line is ignored: name its PF %s instead", address, pf))
		default:
			res.problems = append(res.problems, fmt.Sprintf("node health override: device %s is a VF without "+
				"a PF, so its line is ignored: use a line for the whole node", address))
		}
	}
	slices.Sort(res.problems) // a stable order, so that the log does not change from one check to the next
	return res
}
