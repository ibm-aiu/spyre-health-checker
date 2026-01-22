package server

import (
	"net"
	"os"
	"sync"
	"sync/atomic"

	healthcheck "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/healthcheck"
	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
	"github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"go.uber.org/zap"
)

var (
	loggerMu sync.RWMutex
	logger   *zap.SugaredLogger = zap.NewNop().Sugar()
)

func SetLogger(l *zap.SugaredLogger) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	if l == nil {
		logger = zap.NewNop().Sugar()
		return
	}
	logger = l
}

func getLogger() *zap.SugaredLogger {
	loggerMu.RLock()
	defer loggerMu.RUnlock()
	return logger
}

type healthServer struct {
	mu sync.RWMutex
	pb.UnimplementedSpyreHealthServiceServer
	updateQueue chan []types.DeviceState
	socket      string
	vitals      *healthcheck.Vitals
	streaming   atomic.Bool
}

func NewServer(v *healthcheck.Vitals) *healthServer {
	// Initialize state
	err := v.UpdateStates()
	if err != nil {
		getLogger().Warnf("Error calling UpdateStates(): %v", err)
	}
	s := healthServer{
		updateQueue: make(chan []types.DeviceState),
		vitals:      v,
	}
	return &s
}

func (s *healthServer) StartGRPCServer(socket string) error {
	var log *zap.SugaredLogger
	if err := safeRemove(socket); err != nil {
		log = getLogger()
		log.Errorf("failed to remove present %s: %v", socket, err)
	}

	lis, err := net.Listen("unix", socket)
	if err != nil {
		if log == nil {
			log = getLogger()
		}
		log.Errorf("failed to listen: %v", err)
		return err
	}
	grpcServer := grpc.NewServer()
	pb.RegisterSpyreHealthServiceServer(grpcServer, s)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			if log == nil {
				log = getLogger()
			}
			logger.Errorf("failed to serve: %v", err)
		}
	}()
	s.socket = socket
	return nil
}

func (s *healthServer) RegisterForSpyreDevicesEvents(_ *emptypb.Empty,
	stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsServer) error {
	log := getLogger()
	log.Infof("register health stream")
	devices := pb.Devices{
		Devices: s.getPbDevices(s.vitals.States),
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
				log.Warnf("update channel is not OK, end connection")
				return nil
			}
			devices := pb.Devices{
				Devices: s.getPbDevices(states),
			}
			if err := stream.Send(&devices); err != nil {
				log.Warnf("failed to send, end connection")
				return nil
			}
			log.Infof("send %v", states)
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
		getLogger().Errorf("failed to remove present %s: %v", s.socket, err)
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
