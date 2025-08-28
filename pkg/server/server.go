package server

import (
	"net"
	"os"
	"sync"

	"github.com/golang/glog"
	"github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/utils"
	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	"github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	pseudoDeviceHealths = utils.GetPseudoDeviceHealths()
)

type healthServer struct {
	mu sync.RWMutex
	pb.UnimplementedSpyreHealthServiceServer
	updateQueue chan []types.DeviceState
}

func NewServer() *healthServer {
	s := healthServer{
		updateQueue: make(chan []types.DeviceState),
	}
	return &s
}

func (s *healthServer) StartGRPCServer(socket string) {
	if err := safeRemove(socket); err != nil {
		glog.Errorf("failed to remove present %s: %v", socket, err)
	}

	lis, err := net.Listen("unix", socket)
	if err != nil {
		glog.Errorf("failed to listen: %v", err)

	}
	grpcServer := grpc.NewServer()
	pb.RegisterSpyreHealthServiceServer(grpcServer, s)
	go grpcServer.Serve(lis) //nolint:errcheck
}

func (s *healthServer) RegisterForSpyreDevicesEvents(_ *emptypb.Empty,
	stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsServer) error {
	glog.V(1).Infof("[Server] Got a request")
	devices := pb.Devices{
		Devices: s.getPbDevices(pseudoDeviceHealths),
	}
	if err := stream.Send(&devices); err != nil {
		return err
	}
	go s.send(stream)
	return nil
}

func (s *healthServer) UpdateHealths(states []types.DeviceState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateQueue <- states
}

func (s *healthServer) send(stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsServer) {
	for {
		states, ok := <-s.updateQueue
		if !ok {
			glog.V(1).Infof("update channel is not OK")
			return
		}
		devices := pb.Devices{
			Devices: s.getPbDevices(states),
		}
		if err := stream.Send(&devices); err != nil {
			glog.V(1).Infof("update channel is not OK")
			return
		}
	}
}

func safeRemove(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *healthServer) getPbDevices(states []types.DeviceState) []*pb.Device {
	deviceList := make([]*pb.Device, 0)
	for _, sd := range states {
		deviceList = append(deviceList, sd.Device())
	}
	return deviceList
}
