/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// PFVDID and VFVDID are the PCI vendor:device IDs for IBM Spyre PF and VF
// devices. They are kept here for reference by tests.
const (
	PFVDID = "1014:06a7"
	VFVDID = "1014:06a8"
)

// LSPCIReporter collects device states by running `lspci -vvvnn` and parsing
// the output. It carries the lowest priority (PriorityLSPCI = 1).
type LSPCIReporter struct{}

func (r *LSPCIReporter) Name() string  { return "lspci" }
func (r *LSPCIReporter) Priority() int { return types.PriorityLSPCI }

// Collect executes lspci, parses the output, and stamps each entry with the
// lspci source name and priority.
func (r *LSPCIReporter) Collect() ([]types.DeviceState, error) {
	// Fail early with a clear error if lspci is not on PATH.
	if _, err := exec.LookPath("lspci"); err != nil {
		return nil, fmt.Errorf("lspci reporter: lspci not found (%s): install lspci & pciutils: %w", pathEnv(), err)
	}

	// stdout and stderr are kept apart: a stderr warning merged into the output would land in front of the first
	// device block and hide that device.
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("lspci", "-vvvnn")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("lspci reporter: lspci -vvvnn failed: %w\noutput: %s\nstderr: %s",
			err, stdout.String(), stderr.String())
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" && firstTime("stderr") {
		lspciDebugLog("lspci wrote to stderr (not parsed): %q", clip(msg))
	}

	states, skips := parseLSPCIWithSkips(stdout.String())
	for _, reason := range slices.Sorted(maps.Keys(skips)) {
		if firstTime(reason) {
			lspciDebugLog("skipped a block (%s), first example: %q", reason, clip(skips[reason].example))
		}
	}
	detail := skipSummary(skips)
	if len(states) == 0 {
		// Warn: distinguishes "no cards" from a format change.
		lspciDebugLog("lspci succeeded but found no IBM Spyre devices in output (%d bytes)%s", stdout.Len(), detail)
	} else {
		lspciDebugLog("lspci found %d Spyre device(s)%s", len(states), detail) // detail starts with a comma or is null
	}

	stamp(states, r.Name(), r.Priority())
	return states, nil
}

// pathEnv returns the current PATH for use in error messages.
func pathEnv() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "(PATH not set)"
}

// lspciDebugLog is a package-level hook for diagnostic output; override in tests.
var lspciDebugLog = func(format string, args ...any) {
	fmt.Printf("lspci-reporter: "+format+"\n", args...)
}

// Spyre blocks that do not become a device are counted per reason (summary in the log line of a run); the first
// example of each reason is logged once.
const (
	skipNoHeader = "no-header" // mentions Spyre, but has no parsable device header
	skipRev01    = "rev-01"    // Spyre device with revision 01, which is ignored
)

type skipInfo struct {
	count   int
	example string
}

// skipSummary returns ", skipped N (reason: n, ...)", or "" when nothing was skipped.
func skipSummary(skips map[string]skipInfo) string {
	if len(skips) == 0 {
		return ""
	}
	total := 0
	parts := make([]string, 0, len(skips))
	for _, reason := range slices.Sorted(maps.Keys(skips)) {
		total += skips[reason].count
		parts = append(parts, fmt.Sprintf("%s: %d", reason, skips[reason].count))
	}
	return fmt.Sprintf(", skipped %d (%s)", total, strings.Join(parts, ", "))
}

// lspciLogged remembers which conditions were logged, so a recurring one appears once per process.
var lspciLogged sync.Map

func firstTime(condition string) bool {
	_, seen := lspciLogged.LoadOrStore(condition, struct{}{})
	return !seen
}

const maxLogText = 160 // longest piece of lspci output copied into a log line

func clip(s string) string {
	if len(s) <= maxLogText {
		return s
	}
	return s[:maxLogText] + "..."
}

// ---------------------------------------------------------------------------
// lspci output parser -- private to this package
// ---------------------------------------------------------------------------

