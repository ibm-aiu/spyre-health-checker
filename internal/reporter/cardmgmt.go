/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

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
