package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/common/models"
	pb "github.com/akam1o/arca-lb/pkg/grpc"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// ConfigHandler is called when a new configuration is received
type ConfigHandler func(config *models.Config) error

// Client manages the gRPC connection to the Controller
type Client struct {
	config        *config.Config
	logger        *logrus.Logger
	configHandler ConfigHandler

	// Connection state (protected by mu)
	mu              sync.RWMutex
	conn            *grpc.ClientConn
	client          pb.ConfigSyncClient
	currentRevision int64
	connected       bool
	started         bool
	dialContext     func(ctx context.Context, target string) (net.Conn, error)
	wg              sync.WaitGroup

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewClient creates a new gRPC client
func NewClient(cfg *config.Config, logger *logrus.Logger, handler ConfigHandler) *Client {
	return &Client{
		config:        cfg,
		logger:        logger,
		configHandler: handler,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// Start starts the gRPC client and begins watching for configuration changes
func (c *Client) Start(ctx context.Context) error {
	c.logger.Info("Starting gRPC client")

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return fmt.Errorf("client already started")
	}
	c.started = true
	// Reinitialize channels for restartability
	c.stopCh = make(chan struct{})
	c.doneCh = make(chan struct{})
	c.mu.Unlock()

	// Create cancellable context
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Connect to controller
	if err := c.connect(); err != nil {
		c.cleanupFailedStart()
		return fmt.Errorf("failed to connect to controller: %w", err)
	}

	// Register agent
	if err := c.register(); err != nil {
		c.cleanupFailedStart()
		return fmt.Errorf("failed to register agent: %w", err)
	}

	// Start watch loop in background
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.watchLoop(c.ctx)
	}()

	// Start heartbeat loop in background
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.heartbeatLoop(c.ctx)
	}()

	return nil
}

func (c *Client) cleanupFailedStart() {
	if c.cancel != nil {
		c.cancel()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			c.logger.WithError(err).Warn("failed to close gRPC connection after start failure")
		}
		c.conn = nil
		c.client = nil
	}
	c.started = false
	c.connected = false

	select {
	case <-c.doneCh:
	default:
		close(c.doneCh)
	}
}

// Stop stops the gRPC client
func (c *Client) Stop() {
	c.logger.Info("Stopping gRPC client")

	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		c.logger.Debug("Client not started, nothing to stop")
		return
	}
	c.mu.Unlock()

	// Cancel context to unblock watch/heartbeat loops
	if c.cancel != nil {
		c.cancel()
	}

	// Close stopCh only once (idempotent)
	select {
	case <-c.stopCh:
		// Already stopped, but still wait for completion
		<-c.doneCh
		return
	default:
		close(c.stopCh)
	}

	<-c.doneCh

	// Wait for watch/heartbeat goroutines before taking c.mu. Heartbeat paths
	// acquire c.mu.RLock(), so holding the write lock while waiting can deadlock.
	c.wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			c.logger.WithError(err).Warn("failed to close gRPC connection")
		}
		c.conn = nil
		c.client = nil
	}
	// Reset started flag for restartability
	c.started = false
	c.connected = false
}

