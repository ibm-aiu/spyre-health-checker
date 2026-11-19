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
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.uber.org/zap"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// Values of the node health override and of the spyre_health_override metric.
// An administrator sets ModeHealthy or ModeUnhealthy; the other two are only reported.
const (
	ModeNone      = "none"      // no override for this node
	ModeHealthy   = "healthy"   // every device of the node is reported ONLINE
	ModeUnhealthy = "unhealthy" // every device of the node is reported IN_ERROR
	ModeInvalid   = "invalid"   // the entry holds another value, which is ignored

	// OverrideSource is the DeviceState.Source of a device whose state was forced by the override.
	OverrideSource = "override"
)

// forcedState is the state that each active mode forces on a device.
var forcedState = map[string]pb.DEVICE_STATE{
	ModeHealthy:   pb.DEVICE_STATE_ONLINE,
	ModeUnhealthy: pb.DEVICE_STATE_IN_ERROR,
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

	mu      sync.Mutex
	last    string // mode returned by the previous Read
	problem string // last warning about the entry, so that it is logged once until it changes
}

// NewOverride returns an Override reading <dir>/<node>. An empty dir disables the feature.
func NewOverride(dir, node string) *Override {
	return &Override{dir: dir, node: node, log: zap.NewNop().Sugar(), last: ModeNone}
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

// Read returns the current mode: ModeHealthy, ModeUnhealthy, ModeInvalid or ModeNone.
//
//   - A missing file or directory ends any override (the entry or the ConfigMap was removed).
//   - A value that is not healthy or unhealthy is ignored and reported as ModeInvalid.
//   - Any other read error keeps the previous mode, so that a transient error does not flip the devices.
//
// Changes and problems are logged at warning level, once each.
func (o *Override) Read() string {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.dir == "" {
		return ModeNone
	}
	if !validNodeName(o.node) {
		o.warnOnce(fmt.Sprintf("node health override disabled: unusable node name %.40q (is NODE_NAME set?)", o.node))
		return ModeNone
	}

	data, err := os.ReadFile(filepath.Join(o.dir, o.node))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		o.problem = ""
		return o.set(ModeNone)
	case err != nil:
		o.warnOnce(fmt.Sprintf("node health override: cannot read the entry of node %.40q, keeping %q: %v",
			o.node, o.last, err))
		return o.last
	}

	value := strings.ToLower(strings.TrimSpace(string(data)))
	if _, ok := forcedState[value]; !ok {
		o.warnOnce(fmt.Sprintf("node health override: ignoring invalid value %.40q for node %.40q (want %q or %q)",
			string(data), o.node, ModeHealthy, ModeUnhealthy))
		return o.set(ModeInvalid)
	}
	o.problem = ""
	return o.set(value)
}

// set records the mode and logs the start or the end of an active override. The caller holds o.mu.
func (o *Override) set(mode string) string {
	if mode == o.last {
		return mode
	}
	if forced, ok := forcedState[mode]; ok {
		o.log.Warnf("node health override ACTIVE for node %.40q: every Spyre device is forced to %s (was %s)",
			o.node, forced, o.last)
	} else if _, wasActive := forcedState[o.last]; wasActive {
		o.log.Warnf("node health override %q for node %.40q no longer applies (now %s)", o.last, o.node, mode)
	}
	o.last = mode
	return mode
}

// warnOnce logs msg unless it is the one logged last. The caller holds o.mu.
func (o *Override) warnOnce(msg string) {
	if msg != o.problem {
		o.log.Warn(msg)
		o.problem = msg
	}
}

// applyOverride forces the state of every device to ONLINE (ModeHealthy) or IN_ERROR (ModeUnhealthy)
// and marks it with Source "override". Any other mode changes nothing.
//
// driverFailed holds the devices whose kernel driver binding is missing. ModeHealthy leaves them as
// they are: an override cannot hide a card that cannot run work. The override never adds a device.
func applyOverride(states []types.DeviceState, mode string, driverFailed map[string]bool) {
	forced, ok := forcedState[mode]
	if !ok {
		return
	}
	for i := range states {
		if mode == ModeHealthy && driverFailed[states[i].PciAddress] {
			continue
		}
		states[i].State = forced
		states[i].Source = OverrideSource
	}
}
