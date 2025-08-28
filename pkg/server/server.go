package server

import (
	"github.com/golang/glog"
	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	"google.golang.org/protobuf/types/known/emptypb"
)

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

var (
	simpleAIUs = []SimplifiedDevice{
		{PciAddress: "00", State: pb.DEVICE_STATE_ONLINE},
		{PciAddress: "01", State: pb.DEVICE_STATE_IN_ERROR},
	}
)

type healthServer struct {
	pb.UnimplementedSpyreHealthServiceServer
	deviceList []*pb.Device
}

func NewServer() *healthServer {
	s := healthServer{
		deviceList: make([]*pb.Device, 0),
	}

	for _, sd := range simpleAIUs {
		s.deviceList = append(s.deviceList, sd.Device())
	}
	return &s

}

func (s *healthServer) RegisterForSpyreDevicesEvents(_ *emptypb.Empty,
	stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsServer) error {
	glog.V(1).Infof("[Server] Got a request")
	devices := pb.Devices{
		Devices: s.deviceList,
	}
	if err := stream.Send(&devices); err != nil {
		return err
	}
	return nil
}
