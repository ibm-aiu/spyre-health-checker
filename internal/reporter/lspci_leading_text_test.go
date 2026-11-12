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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
)

// zVFBlock returns an lspci -vvvnn block of a Spyre VF as seen on s390x (PCI domain in front of the address).
func zVFBlock(index int) string {
	return fmt.Sprintf(
		"%04d:00:00.0 Processing accelerators [1200]: IBM Spyre Accelerator Virtual Function [1014:06a8] (rev 02)\n"+
			"\tPhysical Slot: %08d\n"+
			"\tControl: I/O- Mem- BusMaster+ SpecCycle- MemWINV- VGASnoop- ParErr- Stepping- SERR- FastB2B- DisINTx-\n"+
			"\tStatus: Cap+ 66MHz- UDF- FastB2B- ParErr- DEVSEL=fast >TAbort- <TAbort- <MAbort- >SERR- <PERR- INTx-\n"+
			"\tIOMMU group: %d\n"+
			"\tCapabilities: <access denied>\n"+
			"\tKernel driver in use: vfio-pci\n",
		index+1, index, index)
}

func zOutput(count int) string {
	blocks := make([]string, 0, count)
	for i := 0; i < count; i++ {
		blocks = append(blocks, zVFBlock(i))
	}
	return strings.Join(blocks, "\n")
}

func addresses(output string) []string {
	states := parseLSPCI(output)
	out := make([]string, 0, len(states))
	for _, s := range states {
		out = append(out, s.PciAddress)
	}
	return out
}

// firstZAddress is the address of the first device of the s390x style sample.
const firstZAddress = "0001:00:00.0"

var fourAddresses = []string{firstZAddress, "0002:00:00.0", "0003:00:00.0", "0004:00:00.0"}

var _ = Describe("LSPCIReporter text in front of the first device", func() {
	const warning = "pcilib: Cannot open /sys/bus/pci/devices/0001:00:00.0/config: Permission denied\n"

	It("finds all four devices in clean output (baseline)", func() {
		Expect(addresses(zOutput(4))).To(Equal(fourAddresses))
	})

	It("finds the first device when a warning line sits directly in front of it", func() {
		// This is the reported case: stderr merged into the output, no blank line between the warning and the header.
		Expect(addresses(warning + zOutput(4))).To(Equal(fourAddresses))
	})

	It("finds the first device behind several warning lines", func() {
		Expect(addresses(warning + "lspci: Unable to load libkmod resolving\n" + zOutput(4))).To(Equal(fourAddresses))
	})

	It("finds the first device when a warning is followed by a blank line", func() {
		Expect(addresses(warning + "\n" + zOutput(4))).To(Equal(fourAddresses))
	})

	It("finds the first device when the warning has leading blanks and Windows line endings", func() {
		Expect(addresses("  " + strings.TrimSpace(warning) + "\r\n" + zOutput(4))).To(Equal(fourAddresses))
	})

	It("keeps the details of the first device (driver and state) after skipping the warning", func() {
		states := parseLSPCI(warning + zOutput(2))
		Expect(states).To(HaveLen(2))
		Expect(states[0].PciAddress).To(Equal(firstZAddress))
		Expect(states[0].Type).To(Equal(pb.DEVICE_TYPE_VF))
		Expect(states[0].State).To(Equal(pb.DEVICE_STATE_ONLINE))
	})

	It("does not turn a warning that mentions a header-like text in an indented line into a device", func() {
		// Only a line that starts in column 0 can be a header.
		text := "lspci: note\n\t0009:00:00.0 Processing accelerators [1200]: IBM Spyre Accelerator [1014:06a7] (rev 02)\n"
		Expect(addresses(text)).To(BeEmpty())
	})
})

var _ = Describe("LSPCIReporter skipped blocks", func() {
	const rev01 = "0005:00:00.0 Processing accelerators [1200]: " +
		"IBM Spyre Accelerator Virtual Function [1014:06a8] (rev 01)\n\tKernel driver in use: vfio-pci\n"
	const other = "00:00.0 Host bridge [0600]: Intel Corporation Device [8086:1234] (rev 01)\n\tFlags: bus master\n"

	It("reports a Spyre block that has no parsable header, with the first one as the example", func() {
		bad1 := "0006:00:00.0 Processing accelerators [1200]: " +
			"IBM Spyre Accelerator Virtual Function [1014:06a8]\n\tKernel driver in use: vfio-pci\n"
		bad2 := "0007:00:00.0 Processing accelerators: IBM Spyre Accelerator Virtual Function\n"
		states, skips := parseLSPCIWithSkips(zOutput(2) + "\n" + bad1 + "\n" + bad2)
		Expect(states).To(HaveLen(2))
		Expect(skips).To(HaveKey(skipNoHeader))
		Expect(skips[skipNoHeader].count).To(Equal(2))
		Expect(skips[skipNoHeader].example).To(ContainSubstring("0006:00:00.0"))
	})

	It("does not report text without a header that does not mention Spyre (a warning, for example)", func() {
		states, skips := parseLSPCIWithSkips("lspci: some warning\n\n" + zOutput(2))
		Expect(states).To(HaveLen(2))
		Expect(skips).To(BeEmpty())
		Expect(skipSummary(skips)).To(BeEmpty())
	})

	It("reports a Spyre device with revision 01 but ignores other vendors silently", func() {
		states, skips := parseLSPCIWithSkips(zOutput(1) + "\n" + rev01 + "\n" + other)
		Expect(states).To(HaveLen(1))
		Expect(skips).To(HaveKey(skipRev01))
		Expect(skips[skipRev01].count).To(Equal(1))
		Expect(skips[skipRev01].example).To(ContainSubstring("0005:00:00.0"))
		Expect(skips).NotTo(HaveKey(skipNoHeader))
	})

	It("summarises the counts per reason, and says nothing when nothing was skipped", func() {
		_, none := parseLSPCIWithSkips(zOutput(4))
		Expect(skipSummary(none)).To(BeEmpty())
		_, some := parseLSPCIWithSkips(zOutput(3) + "\n" + rev01)
		Expect(skipSummary(some)).To(Equal(", skipped 1 (rev-01: 1)"))
	})

	It("finds the same 14 devices in the full real-world sample as before and reports no unparsable Spyre block", func() {
		states, skips := parseLSPCIWithSkips(sampleLSPCI)
		Expect(states).To(HaveLen(14))
		// The other-vendor-ID and the revision ff devices of the sample have parsable headers and are not skip reasons.
		Expect(skips).NotTo(HaveKey(skipNoHeader))
	})
})

