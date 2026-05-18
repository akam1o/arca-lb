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

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/common/models"
	pb "github.com/akam1o/arca-lb/pkg/grpc"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Mock gRPC server for testing
type mockConfigSyncServer struct {
	pb.UnimplementedConfigSyncServer

	// Control behavior
	registerSuccess  bool
	registerMessage  string
	registerErr      error
	registerConfig   *models.Config
	watchConfig      *models.Config
	watchError       error
	getConfig        *models.Config
	getConfigErr     error
	heartbeatSuccess bool
	requiredAPIKey   string
}

func (m *mockConfigSyncServer) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest) (*pb.RegisterAgentResponse, error) {
	if err := m.requireAPIKey(ctx); err != nil {
		return nil, err
	}
	if m.registerErr != nil {
		return nil, m.registerErr
	}
	return &pb.RegisterAgentResponse{
		Success: m.registerSuccess,
		Message: m.registerMessage,
		Config:  testConfigSnapshot(m.registerConfig),
	}, nil
}

func (m *mockConfigSyncServer) WatchConfig(req *pb.WatchConfigRequest, stream pb.ConfigSync_WatchConfigServer) error {
	if err := m.requireAPIKey(stream.Context()); err != nil {
		return err
	}
	if m.watchError != nil {
		return m.watchError
	}

	// Send initial config if available
	if m.watchConfig != nil {
		resp := &pb.WatchConfigResponse{
			Type:   pb.UpdateType_UPDATE_TYPE_FULL,
			Config: testConfigSnapshot(m.watchConfig),
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}

	// Keep stream open until context is cancelled
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (m *mockConfigSyncServer) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	if err := m.requireAPIKey(ctx); err != nil {
		return nil, err
	}
	return &pb.HeartbeatResponse{
		Success: m.heartbeatSuccess,
	}, nil
}

func (m *mockConfigSyncServer) GetConfig(ctx context.Context, req *pb.GetConfigRequest) (*pb.GetConfigResponse, error) {
	if err := m.requireAPIKey(ctx); err != nil {
		return nil, err
	}
	if m.getConfigErr != nil {
		return nil, m.getConfigErr
	}
	if m.getConfig != nil {
		return &pb.GetConfigResponse{
			Config: testConfigSnapshot(m.getConfig),
		}, nil
	}
	return &pb.GetConfigResponse{
		Unchanged: true,
	}, nil
}

type fakeConfigSyncClient struct {
	getConfigResp *pb.GetConfigResponse
	getConfigErr  error
	getConfigReq  *pb.GetConfigRequest
	watchStream   pb.ConfigSync_WatchConfigClient
	watchErr      error
	registerResp  *pb.RegisterAgentResponse
	registerErr   error
	heartbeatResp *pb.HeartbeatResponse
	heartbeatErr  error
}

func (f *fakeConfigSyncClient) GetConfig(ctx context.Context, req *pb.GetConfigRequest, opts ...grpc.CallOption) (*pb.GetConfigResponse, error) {
	f.getConfigReq = req
	return f.getConfigResp, f.getConfigErr
}

func (f *fakeConfigSyncClient) WatchConfig(ctx context.Context, req *pb.WatchConfigRequest, opts ...grpc.CallOption) (pb.ConfigSync_WatchConfigClient, error) {
	return f.watchStream, f.watchErr
}

func (f *fakeConfigSyncClient) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest, opts ...grpc.CallOption) (*pb.RegisterAgentResponse, error) {
	return f.registerResp, f.registerErr
}

func (f *fakeConfigSyncClient) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest, opts ...grpc.CallOption) (*pb.HeartbeatResponse, error) {
	return f.heartbeatResp, f.heartbeatErr
}

type fakeWatchConfigClient struct {
	grpc.ClientStream
	responses []*pb.WatchConfigResponse
	err       error
}

