package server

import (
	"net"
	"os"
	"sync"
	"sync/atomic"

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
	socket      string
	streaming   atomic.Bool
}

func NewServer() *healthServer {
	s := healthServer{
		updateQueue: make(chan []types.DeviceState),
	}
	return &s
}

func (s *healthServer) StartGRPCServer(socket string) error {
	if err := safeRemove(socket); err != nil {
		glog.Errorf("failed to remove present %s: %v", socket, err)
	}

	lis, err := net.Listen("unix", socket)
	if err != nil {
		glog.Errorf("failed to listen: %v", err)
		return err
	}
	grpcServer := grpc.NewServer()
	pb.RegisterSpyreHealthServiceServer(grpcServer, s)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			glog.Errorf("failed to serve: %v", err)
		}
	}()
	s.socket = socket
	return nil
}

func (s *healthServer) RegisterForSpyreDevicesEvents(_ *emptypb.Empty,
	stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsServer) error {
	glog.V(1).Infof("register health stream")
	devices := pb.Devices{
		Devices: s.getPbDevices(pseudoDeviceHealths),
	}
	if err := stream.Send(&devices); err != nil {
		return err
	}
	s.streaming.Store(true)
	defer s.streaming.Store(false)
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case states, ok := <-s.updateQueue:
			if !ok {
				glog.Warning("update channel is not OK, end connection")
				return nil
			}
			devices := pb.Devices{
				Devices: s.getPbDevices(states),
			}
			if err := stream.Send(&devices); err != nil {
				glog.Warning("failed to send, end connection")
				return nil
			}
			glog.V(1).Infof("send %v", states)
		}
	}
}

func (s *healthServer) UpdateHealths(states []types.DeviceState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streaming.Load() {
		s.updateQueue <- states
	}
}

func (s *healthServer) Stop() {
	close(s.updateQueue)
	if err := safeRemove(s.socket); err != nil {
		glog.Errorf("failed to remove present %s: %v", s.socket, err)
	}
}

func safeRemove(path string) error {
	if path == "" {
		return nil
	}
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