type bitValue struct {
	Present bool // token is present or not
	Enabled bool // '+' means true, '-' means false
}

type deviceInfo struct {
	// From header
	PCIAddress string // supports "xx:xx.x" and "xxxx:xx:xx.x"
	VenDevID   string // ####:#### hex value
	Revision   string // ## hex value

	// From body
	PERR         bitValue
	SERR         bitValue
	TAbort       bitValue
	MAbort       bitValue
	DState       string // looking for "D0"
	DevStaFatal  bool   // true if DevSta contains "FatalErr+"
	KernelDriver string
}

// Splitter: lspci device stanzas delimited by blank lines

func splitByBlankLines(s string) []string {
	sep := regexp.MustCompile(`(?m)(?:\r?\n[ \t]*)+\r?\n`)
	parts := sep.Split(s, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(strings.TrimSpace(p)) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// Parse header

var (
	// PCI Pattern
	pciPat = `(?i)(?P<pci>(?:[0-9a-f]{4}:)?[0-9a-f]{2}:[0-9a-f]{2}\.[0-7])`

	// Sample Header styles
	// 63:00.0 Processing accelerators [1200]: IBM Device [1014:06a7] (rev 02)
	// 0483:70:00.0 Processing accelerators [1200]: IBM Spyre Accelerator [1014:06a7] (rev 02)
	// 2b:00.0 1200: 1014:06a7 (rev 02)
	hdr = regexp.MustCompile(`(?i)^` + pciPat +
		`(?P<stuff>.*?)\s+\[?(?P<vendev>[0-9a-f]{4}:[0-9a-f]{4})\]?\s+\(rev\s+(?P<rev>[0-9a-f]{2})\)\s*$`)
)

func parseHeader(firstLine string) (pci, vendev, rev string, ok bool) {
	if m := hdr.FindStringSubmatch(firstLine); len(m) > 0 {
		names := hdr.SubexpNames()
		mp := map[string]string{}
		for i := range m {
			if names[i] != "" {
				mp[names[i]] = m[i]
			}
		}
		return mp["pci"], mp["vendev"], mp["rev"], true
	}
	return "", "", "", false
}

// Parse body

var (
	// Matches tokens like ">SERR+" "<PERR-" "TAbort+" "MAbort-"
	errTokenRe = regexp.MustCompile(`(?i)(?:[<>])?(PERR|SERR|TAbort|MAbort)([+-])`)

	// DevSta line: look for FatalErr+ or FatalErr-
	fatalRe = regexp.MustCompile(`(?i)\bFatalErr([+-])`)

	// Power Mgmt status line: "Status: D0 ..."
	pmStatusRe = regexp.MustCompile(`(?i)\bStatus:\s*(D\d+?)\b`)

	// Kernel driver line
	driverRe = regexp.MustCompile(`(?i)^\s*Kernel driver in use:\s*([^\s]+)`)
)

func parseDetails(stanza string, di *deviceInfo) {
	sc := bufio.NewScanner(bytes.NewReader([]byte(stanza)))
	for sc.Scan() {
		line := sc.Text()

		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "status:") {
			for _, sm := range errTokenRe.FindAllStringSubmatch(line, -1) {
				name := strings.ToUpper(sm[1])
				sign := sm[2] == "+"
				switch name {
				case "PERR":
					di.PERR.Present = true
					di.PERR.Enabled = di.PERR.Enabled || sign // OR across duplicates
				case "SERR":
					di.SERR.Present = true
					di.SERR.Enabled = di.SERR.Enabled || sign
				case "TABORT":
					di.TAbort.Present = true
					di.TAbort.Enabled = di.TAbort.Enabled || sign
				case "MABORT":
					di.MAbort.Present = true
					di.MAbort.Enabled = di.MAbort.Enabled || sign
				}
			}
			if di.DState == "" {
				if m := pmStatusRe.FindStringSubmatch(line); len(m) == 2 {
					di.DState = m[1]
				}
			}
		}

		// DevSta fatal
		if strings.Contains(strings.ToLower(line), "devsta:") {
			if m := fatalRe.FindStringSubmatch(line); len(m) == 2 {
				di.DevStaFatal = (m[1] == "+")
			}
		}

		// Kernel driver in use
		if m := driverRe.FindStringSubmatch(line); len(m) == 2 {
			di.KernelDriver = strings.TrimSpace(m[1])
		}
	}
}

// Parse device stanza

type lspciHeader struct{ line, pci, vendev, rev string }

// findHeader returns the first column-0 line of a block that parses as a device header. Text in front of it
// (for example stderr merged into the output) is ignored, so it cannot hide the device.
func findHeader(stanza string) (lspciHeader, bool) {
	for _, line := range strings.Split(stanza, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if pci, vendev, rev, ok := parseHeader(line); ok {
			return lspciHeader{line: line, pci: pci, vendev: vendev, rev: rev}, true
		}
	}
	return lspciHeader{}, false
}

// mentionsSpyre reports whether text names a Spyre device or one of its PCI IDs.
func mentionsSpyre(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "spyre") || strings.Contains(l, pfVDID) || strings.Contains(l, vfVDID)
}

