package vpp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"git.fd.io/govpp.git"
	"git.fd.io/govpp.git/api"
	"git.fd.io/govpp.git/core"
	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/sirupsen/logrus"
)

// MetricsRecorder is an interface for recording VPP metrics
type MetricsRecorder interface {
	RecordError(component, operation string)
	RecordReconnect()
}

// Connection represents a VPP connection manager
type Connection struct {
	config  *config.VPPConfig
	logger  *logrus.Logger
	mu      sync.RWMutex
	conn    *core.Connection
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	// Metrics (optional)
	metricsRecorder MetricsRecorder
}

// NewConnection creates a new VPP connection manager
func NewConnection(cfg *config.VPPConfig, logger *logrus.Logger) *Connection {
	return &Connection{
		config: cfg,
		logger: logger,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// SetMetricsRecorder sets the metrics recorder for VPP metrics
func (c *Connection) SetMetricsRecorder(recorder MetricsRecorder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metricsRecorder = recorder
}

// Start starts the VPP connection manager
func (c *Connection) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return fmt.Errorf("VPP connection already started")
	}
	c.started = true
	// Reinitialize channels for restartability
	c.stopCh = make(chan struct{})
	c.doneCh = make(chan struct{})
	c.mu.Unlock()

	c.logger.Info("Starting VPP connection manager")

	// Initial connection
	if err := c.connect(); err != nil {
		c.mu.RLock()
		recorder := c.metricsRecorder
		c.mu.RUnlock()
		if recorder != nil {
			recorder.RecordError("connection", "connect")
		}
		c.mu.Lock()
		c.started = false
		c.mu.Unlock()
		return fmt.Errorf("failed to connect to VPP: %w", err)
	}

	// Start reconnection monitor
	go c.monitorConnection(ctx)

	return nil
}

// Stop stops the VPP connection manager
func (c *Connection) Stop() {
	c.logger.Info("Stopping VPP connection manager")

	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		c.logger.Debug("VPP connection not started, nothing to stop")
		return
	}
	c.mu.Unlock()

	// Close stopCh only once (idempotent)
	select {
	case <-c.stopCh:
		// Already stopped, wait for completion
		<-c.doneCh
		return
	default:
		close(c.stopCh)
	}

	<-c.doneCh

	// Close the VPP connection
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Disconnect()
		c.conn = nil
	}
	c.started = false
	c.mu.Unlock()
}

// connect establishes a connection to VPP
func (c *Connection) connect() error {
	c.logger.WithField("socket", c.config.SocketPath).Info("Connecting to VPP")

	// Create a context with timeout for proper goroutine cleanup
	ctx, cancel := context.WithTimeout(context.Background(), c.config.ConnectTimeout)
	defer cancel()

	// Connect with timeout and cancellation support
	type result struct {
		conn *core.Connection
		err  error
	}
	resultCh := make(chan result, 1)

	go func() {
		conn, err := govpp.Connect(c.config.SocketPath)
		select {
		case resultCh <- result{conn: conn, err: err}:
			// Successfully sent result
		case <-ctx.Done():
			// Context cancelled, cleanup if connection succeeded
			if conn != nil {
				conn.Disconnect()
			}
		}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			return fmt.Errorf("failed to connect to VPP: %w", res.err)
		}
		c.mu.Lock()
		c.conn = res.conn
		c.mu.Unlock()
		c.logger.Info("Successfully connected to VPP")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("VPP connection timeout after %v", c.config.ConnectTimeout)
	}
}

// monitorConnection monitors the VPP connection and reconnects if needed
func (c *Connection) monitorConnection(ctx context.Context) {
	defer close(c.doneCh)

	ticker := time.NewTicker(c.config.ReconnectInterval)
	defer ticker.Stop()

	attempts := 0

	for {
		select {
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if connection is still alive
			// Use IsConnected() which properly handles locking
			if !c.IsConnected() {
				c.logger.Warn("VPP connection lost, attempting to reconnect")
				// Reconnect with proper atomic check-and-set
				if err := c.reconnectWithLock(&attempts); err != nil {
					c.mu.RLock()
					recorder := c.metricsRecorder
					c.mu.RUnlock()
					if recorder != nil {
						recorder.RecordError("connection", "reconnect")
					}
					c.logger.WithError(err).Error("Failed to reconnect to VPP")
				}
			} else {
				// Connection check: try to create a channel to verify health
				if !c.checkConnectionHealth() {
					c.logger.Warn("VPP connection health check failed, attempting to reconnect")
					if err := c.reconnectWithLock(&attempts); err != nil {
						c.mu.RLock()
						recorder := c.metricsRecorder
						c.mu.RUnlock()
						if recorder != nil {
							recorder.RecordError("connection", "reconnect")
						}
						c.logger.WithError(err).Error("Failed to reconnect to VPP")
					}
				} else {
					// Connection is healthy, reset attempts counter
					attempts = 0
				}
			}
		}
	}
}

// checkConnectionHealth verifies if the connection is actually healthy
func (c *Connection) checkConnectionHealth() bool {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return false
	}

	// Try to create a test API channel to verify connection health
	ch, err := conn.NewAPIChannel()
	if err != nil {
		c.logger.WithError(err).Debug("Connection health check failed: cannot create API channel")
		return false
	}
	ch.Close()

	return true
}

// reconnectWithLock attempts to reconnect to VPP with proper locking
// This prevents race conditions between multiple reconnect attempts
func (c *Connection) reconnectWithLock(attempts *int) error {
	// Check if we've reached max attempts
	// Note: MaxReconnectAttempts = 0 means infinite retries (no limit)
	if c.config.MaxReconnectAttempts > 0 && *attempts >= c.config.MaxReconnectAttempts {
		return fmt.Errorf("maximum reconnection attempts (%d) reached", c.config.MaxReconnectAttempts)
	}

	*attempts++

	c.logger.WithFields(logrus.Fields{
		"attempt":      *attempts,
		"max_attempts": c.config.MaxReconnectAttempts,
	}).Info("Attempting to reconnect to VPP")

	// Acquire lock before checking and modifying connection
	c.mu.Lock()

	// Double-check connection is still nil/invalid (another goroutine might have reconnected)
	if c.conn != nil {
		// Try health check while we have the lock
		ch, err := c.conn.NewAPIChannel()
		if err == nil {
			// Connection is actually healthy
			ch.Close()
			c.logger.Debug("Connection already healthy, skipping reconnect")
			*attempts = 0
			c.mu.Unlock()
			return nil
		}
		// Connection is unhealthy, proceed with disconnect
		c.conn.Disconnect()
		c.conn = nil
	}

	// Release lock before connect attempt (connect can take time)
	c.mu.Unlock()

	// Try to connect
	err := c.connect()

	// Reacquire lock to check result
	c.mu.Lock()

	if err != nil {
		c.mu.Unlock()
		return err
	}

	// Reset attempts on successful connection
	*attempts = 0
	c.logger.Info("Successfully reconnected to VPP")

	// Record reconnection metric
	recorder := c.metricsRecorder
	c.mu.Unlock()
	if recorder != nil {
		recorder.RecordReconnect()
	}
	return nil
}

// GetConnection returns the current VPP connection
// Returns nil if not connected
func (c *Connection) GetConnection() *core.Connection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

// IsConnected returns true if connected to VPP
func (c *Connection) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil
}

// NewStream creates a new API channel (stream) for communication
func (c *Connection) NewStream() (api.Channel, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("not connected to VPP")
	}

	ch, err := conn.NewAPIChannel()
	if err != nil {
		return nil, fmt.Errorf("failed to create API channel: %w", err)
	}

	return ch, nil
}
