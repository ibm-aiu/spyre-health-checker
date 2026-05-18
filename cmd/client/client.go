/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"io"
	"os"
	"strings"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"

	"go.uber.org/zap"
)

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

var (
	socket  = flag.String("socket", "checker.sock", "The unix socket for health checker")
	tlsCert = flag.String(
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
		"Path to TLS CA certificate file for server verification (can be set via SPYRE_TLS_CA env var)",
	)
)

func main() {
	flag.Parse()

	logger := zap.Must(zap.NewDevelopment()).Sugar()
	defer logger.Sync() //nolint:errcheck

	var sock string
	if strings.Contains(*socket, "/") {
		sock = "unix://" + *socket
	} else {
		sock = "unix:" + *socket
	}

	logger.Infof("using socket %s", *socket)

	opts := make([]grpc.DialOption, 0, 1)

	cert, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
	if err != nil {
		logger.Fatalf("failed to load client certificate", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	caCert, err := os.ReadFile(*tlsCA)
	if err != nil {
		logger.Fatalf("failed to read CA certificate", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		logger.Fatalf("failed to append CA certificate", err)
	}

	tlsConfig.RootCAs = certPool

	creds := credentials.NewTLS(tlsConfig)
	opts = append(opts, grpc.WithTransportCredentials(creds))
	logger.Infof("TLS enabled for client connection ")

	conn, err := grpc.NewClient(sock, opts...)
	if err != nil {
		logger.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close() //nolint:errcheck

	client := pb.NewSpyreHealthServiceClient(conn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.RegisterForSpyreDevicesEvents(ctx, &emptypb.Empty{})

	if err != nil {
		cancel()
		logger.Fatalf("client.client.RegisterForSpyreDevicesEvents failed: %v", err) // nolint:gocritic
	}

	for {
		deviceList, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			cancel()
			logger.Fatalf("client.RegisterForSpyreDevicesEvents failed: %v", err) // nolint:gocritic
		}

		if len(deviceList.Devices) == 0 {
			logger.Infof("Query did not identify any supported devices.")
		}

		for _, d := range deviceList.Devices {
			logger.Infof("  PCIAddress=%s  Type=%s  State=%s",
				d.GetDeviceID().GetPCIAddress(),
				d.GetDeviceType().String(),
				d.GetDeviceState().String(),
			)
		}
	}
}
