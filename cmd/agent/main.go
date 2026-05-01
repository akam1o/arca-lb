// Package main is the entry point for the arca-lb v2 agent.
// The agent watches VirtualIP CRDs from the K8s API server,
// reconciles each VIP independently, and manages health checks,
// data-plane programming (VPP), and BGP route announcements (FRR).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	v1alpha1 "github.com/akam1o/arca-lb/api/v1alpha1"
	agentconfig "github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/akam1o/arca-lb/internal/agent/dataplane"
	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/akam1o/arca-lb/internal/agent/reconciler"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	agentstatus "github.com/akam1o/arca-lb/internal/agent/status"
	"github.com/akam1o/arca-lb/internal/agent/store"
	"github.com/akam1o/arca-lb/internal/agent/watcher"
	otelsetup "github.com/akam1o/arca-lb/internal/pkg/otel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	defer func() {
		if err := otelShutdown.Shutdown(ctx); err != nil {
			logger.Error("failed to shutdown OpenTelemetry", "error", err)
		}
	}()

	// Open local store
	st, err := store.Open(cfg.Agent.StorePath)
	if err != nil {
		logger.Error("failed to open local store", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("failed to close local store", "error", err)
		}
	}()

	// Create data plane
	dp, err := dataplane.New(cfg.DataPlane.Type, cfg.DataPlane.VPP)
	if err != nil {
		logger.Error("failed to create data plane", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := dp.Close(); err != nil {
			logger.Error("failed to close data plane", "error", err)
		}
	}()

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
	defer func() {
		if err := router.Close(); err != nil {
			logger.Error("failed to close router", "error", err)
		}
	}()

	statusUpdater, err := agentstatus.NewUpdater(agentstatus.Config{
		Kubeconfig: cfg.Kubernetes.Kubeconfig,
	}, logger)
	if err != nil {
		logger.Error("failed to create status updater", "error", err)
		os.Exit(1)
	}

	// Create health check engine
	hcEngine := healthcheck.NewEngine(healthcheck.EngineConfig{
		WorkerCount:         cfg.HealthCheck.WorkerCount,
		MaxConcurrentChecks: cfg.HealthCheck.MaxConcurrentChecks,
		DefaultTimeout:      cfg.HealthCheck.DefaultTimeout,
	}, st, nil, logger)

	// Create reconciler manager
	reconMgr := reconciler.NewManager(dp, router, st, hcEngine, cfg.Agent.ReconcileInterval, logger)
	reconMgr.SetStatusUpdater(statusUpdater)

	// Wire health change callback: when health changes, trigger reconcile
	hcCallback := func(vipKey, backendAddr string, oldState, newState healthcheck.V2BackendState) {
		logger.Info("backend health changed",
			"vip", vipKey, "backend", backendAddr,
			"old", oldState, "new", newState)
		reconMgr.OnHealthChange(vipKey)
	}
	hcEngine.SetCallback(hcCallback)

	// Start health check engine
	if err := hcEngine.Start(ctx); err != nil {
		logger.Error("failed to start health check engine", "error", err)
		os.Exit(1)
	}

	// Start reconciler
	reconMgr.Start(ctx)

	// Create VIP event handler that bridges watcher events to reconciler + health checks
	handler := &vipEventHandler{
		ctx:           ctx,
		reconciler:    reconMgr,
		hcEngine:      hcEngine,
		statusUpdater: statusUpdater,
		logger:        logger,
	}

	// Create and start K8s watcher
	watcherCfg := watcher.Config{
		Kubeconfig:     cfg.Kubernetes.Kubeconfig,
		Namespace:      cfg.Kubernetes.Namespace,
		ResyncInterval: cfg.Kubernetes.ResyncInterval,
	}
	w, err := watcher.New(watcherCfg, handler, logger)
	if err != nil {
		logger.Error("failed to create watcher", "error", err)
		os.Exit(1)
	}

	currentVIPs, err := watcher.ListCurrent(ctx, watcherCfg)
	if err != nil {
		logger.Warn("failed to list current VirtualIPs for stale dataplane cleanup", "error", err)
	} else if err := cleanupStaleLastConfigs(ctx, st, dp, router, currentVIPs, logger); err != nil {
		logger.Warn("stale dataplane cleanup completed with errors", "error", err)
	}

	// Start metrics server
	var metricsServer *http.Server
	if cfg.Metrics.Enabled {
		mux := http.NewServeMux()
		mux.Handle(cfg.Metrics.Path, promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("ok")); err != nil {
				logger.Error("failed to write health response", "error", err)
			}
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
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("failed to shutdown metrics server", "error", err)
		}
	}
	reconMgr.Stop()
	hcEngine.Stop()

	logger.Info("agent shutdown complete")
}

// vipEventHandler bridges watcher events to the reconciler and health check engine.
type vipEventHandler struct {
	ctx           context.Context
	reconciler    *reconciler.Manager
	hcEngine      *healthcheck.Engine
	statusUpdater healthCheckConditionUpdater
	logger        *slog.Logger
}

