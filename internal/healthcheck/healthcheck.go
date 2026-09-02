/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package healthcheck

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	reporter "github.com/ibm-aiu/spyre-health-checker/internal/reporter"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

type Vitals struct {
	States    []types.DeviceState
	mu        sync.RWMutex
	reporters []types.Reporter
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
// States produced by the pseudo reporter are skipped — they have no real
// sysfs entries and their health is already encoded in the static list.
func updateDriverStatus(states []types.DeviceState) {
	for i := range states {
		if states[i].Source == "pseudo" {
			continue
		}
		driverPath := filepath.Join("/sys/bus/pci/devices", states[i].PciAddress, "driver")
		if !fileIsAccessible(driverPath) {
			states[i].State = pb.DEVICE_STATE_IN_ERROR
		}
	}
}

// UpdateStates refreshes device states using the configured reporters.
func (v *Vitals) UpdateStates() error {
	states, err := reporter.Merge(v.reporters)
	if err != nil {
		return err
	}
	updateDriverStatus(states)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.States = states
	return nil
}
