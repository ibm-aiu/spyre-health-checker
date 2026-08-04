/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package servicediscovery

import (
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
)

type CardManagement struct {
	service string
}

type SimplifiedDevice struct {
	PciAddress string
	State      pb.DEVICE_STATE
}

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
	}
}

func InitCardManagement() *CardManagement {
	return &CardManagement{service: "cardmanagement"}
}

func (cm *CardManagement) GetCardStatus(d SimplifiedDevice) *pb.Device {
	return &pb.Device{
		DeviceID: &pb.DeviceID{
			PCIAddress: d.PciAddress,
		},
		DeviceType:  pb.DEVICE_TYPE_PF,
		DeviceState: d.State,
	}
}
