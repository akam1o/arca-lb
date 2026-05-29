package grpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	controllerconfig "github.com/akam1o/arca-lb/internal/controller/config"
	"github.com/akam1o/arca-lb/internal/testutil"
	pb "github.com/akam1o/arca-lb/pkg/grpc"
	"github.com/sirupsen/logrus"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestServerStartReturnsNilAfterGracefulStop(t *testing.T) {
	port := freeTCPPort(t)
	server := newTestServer(port, controllerconfig.GRPCConfig{})
	errCh := startServer(t, server, port)

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

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
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Stop(stopCtx); err != nil {
			t.Fatalf("Stop returned error after TLS test: %v", err)
		}
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
		googlegrpc.WithBlock(), // nolint:staticcheck // WithBlock keeps this grpc 1.x DialContext test bounded by ctx.
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

func TestServerStopForcesStopWhenWatchConfigStreamIsActive(t *testing.T) {
	port := freeTCPPort(t)

	mockDS := testutil.NewMockDataStore()
	mockDS.SetConfig(&models.Config{
		Revision: 1,
		VIPs:     []models.VIPConfig{},
	})
	watchCh := make(chan datastore.WatchEvent)
	mockDS.SetWatchChannel(watchCh)

	server := newTestServerWithDatastore(port, controllerconfig.GRPCConfig{}, mockDS)
	errCh := startServer(t, server, port)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	conn, err := googlegrpc.DialContext(dialCtx, fmt.Sprintf("127.0.0.1:%d", port), // nolint:staticcheck // DialContext is adequate for this server shutdown test.
		googlegrpc.WithBlock(), // nolint:staticcheck // WithBlock keeps this grpc 1.x DialContext test bounded by ctx.
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Fatalf("close connection: %v", err)
		}
	}()

	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	stream, err := pb.NewConfigSyncClient(conn).WatchConfig(streamCtx, &pb.WatchConfigRequest{
		AgentId:         "agent-1",
		CurrentRevision: 0,
	})
	if err != nil {
		t.Fatalf("WatchConfig: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("receive initial config: %v", err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stopCancel()
	if err := server.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop error = %v, want context deadline exceeded", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error after forced stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for gRPC server to stop after forced stop")
	}
}

func TestServerRequiresAPIKeyWhenConfigured(t *testing.T) {
	const apiKey = "controller-grpc-secret"

	certFile, keyFile, certPEM := writeSelfSignedServerCert(t)
	mockDS := testutil.NewMockDataStore()
	mockDS.SetConfig(&models.Config{
		Revision: 1,
		VIPs:     []models.VIPConfig{},
	})

	port := freeTCPPort(t)
	server := newTestServerWithDatastore(port, controllerconfig.GRPCConfig{
		APIKey:   apiKey,
		TLS:      true,
		CertFile: certFile,
		KeyFile:  keyFile,
	}, mockDS)
	errCh := startServer(t, server, port)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Stop(stopCtx); err != nil {
			t.Fatalf("Stop returned error after API key test: %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("Start returned error after API key test: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for API key gRPC server to stop")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to append test server cert")
	}
	conn, err := googlegrpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", port), // nolint:staticcheck // DialContext is adequate for this server auth test.
		googlegrpc.WithBlock(), // nolint:staticcheck // WithBlock keeps this grpc 1.x DialContext test bounded by ctx.
		googlegrpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			RootCAs:    roots,
			ServerName: "localhost",
			MinVersion: tls.VersionTLS12,
		})),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Fatalf("close API key test connection: %v", err)
		}
	}()

	client := pb.NewConfigSyncClient(conn)
	registerReq := &pb.RegisterAgentRequest{AgentId: "agent-1"}
	if _, err := client.RegisterAgent(ctx, registerReq); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("RegisterAgent without API key error = %v, want unauthenticated", err)
	}

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+apiKey)
	resp, err := client.RegisterAgent(authCtx, registerReq)
	if err != nil {
		t.Fatalf("RegisterAgent with API key: %v", err)
	}
	if !resp.Success {
		t.Fatalf("RegisterAgent success = false, message = %q", resp.Message)
	}

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer streamCancel()
	stream, err := client.WatchConfig(streamCtx, &pb.WatchConfigRequest{AgentId: "agent-1"})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("WatchConfig without API key error = %v, want unauthenticated", err)
	}

	authStreamCtx := metadata.AppendToOutgoingContext(streamCtx, "authorization", "Bearer "+apiKey)
	authStream, err := client.WatchConfig(authStreamCtx, &pb.WatchConfigRequest{AgentId: "agent-1"})
	if err != nil {
		t.Fatalf("WatchConfig with API key: %v", err)
	}
	if _, err := authStream.Recv(); err != nil {
		t.Fatalf("receive authenticated initial config: %v", err)
	}
}

func TestServerRejectsAPIKeyWithoutTLS(t *testing.T) {
	server := newTestServer(freeTCPPort(t), controllerconfig.GRPCConfig{
		APIKey: "controller-grpc-secret",
	})

	err := server.initializeGRPCServer()
	if err == nil || !strings.Contains(err.Error(), "grpc.tls must be enabled when grpc.api_key is set") {
		t.Fatalf("initializeGRPCServer error = %v, want API key TLS validation error", err)
	}
}

func newTestServer(port int, grpcCfg controllerconfig.GRPCConfig) *Server {
	return newTestServerWithDatastore(port, grpcCfg, nil)
}

func newTestServerWithDatastore(port int, grpcCfg controllerconfig.GRPCConfig, ds datastore.DataStore) *Server {
	if grpcCfg.Host == "" {
		grpcCfg.Host = "127.0.0.1"
	}
	grpcCfg.Port = port

	logger := logrus.New()
	logger.SetOutput(io.Discard)

	return NewServer(&controllerconfig.Config{
		GRPC: grpcCfg,
	}, ds, logger)
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
