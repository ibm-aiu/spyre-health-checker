/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	utils "github.com/ibm-aiu/spyre-health-checker/internal/utils"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// PseudoReporter is a types.Reporter that returns the static pseudo-device
// health list defined in utils.GetPseudoDeviceHealths(). It carries the same
// priority as LSPCIReporter (PriorityLSPCI = 1) so that a higher-priority
// RASReporter can override its states in pseudo mode, exactly as it would in
// production with the real lspci reporter.
type PseudoReporter struct{}

func (r *PseudoReporter) Name() string  { return "pseudo" }
func (r *PseudoReporter) Priority() int { return types.PriorityLSPCI }

// Collect returns the static pseudo-device health list.
func (r *PseudoReporter) Collect() ([]types.DeviceState, error) {
	states := utils.GetPseudoDeviceHealths()
	stamp(states, r.Name(), r.Priority())
	return states, nil
}
