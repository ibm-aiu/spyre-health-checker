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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	rep "github.com/ibm-aiu/spyre-health-checker/internal/reporter"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

const testNode = "worker-3"

var _ = Describe("node health override", func() {
	var (
		dir  string
		logs *observer.ObservedLogs
		o    *Override
	)

	writeEntry := func(content string) {
		Expect(os.WriteFile(filepath.Join(dir, testNode), []byte(content), 0o600)).To(Succeed())
	}
	warnings := func() []string {
		entries := logs.FilterLevelExact(zapcore.WarnLevel).All()
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.Message)
		}
		return out
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		var core zapcore.Core
		core, logs = observer.New(zapcore.DebugLevel)
		o = NewOverride(dir, testNode)
		o.SetLogger(zap.New(core).Sugar())
	})

	Describe("Read", func() {
		It("is none when the directory does not exist (the operator change is not deployed)", func() {
			o = NewOverride(filepath.Join(dir, "absent"), testNode)
			Expect(o.Read()).To(Equal(ModeNone))
		})

		It("is none when the directory is empty or has no entry for this node", func() {
			Expect(o.Read()).To(Equal(ModeNone))
			Expect(os.WriteFile(filepath.Join(dir, "another-node"), []byte("unhealthy"), 0o600)).To(Succeed())
			Expect(o.Read()).To(Equal(ModeNone))
			Expect(logs.Len()).To(BeZero(), "the normal case must not log")
		})

		It("is none when the feature has no directory", func() {
			Expect(NewOverride("", testNode).Read()).To(Equal(ModeNone))
		})

		DescribeTable("accepts both values in any case, with whitespace and a trailing newline",
			func(content, want string) {
				writeEntry(content)
				Expect(o.Read()).To(Equal(want))
			},
			Entry("unhealthy", "unhealthy", ModeUnhealthy),
			Entry("healthy", "healthy", ModeHealthy),
			Entry("upper case", "UNHEALTHY", ModeUnhealthy),
			Entry("mixed case", "Healthy", ModeHealthy),
			Entry("trailing newline", "healthy\n", ModeHealthy),
			Entry("surrounding blanks", "  unhealthy \r\n", ModeUnhealthy),
		)

		DescribeTable("ignores anything else as invalid",
			func(content string) {
				writeEntry(content)
				Expect(o.Read()).To(Equal(ModeInvalid))
			},
			Entry("empty", ""),
			Entry("only blanks", " \n"),
			Entry("a typo", "unhelthy"),
			Entry("a boolean", "true"),
			Entry("two values", "healthy unhealthy"),
		)

		It("warns once per invalid value, not on every check", func() {
			writeEntry("oops")
			for i := 0; i < 3; i++ {
				Expect(o.Read()).To(Equal(ModeInvalid))
			}
			Expect(warnings()).To(HaveLen(1))
			Expect(warnings()[0]).To(ContainSubstring(`"oops"`))

			writeEntry("oops2")
			Expect(o.Read()).To(Equal(ModeInvalid))
			Expect(warnings()).To(HaveLen(2))
		})

		It("cuts a very long invalid value in the log", func() {
			writeEntry(strings.Repeat("x", 10000))
			Expect(o.Read()).To(Equal(ModeInvalid))
			Expect(len(warnings()[0])).To(BeNumerically("<", 300))
		})

		It("logs an active override as a warning, once, and its end", func() {
			writeEntry("healthy")
			Expect(o.Read()).To(Equal(ModeHealthy))
			Expect(o.Read()).To(Equal(ModeHealthy))
			Expect(warnings()).To(HaveLen(1))
			Expect(warnings()[0]).To(ContainSubstring("ACTIVE"))
			Expect(warnings()[0]).To(ContainSubstring("ONLINE"))

			writeEntry("unhealthy")
			Expect(o.Read()).To(Equal(ModeUnhealthy))
			Expect(warnings()).To(HaveLen(2))
			Expect(warnings()[1]).To(ContainSubstring("IN_ERROR"))

			Expect(os.Remove(filepath.Join(dir, testNode))).To(Succeed())
			Expect(o.Read()).To(Equal(ModeNone))
			Expect(warnings()).To(HaveLen(3))
			Expect(warnings()[2]).To(ContainSubstring("no longer applies"))
		})

		It("ends the override when the entry disappears, and does not keep the last value", func() {
			writeEntry("healthy")
			Expect(o.Read()).To(Equal(ModeHealthy))
			Expect(os.Remove(filepath.Join(dir, testNode))).To(Succeed())
			Expect(o.Read()).To(Equal(ModeNone))
		})

		It("ends the override when the whole directory disappears", func() {
			writeEntry("unhealthy")
			Expect(o.Read()).To(Equal(ModeUnhealthy))
			Expect(os.RemoveAll(dir)).To(Succeed())
			Expect(o.Read()).To(Equal(ModeNone))
		})

		It("keeps the previous value when the entry cannot be read", func() {
			writeEntry("healthy")
			Expect(o.Read()).To(Equal(ModeHealthy))
			// replace the file by a directory of the same name: ReadFile fails, but not with "does not exist"
			Expect(os.Remove(filepath.Join(dir, testNode))).To(Succeed())
			Expect(os.Mkdir(filepath.Join(dir, testNode), 0o700)).To(Succeed())

			Expect(o.Read()).To(Equal(ModeHealthy))
			Expect(o.Read()).To(Equal(ModeHealthy))
			readErrors := 0
			for _, w := range warnings() {
				if strings.Contains(w, "cannot read") {
					readErrors++
				}
			}
			Expect(readErrors).To(Equal(1), "the same read error is logged once")
		})

		It("follows a symlink swap, which is how kubelet refreshes a mounted ConfigMap", func() {
			v1 := filepath.Join(dir, "..v1")
			Expect(os.Mkdir(v1, 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(v1, testNode), []byte("unhealthy"), 0o600)).To(Succeed())
			Expect(os.Symlink("..v1", filepath.Join(dir, "..data"))).To(Succeed())
			Expect(os.Symlink(filepath.Join("..data", testNode), filepath.Join(dir, testNode))).To(Succeed())
			Expect(o.Read()).To(Equal(ModeUnhealthy))

			v2 := filepath.Join(dir, "..v2")
			Expect(os.Mkdir(v2, 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(v2, testNode), []byte("healthy"), 0o600)).To(Succeed())
			Expect(os.Symlink("..v2", filepath.Join(dir, "..data_tmp"))).To(Succeed())
			Expect(os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data"))).To(Succeed())
			Expect(o.Read()).To(Equal(ModeHealthy))
		})

		DescribeTable("refuses a node name that could leave the directory",
			func(node string) {
				// the entry that a path escape would find sits one level above the directory that is read
				mount := filepath.Join(dir, "mount")
				Expect(os.Mkdir(mount, 0o700)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(dir, "secret"), []byte("healthy"), 0o600)).To(Succeed())
				Expect(NewOverride(mount, node).Read()).To(Equal(ModeNone))
			},
			Entry("empty", ""),
			Entry("dot", "."),
			Entry("dot dot", ".."),
			Entry("a path", "../secret"),
			Entry("a nested path", "a/b"),
		)
	})

	Describe("applyOverride", func() {
		newStates := func() []types.DeviceState {
			return []types.DeviceState{
				{PciAddress: "0000:1a:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE,
					Source: "lspci", Priority: 1},
				{PciAddress: "0000:1b:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_IN_ERROR,
					Source: "ras", Priority: 10},
				{PciAddress: "0000:1c:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_OFFLINE,
					Source: "cardmgmt", Priority: 5},
				{PciAddress: "0000:1c:00.1", Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_BOOTING,
					Source: "cardmgmt", Priority: 5},
				{PciAddress: "0000:1d:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED,
					Source: "cardmgmt", Priority: 5},
			}
		}

		It("forces every device to IN_ERROR for unhealthy, keeping address and type", func() {
			states := newStates()
			applyOverride(states, ModeUnhealthy, nil)
			for i, s := range states {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
				Expect(s.Source).To(Equal(OverrideSource))
				Expect(s.PciAddress).To(Equal(newStates()[i].PciAddress))
				Expect(s.Type).To(Equal(newStates()[i].Type))
			}
		})

		It("forces every device to ONLINE for healthy, whatever the reporters said", func() {
			states := newStates()
			applyOverride(states, ModeHealthy, nil)
			for _, s := range states {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_ONLINE))
				Expect(s.Source).To(Equal(OverrideSource))
			}
		})

		It("does not let healthy hide a device that lost its driver", func() {
			states := newStates()
			states[1].State = pb.DEVICE_STATE_IN_ERROR
			applyOverride(states, ModeHealthy, map[string]bool{"0000:1b:00.0": true})
			Expect(states[1].State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			Expect(states[1].Source).To(Equal("ras"), "an untouched device keeps its source")
			Expect(states[0].State).To(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("still forces unhealthy on a device that lost its driver", func() {
			states := newStates()
			applyOverride(states, ModeUnhealthy, map[string]bool{"0000:1a:00.0": true})
			Expect(states[0].State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
		})

		DescribeTable("changes nothing for none and invalid",
			func(mode string) {
				states := newStates()
				applyOverride(states, mode, nil)
				Expect(states).To(Equal(newStates()))
			},
			Entry("none", ModeNone),
			Entry("invalid", ModeInvalid),
			Entry("an unknown mode", "bogus"),
		)

		It("never adds a device", func() {
			states := newStates()
			applyOverride(states, ModeHealthy, nil)
			Expect(states).To(HaveLen(len(newStates())))
			applyOverride(nil, ModeHealthy, nil)
		})
	})

	Describe("Vitals.UpdateStates", func() {
		newVitals := func() *Vitals {
			v := NewVitals([]types.Reporter{&rep.PseudoReporter{}})
			v.SetOverride(o)
			return v
		}

		// reporterStates are the states of the pseudo reporter without any override
		reporterStates := func() []types.DeviceState {
			plain := NewVitals([]types.Reporter{&rep.PseudoReporter{}})
			Expect(plain.UpdateStates()).To(Succeed())
			return plain.GetVitalStates()
		}

		It("changes nothing and reports mode none when there is no override", func() {
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideMode()).To(Equal(ModeNone))
			Expect(v.GetVitalStates()).To(ConsistOf(reporterStates()))
		})

		It("works when no override was attached at all", func() {
			v := NewVitals([]types.Reporter{&rep.PseudoReporter{}})
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideMode()).To(Equal(ModeNone))
		})

		It("reports every pseudo device IN_ERROR with unhealthy, and keeps pseudo working", func() {
			writeEntry("unhealthy")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideMode()).To(Equal(ModeUnhealthy))
			Expect(v.GetVitalStates()).NotTo(BeEmpty())
			for _, s := range v.GetVitalStates() {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
				Expect(s.Source).To(Equal(OverrideSource))
			}
		})

		It("reports every pseudo device ONLINE with healthy, including the one that is bad on its own", func() {
			writeEntry("healthy")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideMode()).To(Equal(ModeHealthy))
			for _, s := range v.GetVitalStates() {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_ONLINE))
			}
		})

		It("returns to the reporters' states when the entry is removed", func() {
			writeEntry("unhealthy")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(os.Remove(filepath.Join(dir, testNode))).To(Succeed())
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideMode()).To(Equal(ModeNone))
			Expect(v.GetVitalStates()).To(ConsistOf(reporterStates()))
		})

		It("ignores an invalid value and reports mode invalid", func() {
			writeEntry("maybe")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideMode()).To(Equal(ModeInvalid))
			Expect(v.GetVitalStates()).To(ConsistOf(reporterStates()))
		})

		It("keeps a device without a driver IN_ERROR under healthy (the driver check runs for real)", func() {
			// An address that cannot exist in sysfs, so the driver check fails it. A non-pseudo source
			// makes the check run.
			const ghost = "ffff:ff:1f.7"
			writeEntry("healthy")
			v := NewVitals([]types.Reporter{fakeReporter{states: []types.DeviceState{
				{PciAddress: ghost, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
			}}})
			v.SetOverride(o)
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.GetVitalStates()).To(HaveLen(1))
			Expect(v.GetVitalStates()[0].State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			Expect(v.OverrideMode()).To(Equal(ModeHealthy))
		})
	})
})

// fakeReporter returns fixed states with a non-pseudo source, so that the driver check applies to them.
type fakeReporter struct{ states []types.DeviceState }

func (f fakeReporter) Name() string  { return "fake" }
func (f fakeReporter) Priority() int { return types.PriorityLSPCI }
func (f fakeReporter) Collect() ([]types.DeviceState, error) {
	out := make([]types.DeviceState, len(f.states))
	copy(out, f.states)
	for i := range out {
		out[i].Source = "fake"
		out[i].Priority = types.PriorityLSPCI
	}
	return out, nil
}
