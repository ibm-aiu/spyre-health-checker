/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	healthcheck "github.com/ibm-aiu/spyre-health-checker/internal/healthcheck"
	utils "github.com/ibm-aiu/spyre-health-checker/internal/utils"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"

	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
)

var (
	TestSocket    = "checker.sock"
	TestCertDir   = "test-certs"
	TestCert      = ""
	TestKey       = ""
	TestHTTPSPort = 0

	TestHealthServer *healthServer
)

// freePort asks the OS for an available TCP port by binding to :0 and
// immediately releasing it. The port is free for the next bind.
func freePort() int {
	l, err := net.Listen("tcp", ":0")
	Expect(err).To(BeNil())
	port := l.Addr().(*net.TCPAddr).Port
	Expect(l.Close()).To(BeNil())
	return port
}

type Client struct {
	client  pb.SpyreHealthServiceClient
	mu      sync.RWMutex
	devices []*pb.Device
	conn    *grpc.ClientConn
	cancel  context.CancelFunc
}

func NewClient() *Client {
	opts := make([]grpc.DialOption, 0, 1)

	cert, err := tls.LoadX509KeyPair(TestCert, TestKey)
	Expect(err).To(BeNil())

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}

	creds := credentials.NewTLS(tlsConfig)
	opts = append(opts, grpc.WithTransportCredentials(creds))

	conn, err := grpc.NewClient("unix:"+TestSocket, opts...)
	Expect(err).To(BeNil())
	client := pb.NewSpyreHealthServiceClient(conn)
	return &Client{
		client: client,
		conn:   conn,
	}
}

func (c *Client) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	stream, err := c.client.RegisterForSpyreDevicesEvents(ctx, &emptypb.Empty{})
	Expect(err).To(BeNil())
	go c.receive(stream)
}

func (c *Client) Stop() {
	c.cancel()
	_ = c.conn.Close()
}

func (c *Client) receive(stream pb.SpyreHealthService_RegisterForSpyreDevicesEventsClient) {
	for {
		deviceList, err := stream.Recv()
		if err == io.EOF {
			return
		}

		select {
		case <-stream.Context().Done():
			return
		default:
			Expect(err).To(BeNil())
			c.mu.Lock()
			c.devices = deviceList.Devices
			c.mu.Unlock()
		}

	}
}

func (c *Client) GetHealths() map[string]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	healths := make(map[string]bool, len(c.devices))
	for _, device := range c.devices {
		healths[device.DeviceID.PCIAddress] = device.DeviceState == pb.DEVICE_STATE_ONLINE
	}
	return healths
}

func TestServer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Spyre Health Checker Test Server Suite")
}

var _ = BeforeSuite(func() {
	_ = os.Setenv(utils.PseudoDeviceModeKey, "1")

	ws := zapcore.AddSync(GinkgoWriter)

	encCfg := zap.NewDevelopmentEncoderConfig()
	enc := zapcore.NewConsoleEncoder(encCfg)

	core := zapcore.NewCore(enc, ws, zap.DebugLevel)
	uber := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
	defer func() { _ = uber.Sync() }()

	crlog.SetLogger(zapr.NewLogger(uber))

	// Create test certificates
	err := createTestCertificates()
	Expect(err).To(BeNil())

	// Set environment variables to use test certificates
	_ = os.Setenv("SPYRE_TLS_CERT", TestCert)
	_ = os.Setenv("SPYRE_TLS_KEY", TestKey)
	_ = os.Setenv("SPYRE_TLS_CA", TestCert) // Use same cert as CA for self-signed

	TestHealthServer = startServer()
})

var _ = AfterSuite(func() {
	err := os.RemoveAll(TestSocket)
	Expect(err).To(BeNil())
	err = os.RemoveAll(TestCertDir)
	Expect(err).To(BeNil())
	err = os.Unsetenv(utils.PseudoDeviceModeKey)
	Expect(err).To(BeNil())
	// Clean up test TLS environment variables
	_ = os.Unsetenv("SPYRE_TLS_CERT")
	_ = os.Unsetenv("SPYRE_TLS_KEY")
	_ = os.Unsetenv("SPYRE_TLS_CA")
})

func createTestCertificates() error {
	// Create test certificate directory
	if err := os.MkdirAll(TestCertDir, 0755); err != nil {
		return err
	}

	// Generate private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
			CommonName:   "test-server",
		},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return err
	}

	// Write certificate to file
	TestCert = TestCertDir + "/tls.crt"
	certFile, err := os.Create(TestCert)
	if err != nil {
		return err
	}
	defer func() { _ = certFile.Close() }()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return err
	}

	// Write private key to file
	TestKey = TestCertDir + "/tls.key"
	keyFile, err := os.Create(TestKey)
	if err != nil {
		return err
	}
	defer func() { _ = keyFile.Close() }()

	privateKeyBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return err
	}

	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyBytes}); err != nil {
		return err
	}

	return nil
}

func startServer() *healthServer {
	logger := zap.Must(zap.NewDevelopment()).Sugar()
	defer logger.Sync() //nolint:errcheck
	SetLogger(logger)

	vitals := healthcheck.Vitals{States: make([]types.DeviceState, 0)}
	s := NewServer(&vitals)

	// Start secure server with mTLS
	err := s.StartSecureGRPCServer(TestSocket, TestCert, TestKey, TestCert)
	Expect(err).To(BeNil())

	// Start HTTPS override server with mTLS (same test cert acts as CA)
	TestHTTPSPort = freePort()
	err = s.StartHealthHTTPSServer(TestHTTPSPort, TestCert, TestKey, TestCert)
	Expect(err).To(BeNil())

	return s
}

