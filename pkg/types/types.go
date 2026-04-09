/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package types

import (
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
)

type DeviceState struct {
	PciAddress string
	Type       pb.DEVICE_TYPE
	State      pb.DEVICE_STATE
}

func (d DeviceState) Device() *pb.Device {
	return &pb.Device{
		DeviceID: &pb.DeviceID{
			PCIAddress: d.PciAddress,
		},
		DeviceType:  d.Type,
		DeviceState: d.State,
	}
}