func (f *fakeWatchConfigClient) Recv() (*pb.WatchConfigResponse, error) {
	if len(f.responses) == 0 {
		return nil, f.err
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (m *mockConfigSyncServer) requireAPIKey(ctx context.Context) error {
	if m.requiredAPIKey == "" {
		return nil
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "unauthenticated")
	}
	for _, value := range md.Get("authorization") {
		fields := strings.Fields(value)
		if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") && fields[1] == m.requiredAPIKey {
			return nil
		}
	}
	for _, value := range md.Get("x-api-key") {
		if strings.TrimSpace(value) == m.requiredAPIKey {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "unauthenticated")
}

// Start a mock gRPC server for testing
func startMockServer(t *testing.T, mock *mockConfigSyncServer) (func(context.Context, string) (net.Conn, error), func()) {
	lis := bufconn.Listen(1024 * 1024)

	s := grpc.NewServer()
	pb.RegisterConfigSyncServer(s, mock)

	go func() {
		if err := s.Serve(lis); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	cleanup := func() {
		s.GracefulStop()
		if err := lis.Close(); err != nil {
			t.Logf("listener close error: %v", err)
		}
	}

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}

	return dialer, cleanup
}

func isClientStarted(c *Client) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.started
}

func isClientConnected(c *Client) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

func clientConn(c *Client) *grpc.ClientConn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

func newClientWithConfigSyncClient(client pb.ConfigSyncClient) *Client {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 100 * time.Millisecond,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	c := NewClient(cfg, logger, nil)
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.mu.Lock()
	c.client = client
	c.connected = true
	c.mu.Unlock()
	return c
}

func TestNewClient(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID: "test-agent",
		},
		Controller: config.ControllerConfig{
			Address:         "localhost:50051",
			Timeout:         5 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	handler := func(config *models.Config) error {
		return nil
	}

	client := NewClient(cfg, logger, handler)

	if client == nil {
		t.Fatal("NewClient returned nil")
	}

	if client.config != cfg {
		t.Error("Client config not set correctly")
	}
}

func testConfigSnapshot(config *models.Config) *pb.ConfigSnapshot {
	if config == nil {
		return nil
	}
	return &pb.ConfigSnapshot{
		Revision: config.Revision,
		Vips:     make([]*pb.VIPConfig, 0),
	}
}

func TestClientStartStop(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 1 * time.Second,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	handler := func(config *models.Config) error {
		return nil
	}

	client := NewClient(cfg, logger, handler)
	client.dialContext = dialer

	ctx := context.Background()

	// Start client
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	// Give it time to connect
	time.Sleep(200 * time.Millisecond)

	// Stop client
	client.Stop()

	// Verify client stopped cleanly
	if isClientStarted(client) {
		t.Error("Client still marked as started after Stop()")
	}
}

func TestClientRestart(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 1 * time.Second,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	handler := func(config *models.Config) error {
		return nil
	}

	client := NewClient(cfg, logger, handler)
	client.dialContext = dialer

	ctx := context.Background()

	// First start
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start client first time: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Stop
	client.Stop()

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Restart
	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to restart client: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Stop again
	client.Stop()

	if isClientStarted(client) {
		t.Error("Client still marked as started after second Stop()")
	}
}

func TestClientStartAlreadyStarted(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 1 * time.Second,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	handler := func(config *models.Config) error {
		return nil
	}

	client := NewClient(cfg, logger, handler)
	client.dialContext = dialer

	ctx := context.Background()

	// Start client
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	// Try to start again - should return error
	err = client.Start(ctx)
	if err == nil {
		t.Error("Expected error when starting already-started client")
	}

	client.Stop()
}

func TestClientStopNotStarted(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID: "test-agent",
		},
		Controller: config.ControllerConfig{
			Address:         "localhost:50051",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	handler := func(config *models.Config) error {
		return nil
	}

	client := NewClient(cfg, logger, handler)

	// Stop without starting - should not panic
	client.Stop()

	// Should be safe to call multiple times
	client.Stop()
}

