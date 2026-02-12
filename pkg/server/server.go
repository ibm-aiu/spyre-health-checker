package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

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
	httpServer  *http.Server
	ready       atomic.Bool
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
	s.ready.Store(false)
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
	s.ready.Store(true)
	return nil
}

// StartHTTPServer starts the HTTP server for health check endpoints
func (s *healthServer) StartHTTPServer(port int) error {
	mux := http.NewServeMux()

	// Liveness probe - always returns 200 if server is running
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprintf(w, "OK"); err != nil {
			getLogger().Warnf("failed to write healthz response: %v", err)
		}
	})

	// Readiness probe - returns 200 only if gRPC server is ready
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.ready.Load() {
			w.WriteHeader(http.StatusOK)
			if _, err := fmt.Fprintf(w, "Ready"); err != nil {
				getLogger().Warnf("failed to write readyz response: %v", err)
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := fmt.Fprintf(w, "Not Ready"); err != nil {
				getLogger().Warnf("failed to write readyz response: %v", err)
			}
		}
	})

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			getLogger().Errorf("HTTP server error: %v", err)
		}
	}()

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
	s.ready.Store(false)
	close(s.updateQueue)

	// Shutdown HTTP server gracefully
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(ctx); err != nil {
			getLogger().Errorf("HTTP server shutdown error: %v", err)
		}
	}

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
