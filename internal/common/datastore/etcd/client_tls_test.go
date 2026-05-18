package etcd

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadTLSConfigAllowsServerTLSWithoutClientCertificate(t *testing.T) {
	_, _, caPEM := writeSelfSignedTLSCert(t, "test-ca", true)
	caFile := writeTLSFile(t, "ca.crt", caPEM)

	tlsConfig, err := loadTLSConfig("", "", caFile)
	if err != nil {
		t.Fatalf("loadTLSConfig: %v", err)
	}
	if tlsConfig.RootCAs == nil {
		t.Fatal("RootCAs is nil")
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %v, want TLS 1.2", tlsConfig.MinVersion)
	}
	if got := len(tlsConfig.Certificates); got != 0 {
		t.Fatalf("client certificate count = %d, want 0", got)
	}
}

func TestLoadTLSConfigLoadsOptionalClientCertificate(t *testing.T) {
	certFile, keyFile, caPEM := writeSelfSignedTLSCert(t, "client", true)
	caFile := writeTLSFile(t, "ca.crt", caPEM)

	tlsConfig, err := loadTLSConfig(certFile, keyFile, caFile)
	if err != nil {
		t.Fatalf("loadTLSConfig: %v", err)
	}
	if got := len(tlsConfig.Certificates); got != 1 {
		t.Fatalf("client certificate count = %d, want 1", got)
	}
}

func TestLoadTLSConfigRejectsPartialClientCertificate(t *testing.T) {
	certFile, _, caPEM := writeSelfSignedTLSCert(t, "client", true)
	caFile := writeTLSFile(t, "ca.crt", caPEM)

	_, err := loadTLSConfig(certFile, "", caFile)
	if err == nil {
		t.Fatal("expected partial client certificate config to fail")
	}
	if got := err.Error(); !strings.Contains(got, "datastore.etcd.cert_file and datastore.etcd.key_file must both be set") {
		t.Fatalf("loadTLSConfig error = %q, want partial client certificate error", got)
	}
}

func writeSelfSignedTLSCert(t *testing.T, commonName string, isCA bool) (string, string, []byte) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}

	keyUsage := x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
	if isCA {
		keyUsage |= x509.KeyUsageCertSign
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              keyUsage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IsCA:                  isCA,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	certFile := writeTLSFile(t, commonName+".crt", certPEM)
	keyFile := writeTLSFile(t, commonName+".key", keyPEM)
	return certFile, keyFile, certPEM
}

func writeTLSFile(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
