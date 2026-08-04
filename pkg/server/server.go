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
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
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
	httpsServer       *http.Server
	metricsHTTPServer *http.Server
	ready             atomic.Bool
	overridesMu       sync.RWMutex
	overrides         map[string]types.DeviceState
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
		overrides:   make(map[string]types.DeviceState),
	}
	s.ready.Store(false)
	return &s
}

// applyOverrides merges the manual override map on top of the provided states.
// Overrides win: any device whose PCI address appears in the override map has its
// state replaced. Overrides for devices not in the current live states are appended
// as additional entries.
func (s *healthServer) applyOverrides(states []types.DeviceState) []types.DeviceState {
	s.overridesMu.RLock()
	defer s.overridesMu.RUnlock()
	if len(s.overrides) == 0 {
		return states
	}
	seen := make(map[string]bool, len(states))
	result := make([]types.DeviceState, len(states))
	copy(result, states)
	for i, st := range result {
		if ov, ok := s.overrides[st.PciAddress]; ok && ov.State != st.State {
			// Preserve the live device type; override only the state and source.
			result[i] = types.DeviceState{
				PciAddress: st.PciAddress,
				Type:       st.Type,
				State:      ov.State,
				Source:     "override",
			}
		}
		seen[st.PciAddress] = true
	}
	// Append overrides for devices not present in the live states.
	for pci, ov := range s.overrides {
		if !seen[pci] {
			result = append(result, types.DeviceState{
				PciAddress: ov.PciAddress,
				Type:       ov.Type,
				State:      ov.State,
				Source:     "override",
			})
		}
	}
	return result
}

// AppliedStates returns the current live states with the override map merged in.
// Use this instead of vitals.GetVitalStates() wherever the full effective view
// (including manual overrides) is required — e.g. when updating Prometheus metrics.
func (s *healthServer) AppliedStates() []types.DeviceState {
	return s.applyOverrides(s.vitals.GetVitalStates())
}

func (s *healthServer) StartSecureGRPCServer(socket, tlsCertPath, tlsKeyPath, caCertPath string) error {
	log := getLogger()
	if err := safeRemove(socket); err != nil {
		log.Errorf("failed to remove present %s: %v", socket, err)
	}

	cert, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
	if err != nil {
		return fmt.Errorf("failed to load TLS credentials: %w", err) // pragma: allowlist secret
	}
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to read CA certification: %w", err)
	}
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	lis, err := net.Listen("unix", socket)
	if err != nil {
		log.Errorf("failed to listen: %v", err)
		return err
	}

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		MinVersion:         tls.VersionTLS12,
		RootCAs:            certPool,
		InsecureSkipVerify: false,
	}

	creds := credentials.NewTLS(tlsConfig)
	opts := []grpc.ServerOption{grpc.Creds(creds)}

	log.Infof("mTLS enabled for gRPC server using cert: %s", tlsCertPath)

	grpcServer := grpc.NewServer(opts...)
	pb.RegisterSpyreHealthServiceServer(grpcServer, s)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Errorf("failed to serve secure gRPC: %v", err)
		}
	}()
	s.socket = socket
	s.grpcServer = grpcServer
	s.ready.Store(true)
	return nil
}

// stateFromString parses a state name for manual device overrides.
// Only ONLINE and RUNNING_DIAGNOSTICS are accepted; all other values return (0, false).
func stateFromString(s string) (pb.DEVICE_STATE, bool) {
	switch strings.ToUpper(s) {
	case "ONLINE":
		return pb.DEVICE_STATE_ONLINE, true
	case "RUNNING_DIAGNOSTICS":
		return pb.DEVICE_STATE_RUNNING_DIAGNOSTICS, true
	default:
		return 0, false
	}
}

// registerOverrideRoutes mounts GET/POST/DELETE /override on mux.
func (s *healthServer) registerOverrideRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/override", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleOverrideGet(w, r)
		case http.MethodPost:
			s.handleOverridePost(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	// DELETE /override/{pciAddress}
	mux.HandleFunc("/override/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleOverrideDelete(w, r)
	})
}

type overrideRequest struct {
	Devices []overrideEntry `json:"devices"`
}

type overrideEntry struct {
	PCIAddress string `json:"pciAddress"`
	State      string `json:"state"`
}

