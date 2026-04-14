/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package healthcheck

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

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

	// First line should be the header
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

	// Parse body
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

// nolint:unused
func debugoutput() {
	sections := splitByBlankLines("<<replace with lspci output>>")
	for i, sec := range sections {
		di := parseDeviceStanza(sec)
		if di == (deviceInfo{}) {
			continue
		}
		fmt.Printf("=== Device %d ===\n", i+1)
		fmt.Printf("PCI Address : %s\n", di.PCIAddress)
		fmt.Printf("Vendor:Device: %s\n", di.VenDevID)
		fmt.Printf("Revision    : %s\n", di.Revision)
		fmt.Printf("PERR        : present=%v enabled=%v\n", di.PERR.Present, di.PERR.Enabled)
		fmt.Printf("SERR        : present=%v enabled=%v\n", di.SERR.Present, di.SERR.Enabled)
		fmt.Printf("TAbort      : present=%v enabled=%v\n", di.TAbort.Present, di.TAbort.Enabled)
		fmt.Printf("MAbort      : present=%v enabled=%v\n", di.MAbort.Present, di.MAbort.Enabled)
		fmt.Printf("D-State     : %s\n", di.DState)
		fmt.Printf("DevSta Fatal: %v\n", di.DevStaFatal)
		fmt.Printf("KernelDriver: %s\n", di.KernelDriver)
		fmt.Println()
	}
}

const (
	PFVDID = "1014:06a7"
	VFVDID = "1014:06a8"
)

func parseLSPCI(output string) []types.DeviceState {
	states := make([]types.DeviceState, 0)
	devices := splitByBlankLines(output)

	for _, dev := range devices {
		di := parseDeviceStanza(dev)
		if di == (deviceInfo{}) {
			continue
		}

		if di.VenDevID != PFVDID && di.VenDevID != VFVDID {
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
		case PFVDID:
			devType = pb.DEVICE_TYPE_PF
		case VFVDID:
			devType = pb.DEVICE_TYPE_VF
		default:
			devType = pb.DEVICE_TYPE_DEVICE_TYPE_UNSPECIFIED
		}

		// Check power state, return STATE_OFFLINE if power state is not D0
		//		if di.DState == "D0" {
		//			state = pb.DEVICE_STATE_ONLINE
		//		} else {
		//			state = pb.DEVICE_STATE_OFFLINE
		//		}

		// return IN_ERROR if SERR+ or Tabort+ or MAbortL or DevStFatal
		//		if (di.PERR.Present && di.PERR.Enabled) ||
		//			(di.SERR.Present && di.SERR.Enabled) ||
		//			(di.TAbort.Present && di.TAbort.Enabled) ||
		//			(di.MAbort.Present && di.MAbort.Enabled) ||
		//			di.DevStaFatal {
		//			state = pb.DEVICE_STATE_IN_ERROR
		//		}

		states = append(states, types.DeviceState{PciAddress: di.PCIAddress, Type: devType, State: state})
	}
	return states
}
