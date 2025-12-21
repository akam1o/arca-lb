package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/agent/frr"
	"github.com/akam1o/arca-lb/internal/agent/grpc"
	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/akam1o/arca-lb/internal/agent/metrics"
	"github.com/akam1o/arca-lb/internal/agent/reconciler"
	"github.com/akam1o/arca-lb/internal/agent/shutdown"
	"github.com/akam1o/arca-lb/internal/agent/state"
	"github.com/akam1o/arca-lb/internal/agent/vpp"
	"github.com/akam1o/arca-lb/internal/common/models"
)

// Components holds all initialized agent components
type Components struct {
	StateManager   *state.Manager
	VPPConnection  *vpp.Connection
	VPPSyncer      *vpp.Syncer
	FRRManager     *frr.Manager // Can be nil if FRR is disabled
	HealthCheckMgr *healthcheck.Manager
	Reconciler     *reconciler.Reconciler
	GRPCClient     *grpc.Client
	MetricsRegistry *metrics.Registry
	MetricsServer   *metrics.Server
}

func main() {
	// Setup logger with default settings
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logger.SetLevel(logrus.InfoLevel)

	// Load configuration
	// Default to /etc/arca-lb/agent.yaml
	configPath := os.Getenv("ARCA_AGENT_CONFIG")
	if configPath == "" {
		configPath = "/etc/arca-lb/agent.yaml"
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.WithError(err).Fatal("Failed to load configuration")
	}

	// Configure logger based on configuration
	configureLogger(logger, cfg)

	logger.Info("Starting arca-lb agent")

	// Create root context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize components
	components, err := initializeComponents(ctx, cfg, logger)
	if err != nil {
		logger.WithError(err).Fatal("Failed to initialize components")
	}

	// Start components
	if err := startComponents(ctx, components, logger); err != nil {
		logger.WithError(err).Fatal("Failed to start components")
	}

	logger.Info("All components started successfully")

	// Wait for shutdown signal
	waitForShutdown(logger)

	// Cancel context to signal all components
	cancel()

	// Graceful shutdown using shutdown handler
	shutdownHandler := shutdown.NewHandler(&shutdown.Components{
		GRPCClient:     components.GRPCClient,
		Reconciler:     components.Reconciler,
		HealthCheckMgr: components.HealthCheckMgr,
		FRRManager:     components.FRRManager,
		VPPConnection:  components.VPPConnection,
		MetricsServer:  components.MetricsServer,
	}, logger)

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	if err := shutdownHandler.Shutdown(shutdownCtx); err != nil {
		logger.WithError(err).Warn("Shutdown completed with errors")
	}

	logger.Info("Agent shutdown complete")
}

