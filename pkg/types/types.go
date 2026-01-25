package types

import (
	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
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