func TestClientConnectionFailure(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID: "test-agent",
		},
		Controller: config.ControllerConfig{
			Address:         "localhost:9999", // Invalid address
			Timeout:         100 * time.Millisecond,
			MaxRetries:      2,
			RetryBackoff:    50 * time.Millisecond,
			MaxRetryBackoff: 100 * time.Millisecond,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	handler := func(config *models.Config) error {
		return nil
	}

	client := NewClient(cfg, logger, handler)

	ctx := context.Background()

	// Start should fail due to connection error
	err := client.Start(ctx)
	if err == nil {
		t.Error("Expected error when connecting to invalid address")
		client.Stop()
	}

	// Client should not be marked as started after failure
	if isClientStarted(client) {
		t.Error("Client marked as started after connection failure")
	}
}

func TestClientStartFailsWhenRegistrationRejected(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  false,
		registerMessage:  "not allowed",
		heartbeatSuccess: true,
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 100 * time.Millisecond,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, nil)
	client.dialContext = dialer

	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("expected registration rejection to fail Start")
	}
	if got := err.Error(); !strings.Contains(got, "registration rejected") {
		t.Fatalf("Start error = %q, want registration rejected", got)
	}
	if isClientStarted(client) {
		t.Fatal("client remains started after registration rejection")
	}
	if isClientConnected(client) {
		t.Fatal("client remains connected after registration rejection")
	}
	if conn := clientConn(client); conn != nil {
		t.Fatal("client connection was retained after registration rejection")
	}
}

func TestClientStartFailsWhenRegistrationRPCFails(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerErr:      status.Error(codes.Unavailable, "register unavailable"),
		heartbeatSuccess: true,
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 100 * time.Millisecond,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, nil)
	client.dialContext = dialer

	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("expected registration RPC failure to fail Start")
	}
	if got := err.Error(); !strings.Contains(got, "registration failed") {
		t.Fatalf("Start error = %q, want registration failed", got)
	}
	if isClientStarted(client) {
		t.Fatal("client remains started after registration RPC failure")
	}
	if isClientConnected(client) {
		t.Fatal("client remains connected after registration RPC failure")
	}
}

func TestClientRegisterRejectsNilResponse(t *testing.T) {
	client := newClientWithConfigSyncClient(&fakeConfigSyncClient{})
	defer client.cancel()

	err := client.register()
	if err == nil {
		t.Fatal("expected nil registration response to fail")
	}
	if got := err.Error(); !strings.Contains(got, "registration returned nil response") {
		t.Fatalf("register error = %q, want nil response error", got)
	}
}

func TestClientStartFailsWhenInitialConfigHandlerFails(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		registerConfig:   &models.Config{Revision: 9},
		heartbeatSuccess: true,
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 100 * time.Millisecond,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, func(config *models.Config) error {
		return fmt.Errorf("apply failed")
	})
	client.dialContext = dialer

	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("expected initial configuration apply failure to fail Start")
	}
	if got := err.Error(); !strings.Contains(got, "failed to apply initial configuration") {
		t.Fatalf("Start error = %q, want failed to apply initial configuration", got)
	}
	if got := client.getCurrentRevision(); got != 0 {
		t.Fatalf("current revision = %d, want 0 after failed apply", got)
	}
	if isClientStarted(client) {
		t.Fatal("client remains started after initial configuration apply failure")
	}
	if isClientConnected(client) {
		t.Fatal("client remains connected after initial configuration apply failure")
	}
}

func TestOutgoingAPIKeyContextAddsBearerMetadata(t *testing.T) {
	const apiKey = "agent-controller-secret"

	ctx := outgoingAPIKeyContext(context.Background(), apiKey)

	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("outgoing metadata was not set")
	}
	values := md.Get("authorization")
	if len(values) != 1 || values[0] != "Bearer "+apiKey {
		t.Fatalf("authorization metadata = %#v, want bearer API key", values)
	}
}

func TestClientStartRejectsAPIKeyWithoutTLS(t *testing.T) {
	const apiKey = "agent-controller-secret"

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 100 * time.Millisecond,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			APIKey:          apiKey,
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, nil)

	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("expected API key without TLS to fail Start")
	}
	if got := err.Error(); !strings.Contains(got, "controller.tls.enabled must be enabled when controller.api_key is set") {
		t.Fatalf("Start error = %q, want controller API key TLS validation error", got)
	}
	if isClientStarted(client) {
		t.Fatal("client remains started after API key without TLS failure")
	}
	if isClientConnected(client) {
		t.Fatal("client remains connected after API key without TLS failure")
	}
}