var _ = Describe("applyOverrides", func() {
	var s *healthServer

	BeforeEach(func() {
		vitals := healthcheck.Vitals{States: make([]types.DeviceState, 0)}
		s = NewServer(&vitals)
	})

	type overrideEntry struct {
		pciAddress string
		state      pb.DEVICE_STATE
	}

	type expectedEntry struct {
		pciAddress string
		deviceType pb.DEVICE_TYPE
		state      pb.DEVICE_STATE
		source     string
	}

	DescribeTable("state override behaviour",
		func(live []types.DeviceState, overrides []overrideEntry, expected []expectedEntry) {
			for _, ov := range overrides {
				s.overrides[ov.pciAddress] = types.DeviceState{
					PciAddress: ov.pciAddress,
					State:      ov.state,
					Source:     "override",
				}
			}

			result := s.applyOverrides(live)

			Expect(result).To(HaveLen(len(expected)))
			// Build a map for order-independent assertions.
			byPCI := make(map[string]types.DeviceState, len(result))
			for _, r := range result {
				byPCI[r.PciAddress] = r
			}
			for _, exp := range expected {
				r, ok := byPCI[exp.pciAddress]
				Expect(ok).To(BeTrue(), "expected PCI %s in result", exp.pciAddress)
				Expect(r.Type).To(Equal(exp.deviceType), "Type for %s", exp.pciAddress)
				Expect(r.State).To(Equal(exp.state), "State for %s", exp.pciAddress)
				Expect(r.Source).To(Equal(exp.source), "Source for %s", exp.pciAddress)
			}
		},

		Entry("no overrides — live states returned unchanged",
			[]types.DeviceState{
				{PciAddress: "0000:1a:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
			},
			[]overrideEntry{},
			[]expectedEntry{
				{"0000:1a:00.0", pb.DEVICE_TYPE_PF, pb.DEVICE_STATE_ONLINE, "lspci"},
			},
		),

		Entry("override state differs — state and source replaced, live Type preserved",
			[]types.DeviceState{
				{PciAddress: "0000:1a:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
			},
			[]overrideEntry{
				{"0000:1a:00.0", pb.DEVICE_STATE_RUNNING_DIAGNOSTICS},
			},
			[]expectedEntry{
				{"0000:1a:00.0", pb.DEVICE_TYPE_PF, pb.DEVICE_STATE_RUNNING_DIAGNOSTICS, "override"},
			},
		),

		Entry("override state equals live state — entry left unchanged",
			[]types.DeviceState{
				{PciAddress: "0000:1a:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
			},
			[]overrideEntry{
				{"0000:1a:00.0", pb.DEVICE_STATE_ONLINE},
			},
			[]expectedEntry{
				{"0000:1a:00.0", pb.DEVICE_TYPE_PF, pb.DEVICE_STATE_ONLINE, "lspci"},
			},
		),

		Entry("VF live Type preserved when override carries wrong Type",
			[]types.DeviceState{
				{PciAddress: "0000:1a:00.1", Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
			},
			[]overrideEntry{
				{"0000:1a:00.1", pb.DEVICE_STATE_RUNNING_DIAGNOSTICS},
			},
			[]expectedEntry{
				{"0000:1a:00.1", pb.DEVICE_TYPE_VF, pb.DEVICE_STATE_RUNNING_DIAGNOSTICS, "override"},
			},
		),

		Entry("orphan override — device absent from live states is appended",
			[]types.DeviceState{
				{PciAddress: "0000:1a:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
			},
			[]overrideEntry{
				{"0000:ff:00.0", pb.DEVICE_STATE_RUNNING_DIAGNOSTICS},
			},
			[]expectedEntry{
				{"0000:1a:00.0", pb.DEVICE_TYPE_PF, pb.DEVICE_STATE_ONLINE, "lspci"},
				{"0000:ff:00.0", pb.DEVICE_TYPE_DEVICE_TYPE_UNSPECIFIED, pb.DEVICE_STATE_RUNNING_DIAGNOSTICS, "override"},
			},
		),

		Entry("multiple devices — only differing entries replaced",
			[]types.DeviceState{
				{PciAddress: "0000:1a:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
				{PciAddress: "0000:1b:00.0", Type: pb.DEVICE_TYPE_PF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
				{PciAddress: "0000:1c:00.0", Type: pb.DEVICE_TYPE_VF, State: pb.DEVICE_STATE_ONLINE, Source: "lspci"},
			},
			[]overrideEntry{
				{"0000:1a:00.0", pb.DEVICE_STATE_RUNNING_DIAGNOSTICS}, // differs — replaced
				{"0000:1b:00.0", pb.DEVICE_STATE_ONLINE},              // same — untouched
			},
			[]expectedEntry{
				{"0000:1a:00.0", pb.DEVICE_TYPE_PF, pb.DEVICE_STATE_RUNNING_DIAGNOSTICS, "override"},
				{"0000:1b:00.0", pb.DEVICE_TYPE_PF, pb.DEVICE_STATE_ONLINE, "lspci"},
				{"0000:1c:00.0", pb.DEVICE_TYPE_VF, pb.DEVICE_STATE_ONLINE, "lspci"},
			},
		),
	)
})
