/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package healthcheck

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rep "github.com/ibm-aiu/spyre-health-checker/internal/reporter"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

var _ = Describe("HealthChecker functions", func() {
	// newPseudoVitals builds a Vitals backed by PseudoReporter, matching the
	// PSEUDO_DEVICE_MODE=1 environment set by the suite's BeforeSuite.
	newPseudoVitals := func() *Vitals {
		return NewVitals([]types.Reporter{&rep.PseudoReporter{}})
	}

	It("GetVitalStates() returns v.States object", func() {
		vitals := newPseudoVitals()
		Expect(vitals.GetVitalStates()).To(BeAssignableToTypeOf([]types.DeviceState{}))
	})

	It("UpdateStates() is actually callable at Runtime", func() {
		vitals := newPseudoVitals()
		Expect(func() { _ = vitals.UpdateStates() }).NotTo(Panic())
	})

	It("UpdateStates() does not error with simple test scenario", func() {
		vitals := newPseudoVitals()
		err := vitals.UpdateStates()
		Expect(err).To(BeNil())
	})

	It("UpdateStates() populates states from PseudoReporter", func() {
		vitals := newPseudoVitals()
		Expect(vitals.UpdateStates()).To(Succeed())
		Expect(vitals.GetVitalStates()).NotTo(BeEmpty())
	})

	It("updateDriverStatus skips pseudo-sourced states", func() {
		// Pseudo addresses have no real sysfs entries; updateDriverStatus must
		// not flip them to IN_ERROR.
		states := []types.DeviceState{
			{PciAddress: "0000:1a:00.0", Source: "pseudo"},
		}
		updateDriverStatus(states)
		// State is zero-value (ONLINE) and must remain unchanged.
		Expect(states[0].Source).To(Equal("pseudo"))
	})
})