func TestClientStartRejectsAPIKeyWithInsecureSkipVerify(t *testing.T) {
	const apiKey = "agent-controller-secret"

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 100 * time.Millisecond,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			APIKey:          apiKey,
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
			TLS: config.TLSConfig{
				Enabled:            true,
				InsecureSkipVerify: true,
			},
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, nil)

	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("expected API key with insecure TLS verification to fail Start")
	}
	if got := err.Error(); !strings.Contains(got, "controller.tls.insecure_skip_verify must be false when controller.api_key is set") {
		t.Fatalf("Start error = %q, want controller API key insecure TLS validation error", got)
	}
	if isClientStarted(client) {
		t.Fatal("client remains started after API key insecure TLS failure")
	}
	if isClientConnected(client) {
		t.Fatal("client remains connected after API key insecure TLS failure")
	}
}

func TestClientLoadTLSConfigAllowsServerTLSWithoutClientCertificate(t *testing.T) {
	caFile := writeSelfSignedCACert(t)
	client := NewClient(&config.Config{
		Controller: config.ControllerConfig{
			TLS: config.TLSConfig{
				Enabled: true,
				CAFile:  caFile,
			},
		},
	}, logrus.New(), nil)

	tlsConfig, err := client.loadTLSConfig()
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

func TestClientLoadTLSConfigRejectsPartialClientCertificate(t *testing.T) {
	caFile := writeSelfSignedCACert(t)
	client := NewClient(&config.Config{
		Controller: config.ControllerConfig{
			TLS: config.TLSConfig{
				Enabled:  true,
				CAFile:   caFile,
				CertFile: "/tmp/client.crt",
			},
		},
	}, logrus.New(), nil)

	_, err := client.loadTLSConfig()
	if err == nil {
		t.Fatal("expected partial client certificate config to fail")
	}
	if got := err.Error(); !strings.Contains(got, "tls.cert_file and tls.key_file must both be set") {
		t.Fatalf("loadTLSConfig error = %q, want partial client certificate error", got)
	}
}

func TestClientStartFailsWhenAPIKeyIsMissing(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
		requiredAPIKey:   "agent-controller-secret",
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 100 * time.Millisecond,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, nil)
	client.dialContext = dialer

	err := client.Start(context.Background())
	if err == nil {
		t.Fatal("expected missing API key to fail Start")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Start error code = %v, want unauthenticated (err = %v)", status.Code(err), err)
	}
	if isClientStarted(client) {
		t.Fatal("client remains started after missing API key failure")
	}
	if isClientConnected(client) {
		t.Fatal("client remains connected after missing API key failure")
	}
}

func writeSelfSignedCACert(t *testing.T) string {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-ca",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, certPEM, 0600); err != nil {
		t.Fatalf("write CA certificate: %v", err)
	}
	return path
}

func TestClientCancellation(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 100 * time.Millisecond,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	handler := func(config *models.Config) error {
		return nil
	}

	client := NewClient(cfg, logger, handler)
	client.dialContext = dialer

	ctx, cancel := context.WithCancel(context.Background())

	// Start client
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Stop should complete quickly even though context is cancelled
	done := make(chan struct{})
	go func() {
		client.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success - Stop completed
	case <-time.After(2 * time.Second):
		t.Error("Stop() did not complete in time after context cancellation")
	}
}

