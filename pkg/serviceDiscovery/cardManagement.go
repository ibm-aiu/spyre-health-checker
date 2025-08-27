package servicediscovery

import (
	pb "ibm.com/vitals/pkg/proto/spyre_health"
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
