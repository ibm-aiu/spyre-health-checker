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

var _ = Describe("card management states for PFs and VFs", func() {
	discovered := []types.DeviceState{
		{PciAddress: "0000:2d:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
		{PciAddress: "0000:2d:00.1", Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_ONLINE},
		{PciAddress: "0000:2d:00.2", Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_ONLINE},
		{PciAddress: "0000:3b:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
		{PciAddress: "0000:3b:00.1", Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_ONLINE},
	}

	Describe("PFSlots", func() {
		It("returns only the physical functions", func() {
			Expect(PFSlots(discovered)).To(Equal([]string{"0000:2d:00.0", "0000:3b:00.0"}))
		})

		It("returns an empty list when nothing was discovered", func() {
			Expect(PFSlots(nil)).To(BeEmpty())
		})
	})

	Describe("WithVFStates", func() {
		pfStates := []types.DeviceState{
			{PciAddress: "0000:2d:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
			{PciAddress: "0000:3b:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_IN_ERROR},
		}

		It("gives each VF the state of the PF in the same slot", func() {
			states := WithVFStates(discovered, pfStates)
			byAddr := map[string]types.DeviceState{}
			for _, s := range states {
				byAddr[s.PciAddress] = s
			}
			Expect(states).To(HaveLen(5))
			Expect(byAddr["0000:2d:00.1"].State).To(Equal(pb.DEVICE_STATE_ONLINE))
			Expect(byAddr["0000:2d:00.2"].State).To(Equal(pb.DEVICE_STATE_ONLINE))
			Expect(byAddr["0000:3b:00.1"].State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			for _, addr := range []string{"0000:2d:00.1", "0000:2d:00.2", "0000:3b:00.1"} {
				Expect(byAddr[addr].Type).To(Equal(pb.DEVICE_TYPE_VF))
			}
		})

		It("gives a VF the detailed state of its PF (for example BOOTING)", func() {
			booting := []types.DeviceState{
				{PciAddress: "0000:2d:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_BOOTING},
			}
			states := WithVFStates(discovered, booting)
			byAddr := map[string]types.DeviceState{}
			for _, s := range states {
				byAddr[s.PciAddress] = s
			}
			Expect(byAddr["0000:2d:00.1"].State).To(Equal(pb.DEVICE_STATE_BOOTING))
			Expect(byAddr["0000:2d:00.2"].State).To(Equal(pb.DEVICE_STATE_BOOTING))
		})

		It("keeps the PF states unchanged", func() {
			states := WithVFStates(discovered, pfStates)
			Expect(states[0]).To(Equal(pfStates[0]))
			Expect(states[1]).To(Equal(pfStates[1]))
		})

		It("reports no state for a VF whose PF was skipped, so its lspci state stands", func() {
			states := WithVFStates(discovered, pfStates[:1])
			for _, s := range states {
				Expect(s.PciAddress).NotTo(Equal("0000:3b:00.1"))
			}
		})

		It("returns nothing when there are no PF states", func() {
			Expect(WithVFStates(discovered, nil)).To(BeEmpty())
		})
	})
})