// connect establishes a connection to the controller with retry logic
func (c *Client) connect() error {
	var opts []grpc.DialOption

	if c.config.Controller.APIKey != "" && !c.config.Controller.TLS.Enabled {
		return fmt.Errorf("controller.tls.enabled must be enabled when controller.api_key is set")
	}
	if c.config.Controller.APIKey != "" && c.config.Controller.TLS.InsecureSkipVerify {
		return fmt.Errorf("controller.tls.insecure_skip_verify must be false when controller.api_key is set")
	}

	// Configure TLS if enabled
	if c.config.Controller.TLS.Enabled {
		tlsConfig, err := c.loadTLSConfig()
		if err != nil {
			return fmt.Errorf("failed to load TLS config: %w", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if c.dialContext != nil {
		opts = append(opts, grpc.WithContextDialer(c.dialContext))
	}
	if c.config.Controller.APIKey != "" {
		opts = append(opts,
			grpc.WithUnaryInterceptor(apiKeyUnaryClientInterceptor(c.config.Controller.APIKey)),
			grpc.WithStreamInterceptor(apiKeyStreamClientInterceptor(c.config.Controller.APIKey)),
		)
	}

	c.logger.WithField("address", c.config.Controller.Address).Info("Connecting to controller")

	// Retry connection with exponential backoff
	backoff := c.config.Controller.RetryBackoff
	for attempt := 1; attempt <= c.config.Controller.MaxRetries; attempt++ {
		// Use c.ctx as parent to support immediate cancellation on Stop()
		ctx, cancel := context.WithTimeout(c.ctx, c.config.Controller.Timeout)
		conn, err := grpc.NewClient(grpcClientTarget(c.config.Controller.Address), opts...)
		if err == nil {
			err = waitForReady(ctx, conn)
			if err != nil {
				if closeErr := conn.Close(); closeErr != nil {
					c.logger.WithError(closeErr).Warn("failed to close unsuccessful gRPC connection")
				}
			}
		}
		cancel()

		if err == nil {
			c.mu.Lock()
			c.conn = conn
			c.client = pb.NewConfigSyncClient(conn)
			c.connected = true
			c.mu.Unlock()
			c.logger.Info("Successfully connected to controller")
			return nil
		}

		c.logger.WithError(err).WithField("attempt", attempt).Warn("Failed to connect to controller")

		if attempt < c.config.Controller.MaxRetries {
			// Sleep with cancellation support
			if !sleepWithStop(c.ctx, c.stopCh, backoff) {
				if c.ctx.Err() == nil {
					return fmt.Errorf("connection cancelled during retry")
				}
				return fmt.Errorf("connection cancelled: %w", c.ctx.Err())
			}

			backoff *= 2
			if backoff > c.config.Controller.MaxRetryBackoff {
				backoff = c.config.Controller.MaxRetryBackoff
			}
		}
	}

	return fmt.Errorf("failed to connect after %d attempts", c.config.Controller.MaxRetries)
}

func grpcClientTarget(address string) string {
	if strings.Contains(address, "://") {
		return address
	}
	return "passthrough:///" + address
}

func waitForReady(ctx context.Context, conn *grpc.ClientConn) error {
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if state == connectivity.Shutdown {
			return fmt.Errorf("connection shut down before becoming ready")
		}
		if !conn.WaitForStateChange(ctx, state) {
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("connection did not become ready")
		}
		conn.Connect()
	}
}

// reconnect attempts to reconnect to the controller
func (c *Client) reconnect() error {
	c.mu.Lock()
	c.connected = false
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			c.logger.WithError(err).Warn("failed to close gRPC connection")
		}
		c.conn = nil
		c.client = nil
	}
	c.mu.Unlock()

	return c.connect()
}

// register registers the agent with the controller
func (c *Client) register() error {
	// Use c.ctx as parent to support cancellation on Stop()
	ctx, cancel := context.WithTimeout(c.ctx, c.config.Controller.Timeout)
	defer cancel()

	req := &pb.RegisterAgentRequest{
		AgentId:  c.config.Agent.ID,
		Version:  c.config.Agent.Version,
		Metadata: c.config.Agent.Metadata,
	}

	c.logger.WithFields(logrus.Fields{
		"agent_id": c.config.Agent.ID,
		"version":  c.config.Agent.Version,
	}).Info("Registering agent with controller")

	// Get client with lock
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}

	resp, err := client.RegisterAgent(ctx, req)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("registration returned nil response")
	}

	if !resp.Success {
		return fmt.Errorf("registration rejected: %s", resp.Message)
	}

	c.logger.WithField("message", resp.Message).Info("Agent registered successfully")

	// Process initial configuration if provided
	if resp.Config != nil {
		config, err := c.convertProtoToConfig(resp.Config)
		if err != nil {
			return fmt.Errorf("failed to convert initial configuration: %w", err)
		}
		if err := c.applyConfig(config); err != nil {
			return fmt.Errorf("failed to apply initial configuration: %w", err)
		}
	}

	return nil
}

