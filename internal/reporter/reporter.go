/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

// Package reporter provides implementations of the types.Reporter interface.
// Each reporter collects device states from a specific source and stamps them
// with its source name and priority level.
//
// Priority hierarchy (highest wins on conflict):
//
//	CardmgmtReporter — priority  5  (card management service)
//	LSPCIReporter    — priority  1  (hardware lspci scan)
package reporter

import (
	"fmt"

	"github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// stamp overwrites Source and Priority on every entry so individual reporter
// implementations do not have to set them manually.
func stamp(states []types.DeviceState, source string, priority int) {
	for i := range states {
		states[i].Source = source
		states[i].Priority = priority
	}
}

// Merge combines states from multiple reporters using priority-based conflict
// resolution. For each PCI address the state from the highest-priority reporter
// wins. When two reporters share the same priority the first encountered is kept.
//
// Once a device has been recorded as unhealthy, a later higher-priority reporter
// cannot flip it back to healthy (ONLINE).
// For more details, check ./enhancements/reporter-framework.md.
func Merge(reporters []types.Reporter) ([]types.DeviceState, error) {
	best := make(map[string]types.DeviceState)

	var errs []error
	for _, rep := range reporters {
		states, err := rep.Collect()
		if err != nil {
			errs = append(errs, fmt.Errorf("reporter %q: %w", rep.Name(), err))
			continue
		}
		for _, s := range states {
			// Insert when no entry exists yet. Otherwise a higher-priority reporter
			// may override, except when the existing state is already unhealthy and
			// the incoming state is healthy.
			if existing, ok := best[s.PciAddress]; !ok ||
				(s.Priority > existing.Priority &&
					(existing.State == spyre.DEVICE_STATE_ONLINE || s.State != spyre.DEVICE_STATE_ONLINE)) {
				best[s.PciAddress] = s
			}
		}
	}

	result := make([]types.DeviceState, 0, len(best))
	for _, s := range best {
		result = append(result, s)
	}

	if len(errs) > 0 {
		return result, fmt.Errorf("merge encountered %d reporter error(s): %v", len(errs), errs)
	}
	return result, nil
}
