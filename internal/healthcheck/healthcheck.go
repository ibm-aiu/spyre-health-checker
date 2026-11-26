/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package healthcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	reporter "github.com/ibm-aiu/spyre-health-checker/internal/reporter"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

type Vitals struct {
	States          []types.DeviceState
	mu              sync.RWMutex
	reporters       []types.Reporter
	override        *Override
	overrideSummary OverrideSummary

	// complete is the result of the last merge in which no reporter failed, as the reporters gave it:
	// before the driver check and the override. UpdateStates falls back to it when a reporter fails.
	complete []types.DeviceState
}

// NewVitals creates a Vitals instance with the given reporters.
// If reporters is empty or nil, defaults to LSPCIReporter.
func NewVitals(reporters []types.Reporter) *Vitals {
	if len(reporters) == 0 {
		reporters = []types.Reporter{&reporter.LSPCIReporter{}}
	}
	return &Vitals{
		States:    make([]types.DeviceState, 0),
		reporters: reporters,
	}
}

// SetOverride attaches the node health override. It must be called before the first UpdateStates,
// which server.NewServer runs, so that the first published state already includes the override.
func (v *Vitals) SetOverride(o *Override) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.override = o
}

// OverrideSummary returns what the node health override did in the last UpdateStates: how many devices
// it forced to ONLINE and to IN_ERROR, and how many lines of the entry it could not apply.
func (v *Vitals) OverrideSummary() OverrideSummary {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.overrideSummary
}

func (v *Vitals) GetVitalStates() []types.DeviceState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.States
}

// fileIsAccessible returns true if os.Stat(path) succeeds within 5 seconds.
// It returns false if Stat() errors for any reason or if it takes longer than 5 seconds.
func fileIsAccessible(path string) bool {
	type result struct {
		ok bool
	}
	ch := make(chan result, 1)

	go func() {
		_, err := os.Stat(path)
		ch <- result{ok: err == nil}
	}()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case r := <-ch:
		return r.ok
	case <-timer.C:
		return false
	}
}

// updateDriverStatus sets the state of a device to DEVICE_STATE_IN_ERROR if
// its driver path is not accessible within the timeout.
// States produced by the pseudo reporter are skipped -- they have no real
// sysfs entries and their health is already encoded in the static list.
// It returns the PCI addresses of the devices it marked, which the node health
// override must not report as healthy.
func updateDriverStatus(states []types.DeviceState) map[string]bool {
	var failed map[string]bool
	for i := range states {
		if states[i].Source == "pseudo" {
			continue
		}
		driverPath := filepath.Join("/sys/bus/pci/devices", states[i].PciAddress, "driver")
		if !fileIsAccessible(driverPath) {
			states[i].State = pb.DEVICE_STATE_IN_ERROR
			if failed == nil {
				failed = make(map[string]bool)
			}
			failed[states[i].PciAddress] = true
		}
	}
	return failed
}

// UpdateStates refreshes device states using the configured reporters.
//
// The steps are: merge the reporters, check the driver binding, then apply the node health override.
// The override comes last so that it cannot hide a card that has lost its driver, and it rewrites the
// merged result because Merge never turns an IN_ERROR back into ONLINE. The override can name the whole
// node or single devices (see override.go).
//
// When a reporter fails, Merge still returns what the others found, and UpdateStates publishes a result
// anyway, so that one unavailable source does not blank the node. Which result depends on whether there
// has been a complete one:
//
//   - After a merge in which no reporter failed, the last such result is kept. A partial result lacks
//     what the failed reporter knew, and could report a card healthy that was reported faulty before.
//   - Before that (at start-up, or while a reporter has never worked), the partial result is used, and
//     it follows the working reporters at every check.
//   - With no result at all, nothing is published and the previous states stay.
//
// The driver check and the override are applied to whichever result is used, so that an override
// written while a reporter is down still takes effect. The returned error then names the failed
// reporters and says which result was published; the states are updated all the same.
func (v *Vitals) UpdateStates() error {
	states, mergeErr := reporter.Merge(v.reporters)

	v.mu.RLock()
	last, override := v.complete, v.override
	v.mu.RUnlock()

	isComplete := mergeErr == nil
	var fresh []types.DeviceState // this merge as the reporters gave it, kept for later failures
	switch {
	case isComplete:
		fresh = slices.Clone(states)
	case len(last) > 0:
		states = slices.Clone(last) // the checks below change states in place
		mergeErr = fmt.Errorf("%w; publishing the last complete result", mergeErr)
	case len(states) > 0:
		mergeErr = fmt.Errorf("%w; publishing the partial result", mergeErr)
	default:
		return mergeErr
	}

	driverFailed := updateDriverStatus(states)
	var summary OverrideSummary
	if override != nil {
		summary = override.Apply(states, driverFailed)
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	v.States = states
	v.overrideSummary = summary
	if isComplete {
		v.complete = fresh
	}
	return mergeErr
}