// initializeComponents creates all agent components in the correct dependency order
func initializeComponents(ctx context.Context, cfg *config.Config, logger *logrus.Logger) (*Components, error) {
	logger.Info("Initializing components...")

	// 1. State Manager (no dependencies)
	logger.Debug("Creating state manager")
	stateManager := state.NewManager()

	// 2. VPP Connection (needed by syncer)
	logger.Debug("Creating VPP connection")
	vppConn := vpp.NewConnection(&cfg.VPP, logger)

	// 3. VPP Syncer (depends on connection + LB config)
	logger.Debug("Creating VPP syncer")
	vppSyncer := vpp.NewSyncer(vppConn, &cfg.VPP.LB, logger)

	// 4. FRR Manager (optional - control plane)
	var frrManager *frr.Manager
	if cfg.FRR.Enabled {
		logger.Debug("Creating FRR manager")
		frrMgr, err := frr.NewManager(&cfg.FRR, logger)
		if err != nil {
			// FRR failure is non-fatal (can run VPP-only mode)
			logger.WithError(err).Warn("Failed to initialize FRR manager, BGP announcements disabled")
			frrManager = nil
		} else {
			logger.Info("FRR integration enabled")
			frrManager = frrMgr
		}
	} else {
		logger.Info("FRR integration disabled in configuration")
	}

	// 5. Health Check Callback (VPP + optional FRR)
	logger.Debug("Creating health check callback")
	var healthCheckCallback healthcheck.StateChangeCallback
	if frrManager != nil {
		// Composite callback: VPP + FRR
		healthCheckCallback = healthcheck.CompositeStateChangeCallback(vppSyncer, frrManager, logger)
	} else {
		// VPP-only callback
		healthCheckCallback = healthcheck.VPPStateChangeCallback(vppSyncer, logger)
	}

	// 6. Health Check Manager (needs callback)
	logger.Debug("Creating health check manager")
	healthCheckMgr := healthcheck.NewManager(&cfg.HealthCheck, logger, healthCheckCallback)

	// 6.5. Metrics Registry and Server (optional, depends on config)
	var metricsRegistry *metrics.Registry
	var metricsServer *metrics.Server
	if cfg.Metrics.Enabled {
		logger.Debug("Creating metrics registry")
		metricsRegistry = metrics.NewRegistry(logger)

		// Register health check collector
		metricsRegistry.RegisterHealthCheckCollector(healthCheckMgr.GetStateTracker())
		
		// Register VIP traffic collector (using VPP Syncer as provider)
		vipProvider := &vppSyncerVIPProvider{syncer: vppSyncer}
		metricsRegistry.RegisterVIPTrafficCollector(vipProvider)

		// Create metrics server
		logger.Debug("Creating metrics server")
		metricsServer = metrics.NewServer(&cfg.Metrics, metricsRegistry.HTTPHandler(), logger)
	} else {
		logger.Debug("Metrics disabled in configuration")
	}

	// 7. Reconciler (needs state manager, VPP syncer, health checker)
	logger.Debug("Creating reconciler")
	reconcilerInstance := reconciler.NewReconciler(
		cfg,
		logger,
		stateManager,
		vppSyncer,
		healthCheckMgr,
	)

	// 8. gRPC Config Handler (composite: state manager + reconciler)
	logger.Debug("Creating gRPC config handler")
	configHandler := func(config *models.Config) error {
		// Update state manager
		stateManager.UpdateConfig(config)

		// Trigger reconciliation
		reconcilerInstance.TriggerReconcile()

		return nil
	}

	// 9. gRPC Client (needs handler)
	logger.Debug("Creating gRPC client")
	grpcClient := grpc.NewClient(cfg, logger, configHandler)

	// Set metrics recorders if metrics are enabled
	if metricsRegistry != nil {
		// Health check metrics
		healthCheckCollector := metricsRegistry.GetHealthCheckCollector()
		if healthCheckCollector != nil {
			healthCheckMgr.SetMetricsRecorder(healthCheckCollector)
		}
		
		// VPP metrics
		vppCollector := metricsRegistry.GetVPPCollector()
		if vppCollector != nil {
			vppConn.SetMetricsRecorder(vppCollector)
			vppSyncer.SetMetricsRecorder(vppCollector)
		}
		
		// Reconciler metrics
		reconcilerCollector := metricsRegistry.GetReconcilerCollector()
		if reconcilerCollector != nil {
			reconcilerInstance.SetMetricsRecorder(reconcilerCollector)
		}
	}

	logger.Info("All components initialized successfully")

	return &Components{
		StateManager:    stateManager,
		VPPConnection:   vppConn,
		VPPSyncer:       vppSyncer,
		FRRManager:      frrManager,
		HealthCheckMgr:  healthCheckMgr,
		Reconciler:      reconcilerInstance,
		GRPCClient:      grpcClient,
		MetricsRegistry: metricsRegistry,
		MetricsServer:   metricsServer,
	}, nil
}

