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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	rep "github.com/ibm-aiu/spyre-health-checker/internal/reporter"
	"github.com/ibm-aiu/spyre-health-checker/internal/utils"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

const (
	testNode = "worker-3"

	// card addresses used by the tests below
	cardA = "0000:1a:00.0" // PF, ONLINE from lspci
	cardB = "0000:1b:00.0" // PF, IN_ERROR from RAS
	cardC = "0000:1c:00.0" // PF, OFFLINE from cardmgmt
	vfC1  = "0000:1c:00.1" // VF of cardC, BOOTING
)

var _ = Describe("node health override", func() {
	var (
		dir  string
		logs *observer.ObservedLogs
		o    *Override
	)

	writeEntry := func(content string) {
		Expect(os.WriteFile(filepath.Join(dir, testNode), []byte(content), 0o600)).To(Succeed())
	}
	removeEntry := func() { Expect(os.Remove(filepath.Join(dir, testNode))).To(Succeed()) }
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

	Describe("parseEntry", func() {
		It("reads a bare value as the whole node", func() {
			Expect(parseEntry("unhealthy").All).To(Equal(ModeUnhealthy))
			Expect(parseEntry("healthy").All).To(Equal(ModeHealthy))
		})

		It("reads 'all <value>' as the whole node", func() {
			Expect(parseEntry("all unhealthy").All).To(Equal(ModeUnhealthy))
			Expect(parseEntry("ALL Healthy").All).To(Equal(ModeHealthy))
		})

		It("reads one line per device, with the address in lower case", func() {
			e := parseEntry("0000:1A:00.0 unhealthy\n0000:41:00.0 healthy\n")
			Expect(e.All).To(BeEmpty())
			Expect(e.Devices).To(Equal(map[string]string{cardA: ModeUnhealthy, "0000:41:00.0": ModeHealthy}))
			Expect(e.Problems).To(BeEmpty())
			Expect(e.Ignored).To(BeZero())
		})

		It("takes a device line together with a node line", func() {
			e := parseEntry("all healthy\n" + cardB + " unhealthy")
			Expect(e.All).To(Equal(ModeHealthy))
			Expect(e.Devices).To(Equal(map[string]string{cardB: ModeUnhealthy}))
		})

		DescribeTable("ignores blanks and comments, and accepts every line ending",
			func(text string) {
				e := parseEntry(text)
				Expect(e.Devices).To(Equal(map[string]string{cardA: ModeUnhealthy}))
				Expect(e.Problems).To(BeEmpty())
			},
			Entry("comment line and blank lines", "# worker-3\n\n"+cardA+" unhealthy\n\n"),
			Entry("comment after the value", cardA+" unhealthy   # card is being replaced\n"),
			Entry("windows line endings", cardA+" unhealthy\r\n"),
			Entry("tabs and extra blanks", "   "+cardA+" \t unhealthy  \n"),
			Entry("no final newline", cardA+" unhealthy"),
		)

		DescribeTable("ignores a byte-order mark at the start of the file, as some Windows editors write",
			func(text string, wantAll string, wantDevices map[string]string) {
				e := parseEntry(text)
				Expect(e.All).To(Equal(wantAll))
				Expect(e.Devices).To(Equal(wantDevices))
				Expect(e.Problems).To(BeEmpty())
			},
			Entry("a bare value", "\ufeffhealthy", ModeHealthy, nil),
			Entry("a card line", "\ufeff"+cardA+" unhealthy\n", "", map[string]string{cardA: ModeUnhealthy}),
			Entry("a comment first", "\ufeff# worker-3\n"+cardA+" unhealthy", "", map[string]string{cardA: ModeUnhealthy}),
		)

		It("is empty for an empty file or one that has only comments", func() {
			for _, text := range []string{"", " \n", "# nothing here\n"} {
				e := parseEntry(text)
				Expect(e.active()).To(BeFalse(), "text %q", text)
				Expect(e.Problems).To(BeEmpty(), "text %q", text)
			}
		})

		It("lets the last line win when a target appears twice", func() {
			Expect(parseEntry(cardA + " healthy\n" + cardA + " unhealthy").Devices[cardA]).To(Equal(ModeUnhealthy))
			Expect(parseEntry("healthy\nunhealthy").All).To(Equal(ModeUnhealthy))
		})

		DescribeTable("skips a line that cannot be used and keeps the lines around it",
			func(bad string) {
				e := parseEntry(cardA + " unhealthy\n" + bad + "\n" + cardB + " healthy")
				Expect(e.Devices).To(Equal(map[string]string{cardA: ModeUnhealthy, cardB: ModeHealthy}))
				Expect(e.Ignored).To(Equal(1))
				Expect(e.Problems).To(HaveLen(1))
				Expect(e.Problems[0]).To(ContainSubstring("line 2"))
			},
			Entry("a typo in the value", cardC+" unhelthy"),
			Entry("a boolean", cardC+" true"),
			Entry("an address without a value", cardC),
			Entry("a value without a known address", "worker-1 healthy"),
			Entry("a short address", "1c:00.0 healthy"),
			Entry("an address with a wrong function", "0000:1c:00.8 healthy"),
			Entry("an address with a trailing dot", "0000:1c:00. healthy"),
			Entry("a wildcard", "0000:1c:00.* healthy"),
			Entry("three words", cardC+" healthy now"),
			Entry("'all' alone", "all"),
		)

		It("cuts a very long bad line in the problem text", func() {
			e := parseEntry(strings.Repeat("x", 10000))
			Expect(e.Problems).To(HaveLen(1))
			Expect(len(e.Problems[0])).To(BeNumerically("<", 400))
		})

		It("lists only the first problems of a large wrong file, but counts all of them", func() {
			e := parseEntry(strings.Repeat("junk\n", 100))
			Expect(e.Ignored).To(Equal(100))
			Expect(len(e.Problems)).To(Equal(maxProblems + 1))
			Expect(e.Problems[maxProblems]).To(ContainSubstring("more lines were ignored"))
		})
	})

	Describe("Read", func() {
		It("is empty when the directory does not exist (the operator change is not deployed)", func() {
			o = NewOverride(filepath.Join(dir, "absent"), testNode)
			Expect(o.Read().active()).To(BeFalse())
		})

		It("is empty when the directory is empty or has no entry for this node", func() {
			Expect(o.Read().active()).To(BeFalse())
			Expect(os.WriteFile(filepath.Join(dir, "another-node"), []byte("unhealthy"), 0o600)).To(Succeed())
			Expect(o.Read().active()).To(BeFalse())
			Expect(logs.Len()).To(BeZero(), "the normal case must not log")
		})

		It("is empty when the feature has no directory", func() {
			Expect(NewOverride("", testNode).Read().active()).To(BeFalse())
		})

		It("reads the entry of this node and no other", func() {
			Expect(os.WriteFile(filepath.Join(dir, "another-node"), []byte("unhealthy"), 0o600)).To(Succeed())
			writeEntry(cardA + " healthy")
			Expect(o.Read().Devices).To(Equal(map[string]string{cardA: ModeHealthy}))
		})

		It("logs the start of an override as a warning, once, and its change and end", func() {
			writeEntry("healthy")
			Expect(o.Read().All).To(Equal(ModeHealthy))
			Expect(o.Read().All).To(Equal(ModeHealthy))
			Expect(warnings()).To(HaveLen(1))
			Expect(warnings()[0]).To(ContainSubstring("ACTIVE"))
			Expect(warnings()[0]).To(ContainSubstring("every device -> ONLINE"))

			writeEntry("healthy\n" + cardB + " unhealthy")
			Expect(o.Read().Devices).To(HaveLen(1))
			Expect(warnings()).To(HaveLen(2))
			Expect(warnings()[1]).To(ContainSubstring(cardB + " and its VFs -> IN_ERROR"))

			removeEntry()
			Expect(o.Read().active()).To(BeFalse())
			Expect(warnings()).To(HaveLen(3))
			Expect(warnings()[2]).To(ContainSubstring("no longer applies"))
		})

		It("does not log when a changed file has the same effect", func() {
			writeEntry("unhealthy")
			o.Read()
			writeEntry("# a comment was added\nall UNHEALTHY\n")
			o.Read()
			Expect(warnings()).To(HaveLen(1))
		})

		It("logs an entry with several cards once, however often it is read and in whatever order its lines are", func() {
			// the effect is described in address order; map order would make the text differ between reads
			writeEntry(cardA + " unhealthy\n" + cardB + " healthy\n" + cardC + " unhealthy\n0000:1d:00.0 healthy\n")
			for i := 0; i < 30; i++ {
				o.Read()
			}
			Expect(warnings()).To(HaveLen(1))

			writeEntry("0000:1d:00.0 healthy\n" + cardC + " unhealthy\n" + cardB + " healthy\n" + cardA + " unhealthy\n")
			for i := 0; i < 30; i++ {
				o.Read()
			}
			Expect(warnings()).To(HaveLen(1), "the same effect in another order is not a change")
		})

		It("ends the override when the entry disappears, and does not keep the last value", func() {
			writeEntry("healthy")
			Expect(o.Read().active()).To(BeTrue())
			removeEntry()
			Expect(o.Read().active()).To(BeFalse())
		})

		It("ends the override when the whole directory disappears", func() {
			writeEntry("unhealthy")
			Expect(o.Read().active()).To(BeTrue())
			Expect(os.RemoveAll(dir)).To(Succeed())
			Expect(o.Read().active()).To(BeFalse())
		})

		It("keeps the previous entry when it cannot be read, and says so", func() {
			writeEntry(cardA + " healthy")
			Expect(o.Read().Devices).To(HaveLen(1))
			// replace the file by a directory of the same name: ReadFile fails, but not with "does not exist"
			removeEntry()
			Expect(os.Mkdir(filepath.Join(dir, testNode), 0o700)).To(Succeed())

			e := o.Read()
			Expect(e.Devices).To(Equal(map[string]string{cardA: ModeHealthy}))
			Expect(e.Problems).To(HaveLen(1))
			Expect(e.Problems[0]).To(ContainSubstring("cannot read"))
		})

		It("reports a problem that the previous entry already had only once more, not twice", func() {
			// the "keeping" message is added to a copy, so the stored entry is not changed by it
			writeEntry(cardA + " healthy\njunk")
			first := o.Read()
			Expect(first.Problems).To(HaveLen(1))
			removeEntry()
			Expect(os.Mkdir(filepath.Join(dir, testNode), 0o700)).To(Succeed())
			Expect(o.Read().Problems).To(HaveLen(2))
			Expect(o.Read().Problems).To(HaveLen(2), "a second failed read does not pile up messages")
		})

		It("follows a symlink swap, which is how kubelet refreshes a mounted ConfigMap", func() {
			v1 := filepath.Join(dir, "..v1")
			Expect(os.Mkdir(v1, 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(v1, testNode), []byte("unhealthy"), 0o600)).To(Succeed())
			Expect(os.Symlink("..v1", filepath.Join(dir, "..data"))).To(Succeed())
			Expect(os.Symlink(filepath.Join("..data", testNode), filepath.Join(dir, testNode))).To(Succeed())
			Expect(o.Read().All).To(Equal(ModeUnhealthy))

			v2 := filepath.Join(dir, "..v2")
			Expect(os.Mkdir(v2, 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(v2, testNode), []byte(cardA+" healthy"), 0o600)).To(Succeed())
			Expect(os.Symlink("..v2", filepath.Join(dir, "..data_tmp"))).To(Succeed())
			Expect(os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data"))).To(Succeed())
			e := o.Read()
			Expect(e.All).To(BeEmpty())
			Expect(e.Devices).To(Equal(map[string]string{cardA: ModeHealthy}))
		})

		DescribeTable("refuses a node name that could leave the directory",
			func(node string) {
				// the entry that a path escape would find sits one level above the directory that is read
				mount := filepath.Join(dir, "mount")
				Expect(os.Mkdir(mount, 0o700)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(dir, "secret"), []byte("healthy"), 0o600)).To(Succeed())
				e := NewOverride(mount, node).Read()
				Expect(e.active()).To(BeFalse())
				Expect(e.Problems).To(HaveLen(1))
				Expect(e.Problems[0]).To(ContainSubstring("NODE_NAME"))
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
				{PciAddress: cardA, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci", Priority: 1},
				{PciAddress: cardB, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_IN_ERROR, Source: "ras", Priority: 10},
				{PciAddress: cardC, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_OFFLINE, Source: "cardmgmt", Priority: 5},
				{PciAddress: vfC1, Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_BOOTING, Source: "cardmgmt", Priority: 5},
				{PciAddress: "0000:1d:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED,
					Source: "cardmgmt", Priority: 5},
			}
		}
		stateOf := func(states []types.DeviceState, address string) types.DeviceState {
			for _, s := range states {
				if s.PciAddress == address {
					return s
				}
			}
			Fail("no device " + address)
			return types.DeviceState{}
		}
		untouched := func(states []types.DeviceState, address string) {
			orig := stateOf(newStates(), address)
			Expect(stateOf(states, address)).To(Equal(orig), "device %s must not change", address)
		}

		It("forces every device to IN_ERROR for a node-wide unhealthy, keeping address and type", func() {
			states := newStates()
			res := applyOverride(states, parseEntry("unhealthy"), nil)
			Expect(res).To(Equal(applyResult{unhealthy: len(states)}))
			for i, s := range states {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
				Expect(s.Source).To(Equal(OverrideSource))
				Expect(s.PciAddress).To(Equal(newStates()[i].PciAddress))
				Expect(s.Type).To(Equal(newStates()[i].Type))
			}
		})

		It("forces every device to ONLINE for a node-wide healthy, whatever the reporters said", func() {
			states := newStates()
			res := applyOverride(states, parseEntry("healthy"), nil)
			Expect(res).To(Equal(applyResult{healthy: len(states)}))
			for _, s := range states {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_ONLINE))
				Expect(s.Source).To(Equal(OverrideSource))
			}
		})

		It("forces only the named device and leaves every other device as it was", func() {
			states := newStates()
			res := applyOverride(states, parseEntry(cardA+" unhealthy"), nil)
			Expect(res).To(Equal(applyResult{unhealthy: 1}))
			Expect(stateOf(states, cardA).State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			Expect(stateOf(states, cardA).Source).To(Equal(OverrideSource))
			for _, other := range []string{cardB, cardC, vfC1, "0000:1d:00.0"} {
				untouched(states, other)
			}
		})

		It("clears an error on one device, which is what a single healthy line is for", func() {
			states := newStates()
			applyOverride(states, parseEntry(cardB+" healthy"), nil)
			Expect(stateOf(states, cardB).State).To(Equal(pb.DEVICE_STATE_ONLINE))
			untouched(states, cardA)
		})

		It("lets a device line win over the node line, in both directions", func() {
			states := newStates()
			res := applyOverride(states, parseEntry("healthy\n"+cardB+" unhealthy"), nil)
			Expect(res).To(Equal(applyResult{healthy: len(states) - 1, unhealthy: 1}))
			Expect(stateOf(states, cardB).State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			Expect(stateOf(states, cardA).State).To(Equal(pb.DEVICE_STATE_ONLINE))

			states = newStates()
			applyOverride(states, parseEntry("unhealthy\n"+cardA+" healthy"), nil)
			Expect(stateOf(states, cardA).State).To(Equal(pb.DEVICE_STATE_ONLINE))
			Expect(stateOf(states, cardB).State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
		})

		// two cards with VFs: 2d has two VFs, 3b has one
		twoCards := func() []types.DeviceState {
			card := func(address string, t pb.DEVICE_TYPE) types.DeviceState {
				return types.DeviceState{PciAddress: address, Type: t, State: pb.DEVICE_STATE_ONLINE,
					Source: "cardmgmt", Priority: 5}
			}
			return []types.DeviceState{
				card("0000:2d:00.0", pb.DEVICE_TYPE_PF), card("0000:2d:00.1", pb.DEVICE_TYPE_VF),
				card("0000:2d:00.2", pb.DEVICE_TYPE_VF),
				card("0000:3b:00.0", pb.DEVICE_TYPE_PF), card("0000:3b:00.1", pb.DEVICE_TYPE_VF),
			}
		}

		It("a PF line covers the PF and its VFs, and nothing else", func() {
			states := newStates()
			res := applyOverride(states, parseEntry(cardC+" healthy"), nil)
			Expect(res).To(Equal(applyResult{healthy: 2}))
			for _, address := range []string{cardC, vfC1} {
				Expect(stateOf(states, address).State).To(Equal(pb.DEVICE_STATE_ONLINE))
				Expect(stateOf(states, address).Source).To(Equal(OverrideSource))
			}
			for _, other := range []string{cardA, cardB, "0000:1d:00.0"} {
				untouched(states, other)
			}
		})

		It("a PF line does not reach the VFs of another PF", func() {
			states := twoCards()
			res := applyOverride(states, parseEntry("0000:2d:00.0 unhealthy"), nil)
			Expect(res).To(Equal(applyResult{unhealthy: 3}))
			for _, address := range []string{"0000:2d:00.0", "0000:2d:00.1", "0000:2d:00.2"} {
				Expect(stateOf(states, address).State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			}
			for _, address := range []string{"0000:3b:00.0", "0000:3b:00.1"} {
				Expect(stateOf(states, address)).To(Equal(stateOf(twoCards(), address)))
			}
		})

		It("a PF line still covers the reported VFs when the PF itself is not reported", func() {
			states := twoCards()[1:3] // only the two VFs of 0000:2d:00.0
			res := applyOverride(states, parseEntry("0000:2d:00.0 unhealthy"), nil)
			Expect(res).To(Equal(applyResult{unhealthy: 2}))
			Expect(res.problems).To(BeEmpty(), "the line reached devices, so it is not reported as unused")
		})

		It("does not call a line unused when the reporter printed the address in capitals", func() {
			// a VF line is refused, and the message must say "is a VF", not "is not reported"
			states := []types.DeviceState{{PciAddress: "0000:1C:00.1", Type: pb.DEVICE_TYPE_VF,
				State: pb.DEVICE_STATE_ONLINE, Source: "lspci"}}
			res := applyOverride(states, parseEntry("0000:1c:00.1 unhealthy"), nil)
			Expect(res.problems).To(HaveLen(1))
			Expect(res.problems[0]).To(ContainSubstring("is a VF"))
			Expect(res.problems[0]).NotTo(ContainSubstring("not reported"))
		})

		It("lets one PF line override the node line for the PF and its VFs together", func() {
			states := twoCards()
			res := applyOverride(states, parseEntry("healthy\n0000:2d:00.0 unhealthy"), nil)
			Expect(res).To(Equal(applyResult{healthy: 2, unhealthy: 3}))
			Expect(stateOf(states, "0000:2d:00.2").State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			Expect(stateOf(states, "0000:3b:00.1").State).To(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("refuses a line for a VF, and the VF follows its PF line or the node line", func() {
			states := newStates()
			res := applyOverride(states, parseEntry(vfC1+" healthy"), nil)
			Expect(states).To(Equal(newStates()), "nothing changes")
			Expect(res.healthy + res.unhealthy).To(BeZero())
			Expect(res.problems).To(Equal([]string{
				"node health override: device 0000:1c:00.1 is a VF, so its line is ignored: " +
					"name its PF 0000:1c:00.0 instead",
			}))

			states = newStates()
			res = applyOverride(states, parseEntry(cardC+" unhealthy\n"+vfC1+" healthy"), nil)
			Expect(stateOf(states, vfC1).State).To(Equal(pb.DEVICE_STATE_IN_ERROR), "the VF follows its PF")
			Expect(res.problems).To(HaveLen(1))
			Expect(res.problems[0]).To(ContainSubstring("is a VF"))

			states = newStates()
			applyOverride(states, parseEntry("healthy\n"+vfC1+" unhealthy"), nil)
			Expect(stateOf(states, vfC1).State).To(Equal(pb.DEVICE_STATE_ONLINE), "the node line applies, not the VF line")
		})

		It("treats a function other than 0 as a VF of its card even when a reporter typed it as a PF", func() {
			// The RAS reporter types every address it records as a PF, and a RAS error replaces the lspci
			// state of that address. This is the case where an administrator needs to clear a false error.
			ras := types.DeviceState{PciAddress: cardC[:len(cardC)-1] + "1", Type: pb.DEVICE_TYPE_PF,
				State: pb.DEVICE_STATE_IN_ERROR, Source: "ras", Priority: 10}
			Expect(ras.PciAddress).To(Equal(vfC1))

			states := append(newStates()[:3], ras) // cardA, cardB, cardC and the RAS-typed VF
			res := applyOverride(states, parseEntry(cardC+" healthy"), nil)
			Expect(res).To(Equal(applyResult{healthy: 2}))
			Expect(stateOf(states, vfC1).State).To(Equal(pb.DEVICE_STATE_ONLINE), "the PF line reaches it")
			Expect(stateOf(states, vfC1).Source).To(Equal(OverrideSource))

			// and a line naming that address is still a VF line: refused, not used for the device
			states = append(newStates()[:3], ras)
			res = applyOverride(states, parseEntry(vfC1+" healthy"), nil)
			Expect(stateOf(states, vfC1)).To(Equal(ras))
			Expect(res.problems).To(HaveLen(1))
			Expect(res.problems[0]).To(ContainSubstring("is a VF"))
		})

		It("cannot name a VF that has no PF, and no PF line reaches it; only the node line does", func() {
			const isolated = "0001:00:00.0" // a VF at function 0: there is no PF in its slot
			vf := types.DeviceState{PciAddress: isolated, Type: pb.DEVICE_TYPE_VF,
				State: pb.DEVICE_STATE_IN_ERROR, Source: "lspci", Priority: 1}

			states := append(newStates(), vf)
			res := applyOverride(states, parseEntry(isolated+" healthy\n"+cardA+" unhealthy"), nil)
			Expect(stateOf(states, isolated)).To(Equal(vf))
			Expect(res.problems).To(HaveLen(1))
			Expect(res.problems[0]).To(ContainSubstring("VF without a PF"))

			states = append(newStates(), vf)
			applyOverride(states, parseEntry("healthy"), nil)
			Expect(stateOf(states, isolated).State).To(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("reaches a device of unspecified type like a PF, by its own address", func() {
			states := []types.DeviceState{{PciAddress: cardA, State: pb.DEVICE_STATE_ONLINE, Source: "ras"}}
			applyOverride(states, parseEntry(cardA+" unhealthy"), nil)
			Expect(states[0].State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
		})

		It("matches an address whatever its case in the reporter's output", func() {
			states := []types.DeviceState{{PciAddress: "0000:1A:00.0", State: pb.DEVICE_STATE_IN_ERROR, Source: "lspci"}}
			applyOverride(states, parseEntry(cardA+" healthy"), nil)
			Expect(states[0].State).To(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("does not let healthy hide a device that lost its driver, whether named or covered by the node line", func() {
			failed := map[string]bool{cardB: true}

			states := newStates()
			res := applyOverride(states, parseEntry(cardB+" healthy"), failed)
			untouched(states, cardB)
			Expect(res.healthy).To(BeZero())
			Expect(res.problems).To(HaveLen(1), "a named device is reported, the admin asked for it")
			Expect(res.problems[0]).To(ContainSubstring("no driver"))

			states = newStates()
			res = applyOverride(states, parseEntry("healthy"), failed)
			untouched(states, cardB)
			Expect(stateOf(states, cardA).State).To(Equal(pb.DEVICE_STATE_ONLINE))
			Expect(res.healthy).To(Equal(len(states) - 1))
			Expect(res.problems).To(BeEmpty(), "the node line is not an error because of one bad device")
		})

		It("does not let a PF line hide a VF that lost its driver, and names that VF", func() {
			states := newStates()
			res := applyOverride(states, parseEntry(cardC+" healthy"), map[string]bool{vfC1: true})
			Expect(stateOf(states, cardC).State).To(Equal(pb.DEVICE_STATE_ONLINE))
			untouched(states, vfC1)
			Expect(res.healthy).To(Equal(1))
			Expect(res.problems).To(HaveLen(1))
			Expect(res.problems[0]).To(ContainSubstring(vfC1))
		})

		It("judges the driver of each function: a PF without a driver stays as it is, its VFs follow the line", func() {
			// the same is true without an override: the driver check changes only the function it checks
			states := newStates()
			applyOverride(states, parseEntry(cardC+" healthy"), map[string]bool{cardC: true})
			untouched(states, cardC)
			Expect(stateOf(states, vfC1).State).To(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("still forces unhealthy on a device that lost its driver", func() {
			states := newStates()
			applyOverride(states, parseEntry(cardA+" unhealthy"), map[string]bool{cardA: true})
			Expect(stateOf(states, cardA).State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			Expect(stateOf(states, cardA).Source).To(Equal(OverrideSource))
		})

		It("reports a line for an address that no reporter returned, and never adds a device", func() {
			states := newStates()
			res := applyOverride(states, parseEntry("0000:99:00.0 healthy\n0000:98:00.0 unhealthy"), nil)
			Expect(states).To(Equal(newStates()))
			Expect(res.healthy + res.unhealthy).To(BeZero())
			Expect(res.problems).To(Equal([]string{
				"node health override: device 0000:98:00.0 is not reported on this node, ignoring its line",
				"node health override: device 0000:99:00.0 is not reported on this node, ignoring its line",
			}), "in address order, so the log does not change from one check to the next")
		})

		It("changes nothing for an entry without effect", func() {
			states := newStates()
			Expect(applyOverride(states, NodeEntry{}, nil)).To(Equal(applyResult{}))
			Expect(applyOverride(states, parseEntry("# only a comment"), nil)).To(Equal(applyResult{}))
			Expect(applyOverride(states, NodeEntry{All: "bogus"}, nil)).To(Equal(applyResult{}))
			Expect(states).To(Equal(newStates()))
		})

		It("accepts an empty device list", func() {
			Expect(func() { applyOverride(nil, parseEntry("healthy"), nil) }).NotTo(Panic())
		})
	})

	Describe("Apply", func() {
		states := func() []types.DeviceState {
			return []types.DeviceState{
				{PciAddress: cardA, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
				{PciAddress: cardB, State: pb.DEVICE_STATE_IN_ERROR, Source: "ras"},
			}
		}

		It("reports what it did and does nothing without an entry", func() {
			s := states()
			Expect(o.Apply(s, nil)).To(Equal(OverrideSummary{}))
			Expect(s).To(Equal(states()))
			Expect(logs.Len()).To(BeZero())
		})

		It("counts forced devices and ignored lines", func() {
			writeEntry("# comment\n" + cardA + " unhealthy\n" + cardB + " healthy\nbad line\n0000:99:00.0 healthy\n")
			Expect(o.Apply(states(), nil)).To(Equal(OverrideSummary{Healthy: 1, Unhealthy: 1, Ignored: 2}))
		})

		It("warns once per problem, not at every check, and again when the problem comes back", func() {
			writeEntry(cardA + " unhealthy\nbad line\n")
			for i := 0; i < 3; i++ {
				o.Apply(states(), nil)
			}
			problems := func() int {
				n := 0
				for _, w := range warnings() {
					if strings.Contains(w, "ignoring") {
						n++
					}
				}
				return n
			}
			Expect(problems()).To(Equal(1))

			writeEntry(cardA + " unhealthy\n") // fixed
			o.Apply(states(), nil)
			writeEntry(cardA + " unhealthy\nbad line\n") // broken again
			o.Apply(states(), nil)
			Expect(problems()).To(Equal(2))
		})

		It("warns about a line for a missing device once, and says so again if the device returns and goes", func() {
			writeEntry("0000:99:00.0 healthy")
			for i := 0; i < 3; i++ {
				o.Apply(states(), nil)
			}
			missing := func() int {
				n := 0
				for _, w := range warnings() {
					if strings.Contains(w, "not reported") {
						n++
					}
				}
				return n
			}
			Expect(missing()).To(Equal(1))

			withDevice := append(states(), types.DeviceState{PciAddress: "0000:99:00.0", State: pb.DEVICE_STATE_OFFLINE})
			o.Apply(withDevice, nil)
			o.Apply(states(), nil)
			Expect(missing()).To(Equal(2))
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
		find := func(states []types.DeviceState, address string) types.DeviceState {
			for _, s := range states {
				if s.PciAddress == address {
					return s
				}
			}
			Fail("no device " + address)
			return types.DeviceState{}
		}

		It("changes nothing and reports no devices when there is no override", func() {
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{}))
			Expect(v.GetVitalStates()).To(ConsistOf(reporterStates()))
		})

		It("works when no override was attached at all", func() {
			v := NewVitals([]types.Reporter{&rep.PseudoReporter{}})
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{}))
		})

		It("reports every pseudo device IN_ERROR with a node-wide unhealthy, and keeps pseudo working", func() {
			writeEntry("unhealthy")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.GetVitalStates()).NotTo(BeEmpty())
			for _, s := range v.GetVitalStates() {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
				Expect(s.Source).To(Equal(OverrideSource))
			}
			Expect(v.OverrideSummary().Unhealthy).To(Equal(len(v.GetVitalStates())))
		})

		It("reports every pseudo device ONLINE with a node-wide healthy, including the one that is bad on its own", func() {
			writeEntry("healthy")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			for _, s := range v.GetVitalStates() {
				Expect(s.State).To(Equal(pb.DEVICE_STATE_ONLINE))
			}
			Expect(v.OverrideSummary().Healthy).To(Equal(len(v.GetVitalStates())))
		})

		// pseudoArch fixes the layout of the pseudo devices for one spec: on every architecture except
		// s390x each card is a PF with two VFs (three devices); on s390x the VFs are isolated.
		pseudoArch := func(arch string) {
			saved := utils.PseudoRuntimeArch
			utils.PseudoRuntimeArch = arch
			DeferCleanup(func() { utils.PseudoRuntimeArch = saved })
		}

		It("changes one pseudo card with its VFs and leaves the rest as the reporter said", func() {
			pseudoArch("amd64")
			// 0000:41:00.0 is the card that the pseudo reporter reports as bad; its VFs are bad too
			writeEntry("0000:41:00.0 healthy\n0000:1a:00.0 unhealthy\n")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			states := v.GetVitalStates()
			for _, address := range []string{"0000:41:00.0", "0000:41:00.1", "0000:41:00.2"} {
				Expect(find(reporterStates(), address).State).To(Equal(pb.DEVICE_STATE_IN_ERROR), address)
				Expect(find(states, address).State).To(Equal(pb.DEVICE_STATE_ONLINE), address)
				Expect(find(states, address).Source).To(Equal(OverrideSource), address)
			}
			for _, address := range []string{"0000:1a:00.0", "0000:1a:00.1", "0000:1a:00.2"} {
				Expect(find(states, address).State).To(Equal(pb.DEVICE_STATE_IN_ERROR), address)
			}
			for _, address := range []string{"0000:1c:00.0", "0000:1c:00.1", "0000:1c:00.2"} {
				Expect(find(states, address)).To(Equal(find(reporterStates(), address)), address)
			}
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{Healthy: 3, Unhealthy: 3}))
		})

		It("refuses a line for a pseudo VF and keeps the PF's line for the rest", func() {
			pseudoArch("amd64")
			writeEntry("0000:41:00.1 healthy\n")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.GetVitalStates()).To(ConsistOf(reporterStates()))
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{Ignored: 1}))
		})

		It("cannot reach an isolated VF (s390x layout) with a line, only with the node line", func() {
			pseudoArch("s390x")
			// 0008:00:00.0 is the isolated VF that the pseudo reporter reports as bad
			writeEntry("0008:00:00.0 healthy\n")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(find(v.GetVitalStates(), "0008:00:00.0").State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{Ignored: 1}))

			writeEntry("healthy\n")
			Expect(v.UpdateStates()).To(Succeed())
			Expect(find(v.GetVitalStates(), "0008:00:00.0").State).To(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("returns to the reporters' states when the entry is removed", func() {
			writeEntry("unhealthy")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			removeEntry()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{}))
			Expect(v.GetVitalStates()).To(ConsistOf(reporterStates()))
		})

		It("ignores a bad line, applies the good ones and counts the bad one", func() {
			pseudoArch("amd64")
			writeEntry("maybe\n0000:41:00.0 healthy\n")
			v := newVitals()
			Expect(v.UpdateStates()).To(Succeed())
			Expect(v.OverrideSummary()).To(Equal(OverrideSummary{Healthy: 3, Ignored: 1}))
			Expect(find(v.GetVitalStates(), "0000:41:00.0").State).To(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("keeps a device without a driver IN_ERROR under healthy (the driver check runs for real)", func() {
			// An address that cannot exist in sysfs, so the driver check fails it. A non-pseudo source
			// makes the check run.
			const ghost = "ffff:ff:1f.7"
			for _, entry := range []string{"healthy", ghost + " healthy"} {
				writeEntry(entry)
				v := NewVitals([]types.Reporter{fakeReporter{states: []types.DeviceState{
					{PciAddress: ghost, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE},
				}}})
				v.SetOverride(o)
				Expect(v.UpdateStates()).To(Succeed())
				Expect(v.GetVitalStates()).To(HaveLen(1), fmt.Sprintf("entry %q", entry))
				Expect(v.GetVitalStates()[0].State).To(Equal(pb.DEVICE_STATE_IN_ERROR), fmt.Sprintf("entry %q", entry))
				Expect(v.OverrideSummary().Healthy).To(BeZero())
			}
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
