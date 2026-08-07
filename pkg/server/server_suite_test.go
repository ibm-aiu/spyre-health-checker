/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package server

import (
	"context"
	"crypto"
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

	healthcheck "github.com/ibm-aiu/spyre-health-checker/internal/healthcheck"
	utils "github.com/ibm-aiu/spyre-health-checker/internal/utils"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"

	crlog "sigs.k8s.io/controller-runtime/pkg/log"

	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
)

var (
	TestSocket  = "checker.sock"
	TestCertDir = "test-certs"
	TestCert    = ""
	TestKey     = ""

	// UnknownCACert / UnknownCAKey are a self-signed cert/key pair issued by a
	// different CA, used to verify that the server rejects untrusted clients.
	UnknownCACert = ""
	UnknownCAKey  = ""

	// NoOrgCert / NoOrgKey are signed by the trusted CA but carry no Organisation
	// field, triggering the application-level Unauthenticated check in the interceptor.
	NoOrgCert = ""
	NoOrgKey  = ""

	TestHealthServer Server
)

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

// writeCertPair generates an ECDSA P-256 cert/key pair and writes them to
// certPath / keyPath. When caSigner is non-nil the cert is signed by that CA;
// otherwise it is self-signed. org is variadic: omit it to leave Organisation empty.
// writeCertPair generates a self-signed ECDSA P-256 cert/key pair.
// org is variadic: omit it to produce a cert with no Organisation field.
func writeCertPair(certPath, keyPath, cn string,
	serial int64, org ...string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	return writeCertPairSigned(certPath, keyPath, cn, serial, nil, nil, org...)
}

// writeCertPairSigned generates an ECDSA P-256 cert/key pair signed by caCert/caKey
// (or self-signed when both are nil). Returns the parsed cert and the new private key
// so the caller can retain them for further signing.
func writeCertPairSigned(certPath, keyPath, cn string, serial int64,
	caCert *x509.Certificate, caKey *ecdsa.PrivateKey, org ...string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject: pkix.Name{
			Organization: org,
			CommonName:   cn,
		},
		DNSNames:              []string{cn},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// self-signed when no parent is provided
	parent := &tmpl
	signingKey := crypto.Signer(key)
	if caCert != nil && caKey != nil {
		parent = caCert
		signingKey = caKey
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, parent, &key.PublicKey, signingKey)
	if err != nil {
		return nil, nil, err
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = certFile.Close() }()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return nil, nil, err
	}

	keyFile, err := os.Create(keyPath)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = keyFile.Close() }()
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		return nil, nil, err
	}

	parsed, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, err
	}
	return parsed, key, nil
}

func createTestCertificates() error {
	if err := os.MkdirAll(TestCertDir, 0755); err != nil {
		return err
	}

	// Primary CA — also used as the server cert (self-signed).
	TestCert = TestCertDir + "/tls.crt"
	TestKey = TestCertDir + "/tls.key"
	caCert, caKey, err := writeCertPair(TestCert, TestKey, "test-server", 1, "Test Org")
	if err != nil {
		return err
	}

	// A second, independent CA/cert pair that the server does NOT trust.
	UnknownCACert = TestCertDir + "/unknown-ca.crt"
	UnknownCAKey = TestCertDir + "/unknown-ca.key"
	if _, _, err := writeCertPair(UnknownCACert, UnknownCAKey, "unknown-ca", 2, "Test Org"); err != nil {
		return err
	}

	// Signed by the trusted CA but carries no Organisation — TLS handshake
	// passes (cert is trusted), but the stream interceptor returns Unauthenticated.
	NoOrgCert = TestCertDir + "/no-org.crt"
	NoOrgKey = TestCertDir + "/no-org.key"
	if _, _, err := writeCertPairSigned(NoOrgCert, NoOrgKey, "test-server", 3, caCert, caKey /* no org */); err != nil {
		return err
	}

	return nil
}

// NewMTLSGrpcConn creates a gRPC client connection with full mTLS:
//   - clientCertPath / clientKeyPath  – the client's own certificate and key
//   - caCertPath                      – the CA whose cert the client uses to
//     verify the server
func NewMTLSGrpcConn(socket, clientCertPath, clientKeyPath, caCertPath string) (*grpc.ClientConn, error) {
	cert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, err
	}

	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, err
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCert)

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS12,
		// Unix sockets have no hostname; override the server-name check so the
		// cert's CN is still verified against the pool without needing a SAN.
		ServerName: "test-server",
	}

	return grpc.NewClient("unix:"+socket, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
}

func startServer() Server {
	logger := zap.Must(zap.NewDevelopment()).Sugar()
	defer logger.Sync() //nolint:errcheck
	SetLogger(logger)

	vitals := healthcheck.Vitals{States: make([]types.DeviceState, 0)}
	s := NewServer(&vitals)

	// Start secure server with mTLS
	err := s.StartSecureGRPCServer(TestSocket, TestCert, TestKey, TestCert)
	Expect(err).To(BeNil())

	return s
}
