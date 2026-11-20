/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cardhealth "github.com/ibm-aiu/spyre-health-checker/pkg/cardhealth"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

var _ = Describe("ResolveStrictNA", func() {
	DescribeTable("takes the flag first, then the environment variable, then the default (off)",
		func(flagGiven, flagValue bool, env string, wantStrict bool, wantSource string) {
			strict, source, err := ResolveStrictNA(flagGiven, flagValue, env)
			Expect(err).NotTo(HaveOccurred())
			Expect(strict).To(Equal(wantStrict))
			Expect(source).To(Equal(wantSource))
		},
		Entry("nothing set", false, false, "", false, "default"),
		Entry("only blanks in the variable", false, false, "  ", false, "default"),
		Entry("variable true", false, false, "true", true, "environment"),
		Entry("variable 1, any case, blanks", false, false, " True ", true, "environment"),
		Entry("variable false", false, false, "false", false, "environment"),
		Entry("flag true", true, true, "", true, "flag"),
		Entry("flag false", true, false, "", false, "flag"),
		Entry("flag true beats variable false", true, true, "false", true, "flag"),
		Entry("flag false beats variable true: it is how to turn it off again", true, false, "true", false, "flag"),
		// a bool flag that was not given is false, which must not hide the variable
		Entry("flag not given does not hide variable true", false, false, "true", true, "environment"),
	)

	DescribeTable("rejects a variable that is not a boolean, whether or not the flag was given",
		func(flagGiven, flagValue bool) {
			_, _, err := ResolveStrictNA(flagGiven, flagValue, "maybe")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("maybe"))
		},
		Entry("without the flag", false, false),
		Entry("with the flag true", true, true),
		Entry("with the flag false", true, false),
	)
})

var _ = Describe("CheckStrayArgs", func() {
	const name = StrictNAFlag

	DescribeTable("catches an argument left after the flag, which would drop every flag after it",
		func(given bool, rest []string, wantError, wantHint bool) {
			err := CheckStrayArgs(name, given, rest)
			if !wantError {
				Expect(err).NotTo(HaveOccurred())
				return
			}
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(rest[0]))
			if wantHint {
				Expect(err.Error()).To(ContainSubstring("write --" + name + "=" + rest[0]))
			} else {
				Expect(err.Error()).NotTo(ContainSubstring("write --"))
			}
		},
		// spec.healthChecker.args: ["--cardhealth-strict-na", "true", "--timer", "5s"]
		Entry("true as its own argument, with the form that works", true, []string{"true", "--timer", "5s"}, true, true),
		Entry("false as its own argument", true, []string{"false"}, true, true),
		Entry("any case", true, []string{"TRUE"}, true, true),
		Entry("1", true, []string{"1"}, true, true),
		// Go does not read yes as a boolean, but flag parsing stops at it just the same
		Entry("yes: not a boolean, so no hint", true, []string{"yes", "--timer", "5s"}, true, false),
		Entry("any other word", true, []string{"some-file"}, true, false),
		Entry("the flag was not given", false, []string{"true", "--timer", "5s"}, false, false),
		Entry("no argument is left", true, nil, false, false),
	)

	It("does not trigger for the form that works, --cardhealth-strict-na=true", func() {
		// Go reads it as one argument, so nothing is left over after parsing
		Expect(CheckStrayArgs(name, true, []string{})).To(Succeed())
	})
})

// lspciStanza returns one device block of `lspci -vvvnn` for an IBM Spyre PF (1014:06a7) or VF (1014:06a8).
func lspciStanza(address, vendevID, revision string) string {
	return fmt.Sprintf("%s Processing accelerators [1200]: IBM Spyre Accelerator [%s] (rev %s)\n"+
		"\tKernel driver in use: vfio-pci\n", address, vendevID, revision)
}

// useFakeLSPCI puts a script called lspci first on PATH that prints output, so that the real LSPCIReporter runs
// against it without hardware.
func useFakeLSPCI(output string) {
	dir := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "out.txt"), []byte(output), 0o600)).To(Succeed())
	script := fmt.Sprintf("#!/bin/sh\ncat %q\n", filepath.Join(dir, "out.txt"))
	Expect(os.WriteFile(filepath.Join(dir, "lspci"), []byte(script), 0o700)).To(Succeed()) //nolint:gosec // test script
	orig := os.Getenv("PATH")
	Expect(os.Setenv("PATH", dir+string(os.PathListSeparator)+orig)).To(Succeed())
	DeferCleanup(func() { Expect(os.Setenv("PATH", orig)).To(Succeed()) })
}

