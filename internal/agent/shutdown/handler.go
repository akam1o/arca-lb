package shutdown

import (
	"context"
	"fmt"
	"time"

	"github.com/akam1o/arca-lb/internal/agent/frr"
	"github.com/akam1o/arca-lb/internal/agent/grpc"
	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/akam1o/arca-lb/internal/agent/metrics"
	"github.com/akam1o/arca-lb/internal/agent/reconciler"
	"github.com/akam1o/arca-lb/internal/agent/vpp"
	"github.com/sirupsen/logrus"
)

// Components holds all agent components that need graceful shutdown
type Components struct {
	GRPCClient     *grpc.Client
	Reconciler     *reconciler.Reconciler
	HealthCheckMgr *healthcheck.Manager
	FRRManager     *frr.Manager // Can be nil if FRR is disabled
	VPPConnection  *vpp.Connection
	MetricsServer  *metrics.Server // Can be nil if metrics are disabled
}

// Handler manages graceful shutdown of agent components
type Handler struct {
	components *Components
	logger     *logrus.Logger

	// Shutdown configuration
	totalTimeout     time.Duration
	componentTimeout time.Duration
}

// NewHandler creates a new shutdown handler
func NewHandler(components *Components, logger *logrus.Logger) *Handler {
	return &Handler{
		components:       components,
		logger:           logger,
		totalTimeout:     30 * time.Second,
		componentTimeout: 5 * time.Second,
	}
}

// Shutdown performs graceful shutdown of all components in the correct order
// The shutdown order is the reverse of startup order:
// 1. Stop gRPC client (stop receiving new configurations)
// 2. Stop reconciler (stop reconciliation loop)
// 3. Stop health check manager (stop health checking)
// 4. FRR manager: Do NOT call Close() to maintain BGP route announcements
// 5. VPP connection: Stop connection but maintain VPP configuration (VIP/Backend)
func (h *Handler) Shutdown(ctx context.Context) error {
	h.logger.Info("Starting graceful shutdown")

	// Create shutdown context with total timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, h.totalTimeout)
	defer cancel()

	// Define shutdown operations in reverse order of startup
	shutdownOps := []struct {
		name        string
		description string
		fn          func(context.Context) error
	}{
		{
			name:        "metrics server",
			description: "Stop metrics HTTP server",
			fn:          h.stopMetricsServer,
		},
		{
			name:        "gRPC client",
			description: "Stop receiving new configurations from controller",
			fn:          h.stopGRPCClient,
		},
		{
			name:        "reconciler",
			description: "Stop reconciliation loop",
			fn:          h.stopReconciler,
		},
		{
			name:        "health check manager",
			description: "Stop health checking",
			fn:          h.stopHealthCheckManager,
		},
		{
			name:        "FRR manager",
			description: "Maintain BGP route announcements (do not withdraw)",
			fn:          h.maintainFRRConfig,
		},
		{
			name:        "VPP connection",
			description: "Stop connection but maintain VPP configuration",
			fn:          h.stopVPPConnection,
		},
	}

	// Execute shutdown operations with per-component timeout
	var lastErr error
	for _, op := range shutdownOps {
		// Check global timeout first (before starting component shutdown)
		select {
		case <-shutdownCtx.Done():
			h.logger.Warn("Global shutdown timeout reached, aborting remaining components")
			if lastErr == nil {
				lastErr = fmt.Errorf("shutdown timeout exceeded")
			}
			return lastErr
		default:
			// Continue with component shutdown
		}

		h.logger.WithFields(logrus.Fields{
			"component":   op.name,
			"description": op.description,
		}).Info("Stopping component")

		// Create per-component timeout context (independent of global timeout for clearer timeout detection)
		// Use background context to avoid confusion with global timeout
		componentCtx, componentCancel := context.WithTimeout(context.Background(), h.componentTimeout)

		// Execute shutdown operation in goroutine to enforce timeout
		done := make(chan error, 1)
		go func() {
			done <- op.fn(componentCtx)
		}()

		// Wait for completion or timeout
		var err error
		select {
		case err = <-done:
			// Operation completed
		case <-componentCtx.Done():
			// Component timeout reached
			err = fmt.Errorf("component shutdown timeout after %v", h.componentTimeout)
			h.logger.WithField("component", op.name).Warn("Component shutdown timeout")
		case <-shutdownCtx.Done():
			// Global timeout reached (higher priority)
			componentCancel()
			h.logger.Warn("Global shutdown timeout reached during component shutdown")
			if lastErr == nil {
				lastErr = fmt.Errorf("shutdown timeout exceeded")
			}
			return lastErr
		}
		componentCancel()

		if err != nil {
			h.logger.WithError(err).WithField("component", op.name).Warn("Component shutdown completed with error")
			lastErr = err
		} else {
			h.logger.WithField("component", op.name).Debug("Component stopped successfully")
		}
	}

	if lastErr != nil {
		h.logger.WithError(lastErr).Warn("Graceful shutdown completed with errors")
	} else {
		h.logger.Info("Graceful shutdown completed successfully")
	}

	return lastErr
}

// stopGRPCClient stops the gRPC client to prevent receiving new configurations
func (h *Handler) stopGRPCClient(ctx context.Context) error {
	if h.components.GRPCClient == nil {
		return nil
	}

	// Stop gRPC client (this stops receiving new configurations)
	// Note: Stop() is synchronous and should complete quickly
	h.components.GRPCClient.Stop()

	// Check if context was cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// stopReconciler stops the reconciliation loop
func (h *Handler) stopReconciler(ctx context.Context) error {
	if h.components.Reconciler == nil {
		return nil
	}

	// Stop reconciler
	// Note: Stop() is synchronous and should complete quickly
	h.components.Reconciler.Stop()

	// Check if context was cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// stopHealthCheckManager stops the health check manager
func (h *Handler) stopHealthCheckManager(ctx context.Context) error {
	if h.components.HealthCheckMgr == nil {
		return nil
	}

	// Stop health check manager
	// Note: Stop() is synchronous and should complete quickly
	h.components.HealthCheckMgr.Stop()

	// Check if context was cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// maintainFRRConfig maintains FRR configuration without withdrawing routes
// This is important for graceful shutdown - we want to maintain BGP route announcements
// so that traffic continues to flow even during agent restart
func (h *Handler) maintainFRRConfig(ctx context.Context) error {
	if h.components.FRRManager == nil {
		return nil
	}

	// Do NOT call Close() on FRR Manager to maintain BGP route announcements
	// The FRR configuration (static routes) will remain in FRR, allowing
	// traffic to continue flowing during agent restart
	h.logger.Info("FRR configuration maintained (BGP routes not withdrawn)")

	return nil
}

// stopMetricsServer stops the metrics HTTP server
func (h *Handler) stopMetricsServer(ctx context.Context) error {
	if h.components.MetricsServer == nil {
		return nil
	}

	// Stop metrics server
	if err := h.components.MetricsServer.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop metrics server: %w", err)
	}

	return nil
}

// stopVPPConnection stops the VPP connection but maintains VPP configuration
// VPP itself continues running, and the VIP/Backend configurations remain active
func (h *Handler) stopVPPConnection(ctx context.Context) error {
	if h.components.VPPConnection == nil {
		return nil
	}

	// Stop VPP connection (this closes the API connection)
	// VPP itself continues running, and the VIP/Backend configurations remain
	// Note: Stop() is synchronous and should complete quickly
	h.components.VPPConnection.Stop()

	// Check if context was cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