func TestClientStopDoesNotHoldLockWhileWaitingForWorkers(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID: "test-agent",
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, nil)
	client.started = true
	client.connected = true
	client.stopCh = make(chan struct{})
	client.doneCh = make(chan struct{})
	client.cancel = func() {}

	workerSawStop := make(chan struct{})
	allowReadLock := make(chan struct{})
	readLockAcquired := make(chan struct{})

	client.wg.Add(1)
	go func() {
		defer client.wg.Done()
		<-client.stopCh
		close(workerSawStop)
		<-allowReadLock
		client.mu.RLock()
		close(readLockAcquired)
		client.mu.RUnlock()
	}()

	stopped := make(chan struct{})
	go func() {
		client.Stop()
		close(stopped)
	}()

	select {
	case <-workerSawStop:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not close stopCh")
	}

	close(client.doneCh)
	time.Sleep(100 * time.Millisecond)
	close(allowReadLock)

	select {
	case <-readLockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("worker could not acquire read lock during Stop")
	}

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not complete")
	}

	if isClientStarted(client) {
		t.Error("Client still marked as started after Stop()")
	}
}

func TestSleepWithStopReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if sleepWithStop(ctx, make(chan struct{}), time.Hour) {
		t.Fatal("sleepWithStop returned true after context cancellation")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("sleepWithStop took %s after context cancellation", elapsed)
	}
}

func TestSleepWithStopReturnsOnStopSignal(t *testing.T) {
	stopCh := make(chan struct{})
	close(stopCh)

	start := time.Now()
	if sleepWithStop(context.Background(), stopCh, time.Hour) {
		t.Fatal("sleepWithStop returned true after stop signal")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("sleepWithStop took %s after stop signal", elapsed)
	}
}

func TestSleepWithStopReturnsTrueAfterDuration(t *testing.T) {
	if !sleepWithStop(context.Background(), make(chan struct{}), time.Millisecond) {
		t.Fatal("sleepWithStop returned false after timer elapsed")
	}
}

func TestClientWatchError(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
		watchError:       status.Error(codes.Unavailable, "service unavailable"),
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 1 * time.Second,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	handler := func(config *models.Config) error {
		return nil
	}

	client := NewClient(cfg, logger, handler)
	client.dialContext = dialer

	ctx := context.Background()

	// Start client - should succeed even though watch will fail
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	// Give watch time to fail and retry
	time.Sleep(300 * time.Millisecond)

	// Stop should work even with watch errors
	client.Stop()
}

func TestClientWatchReturnsErrorOnServerEOF(t *testing.T) {
	client := newClientWithConfigSyncClient(&fakeConfigSyncClient{
		watchStream: &fakeWatchConfigClient{err: io.EOF},
	})
	defer client.cancel()

	err := client.watch(context.Background())
	if err == nil {
		t.Fatal("expected server EOF to fail watch")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("watch error = %v, want io.EOF", err)
	}
	if got := err.Error(); !strings.Contains(got, "watch stream closed by server") {
		t.Fatalf("watch error = %q, want server closed message", got)
	}
}

func TestClientWatchDoesNotAdvanceRevisionWhenHandlerFails(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
		watchConfig:      &models.Config{Revision: 12},
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 1 * time.Second,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, func(config *models.Config) error {
		return fmt.Errorf("apply failed")
	})
	client.dialContext = dialer

	client.ctx, client.cancel = context.WithCancel(context.Background())
	defer client.cancel()
	if err := client.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		if conn := clientConn(client); conn != nil {
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.watch(ctx)
	if err == nil {
		t.Fatal("expected watch to return configuration apply failure")
	}
	if got := err.Error(); !strings.Contains(got, "failed to apply configuration") {
		t.Fatalf("watch error = %q, want failed to apply configuration", got)
	}
	if got := client.getCurrentRevision(); got != 0 {
		t.Fatalf("current revision = %d, want 0 after failed watch apply", got)
	}
}

func TestClientWatchRejectsNilResponse(t *testing.T) {
	client := newClientWithConfigSyncClient(&fakeConfigSyncClient{
		watchStream: &fakeWatchConfigClient{
			responses: []*pb.WatchConfigResponse{nil},
		},
	})
	defer client.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.watch(ctx)
	if err == nil {
		t.Fatal("expected nil watch response to fail")
	}
	if got := err.Error(); !strings.Contains(got, "watch stream returned nil response") {
		t.Fatalf("watch error = %q, want nil response error", got)
	}
}