// startComponents starts all components in the correct order
func startComponents(ctx context.Context, components *Components, logger *logrus.Logger) error {
	logger.Info("Starting components...")

	// 1. Start VPP connection (goroutine-based, returns immediately)
	logger.Info("Starting VPP connection")
	if err := components.VPPConnection.Start(ctx); err != nil {
		return fmt.Errorf("failed to start VPP connection: %w", err)
	}

	// Wait for VPP to be ready (with timeout and context cancellation support)
	logger.Debug("Waiting for VPP connection to be ready")
	if err := waitForVPPConnection(ctx, components.VPPConnection, 30*time.Second, logger); err != nil {
		return fmt.Errorf("VPP connection not ready: %w", err)
	}
	logger.Info("VPP connection established")

	// 2. Start reconciler FIRST (goroutine-based, no error return)
	// This ensures the reconcile channel is ready before gRPC client starts sending configs
	logger.Info("Starting reconciler")
	components.Reconciler.Start(ctx)

	// 3. Start health check manager BEFORE gRPC client (synchronous)
	// This ensures health check manager is ready when config arrives and reconciler tries to start health checks
	logger.Info("Starting health check manager")
	if err := components.HealthCheckMgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start health check manager: %w", err)
	}

	// 4. Start gRPC client AFTER reconciler and health check manager are ready (goroutine-based, returns immediately)
	// This prevents race condition where config arrives before reconciler/health check manager are ready
	logger.Info("Starting gRPC client")
	if err := components.GRPCClient.Start(ctx); err != nil {
		return fmt.Errorf("failed to start gRPC client: %w", err)
	}

	// 5. Start metrics server (if enabled)
	if components.MetricsServer != nil {
		logger.Info("Starting metrics server")
		if err := components.MetricsServer.Start(); err != nil {
			logger.WithError(err).Warn("Failed to start metrics server")
			// Metrics server failure is non-fatal
		}
	}

	logger.Info("All components started")
	return nil
}

// waitForVPPConnection polls VPP connection status until connected, timeout, or context cancelled
func waitForVPPConnection(ctx context.Context, conn *vpp.Connection, timeout time.Duration, logger *logrus.Logger) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		if conn.IsConnected() {
			return nil
		}

		select {
		case <-timeoutCtx.Done():
			// Check if parent context was cancelled or timeout occurred
			if ctx.Err() != nil {
				return fmt.Errorf("cancelled while waiting for VPP connection: %w", ctx.Err())
			}
			return fmt.Errorf("timeout waiting for VPP connection after %v", timeout)
		case <-ticker.C:
			// Continue waiting
		}
	}
}

// waitForShutdown blocks until SIGTERM or SIGINT is received
func waitForShutdown(logger *logrus.Logger) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	sig := <-sigChan
	logger.WithField("signal", sig.String()).Info("Received shutdown signal")
}

// configureLogger configures the logger based on configuration
func configureLogger(logger *logrus.Logger, cfg *config.Config) {
	// Set log level
	level, err := logrus.ParseLevel(cfg.Log.Level)
	if err != nil {
		logger.WithError(err).Warn("Invalid log level, using INFO")
		level = logrus.InfoLevel
	}
	logger.SetLevel(level)

	// Set log format
	switch cfg.Log.Format {
	case "json":
		logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339,
		})
	case "text":
		logger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
		})
	default:
		logger.Warnf("Unknown log format %s, using JSON", cfg.Log.Format)
		logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339,
		})
	}

	// Set log output
	switch cfg.Log.Output {
	case "stdout":
		logger.SetOutput(os.Stdout)
	case "stderr":
		logger.SetOutput(os.Stderr)
	case "":
		// Default to stdout
		logger.SetOutput(os.Stdout)
	default:
		// File path
		file, err := os.OpenFile(cfg.Log.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			logger.WithError(err).Warnf("Failed to open log file %s, using stdout", cfg.Log.Output)
			logger.SetOutput(os.Stdout)
		} else {
			logger.SetOutput(file)
		}
	}
}

// vppSyncerVIPProvider implements metrics.VIPTrafficProvider using VPP Syncer
type vppSyncerVIPProvider struct {
	syncer *vpp.Syncer
}

func (p *vppSyncerVIPProvider) GetVIPs() map[string]metrics.VIPInfo {
	if p.syncer == nil {
		return make(map[string]metrics.VIPInfo)
	}
	
	vppVIPs := p.syncer.GetVIPsForMetrics()
	result := make(map[string]metrics.VIPInfo, len(vppVIPs))
	
	for vipID, vppVIP := range vppVIPs {
		result[vipID] = metrics.VIPInfo{
			ID:       vppVIP.ID,
			IP:       vppVIP.IP,
			Port:     vppVIP.Port,
			Protocol: vppVIP.Protocol,
		}
	}
	
	return result
}