// watchLoop watches for configuration changes
func (c *Client) watchLoop(ctx context.Context) {
	defer close(c.doneCh)

	for {
		select {
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		default:
			if err := c.watch(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				select {
				case <-c.stopCh:
					return
				default:
				}

				c.logger.WithError(err).Error("Watch stream error")

				// Try to reconnect
				c.logger.Info("Attempting to reconnect...")
				if err := c.reconnect(); err != nil {
					c.logger.WithError(err).Error("Reconnection failed, will retry")
				}

				// Avoid tight retry loops when the server repeatedly closes
				// the watch stream without a transport-level connection error.
				if !sleepWithStop(ctx, c.stopCh, c.config.Controller.RetryBackoff) {
					return
				}
			}
		}
	}
}

// watch establishes a watch stream and processes configuration updates
func (c *Client) watch(ctx context.Context) error {
	if !c.isConnected() {
		return fmt.Errorf("not connected to controller")
	}

	req := &pb.WatchConfigRequest{
		AgentId:         c.config.Agent.ID,
		CurrentRevision: c.getCurrentRevision(),
	}

	c.logger.WithFields(logrus.Fields{
		"agent_id":         c.config.Agent.ID,
		"current_revision": req.CurrentRevision,
	}).Info("Starting configuration watch")

	// Get client with lock
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}

	stream, err := client.WatchConfig(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to start watch: %w", err)
	}

	for {
		select {
		case <-c.stopCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			resp, err := stream.Recv()
			if err == io.EOF {
				c.logger.Info("Watch stream closed by server")
				return fmt.Errorf("watch stream closed by server: %w", io.EOF)
			}
			if err != nil {
				return fmt.Errorf("watch stream error: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("watch stream returned nil response")
			}

			if resp.Config != nil {
				config, err := c.convertProtoToConfig(resp.Config)
				if err != nil {
					return fmt.Errorf("failed to convert configuration: %w", err)
				}

				c.logger.WithFields(logrus.Fields{
					"revision":  config.Revision,
					"vip_count": len(config.VIPs),
				}).Info("Received configuration update")

				if err := c.applyConfig(config); err != nil {
					return fmt.Errorf("failed to apply configuration: %w", err)
				}
			}

			if resp.Error != "" {
				c.logger.WithField("error", resp.Error).Warn("Received error from controller")
			}
		}
	}
}

func sleepWithStop(ctx context.Context, stopCh <-chan struct{}, d time.Duration) bool {
	if d <= 0 {
		return true
	}

	timer := time.NewTimer(d)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-stopCh:
		return false
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// heartbeatLoop sends periodic heartbeats to the controller
func (c *Client) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(c.config.Agent.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.sendHeartbeat(); err != nil {
				c.logger.WithError(err).Debug("Heartbeat failed")
			}
		}
	}
}

// sendHeartbeat sends a heartbeat to the controller
func (c *Client) sendHeartbeat() error {
	if !c.isConnected() {
		return fmt.Errorf("not connected")
	}

	// Use c.ctx as parent to support cancellation on Stop()
	ctx, cancel := context.WithTimeout(c.ctx, c.config.Controller.Timeout)
	defer cancel()

	req := &pb.HeartbeatRequest{
		AgentId:         c.config.Agent.ID,
		CurrentRevision: c.getCurrentRevision(),
		Status:          &pb.AgentStatus{},
	}

	// Get client with lock
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}

	resp, err := client.Heartbeat(ctx, req)
	if err != nil {
		return fmt.Errorf("heartbeat failed: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("heartbeat returned nil response")
	}

	if !resp.Success {
		return fmt.Errorf("heartbeat rejected")
	}

	// Check if resync is required
	if resp.ResyncRequired {
		c.logger.WithFields(logrus.Fields{
			"current_revision": req.CurrentRevision,
			"new_revision":     resp.NewRevision,
		}).Info("Configuration resync required")

		// Trigger config fetch
		if err := c.fetchConfig(); err != nil {
			c.logger.WithError(err).Error("Failed to fetch configuration during resync")
		}
	}

	return nil
}