func TestClientFetchConfigDoesNotAdvanceRevisionWhenHandlerFails(t *testing.T) {
	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
		getConfig:        &models.Config{Revision: 15},
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 1 * time.Second,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client := NewClient(cfg, logger, func(config *models.Config) error {
		return fmt.Errorf("apply failed")
	})
	client.dialContext = dialer

	client.ctx, client.cancel = context.WithCancel(context.Background())
	defer client.cancel()
	if err := client.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		if conn := clientConn(client); conn != nil {
			_ = conn.Close()
		}
	}()

	err := client.fetchConfig()
	if err == nil {
		t.Fatal("expected fetchConfig to return configuration apply failure")
	}
	if got := err.Error(); !strings.Contains(got, "failed to apply config") {
		t.Fatalf("fetchConfig error = %q, want failed to apply config", got)
	}
	if got := client.getCurrentRevision(); got != 0 {
		t.Fatalf("current revision = %d, want 0 after failed fetch apply", got)
	}
}

func TestClientFetchConfigRejectsNilResponse(t *testing.T) {
	client := newClientWithConfigSyncClient(&fakeConfigSyncClient{})
	defer client.cancel()

	err := client.fetchConfig()
	if err == nil {
		t.Fatal("expected nil get config response to fail")
	}
	if got := err.Error(); !strings.Contains(got, "get config returned nil response") {
		t.Fatalf("fetchConfig error = %q, want nil response error", got)
	}
}

func TestClientFetchConfigIncludesAgentID(t *testing.T) {
	fakeClient := &fakeConfigSyncClient{
		getConfigResp: &pb.GetConfigResponse{Unchanged: true},
	}
	client := newClientWithConfigSyncClient(fakeClient)
	defer client.cancel()

	if err := client.fetchConfig(); err != nil {
		t.Fatalf("fetchConfig: %v", err)
	}
	if fakeClient.getConfigReq == nil {
		t.Fatal("GetConfig request was not recorded")
	}
	if fakeClient.getConfigReq.AgentId != "test-agent" {
		t.Fatalf("GetConfig AgentId = %q, want test-agent", fakeClient.getConfigReq.AgentId)
	}
}

func TestClientHeartbeatRejectsNilResponse(t *testing.T) {
	client := newClientWithConfigSyncClient(&fakeConfigSyncClient{})
	defer client.cancel()

	err := client.sendHeartbeat()
	if err == nil {
		t.Fatal("expected nil heartbeat response to fail")
	}
	if got := err.Error(); !strings.Contains(got, "heartbeat returned nil response") {
		t.Fatalf("sendHeartbeat error = %q, want nil response error", got)
	}
}

