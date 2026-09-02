/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// pseudoTC is the input/output shape for a single PseudoReporter table entry.
type pseudoTC struct {
	// checkCollect, when set, is called with the full Collect() result.
	checkCollect func(states []types.DeviceState)
	// mergeRASAddr, when non-empty, injects a RAS error for that address and
	// verifies it wins over the pseudo ONLINE state after Merge.
	mergeRASAddr string
}

var _ = DescribeTable("PseudoReporter",
	func(tc pseudoTC) {
		r := &PseudoReporter{}
		states, err := r.Collect()
		Expect(err).To(BeNil())

		if tc.checkCollect != nil {
			tc.checkCollect(states)
		}

		if tc.mergeRASAddr != "" {
			ras := NewRASReporter()
			ras.addRASError(tc.mergeRASAddr)

			merged, mergeErr := Merge([]types.Reporter{r, ras})
			Expect(mergeErr).To(BeNil())

			byAddr := make(map[string]types.DeviceState, len(merged))
			for _, s := range merged {
				byAddr[s.PciAddress] = s
			}
			got, ok := byAddr[tc.mergeRASAddr]
			Expect(ok).To(BeTrue(), "address %s missing from merged result", tc.mergeRASAddr)
			Expect(got.State).To(Equal(pb.DEVICE_STATE_IN_ERROR),
				"RASReporter should override pseudo ONLINE for %s", tc.mergeRASAddr)
			Expect(got.Source).To(Equal(RASSource))
		}
	},

	Entry("Name returns pseudo",
		pseudoTC{checkCollect: func(_ []types.DeviceState) {
			Expect((&PseudoReporter{}).Name()).To(Equal("pseudo"))
		}}),

	Entry("Priority returns PriorityLSPCI",
		pseudoTC{checkCollect: func(_ []types.DeviceState) {
			Expect((&PseudoReporter{}).Priority()).To(Equal(types.PriorityLSPCI))
		}}),

	Entry("Collect returns a non-empty list without error",
		pseudoTC{checkCollect: func(states []types.DeviceState) {
			Expect(states).NotTo(BeEmpty())
		}}),

	Entry("Collect stamps every state with source=pseudo and priority=PriorityLSPCI",
		pseudoTC{checkCollect: func(states []types.DeviceState) {
			for _, s := range states {
				Expect(s.Source).To(Equal("pseudo"), "source mismatch for %s", s.PciAddress)
				Expect(s.Priority).To(Equal(types.PriorityLSPCI), "priority mismatch for %s", s.PciAddress)
			}
		}}),

	Entry("Collect returns at least one ONLINE and at least one IN_ERROR state",
		pseudoTC{checkCollect: func(states []types.DeviceState) {
			var hasOnline, hasError bool
			for _, s := range states {
				if s.State == pb.DEVICE_STATE_ONLINE {
					hasOnline = true
				}
				if s.State == pb.DEVICE_STATE_IN_ERROR {
					hasError = true
				}
			}
			Expect(hasOnline).To(BeTrue(), "expected at least one ONLINE pseudo state")
			Expect(hasError).To(BeTrue(), "expected at least one IN_ERROR pseudo state")
		}}),

	Entry("RASReporter overrides pseudo ONLINE for a known good PF via Merge",
		pseudoTC{mergeRASAddr: "0000:1a:00.0"}),

	Entry("RASReporter overrides pseudo ONLINE for a second good PF via Merge",
		pseudoTC{mergeRASAddr: "0000:40:00.0"}),
)
