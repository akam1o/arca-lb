// Package main is the entry point for the arca-lb v2 agent.
// The agent watches VirtualIP CRDs from the K8s API server,
// reconciles each VIP independently, and manages health checks,
// data-plane programming (VPP), and BGP route announcements (FRR).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	agentconfig "github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/agent/dataplane"
	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/akam1o/arca-lb/internal/agent/reconciler"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	"github.com/akam1o/arca-lb/internal/agent/store"
	"github.com/akam1o/arca-lb/internal/agent/watcher"
	otelsetup "github.com/akam1o/arca-lb/internal/pkg/otel"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to agent configuration file")
	flag.Parse()

	if configPath == "" {
		configPath = os.Getenv("ARCA_AGENT_CONFIG")
	}
	if configPath == "" {
		configPath = "/etc/arca-lb/agent.yaml"
	}

	// Load configuration
	cfg, err := agentconfig.LoadV2Config(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Setup logging
	logger := setupLogger(cfg.Log)
	slog.SetDefault(logger)

	logger.Info("starting arca-lb agent v2",
		"agent_id", cfg.Agent.ID,
		"dataplane", cfg.DataPlane.Type,
		"routing", cfg.Routing.Type)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup OpenTelemetry
	otelShutdown, err := otelsetup.Setup(ctx, otelsetup.Config{
		ServiceName:    "arca-lb-agent",
		ServiceVersion: "2.0.0",
		OTLPEndpoint:   cfg.Telemetry.OTLPEndpoint,
		MetricsEnabled: cfg.Metrics.Enabled,
	})
	if err != nil {
		logger.Error("failed to setup OpenTelemetry", "error", err)
		os.Exit(1)
	}
	defer otelShutdown.Shutdown(ctx)

	// Open local store
	st, err := store.Open(cfg.Agent.StorePath)
	if err != nil {
		logger.Error("failed to open local store", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	// Create data plane
	dp, err := dataplane.New(cfg.DataPlane.Type, cfg.DataPlane.VPP)
	if err != nil {
		logger.Error("failed to create data plane", "error", err)
		os.Exit(1)
	}
	defer dp.Close()

	// Create router
	var router routing.Router
	if cfg.Routing.Enabled && cfg.Routing.Type == "frr" {
		router, err = routing.NewFRR(routing.FRRConfig{
			VTYShPath:  cfg.Routing.VTYShPath,
			RouteTag:   cfg.Routing.RouteTag,
			CmdTimeout: cfg.Routing.CmdTimeout,
		})
		if err != nil {
			logger.Error("failed to create FRR router", "error", err)
			os.Exit(1)
		}
	} else {
		router = routing.NewNoop()
	}
	defer router.Close()

	// Create health check engine
	hcEngine := healthcheck.NewEngine(healthcheck.EngineConfig{
		WorkerCount:         cfg.HealthCheck.WorkerCount,
		MaxConcurrentChecks: cfg.HealthCheck.MaxConcurrentChecks,
		DefaultTimeout:      cfg.HealthCheck.DefaultTimeout,
	}, st, nil, logger) // callback set after reconciler creation

	// Create reconciler manager
	reconMgr := reconciler.NewManager(dp, router, st, hcEngine, cfg.Agent.ReconcileInterval, logger)

	// Wire health change callback: when health changes, trigger reconcile
	hcCallback := func(vipName, backendAddr string, oldState, newState healthcheck.V2BackendState) {
		logger.Info("backend health changed",
			"vip", vipName, "backend", backendAddr,
			"old", oldState, "new", newState)
		reconMgr.OnHealthChange(vipName)
	}
	// Re-create engine with callback
	hcEngine = healthcheck.NewEngine(healthcheck.EngineConfig{
		WorkerCount:         cfg.HealthCheck.WorkerCount,
		MaxConcurrentChecks: cfg.HealthCheck.MaxConcurrentChecks,
		DefaultTimeout:      cfg.HealthCheck.DefaultTimeout,
	}, st, hcCallback, logger)

	// Start health check engine
	if err := hcEngine.Start(ctx); err != nil {
		logger.Error("failed to start health check engine", "error", err)
		os.Exit(1)
	}

	// Start reconciler
	reconMgr.Start(ctx)

	// Create VIP event handler that bridges watcher events to reconciler + health checks
	handler := &vipEventHandler{
		reconciler: reconMgr,
		hcEngine:   hcEngine,
		logger:     logger,
	}

	// Create and start K8s watcher
	w, err := watcher.New(watcher.Config{
		Kubeconfig:     cfg.Kubernetes.Kubeconfig,
		Namespace:      cfg.Kubernetes.Namespace,
		ResyncInterval: cfg.Kubernetes.ResyncInterval,
	}, handler, logger)
	if err != nil {
		logger.Error("failed to create watcher", "error", err)
		os.Exit(1)
	}

	// Start metrics server
	var metricsServer *http.Server
	if cfg.Metrics.Enabled {
		mux := http.NewServeMux()
		mux.Handle(cfg.Metrics.Path, promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
		metricsServer = &http.Server{
			Addr:         cfg.Metrics.Address,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		go func() {
			logger.Info("metrics server starting", "address", cfg.Metrics.Address)
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("metrics server error", "error", err)
			}
		}()
	}

	// Start watcher in background
	watcherErrCh := make(chan error, 1)
	go func() {
		watcherErrCh <- w.Start(ctx)
	}()

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		logger.Info("received signal, shutting down", "signal", sig)
	case err := <-watcherErrCh:
		if err != nil {
			logger.Error("watcher exited with error", "error", err)
		}
	}

	// Graceful shutdown
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if metricsServer != nil {
		metricsServer.Shutdown(shutdownCtx)
	}
	reconMgr.Stop()
	hcEngine.Stop()

	logger.Info("agent shutdown complete")
}

// vipEventHandler bridges watcher events to the reconciler and health check engine.
type vipEventHandler struct {
	reconciler *reconciler.Manager
	hcEngine   *healthcheck.Engine
	logger     *slog.Logger
}

func (h *vipEventHandler) OnVIPUpdate(vip *v1alpha1.VirtualIP) {
	h.logger.Info("VIP update received", "name", vip.Name, "generation", vip.Generation)

	// Start/update health checks
	if vip.Spec.HealthCheck != nil {
		if err := h.hcEngine.UpdateVIP(vip); err != nil {
			h.logger.Error("failed to update health check", "vip", vip.Name, "error", err)
		}
	} else {
		h.hcEngine.StopVIP(vip.Name)
	}

	// Trigger reconciliation
	h.reconciler.OnVIPUpdate(vip)
}

func (h *vipEventHandler) OnVIPDelete(vip *v1alpha1.VirtualIP) {
	h.logger.Info("VIP delete received", "name", vip.Name)

	h.hcEngine.StopVIP(vip.Name)
	h.reconciler.OnVIPDelete(vip)
}

func setupLogger(cfg agentconfig.LogSettings) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
