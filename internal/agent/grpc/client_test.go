package grpc

import (
	"context"
	"fmt"
	"net"
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

func TestClientStartSendsAPIKeyMetadata(t *testing.T) {
	const apiKey = "agent-controller-secret"

	mock := &mockConfigSyncServer{
		registerSuccess:  true,
		heartbeatSuccess: true,
		requiredAPIKey:   apiKey,
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
	client.dialContext = dialer

	if err := client.Start(context.Background()); err != nil {
		t.Fatalf("Start with API key: %v", err)
	}
	client.Stop()
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

	got := client.convertProtoToConfig(&pb.ConfigSnapshot{
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
