/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	healthcheck "github.com/ibm-aiu/spyre-health-checker/internal/healthcheck"
	reporter "github.com/ibm-aiu/spyre-health-checker/internal/reporter"
	utils "github.com/ibm-aiu/spyre-health-checker/internal/utils"
	server "github.com/ibm-aiu/spyre-health-checker/pkg/server"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// buildReporters creates a slice of reporters from a comma-separated string.
// rasReporter and cardHealthClient must be fully configured before this call.
// When PSEUDO_DEVICE_MODE=1, hardware reporters are replaced by PseudoReporter;
// RASReporter is always appended unconditionally regardless of mode.
func buildReporters(reporterNames string, rasReporter *reporter.RASReporter,
	cardHealthClient *reporter.CardHealthClient) []types.Reporter {
	if utils.IsPseudoDeviceMode() {
		return []types.Reporter{&reporter.PseudoReporter{}, rasReporter}
	}
	names := strings.Split(strings.TrimSpace(reporterNames), ",")
	var reporters []types.Reporter
	for _, name := range names {
		name = strings.TrimSpace(name)
		switch name {
		case "lspci":
			reporters = append(reporters, &reporter.LSPCIReporter{})
		case "cardmgmt":
			// CollectFn: discover slots via lspci, then query the sidecar.
			lspci := &reporter.LSPCIReporter{}
			reporters = append(reporters, &reporter.CardmgmtReporter{
				CollectFn: func() ([]types.DeviceState, error) {
					discovered, err := lspci.Collect()
					if err != nil || len(discovered) == 0 {
						return nil, err
					}
					slots := make([]string, len(discovered))
					for i, s := range discovered {
						slots[i] = s.PciAddress
					}
					return cardHealthClient.CollectForSlots(slots)
				},
			})
		}
	}
	if len(reporters) == 0 {
		// Default to lspci if no valid reporters were found
		reporters = append(reporters, &reporter.LSPCIReporter{})
	}
	reporters = append(reporters, rasReporter)
	return reporters
}

var (
	debug  = flag.Bool("debug", false, "Enable per-slot gRPC call logging for the cardmgmt reporter (stdout)")
	socket = flag.String("socket", "/usr/local/etc/device-plugins/health/checker.sock", "The server unix socket")
	timer  = flag.String(
		"timer",
		"1h",
		"Run all tests periodically on each node. Time set in interval format. Defaults to 1h",
	)
	enabledReporters = flag.String(
		"enabled-reporters",
		"lspci",
		"Comma-separated list of enabled reporters (lspci, cardmgmt)",
	)
	healthPort  = flag.Int("health-port", 8080, "HTTP port for server health check endpoints (plain, for k8s probes)")
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
	tlsCA = flag.String(
		"tls-ca",
		getEnvOrDefault("SPYRE_TLS_CA", "/etc/spyre-health-checker/certs/ca.crt"),
		"Path to CA certificate file (can be set via SPYRE_TLS_CA env var)",
	)
	rasWatcherNamespaces = flag.String(
		"ras-watcher-limit-namespaces",
		"",
		"Comma-separated list of namespaces the RAS pod watcher trusts. Empty (default) watches all namespaces.",
	)
	cardHealthSocket = flag.String(
		"cardhealth-socket",
		getEnvOrDefault("CARDHEALTH_GRPC_SOCKET", "/var/run/cardmgmt-health-check-api/health-check-api.sock"),
		"UNIX socket path for the aiu-cardmgmt-health-api sidecar (can be set via CARDHEALTH_GRPC_SOCKET env var)",
	)
	cardHealthServerName = flag.String(
		"cardhealth-server-name",
		getEnvOrDefault("CARDHEALTH_TLS_SERVER_NAME", "spyre-components"),
		"TLS server name for the cardmgmt sidecar certificate (can be set via CARDHEALTH_TLS_SERVER_NAME env var)",
	)
)

func main() {
	flag.Parse()

	logger := zap.Must(zap.NewDevelopment()).Sugar()
	defer logger.Sync() //nolint:errcheck

	server.SetLogger(logger)

	rasReporter := reporter.NewRASReporter()
	rasReporter.SetLogger(logger)
	if *rasWatcherNamespaces != "" {
		nsList := strings.Split(*rasWatcherNamespaces, ",")
		rasReporter.SetAllowedNamespaces(nsList)
		logger.Infof("RAS pod watcher namespace allowlist: %v", nsList)
	}

	cardHealthClient := reporter.NewCardHealthClient(*cardHealthSocket, *tlsCert, *tlsKey, *tlsCA, *cardHealthServerName)
	if *debug {
		cardHealthClient.SetDebug(true)
		logger.Infof("cardmgmt reporter: per-slot debug logging enabled")
	}

	reporters := buildReporters(*enabledReporters, rasReporter, cardHealthClient)
	logger.Infof("Enabled reporters: %v", *enabledReporters)
	vitals := healthcheck.NewVitals(reporters)

	s := server.NewServer(vitals)
	logger.Infof("Starting secure gRPC server with mTLS")
	if err := s.StartSecureGRPCServer(*socket, *tlsCert, *tlsKey, *tlsCA); err != nil {
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
	// Parse the timer interval to a duration.
	timer, err := utils.ParseInterval(*timer)
	if err != nil {
		logger.Errorf("Error parsing repeat interval: %v", err)
		s.Stop()
		_ = logger.Sync()
		os.Exit(1) //nolint:gocritic
	}
	defer s.Stop()

	// defer cancel() registered after defer s.Stop(), so it runs first (LIFO).
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Start the RAS pod watcher; non-fatal if not running in-cluster.
	if cfg, err := rest.InClusterConfig(); err != nil {
		logger.Warnf("RAS pod watcher disabled: not running in-cluster (%v)", err)
	} else if kubeClient, err := kubernetes.NewForConfig(cfg); err != nil {
		logger.Warnf("RAS pod watcher disabled: failed to build kube client (%v)", err)
	} else {
		logger.Infof("Starting RAS pod watcher for node %q", utils.NodeName)
		rasReporter.Start(ctx, kubeClient, utils.NodeName)
	}

	utils.InitMetrics(prometheus.DefaultRegisterer)

	if err := vitals.UpdateStates(); err != nil {
		logger.Warnf("Error calling initial UpdateState(): %v", err)
	} else {
		states := vitals.GetVitalStates()
		s.UpdateHealths(states)
		utils.UpdateDeviceMetrics(states)
	}

	periodicChecksTicker := time.NewTicker(timer)
	defer periodicChecksTicker.Stop()
	for range periodicChecksTicker.C {
		err := vitals.UpdateStates()
		if err != nil {
			logger.Warnf("Error calling UpdateState(): %v", err)
		}
		states := vitals.GetVitalStates()
		s.UpdateHealths(states)
		utils.UpdateDeviceMetrics(states)
	}
}
