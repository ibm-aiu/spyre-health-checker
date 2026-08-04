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

// TODO: define a Reporter interface (e.g. Reporter interface { Name() string; Collect() ([]DeviceState, error) })
// so that each source — lspci, cardmgmt, and any future reporter — is a first-class pluggable type
// rather than a plain string constant.

type DeviceState struct {
	PciAddress string
	Type       pb.DEVICE_TYPE
	State      pb.DEVICE_STATE
	// Source identifies who produced this state.
	// Known values: "lspci", "cardmgmt", "override".
	Source string
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
