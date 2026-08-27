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

// Reporter priority levels — higher value wins when multiple reporters provide
// data for the same device.
const (
	PriorityLSPCI    = 1 // lowest — simple hardware scan
	PriorityCardmgmt = 5 // medium — card management service
)

// Reporter is the interface that every device-state source must implement.
// Name returns a human-readable identifier (used as DeviceState.Source).
// Priority returns the reporter's priority level; when two reporters supply
// a state for the same PCI address the one with the higher priority wins.
// Collect returns the current device states or an error.
type Reporter interface {
	Name() string
	Priority() int
	Collect() ([]DeviceState, error)
}

type DeviceState struct {
	PciAddress string
	Type       pb.DEVICE_TYPE
	State      pb.DEVICE_STATE
	// Source identifies who produced this state.
	// Known values: "lspci", "cardmgmt".
	Source string
	// Priority mirrors the producing reporter's priority level and is used
	// by the merge logic to decide which state wins when multiple reporters
	// disagree on the same device.
	Priority int
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