// fetchConfig fetches the current configuration from the controller
func (c *Client) fetchConfig() error {
	if !c.isConnected() {
		return fmt.Errorf("not connected")
	}

	// Use c.ctx as parent to support cancellation on Stop()
	ctx, cancel := context.WithTimeout(c.ctx, c.config.Controller.Timeout)
	defer cancel()

	req := &pb.GetConfigRequest{
		CurrentRevision: c.getCurrentRevision(),
		AgentId:         c.config.Agent.ID,
	}

	// Get client with lock
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}

	resp, err := client.GetConfig(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("get config returned nil response")
	}

	if resp.Unchanged {
		c.logger.Debug("Configuration unchanged")
		return nil
	}

	if resp.Config != nil {
		config, err := c.convertProtoToConfig(resp.Config)
		if err != nil {
			return fmt.Errorf("failed to convert config: %w", err)
		}
		if err := c.applyConfig(config); err != nil {
			return fmt.Errorf("failed to apply config: %w", err)
		}
	}

	return nil
}

func (c *Client) applyConfig(config *models.Config) error {
	if config == nil {
		return fmt.Errorf("config is required")
	}
	if c.configHandler != nil {
		if err := c.configHandler(config); err != nil {
			return err
		}
	}

	c.setCurrentRevision(config.Revision)
	return nil
}