// byAddress indexes states by PCI address. A device that appears twice fails the test: a reporter must give
// one state per device.
func byAddress(states []types.DeviceState) map[string]types.DeviceState {
	GinkgoHelper()
	out := make(map[string]types.DeviceState, len(states))
	for _, s := range states {
		Expect(out).NotTo(HaveKey(s.PciAddress), "%s appears twice", s.PciAddress)
		out[s.PciAddress] = s
	}
	return out
}

// The node of these tests has four cards, each with one VF. lspci sees them as below; card management is the
// fake sidecar, which answers as each test says.
const (
	naGoodCard  = "0000:1a:00.0" // lspci: fine
	naGoodVF    = "0000:1a:00.1"
	naFaultCard = "0000:1d:00.0" // lspci: revision ff, which lspci reports as a fault
	naFaultVF   = "0000:1d:00.1"
	knownCard   = "0000:2d:00.0" // lspci: fine
	knownVF     = "0000:2d:00.1"
	failedCard  = "0000:3b:00.0" // lspci: fine
	failedVF    = "0000:3b:00.1"
)

var _ = Describe("card management answering na, with the lspci and cardmgmt reporters", func() {
	var (
		logged []string
		stop   func()
		client *CardHealthClient
	)

	allAddresses := []string{naGoodCard, naGoodVF, naFaultCard, naFaultVF, knownCard, knownVF, failedCard, failedVF}

	// start runs the real reporters against a fake lspci and a fake sidecar that answers as responses say.
	start := func(responses map[string]*cardhealth.GetCardHealthResponse) {
		useFakeLSPCI(
			lspciStanza(naGoodCard, "1014:06a7", "02") + "\n" + lspciStanza(naGoodVF, "1014:06a8", "02") + "\n" +
				lspciStanza(naFaultCard, "1014:06a7", "ff") + "\n" + lspciStanza(naFaultVF, "1014:06a8", "ff") + "\n" +
				lspciStanza(knownCard, "1014:06a7", "02") + "\n" + lspciStanza(knownVF, "1014:06a8", "02") + "\n" +
				lspciStanza(failedCard, "1014:06a7", "02") + "\n" + lspciStanza(failedVF, "1014:06a8", "02") + "\n")

		// The real sidecar answers "unhealthy" for the slot of a VF, because card management tracks PFs only. The
		// fake does the same, so that a reporter that asked for VF slots would mark every VF IN_ERROR.
		answers := map[string]*cardhealth.GetCardHealthResponse{}
		for slot, resp := range responses {
			answers[slot] = resp
		}
		for _, vf := range []string{naGoodVF, naFaultVF, knownVF, failedVF} {
			answers[vf] = &cardhealth.GetCardHealthResponse{Health: "unhealthy"}
		}

		var sock string
		sock, stop = startFakeServer(answers)
		client = newTestClient(sock)
	}

	// someNA is a card management that knows two cards and answers na for the other two.
	someNA := map[string]*cardhealth.GetCardHealthResponse{
		naGoodCard:  {Health: "na"},
		naFaultCard: {Health: "na"},
		knownCard:   {Health: "online"},
		failedCard:  {Health: "failed"},
	}

	// allNA is a card management that predates DRA: it manages no card and answers na for every slot.
	allNA := map[string]*cardhealth.GetCardHealthResponse{
		naGoodCard:  {Health: "na"},
		naFaultCard: {Health: "na"},
		knownCard:   {Health: "na"},
		failedCard:  {Health: "na"},
	}

	lspciReporter := func() types.Reporter { return &LSPCIReporter{} }
	cardmgmtReporter := func() types.Reporter { return NewCardmgmtReporter(&LSPCIReporter{}, client) }

	// merged is the result of the two reporters together, as the health checker computes it.
	merged := func() map[string]types.DeviceState {
		states, err := Merge([]types.Reporter{lspciReporter(), cardmgmtReporter()})
		Expect(err).NotTo(HaveOccurred())
		return byAddress(states)
	}

	collect := func(r types.Reporter) map[string]types.DeviceState {
		states, err := r.Collect()
		Expect(err).NotTo(HaveOccurred())
		return byAddress(states)
	}

	expect := func(states map[string]types.DeviceState, address string, state pb.DEVICE_STATE, source string) {
		GinkgoHelper()
		Expect(states).To(HaveKey(address))
		Expect(states[address].State).To(Equal(state), "state of %s", address)
		Expect(states[address].Source).To(Equal(source), "source of %s", address)
	}

	BeforeEach(func() {
		logged = nil
		savedLog, savedLspci := untrackedLog, lspciDebugLog
		untrackedLog = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
		lspciDebugLog = func(string, ...any) {}
		untrackedLogged.Store(false)
		lspciLogged.Clear()
		DeferCleanup(func() {
			untrackedLog, lspciDebugLog = savedLog, savedLspci
			untrackedLogged.Store(false)
			lspciLogged.Clear()
		})
	})

	AfterEach(func() {
		if stop != nil {
			stop()
			stop = nil
		}
	})

	Describe("the cardmgmt reporter wiring", func() {
		It("asks the sidecar for PF slots only, so that no VF is marked in error", func() {
			start(someNA)
			// the fake sidecar answers "unhealthy" for any VF slot, as the real one does: a VF with a state of
			// its own would be IN_ERROR, but it must take the state of its PF
			states := collect(cardmgmtReporter())
			expect(states, knownVF, pb.DEVICE_STATE_ONLINE, "cardmgmt")
			Expect(states[knownVF].Type).To(Equal(pb.DEVICE_TYPE_VF))
		})

		It("returns the error of the discoverer, and not an empty result, so that the failure is seen", func() {
			start(someNA)
			failing := &errReporter{name: "lspci"}
			states, err := NewCardmgmtReporter(failing, client).Collect()
			Expect(err).To(MatchError(ContainSubstring("collect failed")))
			Expect(states).To(BeEmpty())
		})

		It("returns nothing, without an error, when the discoverer finds no device", func() {
			start(someNA)
			states, err := NewCardmgmtReporter(&stubReporter{name: "lspci"}, client).Collect()
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(BeEmpty())
		})

		Describe("with a client that cannot be set up (no TLS files)", func() {
			// A node without Spyre cards, or one where the certificates are not mounted yet. The error of the
			// client must only show when there is something to ask about.
			var broken *CardHealthClient
			BeforeEach(func() {
				broken = NewCardHealthClient("/nonexistent/health.sock", "/nonexistent/tls.crt", "/nonexistent/tls.key",
					"/nonexistent/ca.crt", TestTLSServerName)
			})

			It("does not ask the sidecar, so there is no error, when the discoverer finds no device", func() {
				states, err := NewCardmgmtReporter(&stubReporter{name: "lspci"}, broken).Collect()
				Expect(err).NotTo(HaveOccurred())
				Expect(states).To(BeEmpty())
			})

			It("returns the error of the client when there are devices to ask about", func() {
				one := &stubReporter{name: "lspci", states: []types.DeviceState{
					{PciAddress: naGoodCard, Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE}}}
				_, err := NewCardmgmtReporter(one, broken).Collect()
				Expect(err).To(HaveOccurred())
			})
		})

		It("returns no state, and no error, when the sidecar cannot be reached: the other reporters decide", func() {
			start(someNA)
			stop() // nothing answers on the socket any more
			stop = nil
			states, err := cardmgmtReporter().Collect()
			// the client skips every slot whose call fails, and says so in its log; that is not an error
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(BeEmpty())
		})
	})

	Describe("the lspci reporter on its own", func() {
		It("reports all eight devices from lspci, and a revision ff card and its VF as IN_ERROR", func() {
			start(someNA)
			states := collect(lspciReporter())
			Expect(states).To(HaveLen(len(allAddresses)))
			for _, a := range allAddresses {
				want := pb.DEVICE_STATE_ONLINE
				if a == naFaultCard || a == naFaultVF {
					want = pb.DEVICE_STATE_IN_ERROR
				}
				expect(states, a, want, "lspci")
			}
		})
	})

	Describe("strict mode off (the default): the other reporters decide for na", func() {
		BeforeEach(func() { start(someNA) })

		It("the cardmgmt reporter reports only the cards that card management knows, and their VFs", func() {
			states := collect(cardmgmtReporter())
			Expect(states).To(HaveLen(4))
			expect(states, knownCard, pb.DEVICE_STATE_ONLINE, "cardmgmt")
			expect(states, knownVF, pb.DEVICE_STATE_ONLINE, "cardmgmt")
			expect(states, failedCard, pb.DEVICE_STATE_IN_ERROR, "cardmgmt")
			expect(states, failedVF, pb.DEVICE_STATE_IN_ERROR, "cardmgmt")
			for _, a := range []string{naGoodCard, naGoodVF, naFaultCard, naFaultVF} {
				Expect(states).NotTo(HaveKey(a), "na leaves %s to the other reporters", a)
			}
		})

		It("merged: a card with na keeps the state lspci gave it, a fault included", func() {
			states := merged()
			Expect(states).To(HaveLen(len(allAddresses)))
			expect(states, naGoodCard, pb.DEVICE_STATE_ONLINE, "lspci")
			expect(states, naGoodVF, pb.DEVICE_STATE_ONLINE, "lspci")
			expect(states, naFaultCard, pb.DEVICE_STATE_IN_ERROR, "lspci")
			expect(states, naFaultVF, pb.DEVICE_STATE_IN_ERROR, "lspci")
		})

		It("merged: a card that card management knows follows card management, a fault it reports included", func() {
			states := merged()
			expect(states, knownCard, pb.DEVICE_STATE_ONLINE, "cardmgmt")
			expect(states, knownVF, pb.DEVICE_STATE_ONLINE, "cardmgmt")
			expect(states, failedCard, pb.DEVICE_STATE_IN_ERROR, "cardmgmt") // lspci said fine
			expect(states, failedVF, pb.DEVICE_STATE_IN_ERROR, "cardmgmt")
		})

		It("logs the first na slot once, however many times the reporter runs", func() {
			for i := 0; i < 3; i++ {
				collect(cardmgmtReporter())
			}
			Expect(logged).To(HaveLen(1))
			Expect(logged[0]).To(ContainSubstring("status na"))
		})
	})

	Describe("strict mode on: a slot with na is UNSPECIFIED", func() {
		BeforeEach(func() {
			start(someNA)
			client.SetStrictNA(true)
		})

		It("the cardmgmt reporter also reports the na cards, and their VFs, as UNSPECIFIED", func() {
			states := collect(cardmgmtReporter())
			Expect(states).To(HaveLen(len(allAddresses)))
			for _, a := range []string{naGoodCard, naGoodVF, naFaultCard, naFaultVF} {
				expect(states, a, pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED, "cardmgmt")
			}
			expect(states, knownCard, pb.DEVICE_STATE_ONLINE, "cardmgmt")
			expect(states, failedCard, pb.DEVICE_STATE_IN_ERROR, "cardmgmt")
		})

		It("merged: a card that lspci reports as fine is UNSPECIFIED, so it is not offered", func() {
			states := merged()
			expect(states, naGoodCard, pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED, "cardmgmt")
			expect(states, naGoodVF, pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED, "cardmgmt")
		})

		It("merged: a card that lspci reports as faulty is not ONLINE either", func() {
			// Merge lets a state that is not ONLINE replace another one of lower priority, so the lspci
			// IN_ERROR is replaced by UNSPECIFIED here. Neither is offered; only the metric label differs.
			states := merged()
			Expect(states[naFaultCard].State).NotTo(Equal(pb.DEVICE_STATE_ONLINE))
			Expect(states[naFaultVF].State).NotTo(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("merged: the cards that card management knows are the same as with strict mode off", func() {
			states := merged()
			expect(states, knownCard, pb.DEVICE_STATE_ONLINE, "cardmgmt")
			expect(states, knownVF, pb.DEVICE_STATE_ONLINE, "cardmgmt")
			expect(states, failedCard, pb.DEVICE_STATE_IN_ERROR, "cardmgmt")
			expect(states, failedVF, pb.DEVICE_STATE_IN_ERROR, "cardmgmt")
		})

		It("does not log the na message, which is only for slots that are left to the other reporters", func() {
			collect(cardmgmtReporter())
			Expect(logged).To(BeEmpty())
		})

		It("goes back to leaving na to the other reporters when strict mode is turned off again", func() {
			client.SetStrictNA(false)
			expect(merged(), naGoodCard, pb.DEVICE_STATE_ONLINE, "lspci")
		})
	})

	Describe("a card management that predates DRA and answers na for every slot", func() {
		BeforeEach(func() { start(allNA) })

		It("strict mode off: the node looks exactly as it does with lspci alone, so nothing is withdrawn", func() {
			alone, err := Merge([]types.Reporter{lspciReporter()})
			Expect(err).NotTo(HaveOccurred())

			states := merged()
			Expect(states).To(Equal(byAddress(alone)))
			Expect(collect(cardmgmtReporter())).To(BeEmpty(), "card management contributes nothing")
		})

		It("strict mode on: no device is ONLINE, so nothing on the node is offered", func() {
			client.SetStrictNA(true)
			states := merged()
			Expect(states).To(HaveLen(len(allAddresses)))
			for _, a := range allAddresses {
				Expect(states[a].State).NotTo(Equal(pb.DEVICE_STATE_ONLINE), "%s must not be offered", a)
				Expect(states[a].Source).To(Equal("cardmgmt"))
			}
		})
	})
})