func TestClientConfigHandler(t *testing.T) {
	testConfig := &models.Config{
		Revision: 1,
		VIPs:     []models.VIPConfig{},
	}

	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
		watchConfig:      testConfig,
	}

	dialer, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &config.Config{
		Agent: config.AgentConfig{
			ID:                "test-agent",
			HeartbeatInterval: 1 * time.Second,
		},
		Controller: config.ControllerConfig{
			Address:         "bufnet",
			Timeout:         2 * time.Second,
			MaxRetries:      3,
			RetryBackoff:    100 * time.Millisecond,
			MaxRetryBackoff: 1 * time.Second,
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	configReceived := false
	handlerErr := fmt.Errorf("handler error")

	handler := func(config *models.Config) error {
		configReceived = true
		return handlerErr
	}

	client := NewClient(cfg, logger, handler)
	client.dialContext = dialer

	ctx := context.Background()

	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	// Wait for config to be received
	time.Sleep(200 * time.Millisecond)

	client.Stop()

	if !configReceived {
		t.Error("Config handler was not called")
	}
}

func TestConvertProtoToConfigParsesHealthCheckConfig(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	client := NewClient(&config.Config{}, logger, nil)
	now := timestamppb.Now()

	got, err := client.convertProtoToConfig(&pb.ConfigSnapshot{
		Revision: 7,
		Vips: []*pb.VIPConfig{
			{
				Vip: &pb.VIP{
					Id:        "vip-1",
					Vip:       "192.168.1.100",
					Port:      80,
					Protocol:  pb.Protocol_PROTOCOL_TCP,
					LbMethod:  pb.LBMethod_LB_METHOD_MAGLEV,
					CreatedAt: now,
					UpdatedAt: now,
				},
				HealthCheck: &pb.HealthCheck{
					Id:          "hc-1",
					VipId:       "vip-1",
					Type:        pb.HCType_HC_TYPE_HTTP,
					IntervalSec: 10,
					TimeoutSec:  5,
					RiseCount:   3,
					FallCount:   3,
					Config:      `{"port":8080,"path":"/health","expected_codes":[200,204]}`,
					CreatedAt:   now,
					UpdatedAt:   now,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("convertProtoToConfig: %v", err)
	}

	if got.Revision != 7 {
		t.Fatalf("Revision = %d, want 7", got.Revision)
	}
	if len(got.VIPs) != 1 || got.VIPs[0].HealthCheck == nil {
		t.Fatalf("expected one VIP with health check, got %#v", got.VIPs)
	}

	hcConfig := got.VIPs[0].HealthCheck.Config
	if port, ok := hcConfig["port"].(float64); !ok || port != 8080 {
		t.Fatalf("port = %#v, want JSON number 8080", hcConfig["port"])
	}
	if path, ok := hcConfig["path"].(string); !ok || path != "/health" {
		t.Fatalf("path = %#v, want /health", hcConfig["path"])
	}
	codes, ok := hcConfig["expected_codes"].([]interface{})
	if !ok || len(codes) != 2 {
		t.Fatalf("expected_codes = %#v, want [200 204]", hcConfig["expected_codes"])
	}
	first, firstOK := codes[0].(float64)
	second, secondOK := codes[1].(float64)
	if !firstOK || !secondOK || first != 200 || second != 204 {
		t.Fatalf("expected_codes = %#v, want [200 204]", hcConfig["expected_codes"])
	}
}

func TestConvertProtoToConfigRejectsMalformedConfig(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	client := NewClient(&config.Config{}, logger, nil)

	tests := []struct {
		name    string
		input   *pb.ConfigSnapshot
		wantErr string
	}{
		{
			name:    "nil snapshot",
			input:   nil,
			wantErr: "config snapshot is required",
		},
		{
			name: "nil vip config",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{nil},
			},
			wantErr: "vip config at index 0 is required",
		},
		{
			name: "missing vip",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{{}},
			},
			wantErr: "vip config at index 0 is missing vip",
		},
		{
			name: "invalid dscp above range",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{
					{
						Vip: &pb.VIP{
							Id:   "vip-1",
							Dscp: wrapperspb.UInt32(64),
						},
					},
				},
			},
			wantErr: "vip config at index 0 dscp must be between 0 and 63",
		},
		{
			name: "zero dscp for default L3DSR",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{
					{
						Vip: &pb.VIP{
							Id:   "vip-1",
							Dscp: wrapperspb.UInt32(0),
						},
					},
				},
			},
			wantErr: "vip config at index 0 dscp must be 1-63 when encap_type is L3DSR",
		},
		{
			name: "invalid health check config json",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{
					{
						Vip: &pb.VIP{Id: "vip-1"},
						HealthCheck: &pb.HealthCheck{
							Id:     "hc-1",
							VipId:  "vip-1",
							Config: `{"port":`,
						},
					},
				},
			},
			wantErr: "health check config at vip index 0 is invalid",
		},
		{
			name: "null health check config json",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{
					{
						Vip: &pb.VIP{Id: "vip-1"},
						HealthCheck: &pb.HealthCheck{
							Id:     "hc-1",
							VipId:  "vip-1",
							Config: `null`,
						},
					},
				},
			},
			wantErr: "health check config at vip index 0 must be a JSON object",
		},
		{
			name: "health check config missing required port",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{
					{
						Vip: &pb.VIP{Id: "vip-1"},
						HealthCheck: &pb.HealthCheck{
							Id:     "hc-1",
							VipId:  "vip-1",
							Type:   pb.HCType_HC_TYPE_HTTP,
							Config: `{"path":"/health"}`,
						},
					},
				},
			},
			wantErr: "health check config at vip index 0 is invalid: port is required",
		},
		{
			name: "health check config rejects fractional expected code",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{
					{
						Vip: &pb.VIP{Id: "vip-1"},
						HealthCheck: &pb.HealthCheck{
							Id:     "hc-1",
							VipId:  "vip-1",
							Type:   pb.HCType_HC_TYPE_HTTP,
							Config: `{"port":8080,"expected_codes":[200.5]}`,
						},
					},
				},
			},
			wantErr: "health check config at vip index 0 is invalid: expected_codes must be integers between 100 and 599",
		},
		{
			name: "nil backend",
			input: &pb.ConfigSnapshot{
				Vips: []*pb.VIPConfig{
					{
						Vip:      &pb.VIP{Id: "vip-1"},
						Backends: []*pb.Backend{nil},
					},
				},
			},
			wantErr: "backend at vip index 0 backend index 0 is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.convertProtoToConfig(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("convertProtoToConfig error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestConvertProtoToConfigAllowsMissingTimestamps(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	client := NewClient(&config.Config{}, logger, nil)

	got, err := client.convertProtoToConfig(&pb.ConfigSnapshot{
		Revision: 3,
		Vips: []*pb.VIPConfig{
			{
				Vip: &pb.VIP{
					Id:   "vip-1",
					Vip:  "192.168.1.100",
					Port: 80,
				},
				HealthCheck: &pb.HealthCheck{
					Id:     "hc-1",
					VipId:  "vip-1",
					Type:   pb.HCType_HC_TYPE_TCP,
					Config: `{"port":8080}`,
				},
				Backends: []*pb.Backend{
					{
						Id:     "backend-1",
						VipId:  "vip-1",
						Ip:     "10.0.0.1",
						Weight: 1,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("convertProtoToConfig: %v", err)
	}
	if got.Revision != 3 {
		t.Fatalf("Revision = %d, want 3", got.Revision)
	}
	if len(got.VIPs) != 1 {
		t.Fatalf("VIPs length = %d, want 1", len(got.VIPs))
	}
	if !got.VIPs[0].VIP.CreatedAt.IsZero() || !got.VIPs[0].VIP.UpdatedAt.IsZero() {
		t.Fatalf("VIP timestamps = %v/%v, want zero values", got.VIPs[0].VIP.CreatedAt, got.VIPs[0].VIP.UpdatedAt)
	}
	if got.VIPs[0].HealthCheck == nil || !got.VIPs[0].HealthCheck.CreatedAt.IsZero() || !got.VIPs[0].HealthCheck.UpdatedAt.IsZero() {
		t.Fatalf("health check timestamps = %#v, want zero values", got.VIPs[0].HealthCheck)
	}
	if len(got.VIPs[0].Backends) != 1 || !got.VIPs[0].Backends[0].CreatedAt.IsZero() || !got.VIPs[0].Backends[0].UpdatedAt.IsZero() {
		t.Fatalf("backends = %#v, want one backend with zero timestamps", got.VIPs[0].Backends)
	}
}

func TestConvertProtoHCType(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	client := NewClient(&config.Config{}, logger, nil)

	tests := []struct {
		name string
		in   pb.HCType
		want models.HCType
	}{
		{name: "http", in: pb.HCType_HC_TYPE_HTTP, want: models.HCTypeHTTP},
		{name: "https", in: pb.HCType_HC_TYPE_HTTPS, want: models.HCTypeHTTPS},
		{name: "tcp", in: pb.HCType_HC_TYPE_TCP, want: models.HCTypeTCP},
		{name: "ping", in: pb.HCType_HC_TYPE_PING, want: models.HCTypePing},
		{name: "tls hello", in: pb.HCType_HC_TYPE_TLS_HELLO, want: models.HCTypeTLSHello},
		{name: "unknown", in: pb.HCType_HC_TYPE_UNSPECIFIED, want: models.HCTypeTCP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := client.convertProtoHCType(tt.in); got != tt.want {
				t.Fatalf("convertProtoHCType(%s) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}