var _ = Describe("log helpers", func() {
	It("firstTime is true once per condition", func() {
		lspciLogged.Clear()
		Expect(firstTime("a")).To(BeTrue())
		Expect(firstTime("a")).To(BeFalse())
		Expect(firstTime("b")).To(BeTrue())
	})

	It("clip shortens long text and leaves short text alone", func() {
		Expect(clip("short")).To(Equal("short"))
		Expect(clip(strings.Repeat("x", maxLogText+20))).To(Equal(strings.Repeat("x", maxLogText) + "..."))
	})
})

var _ = Describe("LSPCIReporter.Collect with a fake lspci", func() {
	var logged []string
	var savedDebugLog func(string, ...any)

	// fakeLSPCI puts a script called lspci first on PATH that prints stdout text and stderr text.
	fakeLSPCI := func(stdout, stderr string, exitCode int) {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "out.txt"), []byte(stdout), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "err.txt"), []byte(stderr), 0o600)).To(Succeed())
		script := fmt.Sprintf("#!/bin/sh\ncat %q >&2\ncat %q\nexit %d\n",
			filepath.Join(dir, "err.txt"), filepath.Join(dir, "out.txt"), exitCode)
		Expect(os.WriteFile(filepath.Join(dir, "lspci"), []byte(script), 0o700)).To(Succeed()) //nolint:gosec // test script
		orig := os.Getenv("PATH")
		Expect(os.Setenv("PATH", dir+string(os.PathListSeparator)+orig)).To(Succeed())
		DeferCleanup(func() { Expect(os.Setenv("PATH", orig)).To(Succeed()) })
	}

	BeforeEach(func() {
		logged = nil
		savedDebugLog = lspciDebugLog
		lspciDebugLog = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
		lspciLogged.Clear()
	})

	AfterEach(func() {
		lspciDebugLog = savedDebugLog
		lspciLogged.Clear()
	})

	It("finds all four devices although lspci writes a warning on stderr (the reported case)", func() {
		fakeLSPCI(zOutput(4), "pcilib: Cannot open /sys/bus/pci/devices/0001:00:00.0/config: Permission denied\n", 0)
		states, err := (&LSPCIReporter{}).Collect()
		Expect(err).NotTo(HaveOccurred())
		Expect(states).To(HaveLen(4))
		Expect(states[0].PciAddress).To(Equal(firstZAddress))
		Expect(logged).To(ContainElement(ContainSubstring("wrote to stderr")))
		Expect(logged).To(ContainElement(ContainSubstring("lspci found 4 Spyre device(s)")))
	})

	It("logs a given stderr message only once however often lspci runs", func() {
		fakeLSPCI(zOutput(4), "lspci: some warning\n", 0)
		for i := 0; i < 5; i++ {
			_, err := (&LSPCIReporter{}).Collect()
			Expect(err).NotTo(HaveOccurred())
		}
		count := 0
		for _, l := range logged {
			if strings.Contains(l, "wrote to stderr") {
				count++
			}
		}
		Expect(count).To(Equal(1))
	})

	It("writes the skip summary in the usual log line and the first example once", func() {
		bad := "0006:00:00.0 Processing accelerators: IBM Spyre Accelerator Virtual Function\n"
		fakeLSPCI(zOutput(3)+"\n"+bad, "", 0)
		for i := 0; i < 3; i++ {
			_, err := (&LSPCIReporter{}).Collect()
			Expect(err).NotTo(HaveOccurred())
		}
		summary, examples := 0, 0
		for _, l := range logged {
			if strings.Contains(l, "lspci found 3 Spyre device(s), skipped 1 (no-header: 1)") {
				summary++
			}
			if strings.Contains(l, "first example") {
				examples++
			}
		}
		Expect(summary).To(Equal(3))
		Expect(examples).To(Equal(1))
	})

	It("keeps the error text of a failing lspci, including its stderr", func() {
		fakeLSPCI("", "lspci: cannot open the PCI bus\n", 2)
		_, err := (&LSPCIReporter{}).Collect()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("lspci -vvvnn failed"))
		Expect(err.Error()).To(ContainSubstring("cannot open the PCI bus"))
	})
})