// parseDeviceStanza parses one block. A Spyre block that is skipped comes back with its reason and an example line.
func parseDeviceStanza(stanza string) (di deviceInfo, skip, example string) {
	head, ok := findHeader(stanza)
	if !ok {
		if mentionsSpyre(stanza) {
			return di, skipNoHeader, firstNonEmptyLine(stanza)
		}
		return di, "", ""
	}
	if head.rev == "01" { // revision 01 devices have always been ignored
		if mentionsSpyre(head.line) {
			return di, skipRev01, head.line
		}
		return di, "", ""
	}
	if len(head.pci) == 7 {
		di.PCIAddress = "0000:" + head.pci
	} else {
		di.PCIAddress = head.pci
	}
	di.VenDevID = strings.ToLower(head.vendev)
	di.Revision = strings.ToLower(head.rev)

	parseDetails(stanza, &di)
	return di, "", ""
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if len(strings.TrimSpace(line)) > 0 {
			return strings.TrimRight(line, "\r")
		}
	}
	return ""
}

const (
	pfVDID = "1014:06a7"
	vfVDID = "1014:06a8"
)

func parseLSPCI(output string) []types.DeviceState {
	states, _ := parseLSPCIWithSkips(output)
	return states
}

// parseLSPCIWithSkips also returns the Spyre blocks that were skipped, per reason.
func parseLSPCIWithSkips(output string) ([]types.DeviceState, map[string]skipInfo) {
	states := make([]types.DeviceState, 0)
	skips := map[string]skipInfo{}
	for _, dev := range splitByBlankLines(output) {
		di, reason, example := parseDeviceStanza(dev)
		if reason != "" {
			s := skips[reason]
			if s.count == 0 {
				s.example = example
			}
			s.count++
			skips[reason] = s
			continue
		}
		if di == (deviceInfo{}) {
			continue
		}
		if di.VenDevID != pfVDID && di.VenDevID != vfVDID {
			continue
		}

		var state pb.DEVICE_STATE
		if di.Revision == "ff" {
			state = pb.DEVICE_STATE_IN_ERROR
		} else {
			state = pb.DEVICE_STATE_ONLINE
		}

		var devType pb.DEVICE_TYPE
		switch di.VenDevID {
		case pfVDID:
			devType = pb.DEVICE_TYPE_PF
		case vfVDID:
			devType = pb.DEVICE_TYPE_VF
		default:
			devType = pb.DEVICE_TYPE_DEVICE_TYPE_UNSPECIFIED
		}

		states = append(states, types.DeviceState{
			PciAddress: di.PCIAddress,
			Type:       devType,
			State:      state,
		})
	}
	return states, skips
}
