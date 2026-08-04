/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// PFVDID and VFVDID are the PCI vendor:device IDs for IBM Spyre PF and VF
// devices. They are kept here for reference by tests.
const (
	PFVDID = "1014:06a7"
	VFVDID = "1014:06a8"
)

// LSPCIReporter collects device states by running `lspci -vvvnn` and parsing
// the output. It carries the lowest priority (PriorityLSPCI = 1).
type LSPCIReporter struct{}

func (r *LSPCIReporter) Name() string  { return "lspci" }
func (r *LSPCIReporter) Priority() int { return types.PriorityLSPCI }

// Collect executes lspci, parses the output, and stamps each entry with the
// lspci source name and priority.
func (r *LSPCIReporter) Collect() ([]types.DeviceState, error) {
	out, err := exec.Command("sh", "-c", "lspci -vvvnn 2>/dev/null").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("lspci reporter: %w", err)
	}
	states := parseLSPCI(string(out))
	stamp(states, r.Name(), r.Priority())
	return states, nil
}

// ---------------------------------------------------------------------------
// lspci output parser — private to this package
// ---------------------------------------------------------------------------

type bitValue struct {
	Present bool // token is present or not
	Enabled bool // '+' means true, '-' means false
}

type deviceInfo struct {
	// From header
	PCIAddress string // supports "xx:xx.x" and "xxxx:xx:xx.x"
	VenDevID   string // ####:#### hex value
	Revision   string // ## hex value

	// From body
	PERR         bitValue
	SERR         bitValue
	TAbort       bitValue
	MAbort       bitValue
	DState       string // looking for "D0"
	DevStaFatal  bool   // true if DevSta contains "FatalErr+"
	KernelDriver string
}

// Splitter: lspci device stanzas delimited by blank lines

func splitByBlankLines(s string) []string {
	sep := regexp.MustCompile(`(?m)(?:\r?\n[ \t]*)+\r?\n`)
	parts := sep.Split(s, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(strings.TrimSpace(p)) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// Parse header

var (
	// PCI Pattern
	pciPat = `(?i)(?P<pci>(?:[0-9a-f]{4}:)?[0-9a-f]{2}:[0-9a-f]{2}\.[0-7])`

	// Sample Header styles
	// 63:00.0 Processing accelerators [1200]: IBM Device [1014:06a7] (rev 02)
	// 0483:70:00.0 Processing accelerators [1200]: IBM Spyre Accelerator [1014:06a7] (rev 02)
	// 2b:00.0 1200: 1014:06a7 (rev 02)
	hdr = regexp.MustCompile(`(?i)^` + pciPat +
		`(?P<stuff>.*?)\s+\[?(?P<vendev>[0-9a-f]{4}:[0-9a-f]{4})\]?\s+\(rev\s+(?P<rev>[0-9a-f]{2})\)\s*$`)
)

func parseHeader(firstLine string) (pci, vendev, rev string, ok bool) {
	if m := hdr.FindStringSubmatch(firstLine); len(m) > 0 {
		names := hdr.SubexpNames()
		mp := map[string]string{}
		for i := range m {
			if names[i] != "" {
				mp[names[i]] = m[i]
			}
		}
		return mp["pci"], mp["vendev"], mp["rev"], true
	}
	return "", "", "", false
}

// Parse body

var (
	// Matches tokens like ">SERR+" "<PERR-" "TAbort+" "MAbort-"
	errTokenRe = regexp.MustCompile(`(?i)(?:[<>])?(PERR|SERR|TAbort|MAbort)([+-])`)

	// DevSta line: look for FatalErr+ or FatalErr-
	fatalRe = regexp.MustCompile(`(?i)\bFatalErr([+-])`)

	// Power Mgmt status line: "Status: D0 ..."
	pmStatusRe = regexp.MustCompile(`(?i)\bStatus:\s*(D\d+?)\b`)

	// Kernel driver line
	driverRe = regexp.MustCompile(`(?i)^\s*Kernel driver in use:\s*([^\s]+)`)
)

func parseDetails(stanza string, di *deviceInfo) {
	sc := bufio.NewScanner(bytes.NewReader([]byte(stanza)))
	for sc.Scan() {
		line := sc.Text()

		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "status:") {
			for _, sm := range errTokenRe.FindAllStringSubmatch(line, -1) {
				name := strings.ToUpper(sm[1])
				sign := sm[2] == "+"
				switch name {
				case "PERR":
					di.PERR.Present = true
					di.PERR.Enabled = di.PERR.Enabled || sign // OR across duplicates
				case "SERR":
					di.SERR.Present = true
					di.SERR.Enabled = di.SERR.Enabled || sign
				case "TABORT":
					di.TAbort.Present = true
					di.TAbort.Enabled = di.TAbort.Enabled || sign
				case "MABORT":
					di.MAbort.Present = true
					di.MAbort.Enabled = di.MAbort.Enabled || sign
				}
			}
			if di.DState == "" {
				if m := pmStatusRe.FindStringSubmatch(line); len(m) == 2 {
					di.DState = m[1]
				}
			}
		}

		// DevSta fatal
		if strings.Contains(strings.ToLower(line), "devsta:") {
			if m := fatalRe.FindStringSubmatch(line); len(m) == 2 {
				di.DevStaFatal = (m[1] == "+")
			}
		}

		// Kernel driver in use
		if m := driverRe.FindStringSubmatch(line); len(m) == 2 {
			di.KernelDriver = strings.TrimSpace(m[1])
		}
	}
}

// Parse device stanza

func parseDeviceStanza(stanza string) deviceInfo {
	var di deviceInfo

	firstLine := firstNonEmptyLine(stanza)
	pci, vendev, rev, ok := parseHeader(firstLine)
	if !ok || rev == "01" {
		return di
	}
	if len(pci) == 7 {
		di.PCIAddress = "0000:" + pci
	} else {
		di.PCIAddress = pci
	}
	di.VenDevID = strings.ToLower(vendev)
	di.Revision = strings.ToLower(rev)

	parseDetails(stanza, &di)
	return di
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if len(strings.TrimSpace(line)) > 0 {
			return strings.TrimRight(line, "\r")
		}
	}
	return ""
}

const (
	pfVDID = "1014:06a7"
	vfVDID = "1014:06a8"
)

func parseLSPCI(output string) []types.DeviceState {
	states := make([]types.DeviceState, 0)
	for _, dev := range splitByBlankLines(output) {
		di := parseDeviceStanza(dev)
		if di == (deviceInfo{}) {
			continue
		}
		if di.VenDevID != pfVDID && di.VenDevID != vfVDID {
			continue
		}

		var state pb.DEVICE_STATE
		if di.Revision == "ff" {
			state = pb.DEVICE_STATE_IN_ERROR
		} else {
			state = pb.DEVICE_STATE_ONLINE
		}

		var devType pb.DEVICE_TYPE
		switch di.VenDevID {
		case pfVDID:
			devType = pb.DEVICE_TYPE_PF
		case vfVDID:
			devType = pb.DEVICE_TYPE_VF
		default:
			devType = pb.DEVICE_TYPE_DEVICE_TYPE_UNSPECIFIED
		}

		states = append(states, types.DeviceState{
			PciAddress: di.PCIAddress,
			Type:       devType,
			State:      state,
		})
	}
	return states
}
