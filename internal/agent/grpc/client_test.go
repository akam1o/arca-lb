package grpc

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/common/models"
	pb "github.com/akam1o/arca-lb/pkg/grpc"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// Mock gRPC server for testing
type mockConfigSyncServer struct {
	pb.UnimplementedConfigSyncServer

	// Control behavior
	registerSuccess  bool
	registerMessage  string
	watchConfig      *models.Config
	watchError       error
	heartbeatSuccess bool
}

func (m *mockConfigSyncServer) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest) (*pb.RegisterAgentResponse, error) {
	return &pb.RegisterAgentResponse{
		Success: m.registerSuccess,
		Message: m.registerMessage,
	}, nil
}

func (m *mockConfigSyncServer) WatchConfig(req *pb.WatchConfigRequest, stream pb.ConfigSync_WatchConfigServer) error {
	if m.watchError != nil {
		return m.watchError
	}

	// Send initial config if available
	if m.watchConfig != nil {
		pbConfig := &pb.ConfigSnapshot{
			Revision: m.watchConfig.Revision,
			Vips:     make([]*pb.VIPConfig, 0),
		}

		resp := &pb.WatchConfigResponse{
			Type:   pb.UpdateType_UPDATE_TYPE_FULL,
			Config: pbConfig,
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
	return &pb.HeartbeatResponse{
		Success: m.heartbeatSuccess,
	}, nil
}

func (m *mockConfigSyncServer) GetConfig(ctx context.Context, req *pb.GetConfigRequest) (*pb.GetConfigResponse, error) {
	return &pb.GetConfigResponse{
		Unchanged: true,
	}, nil
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
