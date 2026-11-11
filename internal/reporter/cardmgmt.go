/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"strings"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

// PFSlots returns the PCI addresses of the physical functions among the discovered devices.
// Card management only tracks physical functions: for a VF slot its worker returns an empty status,
// which the health API reports as "unhealthy". Querying VF slots would therefore mark every VF in error,
// so only PF slots are asked.
func PFSlots(discovered []types.DeviceState) []string {
	slots := make([]string, 0, len(discovered))
	for _, d := range discovered {
		if d.Type == pb.DEVICE_TYPE_PF {
			slots = append(slots, d.PciAddress)
		}
	}
	return slots
}

// pfAddressOf returns the address of function 0 in the same PCI slot ("0000:3b:00.2" -> "0000:3b:00.0").
func pfAddressOf(addr string) string {
	i := strings.LastIndex(addr, ".")
	if i < 0 {
		return ""
	}
	return addr[:i] + ".0"
}

// WithVFStates returns pfStates plus one state per discovered VF whose parent PF has a state in pfStates;
// the VF takes the state of its PF, so the VFs of a failed card are not offered as healthy.
// The parent is function 0 of the same PCI slot, which is how the PF and VFs of a Spyre card are laid out.
// A VF whose PF has no state (not queried, or the query was skipped) gets none and keeps its lspci state.
func WithVFStates(discovered, pfStates []types.DeviceState) []types.DeviceState {
	byAddr := make(map[string]types.DeviceState, len(pfStates))
	for _, s := range pfStates {
		byAddr[s.PciAddress] = s
	}
	out := append(make([]types.DeviceState, 0, len(pfStates)+len(discovered)), pfStates...)
	for _, d := range discovered {
		if d.Type != pb.DEVICE_TYPE_VF {
			continue
		}
		if pf, ok := byAddr[pfAddressOf(d.PciAddress)]; ok {
			out = append(out, types.DeviceState{PciAddress: d.PciAddress, Type: pb.DEVICE_TYPE_VF, State: pf.State})
		}
	}
	return out
}

// CardmgmtReporter collects device states from the card management service.
// It carries medium priority (PriorityCardmgmt = 5).
//
// CollectFn must be provided by the caller; it should query the card management
// service and return raw DeviceState entries. Source and Priority fields are
// overwritten by the reporter before returning.
type CardmgmtReporter struct {
	CollectFn func() ([]types.DeviceState, error)
}

func (r *CardmgmtReporter) Name() string  { return "cardmgmt" }
func (r *CardmgmtReporter) Priority() int { return types.PriorityCardmgmt }

// Collect calls CollectFn and stamps the results. Returns an empty slice
// without error when CollectFn is nil.
func (r *CardmgmtReporter) Collect() ([]types.DeviceState, error) {
	if r.CollectFn == nil {
		return nil, nil
	}
	states, err := r.CollectFn()
	if err != nil {
		return nil, err
	}
	stamp(states, r.Name(), r.Priority())
	return states, nil
}

// CardManagement manages card service state.
type CardManagement struct {
	service string
}

// SimplifiedDevice represents a device with basic PCI and state information.
type SimplifiedDevice struct {
	PciAddress string
	State      pb.DEVICE_STATE
}

// Device converts SimplifiedDevice to a protobuf Device.
func (d SimplifiedDevice) Device() *pb.Device {
	return &pb.Device{
		DeviceID: &pb.DeviceID{
			PCIAddress: d.PciAddress,
		},
		DeviceType:  pb.DEVICE_TYPE_PF,
		DeviceState: d.State,
	}
}

// ToDeviceState converts a SimplifiedDevice to a types.DeviceState tagged with
// source "cardmgmt".
func (d SimplifiedDevice) ToDeviceState() types.DeviceState {
	return types.DeviceState{
		PciAddress: d.PciAddress,
		Type:       pb.DEVICE_TYPE_PF,
		State:      d.State,
		Source:     "cardmgmt",
		Priority:   types.PriorityCardmgmt,
	}
}

// InitCardManagement initializes a CardManagement instance.
func InitCardManagement() *CardManagement {
	return &CardManagement{service: "cardmanagement"}
}

// GetCardStatus returns the card status as a protobuf Device.
func (cm *CardManagement) GetCardStatus(d SimplifiedDevice) *pb.Device {
	return &pb.Device{
		DeviceID: &pb.DeviceID{
			PCIAddress: d.PciAddress,
		},
		DeviceType:  pb.DEVICE_TYPE_PF,
		DeviceState: d.State,
	}
}

// Connect connects to card management service to get card status
// for now we stub it
func (d SimplifiedDevice) CollectFn() ([]types.DeviceState, error) {
	return []types.DeviceState{}, nil
}