func (s *healthServer) handleOverridePost(w http.ResponseWriter, r *http.Request) {
	var req overrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Validate all entries first before mutating state
	parsed := make([]types.DeviceState, 0, len(req.Devices))
	for _, entry := range req.Devices {
		state, ok := stateFromString(entry.State)
		if !ok {
			http.Error(w, "invalid or disallowed state: "+entry.State, http.StatusBadRequest)
			return
		}
		parsed = append(parsed, types.DeviceState{PciAddress: entry.PCIAddress, State: state, Source: "override"})
	}
	s.overridesMu.Lock()
	for _, ds := range parsed {
		s.overrides[ds.PciAddress] = ds
	}
	s.overridesMu.Unlock()
	overridden := make([]string, len(parsed))
	for i, ds := range parsed {
		overridden[i] = ds.PciAddress
	}
	getLogger().Infof("manual overrides set: %v", overridden)
	// Push the current live states through the override layer immediately so
	// connected gRPC clients receive the change without waiting for the next tick.
	s.UpdateHealths(s.vitals.GetVitalStates())
	setJSONContentType(w)
	_ = json.NewEncoder(w).Encode(map[string]any{"overridden": overridden})
}

func (s *healthServer) handleOverrideDelete(w http.ResponseWriter, r *http.Request) {
	pci := strings.TrimPrefix(r.URL.Path, "/override/")
	if pci == "" {
		http.Error(w, "missing PCI address in path", http.StatusBadRequest)
		return
	}
	s.overridesMu.Lock()
	_, exists := s.overrides[pci]
	if exists {
		delete(s.overrides, pci)
	}
	s.overridesMu.Unlock()
	if !exists {
		http.Error(w, "no override for "+pci, http.StatusNotFound)
		return
	}
	getLogger().Infof("manual override removed: %s", pci)
	// Push live states immediately so clients see the override lifted without
	// waiting for the next periodic tick.
	s.UpdateHealths(s.vitals.GetVitalStates())
	setJSONContentType(w)
	_ = json.NewEncoder(w).Encode(map[string]any{"removed": pci})
}

func (s *healthServer) handleOverrideGet(w http.ResponseWriter, _ *http.Request) {
	s.overridesMu.RLock()
	entries := make([]overrideEntry, 0, len(s.overrides))
	for _, ov := range s.overrides {
		entries = append(entries, overrideEntry{
			PCIAddress: ov.PciAddress,
			State:      ov.State.String(),
		})
	}
	s.overridesMu.RUnlock()
	setJSONContentType(w)
	_ = json.NewEncoder(w).Encode(map[string]any{"overrides": entries})
}

// setJSONContentType sets the Content-Type response header to application/json.
func setJSONContentType(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
}

// handleReadyz is the /readyz handler shared by both HTTP and HTTPS health servers.
func (s *healthServer) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if s.ready.Load() {
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprintf(w, "Ready"); err != nil {
			getLogger().Warnf("failed to write readyz response: %v", err)
		}
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	if _, err := fmt.Fprintf(w, "Not Ready"); err != nil {
		getLogger().Warnf("failed to write readyz response: %v", err)
	}
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
	mux.HandleFunc("/readyz", s.handleReadyz)

	s.registerOverrideRoutes(mux)

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

// StartHealthHTTPSServer starts the health HTTP server over mTLS.
// It serves the same /healthz, /readyz, and /override endpoints as StartHealthHTTPServer
// but requires a valid client certificate signed by the provided CA.
func (s *healthServer) StartHealthHTTPSServer(port int, tlsCertPath, tlsKeyPath, caCertPath string) error {
	cert, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
	if err != nil {
		return fmt.Errorf("failed to load TLS credentials: %w", err) // pragma: allowlist secret
	}
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to read CA certificate: %w", err)
	}
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    certPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprintf(w, "OK"); err != nil {
			getLogger().Warnf("failed to write healthz response: %v", err)
		}
	})
	mux.HandleFunc("/readyz", s.handleReadyz)
	s.registerOverrideRoutes(mux)

	lis, err := tls.Listen("tcp", fmt.Sprintf(":%d", port), tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to listen on HTTPS port %d: %w", port, err)
	}

	s.httpsServer = &http.Server{Handler: mux}
	getLogger().Infof("mTLS enabled for HTTPS override server on port %d using cert: %s", port, tlsCertPath)

	go func() {
		if err := s.httpsServer.Serve(lis); err != nil && err != http.ErrServerClosed {
			getLogger().Errorf("HTTPS override server error: %v", err)
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
		Devices: s.getPbDevices(s.applyOverrides(s.vitals.GetVitalStates())),
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
				Devices: s.getPbDevices(s.applyOverrides(states)),
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
	statesToSend := s.checkForRemovedDevices(s.applyOverrides(currentStates), initialDeviceMap)

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
			statesToSend := s.checkForRemovedDevices(s.applyOverrides(states), initialDeviceMap)

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
		s.updateQueue <- s.applyOverrides(states)
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

	// Shutdown HTTPS override server gracefully
	if s.httpsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpsServer.Shutdown(ctx); err != nil {
			getLogger().Errorf("HTTPS override server shutdown error: %v", err)
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
