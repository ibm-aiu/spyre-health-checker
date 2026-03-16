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

	healthcheck "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/healthcheck"
	utils "github.ibm.com/ai-chip-toolchain/spyre-health-checker/internal/utils"
	types "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/types"

	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	pb "github.ibm.com/ai-chip-toolchain/spyre-health-checker/pkg/health/spyre"
)

var (
	TestSocket  = "checker.sock"
	TestCertDir = "test-certs"
	TestCert    = ""
	TestKey     = ""

	TestHealthServer *healthServer
)

type Client struct {
	client  pb.SpyreHealthServiceClient
	mu      sync.RWMutex
	devices []*pb.Device
	conn    *grpc.ClientConn
	cancel  context.CancelFunc
}

func NewClient() *Client {
	var opts []grpc.DialOption

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
	c.conn.Close()
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
	os.Setenv(utils.PseudoDeviceModeKey, "1")

	ws := zapcore.AddSync(GinkgoWriter)

	encCfg := zap.NewDevelopmentEncoderConfig()
	enc := zapcore.NewConsoleEncoder(encCfg)

	core := zapcore.NewCore(enc, ws, zap.DebugLevel)
	uber := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
	defer uber.Sync()

	crlog.SetLogger(zapr.NewLogger(uber))

	// Create test certificates
	err := createTestCertificates()
	Expect(err).To(BeNil())

	// Set environment variables to use test certificates
	os.Setenv("SPYRE_TLS_CERT", TestCert)
	os.Setenv("SPYRE_TLS_KEY", TestKey)
	os.Setenv("SPYRE_TLS_CA", TestCert) // Use same cert as CA for self-signed

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
	os.Unsetenv("SPYRE_TLS_CERT")
	os.Unsetenv("SPYRE_TLS_KEY")
	os.Unsetenv("SPYRE_TLS_CA")
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
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return err
	}

	// Write private key to file
	TestKey = TestCertDir + "/tls.key"
	keyFile, err := os.Create(TestKey)
	if err != nil {
		return err
	}
	defer keyFile.Close()

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
	err := s.StartSecureGRPCServer(TestSocket, TestCert, TestKey)
	Expect(err).To(BeNil())

	return s
}
