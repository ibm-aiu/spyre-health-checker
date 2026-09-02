/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// errReporter is a test double that always returns an error from Collect.
type errReporter struct {
	name string
}

func (e *errReporter) Name() string  { return e.name }
func (e *errReporter) Priority() int { return 0 }
func (e *errReporter) Collect() ([]types.DeviceState, error) {
	return nil, errors.New("collect failed")
}

// mergeTC is the input/output shape for a single Merge table entry.
type mergeTC struct {
	reporters     []types.Reporter
	wantLen       int
	wantErr       bool
	wantErrSubstr []string
	wantSource    string // if non-empty, result[0].Source must equal this
	wantState     pb.DEVICE_STATE
	// wantByAddr checks Source and State per PCI address (used for multi-device entries).
	wantByAddr map[string]types.DeviceState
}

var _ = DescribeTable("Merge",
	func(tc mergeTC) {
		result, err := Merge(tc.reporters)

		if tc.wantErr {
			Expect(err).NotTo(BeNil())
			for _, sub := range tc.wantErrSubstr {
				Expect(err.Error()).To(ContainSubstring(sub))
			}
		} else {
			Expect(err).To(BeNil())
		}

		Expect(result).To(HaveLen(tc.wantLen))

		if tc.wantSource != "" {
			Expect(result[0].Source).To(Equal(tc.wantSource))
			Expect(result[0].State).To(Equal(tc.wantState))
		}

		if len(tc.wantByAddr) > 0 {
			byAddr := make(map[string]types.DeviceState, len(result))
			for _, s := range result {
				byAddr[s.PciAddress] = s
			}
			for addr, want := range tc.wantByAddr {
				got, ok := byAddr[addr]
				Expect(ok).To(BeTrue(), "expected PCI address %s in result", addr)
				Expect(got.Source).To(Equal(want.Source), "source mismatch for %s", addr)
				Expect(got.State).To(Equal(want.State), "state mismatch for %s", addr)
			}
		}
	},

	Entry("no reporters → empty result",
		mergeTC{
			reporters: []types.Reporter{},
		}),

	Entry("single reporter with no states → empty result",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: "empty", priority: types.PriorityLSPCI, states: []types.DeviceState{}},
			},
		}),

	Entry("single reporter with states → all states returned",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
					{PciAddress: TestPCIAddress2, State: pb.DEVICE_STATE_IN_ERROR, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
			},
			wantLen: 2,
		}),

	// existing=ERROR, new=ONLINE → no override; unhealthy is sticky.
	Entry("higher-priority reporter cannot promote error→online",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_IN_ERROR, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
				&stubReporter{name: CardmgmtSource, priority: types.PriorityCardmgmt, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: CardmgmtSource, Priority: types.PriorityCardmgmt}, // nolint:lll
				}},
			},
			wantLen:    1,
			wantSource: LsPCISource,
			wantState:  pb.DEVICE_STATE_IN_ERROR,
		}),

	// existing=ONLINE, new=ERROR → override; higher-priority can mark a healthy device unhealthy.
	Entry("higher-priority reporter can downgrade online→error",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
				&stubReporter{name: CardmgmtSource, priority: types.PriorityCardmgmt, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_IN_ERROR, Source: CardmgmtSource, Priority: types.PriorityCardmgmt}, // nolint:lll
				}},
			},
			wantLen:    1,
			wantSource: CardmgmtSource,
			wantState:  pb.DEVICE_STATE_IN_ERROR,
		}),

	// existing=ONLINE, new=ONLINE → override; higher-priority wins.
	Entry("higher-priority reporter overrides online→online",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
				&stubReporter{name: CardmgmtSource, priority: types.PriorityCardmgmt, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: CardmgmtSource, Priority: types.PriorityCardmgmt}, // nolint:lll
				}},
			},
			wantLen:    1,
			wantSource: CardmgmtSource,
			wantState:  pb.DEVICE_STATE_ONLINE,
		}),

	// existing=ERROR, new=ERROR → override; higher-priority wins.
	Entry("higher-priority reporter overrides error→error",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_IN_ERROR, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
				&stubReporter{name: CardmgmtSource, priority: types.PriorityCardmgmt, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_IN_ERROR, Source: CardmgmtSource, Priority: types.PriorityCardmgmt}, // nolint:lll
				}},
			},
			wantLen:    1,
			wantSource: CardmgmtSource,
			wantState:  pb.DEVICE_STATE_IN_ERROR,
		}),

	// Equal priority: first encountered is always kept regardless of state.
	Entry("equal-priority: first (cardmgmt) encountered is always kept",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: CardmgmtSource, priority: types.PriorityCardmgmt, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_IN_ERROR, Source: CardmgmtSource, Priority: types.PriorityCardmgmt}, // nolint:lll
				}},
				&stubReporter{name: "second", priority: types.PriorityCardmgmt, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: "second", Priority: types.PriorityCardmgmt},
				}},
			},
			wantLen:    1,
			wantSource: CardmgmtSource,
			wantState:  pb.DEVICE_STATE_IN_ERROR,
		}),

	Entry("non-conflicting addresses from different reporters are all present",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
				&stubReporter{name: CardmgmtSource, priority: types.PriorityCardmgmt, states: []types.DeviceState{
					{PciAddress: TestPCIAddress2, Source: CardmgmtSource, Priority: types.PriorityCardmgmt},
				}},
			},
			wantLen: 2,
		}),

	Entry("partial failure: errors accumulated, partial results returned",
		mergeTC{
			reporters: []types.Reporter{
				&errReporter{name: "bad1"},
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
				&errReporter{name: "bad2"},
			},
			wantLen:       1,
			wantErr:       true,
			wantErrSubstr: []string{"2 reporter error(s)", `"bad1"`, `"bad2"`},
		}),

	Entry("all reporters fail → error, no results",
		mergeTC{
			reporters:     []types.Reporter{&errReporter{name: "bad1"}, &errReporter{name: "bad2"}},
			wantErr:       true,
			wantErrSubstr: []string{"2 reporter error(s)"},
		}),

	// All four state-transition cases exercised across four devices in one pass:
	// 1a: lspci=ERROR, cardmgmt=ONLINE  → lspci/ERROR kept   (sticky unhealthy, no promotion)
	// 1b: lspci=ONLINE, cardmgmt=ERROR  → cardmgmt/ERROR wins (higher-priority downgrades)
	// 1c: lspci=ONLINE, cardmgmt=ONLINE → cardmgmt/ONLINE wins (higher-priority, same state)
	// 1d: lspci=ERROR,  cardmgmt=ERROR  → cardmgmt/ERROR wins  (higher-priority, same state)
	Entry("all four transition cases across multiple devices",
		mergeTC{
			reporters: []types.Reporter{
				&stubReporter{name: LsPCISource, priority: types.PriorityLSPCI, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_IN_ERROR, Source: LsPCISource, Priority: types.PriorityLSPCI},
					{PciAddress: TestPCIAddress2, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
					{PciAddress: TestPCIAddress3, State: pb.DEVICE_STATE_ONLINE, Source: LsPCISource, Priority: types.PriorityLSPCI},
					{PciAddress: TestPCIAddress4, State: pb.DEVICE_STATE_IN_ERROR, Source: LsPCISource, Priority: types.PriorityLSPCI},
				}},
				&stubReporter{name: CardmgmtSource, priority: types.PriorityCardmgmt, states: []types.DeviceState{
					{PciAddress: TestPCIAddress, State: pb.DEVICE_STATE_ONLINE, Source: CardmgmtSource, Priority: types.PriorityCardmgmt},    // nolint:lll
					{PciAddress: TestPCIAddress2, State: pb.DEVICE_STATE_IN_ERROR, Source: CardmgmtSource, Priority: types.PriorityCardmgmt}, // nolint:lll
					{PciAddress: TestPCIAddress3, State: pb.DEVICE_STATE_ONLINE, Source: CardmgmtSource, Priority: types.PriorityCardmgmt},   // nolint:lll
					{PciAddress: TestPCIAddress4, State: pb.DEVICE_STATE_IN_ERROR, Source: CardmgmtSource, Priority: types.PriorityCardmgmt}, // nolint:lll
				}},
			},
			wantLen: 4,
			wantByAddr: map[string]types.DeviceState{
				TestPCIAddress:  {Source: LsPCISource, State: pb.DEVICE_STATE_IN_ERROR},
				TestPCIAddress2: {Source: CardmgmtSource, State: pb.DEVICE_STATE_IN_ERROR},
				TestPCIAddress3: {Source: CardmgmtSource, State: pb.DEVICE_STATE_ONLINE},
				TestPCIAddress4: {Source: CardmgmtSource, State: pb.DEVICE_STATE_IN_ERROR},
			},
		}),
)
