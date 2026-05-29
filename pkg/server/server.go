/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	healthcheck "github.com/ibm-aiu/spyre-health-checker/internal/healthcheck"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	"github.com/ibm-aiu/spyre-health-checker/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
	updateQueue       chan []types.DeviceState
	socket            string
	grpcServer        *grpc.Server
	vitals            *healthcheck.Vitals
	streaming         atomic.Bool
	healthHTTPServer  *http.Server
	metricsHTTPServer *http.Server
	ready             atomic.Bool
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

func (s *healthServer) StartSecureGRPCServer(socket string, tlsCertPath string, tlsKeyPath string) error {
	var log *zap.SugaredLogger
	if err := safeRemove(socket); err != nil {
		log = getLogger()
		log.Errorf("failed to remove present %s: %v", socket, err)
	}

	cert, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
	if err != nil {
		if log == nil {
			log = getLogger()
		}
		return fmt.Errorf("failed to load TLS credentials: %w", err) // pragma: allowlist secret
	}

	lis, err := net.Listen("unix", socket)
	if err != nil {
		if log == nil {
			log = getLogger()
		}
		log.Errorf("failed to listen: %v", err)
		return err
	}

	opts := make([]grpc.ServerOption, 0, 1)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	creds := credentials.NewTLS(tlsConfig)
	opts = append(opts, grpc.Creds(creds))

	if log == nil {
		log = getLogger()
	}
	log.Infof("mTLS enabled for gRPC server using cert: %s", tlsCertPath)

	grpcServer := grpc.NewServer(opts...)
	pb.RegisterSpyreHealthServiceServer(grpcServer, s)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			if log == nil {
				log = getLogger()
			}
			logger.Errorf("failed to serve secure gRPC: %v", err)
		}
	}()
	s.socket = socket
	s.grpcServer = grpcServer
	s.ready.Store(true)
	return nil
}

// StartHealthHTTPServer starts the HTTP server for server health check endpoints
func (s *healthServer) StartHealthHTTPServer(port int) error {
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

	s.healthHTTPServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		if err := s.healthHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			getLogger().Errorf("health HTTP server error: %v", err)
		}
	}()

	return nil
}

// StartMetricsHTTPServer starts the HTTP server for Prometheus compatible metrics
func (s *healthServer) StartMetricsHTTPServer(port int) error {
	mux := http.NewServeMux()

	// Prometheus metrics
	mux.Handle("/metrics", promhttp.Handler())

	s.metricsHTTPServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		if err := s.metricsHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			getLogger().Errorf("metrics HTTP server error: %v", err)
		}
	}()

	return nil
}

func (s *healthServer) RegisterForSpyreDevicesEvents(_ *emptypb.Empty,
	stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsServer) error {
	log := getLogger()
	log.Infof("register health stream")
	devices := pb.Devices{
		Devices: s.getPbDevices(s.vitals.GetVitalStates()),
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

func (s *healthServer) RegisterForSpyreDevicesEventsWithDevices(initialDevices *pb.Devices,
	stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsWithDevicesServer) error {
	log := getLogger()
	log.Infof("register health stream with initial devices")

	// Build a map of initial device PCI addresses for quick lookup
	initialDeviceMap := make(map[string]bool)
	if initialDevices != nil && len(initialDevices.Devices) > 0 {
		for _, device := range initialDevices.Devices {
			if device.DeviceID != nil {
				initialDeviceMap[device.DeviceID.PCIAddress] = true
			}
		}
		log.Infof("tracking %d initial devices for removal detection", len(initialDeviceMap))
	}

	// Get current states and check for removed devices
	currentStates := s.vitals.GetVitalStates()
	statesToSend := s.checkForRemovedDevices(currentStates, initialDeviceMap)

	// Set streaming flag before sending first message to avoid race condition
	s.streaming.Store(true)
	defer s.streaming.Store(false)

	devices := pb.Devices{
		Devices: s.getPbDevices(statesToSend),
	}
	if err := stream.Send(&devices); err != nil {
		return err
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case states, ok := <-s.updateQueue:
			if !ok {
				log.Warnf("update channel is not OK, end connection")
				return nil
			}
			// Check for removed devices in updates and update the tracking map with new devices
			statesToSend := s.checkForRemovedDevices(states, initialDeviceMap)

			// Add any new devices to the tracking map so they won't be marked as REMOVED later
			for _, state := range states {
				if !initialDeviceMap[state.PciAddress] {
					initialDeviceMap[state.PciAddress] = true
					log.Infof("added new device %s to tracking map", state.PciAddress)
				}
			}

			devices := pb.Devices{
				Devices: s.getPbDevices(statesToSend),
			}
			if err := stream.Send(&devices); err != nil {
				log.Warnf("failed to send, end connection")
				return nil
			}
			log.Infof("send %v", statesToSend)
		}
	}
}

// checkForRemovedDevices compares current states with initial devices and marks missing ones as REMOVED
func (s *healthServer) checkForRemovedDevices(
	currentStates []types.DeviceState,
	initialDeviceMap map[string]bool,
) []types.DeviceState {
	if len(initialDeviceMap) == 0 {
		// No initial devices to track, return current states as-is
		return currentStates
	}

	// Create a map of current device PCI addresses
	currentDeviceMap := make(map[string]bool)
	for _, state := range currentStates {
		currentDeviceMap[state.PciAddress] = true
	}

	// Start with current states
	result := make([]types.DeviceState, len(currentStates))
	copy(result, currentStates)

	// Check for devices in initial list that are missing from current states
	for pciAddr := range initialDeviceMap {
		if !currentDeviceMap[pciAddr] {
			// Device was in initial list but is now missing - mark as REMOVED
			result = append(result, types.DeviceState{
				PciAddress: pciAddr,
				Type:       pb.DEVICE_TYPE_DEVICE_TYPE_UNSPECIFIED,
				State:      pb.DEVICE_STATE_REMOVED,
			})
		}
	}

	return result
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

	// Shutdown health HTTP server gracefully
	if s.healthHTTPServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.healthHTTPServer.Shutdown(ctx); err != nil {
			getLogger().Errorf("Health HTTP server shutdown error: %v", err)
		}
	}

	// Shutdown metrics HTTP server gracefully
	if s.metricsHTTPServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.metricsHTTPServer.Shutdown(ctx); err != nil {
			getLogger().Errorf("Metrics HTTP server shutdown error: %v", err)
		}
	}

	// Gracefully stop gRPC server
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}

	// Remove socket file
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
	deviceList := make([]*pb.Device, 0, len(states))
	for _, sd := range states {
		deviceList = append(deviceList, sd.Device())
	}
	return deviceList
}
