/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package healthcheck

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// switchReporter returns its states, or an error while down is set. Its states carry the source "pseudo"
// unless source says otherwise, so that the driver check, which needs a real sysfs, leaves them alone.
type switchReporter struct {
	name   string
	prio   int
	source string
	states []types.DeviceState
	down   atomic.Bool
}

func (s *switchReporter) Name() string  { return s.name }
func (s *switchReporter) Priority() int { return s.prio }
func (s *switchReporter) Collect() ([]types.DeviceState, error) {
	if s.down.Load() {
		return nil, errors.New(s.name + " is down")
	}
	source := s.source
	if source == "" {
		source = "pseudo"
	}
	out := make([]types.DeviceState, len(s.states))
	copy(out, s.states)
	for i := range out {
		out[i].Source, out[i].Priority = source, s.prio
	}
	return out, nil
}

var _ = Describe("UpdateStates when a reporter fails", func() {
	var (
		lspci, cardmgmt *switchReporter
		v               *Vitals
		dir             string
	)

	published := func() map[string]pb.DEVICE_STATE {
		out := map[string]pb.DEVICE_STATE{}
		for _, s := range v.GetVitalStates() {
			out[s.PciAddress] = s.State
		}
		return out
	}
	writeEntry := func(text string) {
		Expect(os.WriteFile(filepath.Join(dir, testNode), []byte(text), 0o600)).To(Succeed())
	}

	BeforeEach(func() {
		// lspci sees both cards as fine; card management knows that card A is faulty
		lspci = &switchReporter{name: "lspci", prio: types.PriorityLSPCI, states: []types.DeviceState{
			{PciAddress: cardA, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
			{PciAddress: cardB, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
		}}
		cardmgmt = &switchReporter{name: "cardmgmt", prio: types.PriorityCardmgmt, states: []types.DeviceState{
			{PciAddress: cardA, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_IN_ERROR},
			{PciAddress: cardB, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
		}}
		v = NewVitals([]types.Reporter{lspci, cardmgmt})
		dir = GinkgoT().TempDir()
		v.SetOverride(NewOverride(dir, testNode))
	})

	complete := map[string]pb.DEVICE_STATE{cardA: pb.DEVICE_STATE_IN_ERROR, cardB: pb.DEVICE_STATE_ONLINE}
	partial := map[string]pb.DEVICE_STATE{cardA: pb.DEVICE_STATE_ONLINE, cardB: pb.DEVICE_STATE_ONLINE}

	It("publishes the merged result without an error when every reporter works", func() {
		Expect(v.UpdateStates()).To(Succeed())
		Expect(published()).To(Equal(complete))
	})

	Describe("at start-up, with no complete result yet", func() {
		It("publishes what the working reporters found, and returns the error", func() {
			cardmgmt.down.Store(true)
			err := v.UpdateStates()
			Expect(err).To(MatchError(ContainSubstring("cardmgmt is down")))
			Expect(err).To(MatchError(ContainSubstring("publishing the partial result")))
			Expect(published()).To(Equal(partial))
		})

		It("publishes nothing when every reporter fails", func() {
			lspci.down.Store(true)
			cardmgmt.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(v.GetVitalStates()).To(BeEmpty())
		})

		It("keeps following the working reporters at every check, because a partial result is not kept", func() {
			cardmgmt.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())

			lspci.states[1].State = pb.DEVICE_STATE_IN_ERROR // lspci now sees card B fail
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()[cardB]).To(Equal(pb.DEVICE_STATE_IN_ERROR))

			cardmgmt.down.Store(false)
			Expect(v.UpdateStates()).To(Succeed())
			Expect(published()[cardA]).To(Equal(pb.DEVICE_STATE_IN_ERROR), "card management's view is back")
		})

		It("uses the partial result when the last complete result had no devices at all", func() {
			lspci.states, cardmgmt.states = nil, nil
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.GetVitalStates()).To(BeEmpty())

			lspci.states = []types.DeviceState{{PciAddress: cardB, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE}}
			cardmgmt.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()).To(Equal(map[string]pb.DEVICE_STATE{cardB: pb.DEVICE_STATE_ONLINE}))
		})
	})

	Describe("while running, after a complete result", func() {
		BeforeEach(func() {
			Expect(v.UpdateStates()).To(Succeed())
			Expect(published()).To(Equal(complete))
		})

		It("keeps the last complete result, not the partial one that would forget card A's fault", func() {
			cardmgmt.down.Store(true)
			err := v.UpdateStates()
			Expect(err).To(MatchError(ContainSubstring("cardmgmt is down")))
			Expect(err).To(MatchError(ContainSubstring("publishing the last complete result")))
			Expect(published()).To(Equal(complete))
		})

		It("keeps it while every reporter fails", func() {
			lspci.down.Store(true)
			cardmgmt.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()).To(Equal(complete))
		})

		It("goes back to the merged result when the reporter returns", func() {
			cardmgmt.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())

			cardmgmt.states[0].State = pb.DEVICE_STATE_ONLINE // card A was repaired meanwhile
			cardmgmt.down.Store(false)
			Expect(v.UpdateStates()).To(Succeed())
			Expect(published()).To(Equal(partial))
		})

		It("replaces the kept result by every new complete result", func() {
			cardmgmt.states[0].State = pb.DEVICE_STATE_ONLINE
			Expect(v.UpdateStates()).To(Succeed())
			cardmgmt.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()).To(Equal(partial), "the kept result is the newer one")
		})
	})

	Describe("what the kept result holds", func() {
		It("is the merge as the reporters gave it, without the override that was active at the time", func() {
			writeEntry("healthy") // forces card A, which card management reports as faulty, to ONLINE
			Expect(v.UpdateStates()).To(Succeed())
			Expect(published()).To(Equal(partial))

			Expect(os.Remove(filepath.Join(dir, testNode))).To(Succeed())
			cardmgmt.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()).To(Equal(complete), "the override ended, so card A's fault is back")
		})

		It("goes through the driver check again, so a card that has lost its driver is IN_ERROR on it too", func() {
			// an address that cannot exist in sysfs, from reporters that are not pseudo, so the check runs
			const ghost = "ffff:ff:1f.7"
			online := []types.DeviceState{{PciAddress: ghost, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE}}
			sure := &switchReporter{name: "sure", prio: types.PriorityLSPCI, source: "lspci", states: online}
			flaky := &switchReporter{name: "flaky", prio: types.PriorityCardmgmt, source: "cardmgmt", states: online}
			v = NewVitals([]types.Reporter{sure, flaky})

			Expect(v.UpdateStates()).To(Succeed())
			Expect(published()[ghost]).To(Equal(pb.DEVICE_STATE_IN_ERROR))

			flaky.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()[ghost]).To(Equal(pb.DEVICE_STATE_IN_ERROR), "on the kept result")

			// and on a partial result at start-up
			v = NewVitals([]types.Reporter{sure, flaky})
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()[ghost]).To(Equal(pb.DEVICE_STATE_IN_ERROR), "on a partial result")
		})
	})

	Describe("with a node health override", func() {
		It("applies an override written at start-up while a reporter is down", func() {
			writeEntry("unhealthy")
			cardmgmt.down.Store(true)
			Expect(v.UpdateStates()).To(HaveOccurred())
			for _, s := range v.GetVitalStates() {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
				Expect(s.Source).To(Equal(OverrideSource))
			}
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{Unhealthy: 2}))
		})

		It("applies, changes and removes an override while a reporter is down, on the kept result", func() {
			Expect(v.UpdateStates()).To(Succeed())
			cardmgmt.down.Store(true)

			writeEntry(cardA + " healthy\n" + cardB + " unhealthy")
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()).To(Equal(map[string]pb.DEVICE_STATE{
				cardA: pb.DEVICE_STATE_ONLINE, cardB: pb.DEVICE_STATE_IN_ERROR}))
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{Healthy: 1, Unhealthy: 1}))

			// the override must not have changed the kept result: removing it brings the old view back
			Expect(os.Remove(filepath.Join(dir, testNode))).To(Succeed())
			Expect(v.UpdateStates()).To(HaveOccurred())
			Expect(published()).To(Equal(complete))
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{}))
		})

		It("keeps the override when the reporter returns", func() {
			Expect(v.UpdateStates()).To(Succeed())
			cardmgmt.down.Store(true)
			writeEntry(cardA + " healthy")
			Expect(v.UpdateStates()).To(HaveOccurred())

			cardmgmt.down.Store(false)
			Expect(v.UpdateStates()).To(Succeed())
			Expect(published()[cardA]).To(Equal(pb.DEVICE_STATE_ONLINE))
		})
	})
})
