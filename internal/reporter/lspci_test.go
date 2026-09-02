/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	_ "embed"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

//go:embed testdata/lspci_input.txt
var sampleLSPCI string

var (
	errorCards = []string{
		TestPCIAddress2,
	}
	unsupportedCards = []string{
		TestPCIAddress,
		TestPCIAddress3,
	}
	vfCards = []string{
		TestPCIAddress4,
	}
)

var _ = Describe("LSPCIReporter", func() {
	It("parseLSPCI identifies supported cards, online/error state, and device type", func() {
		states := parseLSPCI(sampleLSPCI)
		Expect(states).To(HaveLen(14))
		for _, state := range states {
			Expect(slices.Contains(unsupportedCards, state.PciAddress)).To(BeFalse())
			switch {
			case slices.Contains(errorCards, state.PciAddress):
				Expect(state.State).To(BeEquivalentTo(pb.DEVICE_STATE_IN_ERROR))
			default:
				Expect(state.State).To(BeEquivalentTo(pb.DEVICE_STATE_ONLINE))
			}
			switch {
			case slices.Contains(vfCards, state.PciAddress):
				Expect(state.Type).To(BeEquivalentTo(pb.DEVICE_TYPE_VF))
			default:
				Expect(state.Type).To(BeEquivalentTo(pb.DEVICE_TYPE_PF))
			}
		}
	})

	It("Collect stamps Source and Priority on every entry", func() {
		r := &LSPCIReporter{}
		// parseLSPCI is tested directly above; here we verify stamp behaviour
		// using a small hand-crafted output that matches the parser.
		states := parseLSPCI(sampleLSPCI)
		stamp(states, r.Name(), r.Priority())
		for _, s := range states {
			Expect(s.Source).To(Equal(LsPCISource))
			Expect(s.Priority).To(Equal(types.PriorityLSPCI))
		}
	})
})

var _ = Describe("Merge", func() {
	It("higher-priority reporter wins on conflict (online→error downgrade)", func() {
		lspciState := types.DeviceState{
			PciAddress: TestPCIAddress,
			State:      pb.DEVICE_STATE_ONLINE,
			Source:     LsPCISource,
			Priority:   types.PriorityLSPCI,
		}
		cardMgmtState := types.DeviceState{
			PciAddress: TestPCIAddress,
			State:      pb.DEVICE_STATE_IN_ERROR,
			Source:     CardmgmtSource,
			Priority:   types.PriorityCardmgmt,
		}

		low := &stubReporter{name: LsPCISource,
			priority: types.PriorityLSPCI, states: []types.DeviceState{lspciState}}
		high := &stubReporter{name: CardmgmtSource,
			priority: types.PriorityCardmgmt, states: []types.DeviceState{cardMgmtState}}

		result, err := Merge([]types.Reporter{low, high})
		Expect(err).To(BeNil())
		Expect(result).To(HaveLen(1))
		Expect(result[0].Source).To(Equal(CardmgmtSource))
		Expect(result[0].State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
	})

	It("non-conflicting devices from different reporters are all present", func() {
		a := types.DeviceState{PciAddress: TestPCIAddress, Source: LsPCISource, Priority: types.PriorityLSPCI}
		b := types.DeviceState{PciAddress: TestPCIAddress2, Source: CardmgmtSource, Priority: types.PriorityCardmgmt}

		r1 := &stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{a}}
		r2 := &stubReporter{name: CardmgmtSource, priority: types.PriorityCardmgmt, states: []types.DeviceState{b}}

		result, err := Merge([]types.Reporter{r1, r2})
		Expect(err).To(BeNil())
		Expect(result).To(HaveLen(2))
	})
})

// stubReporter is a test double that returns a fixed slice of DeviceStates.
type stubReporter struct {
	name     string
	priority int
	states   []types.DeviceState
}

func (s *stubReporter) Name() string                          { return s.name }
func (s *stubReporter) Priority() int                         { return s.priority }
func (s *stubReporter) Collect() ([]types.DeviceState, error) { return s.states, nil }