type healthCheckConditionUpdater interface {
	UpdateHealthCheckCondition(ctx context.Context, vip *v1alpha1.VirtualIP, condition metav1.Condition) error
}

func (h *vipEventHandler) OnVIPUpdate(vip *v1alpha1.VirtualIP) {
	vipKey := healthcheck.KeyForVIP(vip)
	h.logger.Info("VIP update received", "vip", vipKey, "generation", vip.Generation)

	// Start/update health checks
	if vip.Spec.HealthCheck != nil {
		if err := h.hcEngine.UpdateVIP(vip); err != nil {
			h.logger.Error("failed to update health check", "vip", vipKey, "error", err)
			h.updateHealthCheckCondition(vip, metav1.ConditionFalse, "InvalidHealthCheck", err.Error())
			return
		}
		h.updateHealthCheckCondition(vip, metav1.ConditionTrue, "Configured", "Health check configured")
	} else {
		h.hcEngine.StopVIP(vipKey)
		h.updateHealthCheckCondition(vip, metav1.ConditionTrue, "Disabled", "Health check disabled")
	}

	// Trigger reconciliation
	h.reconciler.OnVIPUpdate(vip)
}

func (h *vipEventHandler) OnVIPDelete(vip *v1alpha1.VirtualIP) {
	vipKey := healthcheck.KeyForVIP(vip)
	h.logger.Info("VIP delete received", "vip", vipKey)

	h.hcEngine.StopVIP(vipKey)
	h.reconciler.OnVIPDelete(vip)
}

func (h *vipEventHandler) updateHealthCheckCondition(vip *v1alpha1.VirtualIP, status metav1.ConditionStatus, reason, message string) {
	if h.statusUpdater == nil {
		return
	}

	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	condition := metav1.Condition{
		Type:               agentstatus.ConditionHealthCheckReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: vip.Generation,
	}
	if err := h.statusUpdater.UpdateHealthCheckCondition(ctx, vip, condition); err != nil {
		h.logger.Warn("failed to update health check condition", "vip", healthcheck.KeyForVIP(vip), "error", err)
	}
}

func cleanupStaleLastConfigs(
	ctx context.Context,
	st *store.Store,
	dp dataplane.DataPlane,
	router routing.Router,
	currentVIPs []v1alpha1.VirtualIP,
	logger *slog.Logger,
) error {
	if st == nil || dp == nil {
		return nil
	}

	lastConfigs, err := st.LoadAllLastConfigs()
	if err != nil {
		return fmt.Errorf("failed to load last-applied configs: %w", err)
	}
	if len(lastConfigs) == 0 {
		return nil
	}

	currentByKey := make(map[string]*v1alpha1.VirtualIP, len(currentVIPs))
	for i := range currentVIPs {
		currentByKey[healthcheck.KeyForVIP(&currentVIPs[i])] = &currentVIPs[i]
	}

	var firstErr error
	for key, data := range lastConfigs {
		vip, err := virtualIPFromLastConfig(key, data)
		if err != nil {
			logger.Warn("failed to decode stale last-applied config", "vip", key, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if currentVIP, ok := currentByKey[key]; ok && sameRetainedVIPIdentity(vip, currentVIP) {
			continue
		}

		logger.Info("cleaning stale retained VIP from dataplane", "vip", key, "address", vip.Spec.Address)
		if err := dp.RemoveVIP(ctx, vip); err != nil {
			logger.Warn("failed to remove stale retained VIP from dataplane", "vip", key, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if router != nil {
			if err := router.WithdrawVIP(ctx, vip.Spec.Address); err != nil {
				logger.Warn("failed to withdraw stale retained VIP route", "vip", key, "error", err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
		}
		if err := st.DeleteLastConfig(key); err != nil {
			logger.Warn("failed to delete stale last-applied config", "vip", key, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
		if err := st.DeleteHealthStatesForVIP(key); err != nil {
			logger.Warn("failed to delete stale health states", "vip", key, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

func virtualIPFromLastConfig(key string, data []byte) (*v1alpha1.VirtualIP, error) {
	namespace, name, ok := strings.Cut(key, "/")
	if !ok || namespace == "" || name == "" {
		return nil, fmt.Errorf("invalid namespaced VIP key %q", key)
	}

	var spec v1alpha1.VirtualIPSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to decode last-applied config for %s: %w", key, err)
	}

	return &v1alpha1.VirtualIP{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: spec,
	}, nil
}

func sameRetainedVIPIdentity(a, b *v1alpha1.VirtualIP) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Spec.Address == b.Spec.Address &&
		a.Spec.Port == b.Spec.Port &&
		a.Spec.Protocol == b.Spec.Protocol &&
		a.Spec.EncapType == b.Spec.EncapType &&
		uint8PtrEqual(a.Spec.DSCP, b.Spec.DSCP)
}

func uint8PtrEqual(a, b *uint8) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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
