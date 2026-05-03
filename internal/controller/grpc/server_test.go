package grpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	controllerconfig "github.com/akam1o/arca-lb/internal/controller/config"
	"github.com/sirupsen/logrus"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestServerStartReturnsNilAfterGracefulStop(t *testing.T) {
	port := freeTCPPort(t)
	server := newTestServer(port, controllerconfig.GRPCConfig{})
	errCh := startServer(t, server, port)

	server.Stop()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error after GracefulStop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for gRPC server to stop")
	}
}

func TestServerAcceptsTLSConnectionsWhenEnabled(t *testing.T) {
	certFile, keyFile, certPEM := writeSelfSignedServerCert(t)
	port := freeTCPPort(t)
	server := newTestServer(port, controllerconfig.GRPCConfig{
		TLS:      true,
		CertFile: certFile,
		KeyFile:  keyFile,
	})
	errCh := startServer(t, server, port)
	defer func() {
		server.Stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("Start returned error after TLS test: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for TLS gRPC server to stop")
		}
	}()

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to append test server cert")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := googlegrpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", port), // nolint:staticcheck // DialContext is adequate for this server compatibility test.
		googlegrpc.WithBlock(),
		googlegrpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			RootCAs:    roots,
			ServerName: "localhost",
			MinVersion: tls.VersionTLS12,
		})),
	)
	if err != nil {
		t.Fatalf("DialContext with TLS: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close TLS connection: %v", err)
	}
}

func newTestServer(port int, grpcCfg controllerconfig.GRPCConfig) *Server {
	if grpcCfg.Host == "" {
		grpcCfg.Host = "127.0.0.1"
	}
	grpcCfg.Port = port

	logger := logrus.New()
	logger.SetOutput(io.Discard)

	return NewServer(&controllerconfig.Config{
		GRPC: grpcCfg,
	}, nil, logger)
}

func startServer(t *testing.T, server *Server, port int) <-chan error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			t.Fatalf("server exited before accepting connections: %v", err)
		case <-deadline:
			t.Fatalf("timed out waiting for %s to accept connections", addr)
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return errCh
			}
		}
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on free port: %v", err)
	}
	defer func() {
		if err := ln.Close(); err != nil {
			t.Fatalf("close free-port listener: %v", err)
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port
}

func writeSelfSignedServerCert(t *testing.T) (string, string, []byte) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile, certPEM
}
