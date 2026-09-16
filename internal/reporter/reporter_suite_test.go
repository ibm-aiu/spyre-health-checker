/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	RASSource      = "ras"
	LsPCISource    = "lspci"
	CardmgmtSource = "cardmgmt"

	TestPCIAddress  = "0000:1a:00.0"
	TestPCIAddress2 = "0000:1b:00.0"
	TestPCIAddress3 = "0000:1c:00.0"
	TestPCIAddress4 = "0000:1d:00.0"
)

var (
	// testCertDir holds the temp directory for test TLS materials.
	testCertDir string
	// TestTLSCert / TestTLSKey / TestTLSCA are paths to a self-signed cert
	// used by both the fake mTLS cardhealth server and the test client.
	TestTLSCert string
	TestTLSKey  string
	TestTLSCA   string
	// TestTLSServerName is the CN / SAN embedded in the test certificate.
	TestTLSServerName = "test-cardhealth"
)

func TestReporter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reporter Suite")
}

var _ = BeforeSuite(func() {
	var err error
	testCertDir, err = os.MkdirTemp("", "reporter-test-certs-*")
	Expect(err).NotTo(HaveOccurred())

	TestTLSCert = testCertDir + "/tls.crt"
	TestTLSKey = testCertDir + "/tls.key"
	TestTLSCA = TestTLSCert // self-signed; CA == the cert itself

	Expect(writeTestCertPair(TestTLSCert, TestTLSKey, TestTLSServerName, 1, "TestOrg")).To(Succeed())
})

var _ = AfterSuite(func() {
	if testCertDir != "" {
		Expect(os.RemoveAll(testCertDir)).To(Succeed())
	}
})

// writeTestCertPair generates a self-signed ECDSA P-256 cert/key pair and
// writes them to certPath / keyPath. cn is used for both the CommonName and
// a DNS SAN; org is the Subject Organization.
func writeTestCertPair(certPath, keyPath, cn string, serial int64, org string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject: pkix.Name{
			Organization: []string{org},
			CommonName:   cn,
		},
		DNSNames:              []string{cn},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, crypto.Signer(key))
	if err != nil {
		return err
	}

	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer func() { _ = certFile.Close() }()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return err
	}

	keyFile, err := os.Create(keyPath)
	if err != nil {
		return err
	}
	defer func() { _ = keyFile.Close() }()
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
}
