/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package main

import (
	"flag"
	"os"
	"time"

	healthcheck "github.com/ibm-aiu/spyre-health-checker/internal/healthcheck"
	utils "github.com/ibm-aiu/spyre-health-checker/internal/utils"
	server "github.com/ibm-aiu/spyre-health-checker/pkg/server"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

var (
	socket = flag.String("socket", "/usr/local/etc/device-plugins/health/checker.sock", "The server unix socket")
	timer  = flag.String(
		"timer",
		"1h",
		"Run all tests periodically on each node. Time set in interval format. Defaults to 1h",
	)
	healthPort  = flag.Int("health-port", 8080, "HTTP port for server health check endpoints")
	metricsPort = flag.Int("metrics-port", 8081, "HTTP port for Prometheus compatible card metrics")
	tlsCert     = flag.String(
		"tls-cert",
		getEnvOrDefault("SPYRE_TLS_CERT", "/etc/spyre-health-checker/certs/tls.crt"),
		"Path to TLS certificate file (can be set via SPYRE_TLS_CERT env var)",
	)
	tlsKey = flag.String(
		"tls-key",
		getEnvOrDefault("SPYRE_TLS_KEY", "/etc/spyre-health-checker/certs/tls.key"),
		"Path to TLS private key file (can be set via SPYRE_TLS_KEY env var)",
	)
)

func main() {
	flag.Parse()

	logger := zap.Must(zap.NewDevelopment()).Sugar()
	defer logger.Sync() //nolint:errcheck

	server.SetLogger(logger)

	vitals := healthcheck.Vitals{States: make([]types.DeviceState, 0)}

	s := server.NewServer(&vitals)
	logger.Infof("Starting secure gRPC server with mTLS")
	if err := s.StartSecureGRPCServer(*socket, *tlsCert, *tlsKey); err != nil {
		logger.Fatalf("Error starting secure gRPC Server: %v", err)
	}

	logger.Infof("Starting HTTP server for server health on port %d", *healthPort)
	if err := s.StartHealthHTTPServer(*healthPort); err != nil {
		logger.Fatal(err)
	}

	logger.Infof("Starting HTTP for Prometheus compatible card metrics on port %d", *metricsPort)
	if err := s.StartMetricsHTTPServer(*metricsPort); err != nil {
		logger.Fatal(err)
	}

	logger.Infof("Starting timer for periodic checks")
	// Parse the repeat and invasive intervals to durations
	timer, err := utils.ParseInterval(*timer)
	if err != nil {
		logger.Errorf("Error parsing repeat interval: %v", err)
		s.Stop()
		_ = logger.Sync()
		os.Exit(1) //nolint:gocritic
	}
	defer s.Stop()

	utils.InitMetrics(prometheus.DefaultRegisterer)

	if err := vitals.UpdateStates(); err != nil {
		logger.Warnf("Error calling initial UpdateState(): %v", err)
	} else {
		utils.UpdateDeviceMetrics(vitals.GetVitalStates())
		s.UpdateHealths(vitals.GetVitalStates())
	}

	periodicChecksTicker := time.NewTicker(timer)
	defer periodicChecksTicker.Stop()
	for range periodicChecksTicker.C {
		err := vitals.UpdateStates()
		if err != nil {
			logger.Warnf("Error calling UpdateState(): %v", err)
		}
		s.UpdateHealths(vitals.GetVitalStates())
		utils.UpdateDeviceMetrics(vitals.GetVitalStates())
	}
}
