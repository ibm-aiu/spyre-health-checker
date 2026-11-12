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
	"fmt"
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
	// Unknown slot -- return application-level error so the client skips it.
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
	slotOnline    = "0000:04:00.0" // sidecar 0.0.5 and later: raw card management status
	slotOffline   = "0000:05:00.0"
	slotFailed    = "0000:06:00.0"
	slotNA        = "0000:07:00.0"
)

var _ = Describe("mapCardStatus", func() {
	DescribeTable("maps the Health value of the sidecar to a device state",
		func(health string, expectedState pb.DEVICE_STATE) {
			Expect(mapCardStatus(health)).To(Equal(expectedState))
		},
		// sidecar 0.0.4 and earlier
		Entry("healthy (0.0.4)", "healthy", pb.DEVICE_STATE_ONLINE),
		Entry("unhealthy (0.0.4)", "unhealthy", pb.DEVICE_STATE_IN_ERROR),
		// sidecar 0.0.5 and later: card management status as is
		Entry("online", "online", pb.DEVICE_STATE_ONLINE),
		Entry("offline", "offline", pb.DEVICE_STATE_OFFLINE),
		Entry("starting", "starting", pb.DEVICE_STATE_BOOTING),
		Entry("boot step pfcheck", "pfcheck", pb.DEVICE_STATE_BOOTING),
		Entry("boot step pfbooted", "pfbooted", pb.DEVICE_STATE_BOOTING),
		Entry("pf-quiescing", "pf-quiescing", pb.DEVICE_STATE_SHUTTING_DOWN),
		Entry("failed", "failed", pb.DEVICE_STATE_IN_ERROR),
		Entry("an unknown value", "something-new", pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED),
		Entry("an empty value", "", pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED),
		Entry("a value with other case (Online)", "Online", pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED),
	)
})

var _ = Describe("SetNAState", func() {
	It("accepts lspci and unspecified in any case", func() {
		c := NewCardHealthClient("", "", "", "", "")
		for _, v := range []string{"lspci", "unspecified", " LSPCI ", "Unspecified"} {
			Expect(c.SetNAState(v)).To(Succeed())
		}
	})

	It("rejects other values and keeps the previous setting", func() {
		c := NewCardHealthClient("", "", "", "", "")
		Expect(c.SetNAState("unspecified")).To(Succeed())
		err := c.SetNAState("online")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"online"`))
		Expect(c.naState).To(Equal(NAUnspecified))
	})
})

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
				slotOnline:    {Health: "online"},
				slotOffline:   {Health: "offline"},
				slotFailed:    {Health: "failed"},
				slotNA:        {Health: "na"},
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

		It("maps the raw status online (sidecar 0.0.5 and later) to DEVICE_STATE_ONLINE", func() {
			client := newTestClient(sockPath)
			states, err := client.CollectForSlots([]string{slotOnline})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(HaveLen(1))
			Expect(states[0].State).To(Equal(pb.DEVICE_STATE_ONLINE))
		})

		It("maps the raw status offline to DEVICE_STATE_OFFLINE and failed to DEVICE_STATE_IN_ERROR", func() {
			client := newTestClient(sockPath)
			states, err := client.CollectForSlots([]string{slotOffline, slotFailed})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(HaveLen(2))
			byAddr := map[string]pb.DEVICE_STATE{}
			for _, s := range states {
				byAddr[s.PciAddress] = s.State
			}
			Expect(byAddr[slotOffline]).To(Equal(pb.DEVICE_STATE_OFFLINE))
			Expect(byAddr[slotFailed]).To(Equal(pb.DEVICE_STATE_IN_ERROR))
		})

		It("leaves slots with status na to the other reporters, and logs it once", func() {
			var logged []string
			saved := untrackedLog
			untrackedLog = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
			untrackedLogged.Store(false)
			DeferCleanup(func() { untrackedLog = saved; untrackedLogged.Store(false) })

			client := newTestClient(sockPath)
			for i := 0; i < 3; i++ {
				states, err := client.CollectForSlots([]string{slotHealthy, slotNA})
				Expect(err).NotTo(HaveOccurred())
				Expect(states).To(HaveLen(1)) // only the tracked slot
				Expect(states[0].PciAddress).To(Equal(slotHealthy))
			}
			Expect(logged).To(HaveLen(1))
			Expect(logged[0]).To(ContainSubstring(slotNA))
		})

		It("reports slots with status na as UNSPECIFIED when the setting is unspecified", func() {
			client := newTestClient(sockPath)
			Expect(client.SetNAState(NAUnspecified)).To(Succeed())
			states, err := client.CollectForSlots([]string{slotHealthy, slotNA})
			Expect(err).NotTo(HaveOccurred())
			Expect(states).To(HaveLen(2))
			byAddr := map[string]pb.DEVICE_STATE{}
			for _, s := range states {
				byAddr[s.PciAddress] = s.State
			}
			Expect(byAddr[slotNA]).To(Equal(pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED))
			Expect(byAddr[slotHealthy]).To(Equal(pb.DEVICE_STATE_ONLINE))
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