// loadTLSConfig loads TLS configuration
func (c *Client) loadTLSConfig() (*tls.Config, error) {
	caData, err := os.ReadFile(c.config.Controller.TLS.CAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caData) {
		return nil, fmt.Errorf("failed to parse CA cert")
	}

	tlsConfig := &tls.Config{
		RootCAs:            caPool,
		InsecureSkipVerify: c.config.Controller.TLS.InsecureSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}

	if c.config.Controller.TLS.CertFile != "" || c.config.Controller.TLS.KeyFile != "" {
		if c.config.Controller.TLS.CertFile == "" || c.config.Controller.TLS.KeyFile == "" {
			return nil, fmt.Errorf("tls.cert_file and tls.key_file must both be set when client certificate is configured")
		}
		cert, err := tls.LoadX509KeyPair(c.config.Controller.TLS.CertFile, c.config.Controller.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client cert: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// convertProtoToConfig converts protobuf config to internal model
func (c *Client) convertProtoToConfig(pbConfig *pb.ConfigSnapshot) (*models.Config, error) {
	if pbConfig == nil {
		return nil, fmt.Errorf("config snapshot is required")
	}

	config := &models.Config{
		Revision: pbConfig.Revision,
		VIPs:     make([]models.VIPConfig, 0, len(pbConfig.Vips)),
	}
	seenVIPIDs := make(map[string]int, len(pbConfig.Vips))
	seenVIPTuples := make(map[string]int, len(pbConfig.Vips))
	seenBackendIDs := make(map[string]backendConfigLocation)

	for i, pbVipConfig := range pbConfig.Vips {
		if pbVipConfig == nil {
			return nil, fmt.Errorf("vip config at index %d is required", i)
		}
		if pbVipConfig.Vip == nil {
			return nil, fmt.Errorf("vip config at index %d is missing vip", i)
		}

		encapType := c.convertProtoEncapType(pbVipConfig.Vip.EncapType)
		dscp, err := convertProtoDSCP(i, encapType, pbVipConfig.Vip.Dscp)
		if err != nil {
			return nil, err
		}
		vipCreatedAt := time.Time{}
		if pbVipConfig.Vip.CreatedAt != nil {
			vipCreatedAt = pbVipConfig.Vip.CreatedAt.AsTime()
		}
		vipUpdatedAt := time.Time{}
		if pbVipConfig.Vip.UpdatedAt != nil {
			vipUpdatedAt = pbVipConfig.Vip.UpdatedAt.AsTime()
		}

		vipConfig := models.VIPConfig{
			VIP: models.VIP{
				ID:        pbVipConfig.Vip.Id,
				VIP:       pbVipConfig.Vip.Vip,
				Port:      int(pbVipConfig.Vip.Port),
				Protocol:  c.convertProtoProtocol(pbVipConfig.Vip.Protocol),
				LBMethod:  c.convertProtoLBMethod(pbVipConfig.Vip.LbMethod),
				EncapType: encapType,
				DSCP:      dscp,
				CreatedAt: vipCreatedAt,
				UpdatedAt: vipUpdatedAt,
			},
			Backends: make([]models.Backend, 0, len(pbVipConfig.Backends)),
		}

		// Convert health check
		if pbVipConfig.HealthCheck != nil {
			healthCheckCreatedAt := time.Time{}
			if pbVipConfig.HealthCheck.CreatedAt != nil {
				healthCheckCreatedAt = pbVipConfig.HealthCheck.CreatedAt.AsTime()
			}
			healthCheckUpdatedAt := time.Time{}
			if pbVipConfig.HealthCheck.UpdatedAt != nil {
				healthCheckUpdatedAt = pbVipConfig.HealthCheck.UpdatedAt.AsTime()
			}
			vipConfig.HealthCheck = &models.HealthCheck{
				ID:          pbVipConfig.HealthCheck.Id,
				VIPID:       pbVipConfig.HealthCheck.VipId,
				Type:        c.convertProtoHCType(pbVipConfig.HealthCheck.Type),
				IntervalSec: int(pbVipConfig.HealthCheck.IntervalSec),
				TimeoutSec:  int(pbVipConfig.HealthCheck.TimeoutSec),
				RiseCount:   int(pbVipConfig.HealthCheck.RiseCount),
				FallCount:   int(pbVipConfig.HealthCheck.FallCount),
				CreatedAt:   healthCheckCreatedAt,
				UpdatedAt:   healthCheckUpdatedAt,
			}

			// Parse Config JSON if present
			if pbVipConfig.HealthCheck.Config != "" {
				var hcConfig models.HCConfig
				if err := json.Unmarshal([]byte(pbVipConfig.HealthCheck.Config), &hcConfig); err != nil {
					return nil, fmt.Errorf("health check config at vip index %d is invalid: %w", i, err)
				}
				if hcConfig == nil {
					return nil, fmt.Errorf("health check config at vip index %d must be a JSON object", i)
				}
				vipConfig.HealthCheck.Config = hcConfig
			}
			if err := models.ValidateHealthCheckConfig(vipConfig.HealthCheck.Type, vipConfig.HealthCheck.Config); err != nil {
				return nil, fmt.Errorf("health check config at vip index %d is invalid: %w", i, err)
			}
			if err := models.ValidateHealthCheckTiming(vipConfig.HealthCheck); err != nil {
				return nil, fmt.Errorf("health check at vip index %d is invalid: %w", i, err)
			}
		}

		// Convert backends
		for j, pbBackend := range pbVipConfig.Backends {
			if pbBackend == nil {
				return nil, fmt.Errorf("backend at vip index %d backend index %d is required", i, j)
			}
			backendCreatedAt := time.Time{}
			if pbBackend.CreatedAt != nil {
				backendCreatedAt = pbBackend.CreatedAt.AsTime()
			}
			backendUpdatedAt := time.Time{}
			if pbBackend.UpdatedAt != nil {
				backendUpdatedAt = pbBackend.UpdatedAt.AsTime()
			}
			backend := models.Backend{
				ID:        pbBackend.Id,
				VIPID:     pbBackend.VipId,
				IP:        pbBackend.Ip,
				Weight:    int(pbBackend.Weight),
				CreatedAt: backendCreatedAt,
				UpdatedAt: backendUpdatedAt,
			}
			vipConfig.Backends = append(vipConfig.Backends, backend)
		}

		if err := validateVIPConfigDataPlane(i, &vipConfig); err != nil {
			return nil, err
		}
		if err := validateVIPConfigIdentity(i, &vipConfig, seenVIPIDs, seenVIPTuples, seenBackendIDs); err != nil {
			return nil, err
		}

		config.VIPs = append(config.VIPs, vipConfig)
	}

	return config, nil
}

func validateVIPConfigDataPlane(vipIndex int, vipConfig *models.VIPConfig) error {
	vipIP := net.ParseIP(vipConfig.VIP.VIP)
	if vipIP == nil {
		return fmt.Errorf("vip config at index %d vip %q is not a valid IP address", vipIndex, vipConfig.VIP.VIP)
	}
	if vipConfig.VIP.Port < 1 || vipConfig.VIP.Port > 65535 {
		return fmt.Errorf("vip config at index %d port must be between 1 and 65535, got %d", vipIndex, vipConfig.VIP.Port)
	}

	encapType := effectiveProtoEncapType(vipConfig.VIP.EncapType)
	switch vipConfig.VIP.Protocol {
	case models.ProtocolTCP, models.ProtocolUDP:
	default:
		return fmt.Errorf("vip config at index %d protocol must be TCP or UDP", vipIndex)
	}
	switch vipConfig.VIP.LBMethod {
	case models.LBMethodMaglev:
	default:
		return fmt.Errorf("vip config at index %d lb_method must be maglev", vipIndex)
	}
	switch vipConfig.VIP.EncapType {
	case "", models.EncapTypeGRE4, models.EncapTypeGRE6, models.EncapTypeL3DSR, models.EncapTypeNAT4, models.EncapTypeNAT6:
	default:
		return fmt.Errorf("vip config at index %d encap_type %q is not valid", vipIndex, vipConfig.VIP.EncapType)
	}
	if err := validateVIPAddressFamily(vipIndex, vipIP, encapType); err != nil {
		return err
	}

	for backendIndex, backend := range vipConfig.Backends {
		backendIP := net.ParseIP(backend.IP)
		if backendIP == nil {
			return fmt.Errorf("backend at vip index %d backend index %d ip %q is not a valid IP address", vipIndex, backendIndex, backend.IP)
		}
		if err := validateBackendAddressFamily(vipIndex, backendIndex, backend.IP, backendIP, encapType); err != nil {
			return err
		}
		if backend.Weight < 1 || backend.Weight > 100 {
			return fmt.Errorf("backend at vip index %d backend index %d weight must be between 1 and 100, got %d", vipIndex, backendIndex, backend.Weight)
		}
	}

	return nil
}

type backendConfigLocation struct {
	vipIndex     int
	backendIndex int
}

func validateVIPConfigIdentity(vipIndex int, vipConfig *models.VIPConfig, seenVIPIDs, seenVIPTuples map[string]int, seenBackendIDs map[string]backendConfigLocation) error {
	if err := datastore.ValidateResourceID(fmt.Sprintf("vip config at index %d id", vipIndex), vipConfig.VIP.ID); err != nil {
		return err
	}
	if firstIndex, ok := seenVIPIDs[vipConfig.VIP.ID]; ok {
		return fmt.Errorf("vip config at index %d duplicates vip id %q first seen at index %d", vipIndex, vipConfig.VIP.ID, firstIndex)
	}
	seenVIPIDs[vipConfig.VIP.ID] = vipIndex

	tupleKey := vipTupleKey(vipConfig.VIP)
	if firstIndex, ok := seenVIPTuples[tupleKey]; ok {
		return fmt.Errorf("vip config at index %d duplicates vip tuple %s first seen at index %d", vipIndex, tupleKey, firstIndex)
	}
	seenVIPTuples[tupleKey] = vipIndex

	if vipConfig.HealthCheck != nil {
		if vipConfig.HealthCheck.ID != "" {
			if err := datastore.ValidateResourceID(fmt.Sprintf("health check at vip index %d id", vipIndex), vipConfig.HealthCheck.ID); err != nil {
				return err
			}
		}
		if err := datastore.ValidateResourceID(fmt.Sprintf("health check at vip index %d vip_id", vipIndex), vipConfig.HealthCheck.VIPID); err != nil {
			return err
		}
		if vipConfig.HealthCheck.VIPID != vipConfig.VIP.ID {
			return fmt.Errorf("health check at vip index %d vip_id %q does not match vip id %q", vipIndex, vipConfig.HealthCheck.VIPID, vipConfig.VIP.ID)
		}
	}

	seenBackendIPs := make(map[string]int, len(vipConfig.Backends))
	for backendIndex, backend := range vipConfig.Backends {
		if err := datastore.ValidateResourceID(fmt.Sprintf("backend at vip index %d backend index %d id", vipIndex, backendIndex), backend.ID); err != nil {
			return err
		}
		if err := datastore.ValidateResourceID(fmt.Sprintf("backend at vip index %d backend index %d vip_id", vipIndex, backendIndex), backend.VIPID); err != nil {
			return err
		}
		if backend.VIPID != vipConfig.VIP.ID {
			return fmt.Errorf("backend at vip index %d backend index %d vip_id %q does not match vip id %q", vipIndex, backendIndex, backend.VIPID, vipConfig.VIP.ID)
		}
		if firstLocation, ok := seenBackendIDs[backend.ID]; ok {
			return fmt.Errorf("backend at vip index %d backend index %d duplicates backend id %q first seen at vip index %d backend index %d", vipIndex, backendIndex, backend.ID, firstLocation.vipIndex, firstLocation.backendIndex)
		}
		seenBackendIDs[backend.ID] = backendConfigLocation{
			vipIndex:     vipIndex,
			backendIndex: backendIndex,
		}

		backendIP := net.ParseIP(backend.IP)
		if backendIP == nil {
			continue
		}
		backendIPKey := backendIP.String()
		if firstIndex, ok := seenBackendIPs[backendIPKey]; ok {
			return fmt.Errorf("backend at vip index %d backend index %d duplicates backend ip %q first seen at backend index %d", vipIndex, backendIndex, backendIPKey, firstIndex)
		}
		seenBackendIPs[backendIPKey] = backendIndex
	}

	return nil
}

func vipTupleKey(vip models.VIP) string {
	vipIP := net.ParseIP(vip.VIP)
	if vipIP != nil {
		return fmt.Sprintf("%s/%d/%s", vipIP.String(), vip.Port, vip.Protocol)
	}
	return fmt.Sprintf("%s/%d/%s", vip.VIP, vip.Port, vip.Protocol)
}

func validateVIPAddressFamily(vipIndex int, vipIP net.IP, encapType models.EncapType) error {
	isIPv4 := vipIP.To4() != nil
	switch encapType {
	case models.EncapTypeL3DSR, models.EncapTypeNAT4:
		if !isIPv4 {
			return fmt.Errorf("vip config at index %d encap_type %s requires an IPv4 vip", vipIndex, encapType)
		}
	case models.EncapTypeNAT6:
		if isIPv4 {
			return fmt.Errorf("vip config at index %d encap_type NAT6 requires an IPv6 vip", vipIndex)
		}
	}

	return nil
}

func validateBackendAddressFamily(vipIndex, backendIndex int, address string, backendIP net.IP, encapType models.EncapType) error {
	isIPv4 := backendIP.To4() != nil
	switch encapType {
	case models.EncapTypeGRE4, models.EncapTypeL3DSR, models.EncapTypeNAT4:
		if !isIPv4 {
			return fmt.Errorf("backend at vip index %d backend index %d ip %q must be IPv4 for encap_type %s", vipIndex, backendIndex, address, encapType)
		}
	case models.EncapTypeGRE6, models.EncapTypeNAT6:
		if isIPv4 {
			return fmt.Errorf("backend at vip index %d backend index %d ip %q must be IPv6 for encap_type %s", vipIndex, backendIndex, address, encapType)
		}
	}

	return nil
}

func convertProtoDSCP(vipIndex int, encapType models.EncapType, dscp *wrapperspb.UInt32Value) (*uint8, error) {
	if dscp == nil {
		return nil, nil
	}
	if dscp.Value > 63 {
		return nil, fmt.Errorf("vip config at index %d dscp must be between 0 and 63, got %d", vipIndex, dscp.Value)
	}
	if effectiveProtoEncapType(encapType) == models.EncapTypeL3DSR && dscp.Value == 0 {
		return nil, fmt.Errorf("vip config at index %d dscp must be 1-63 when encap_type is L3DSR", vipIndex)
	}

	value := uint8(dscp.Value)
	return &value, nil
}

func effectiveProtoEncapType(encapType models.EncapType) models.EncapType {
	if encapType == "" {
		return models.EncapTypeL3DSR
	}
	return encapType
}

// convertProtoProtocol converts protobuf protocol to internal model
func (c *Client) convertProtoProtocol(protocol pb.Protocol) models.Protocol {
	switch protocol {
	case pb.Protocol_PROTOCOL_TCP:
		return models.ProtocolTCP
	case pb.Protocol_PROTOCOL_UDP:
		return models.ProtocolUDP
	default:
		return ""
	}
}

// convertProtoLBMethod converts protobuf LB method to internal model
func (c *Client) convertProtoLBMethod(method pb.LBMethod) models.LBMethod {
	switch method {
	case pb.LBMethod_LB_METHOD_UNSPECIFIED, pb.LBMethod_LB_METHOD_MAGLEV:
		return models.LBMethodMaglev
	default:
		return models.LBMethod("invalid")
	}
}

// convertProtoEncapType converts protobuf encap type to internal model
func (c *Client) convertProtoEncapType(encapType pb.EncapType) models.EncapType {
	switch encapType {
	case pb.EncapType_ENCAP_TYPE_GRE4:
		return models.EncapTypeGRE4
	case pb.EncapType_ENCAP_TYPE_GRE6:
		return models.EncapTypeGRE6
	case pb.EncapType_ENCAP_TYPE_L3DSR:
		return models.EncapTypeL3DSR
	case pb.EncapType_ENCAP_TYPE_NAT4:
		return models.EncapTypeNAT4
	case pb.EncapType_ENCAP_TYPE_NAT6:
		return models.EncapTypeNAT6
	case pb.EncapType_ENCAP_TYPE_UNSPECIFIED:
		return ""
	default:
		return models.EncapType("invalid")
	}
}

// convertProtoHCType converts protobuf health check type to internal model
func (c *Client) convertProtoHCType(hcType pb.HCType) models.HCType {
	switch hcType {
	case pb.HCType_HC_TYPE_HTTP:
		return models.HCTypeHTTP
	case pb.HCType_HC_TYPE_HTTPS:
		return models.HCTypeHTTPS
	case pb.HCType_HC_TYPE_TCP:
		return models.HCTypeTCP
	case pb.HCType_HC_TYPE_PING:
		return models.HCTypePing
	case pb.HCType_HC_TYPE_TLS_HELLO:
		return models.HCTypeTLSHello
	default:
		return ""
	}
}

// getCurrentRevision returns the current configuration revision
func (c *Client) getCurrentRevision() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentRevision
}

// setCurrentRevision sets the current configuration revision
func (c *Client) setCurrentRevision(revision int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentRevision = revision
}

// isConnected returns whether the client is connected
func (c *Client) isConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}
