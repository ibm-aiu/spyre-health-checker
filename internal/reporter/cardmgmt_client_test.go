/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	cardhealth "github.com/ibm-aiu/spyre-health-checker/pkg/cardhealth"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
)

// fakeCardHealthServer is a minimal in-process CardHealth gRPC server used by
// the tests. It returns pre-configured responses keyed by PCI slot.
type fakeCardHealthServer struct {
	cardhealth.UnimplementedCardHealthServer
	responses map[string]*cardhealth.GetCardHealthResponse
}

func (s *fakeCardHealthServer) GetCardHealth(
	_ context.Context,
	req *cardhealth.GetCardHealthRequest,
) (*cardhealth.GetCardHealthResponse, error) {
	if resp, ok := s.responses[req.PciSlot]; ok {
		return resp, nil
	}
	// Unknown slot — return application-level error so the client skips it.
	return &cardhealth.GetCardHealthResponse{
		Error: &cardhealth.ErrorDetails{ErrorCode: "NOT_FOUND", ErrorMessage: "unknown slot"},
	}, nil
}

// buildServerTLSCreds builds gRPC server credentials from the shared test cert
// and key, requiring client certificates signed by the same self-signed CA.
func buildServerTLSCreds() credentials.TransportCredentials {
	cert, err := tls.LoadX509KeyPair(TestTLSCert, TestTLSKey)
	Expect(err).NotTo(HaveOccurred())

	caPEM, err := os.ReadFile(TestTLSCA)
	Expect(err).NotTo(HaveOccurred())
	caPool := x509.NewCertPool()
	Expect(caPool.AppendCertsFromPEM(caPEM)).To(BeTrue())

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert, // pragma: allowlist secret
		MinVersion:   tls.VersionTLS12,
	}
	return credentials.NewTLS(tlsCfg)
}

// startFakeServer starts a fake CardHealth gRPC server on a temporary UNIX
// socket with mTLS and returns the socket path plus a stop function.
func startFakeServer(responses map[string]*cardhealth.GetCardHealthResponse) (string, func()) {
	dir, err := os.MkdirTemp("", "cardhealth-test-*")
	Expect(err).NotTo(HaveOccurred())
	sockPath := filepath.Join(dir, "cardhealth.sock")

	lis, err := net.Listen("unix", sockPath)
	Expect(err).NotTo(HaveOccurred())

	srv := grpc.NewServer(grpc.Creds(buildServerTLSCreds()))
	cardhealth.RegisterCardHealthServer(srv, &fakeCardHealthServer{responses: responses})

	go func() { _ = srv.Serve(lis) }()

	stop := func() {
		srv.Stop()
		_ = os.RemoveAll(dir)
	}
	return sockPath, stop
}

// newTestClient returns a CardHealthClient wired with the test mTLS certificates.
func newTestClient(sockPath string) *CardHealthClient {
	return NewCardHealthClient(sockPath, TestTLSCert, TestTLSKey, TestTLSCA, TestTLSServerName)
}

const (
	slotHealthy   = "0000:01:00.0"
	slotUnhealthy = "0000:02:00.0"
	slotErrorCode = "0000:03:00.0"
	slotUnknown   = "0000:ff:00.0"
)

var _ = Describe("CardHealthClient", func() {
	Describe("CollectForSlots", func() {
		var (
			sockPath string
			stop     func()
		)

		BeforeEach(func() {
			sockPath, stop = startFakeServer(map[string]*cardhealth.GetCardHealthResponse{
				slotHealthy:   {Health: "healthy"},
				slotUnhealthy: {Health: "unhealthy"},
				slotErrorCode: {
					Error: &cardhealth.ErrorDetails{ErrorCode: "CALL_FAILED", ErrorMessage: "worker error"},
				},
			})
		})

		AfterEach(func() { stop() })

		It("maps healthy response to DEVICE_STATE_ONLINE", func() {
			client := newTestClient(sockPath)
			states, err := client.CollectForSlots([]string{slotHealthy})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(HaveLen(1))
			Expect(states[0].PciAddress).To(Equal(slotHealthy))
			Expect(states[0].State).To(Equal(pb.DEVICE_STATE_ONLINE))
			Expect(states[0].Type).To(Equal(pb.DEVICE_TYPE_PF))
		})

		It("maps unhealthy response to DEVICE_STATE_IN_ERROR", func() {
			client := newTestClient(sockPath)
			states, err := client.CollectForSlots([]string{slotUnhealthy})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(HaveLen(1))
			Expect(states[0].State).To(Equal(pb.DEVICE_STATE_IN_ERROR))
		})

		It("skips slots that return an application-level error_code", func() {
			client := newTestClient(sockPath)
			states, err := client.CollectForSlots([]string{slotErrorCode})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(BeEmpty())
		})

		It("skips slots unknown to the sidecar", func() {
			client := newTestClient(sockPath)
			states, err := client.CollectForSlots([]string{slotUnknown})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(BeEmpty())
		})

		It("collects multiple slots in a single call", func() {
			client := newTestClient(sockPath)
			states, err := client.CollectForSlots([]string{slotHealthy, slotUnhealthy})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(HaveLen(2))
		})

		It("returns empty slice for an empty slot list", func() {
			client := newTestClient(sockPath)
			states, err := client.CollectForSlots([]string{})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(BeEmpty())
		})

		It("returns an error when the TLS cert files do not exist", func() {
			client := NewCardHealthClient(sockPath, "/nonexistent/tls.crt", "/nonexistent/tls.key", TestTLSCA, TestTLSServerName)
			// buildMTLSCredentials fails before any dial attempt.
			_, err := client.CollectForSlots([]string{"0000:01:00.0"})
			Expect(err).To(HaveOccurred())
		})
	})
})
