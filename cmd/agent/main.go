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
	agentrollout "github.com/akam1o/arca-lb/internal/agent/rollout"
	"github.com/akam1o/arca-lb/internal/agent/routing"
	agentstatus "github.com/akam1o/arca-lb/internal/agent/status"
	"github.com/akam1o/arca-lb/internal/agent/store"
	"github.com/akam1o/arca-lb/internal/agent/watcher"
	otelsetup "github.com/akam1o/arca-lb/internal/pkg/otel"
	vipvalidation "github.com/akam1o/arca-lb/internal/virtualip/validation"
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
		Kubeconfig:     cfg.Kubernetes.Kubeconfig,
		AgentID:        cfg.Agent.ID,
		AgentStatusTTL: cfg.Agent.StatusTTL,
	}, logger)
	if err != nil {
		logger.Error("failed to create status updater", "error", err)
		os.Exit(1)
	}

	var rolloutCoordinator reconciler.RolloutCoordinator
	if cfg.Rollout.Enabled {
		rolloutCoordinator, err = agentrollout.New(agentrollout.Config{
			Kubeconfig:     cfg.Kubernetes.Kubeconfig,
			Namespace:      cfg.Rollout.LeaseNamespace,
			HolderIdentity: cfg.Agent.ID,
			LeaseDuration:  cfg.Rollout.LeaseDuration,
			RetryInterval:  cfg.Rollout.RetryInterval,
		}, logger)
		if err != nil {
			logger.Error("failed to create rollout coordinator", "error", err)
			os.Exit(1)
		}
		logger.Info("rollout coordinator enabled",
			"lease_namespace", cfg.Rollout.LeaseNamespace,
			"lease_duration", cfg.Rollout.LeaseDuration,
			"retry_interval", cfg.Rollout.RetryInterval)
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
	reconMgr.SetTuningDriftConfig(retainedVIPTuningDriftConfig(cfg.DataPlane.VPP, logger))
	reconMgr.SetRolloutCoordinator(rolloutCoordinator)

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

	// Start HTTP server for the container healthcheck and optional metrics before
	// initial sync so liveness does not depend on stale dataplane cleanup time.
	metricsServer := newAgentHTTPServer(cfg.Metrics, logger)
	go func() {
		logger.Info("agent HTTP server starting", "address", cfg.Metrics.Address, "metrics_enabled", cfg.Metrics.Enabled)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("agent HTTP server error", "error", err)
		}
	}()

	watcherErrCh := make(chan error, 1)
	watcherSyncedCh := make(chan struct{})
	go func() {
		watcherErrCh <- w.StartWithInitialSync(ctx, func(syncCtx context.Context, currentVIPs []v1alpha1.VirtualIP) error {
			if err := cleanupStaleLastConfigs(syncCtx, st, dp, router, rolloutCoordinator, currentVIPs, logger); err != nil {
				return fmt.Errorf("stale dataplane cleanup failed: %w", err)
			}
			close(watcherSyncedCh)
			return nil
		})
	}()

	select {
	case err := <-watcherErrCh:
		if err != nil {
			logger.Error("watcher exited before initial sync", "error", err)
			os.Exit(1)
		}
	case <-watcherSyncedCh:
	}

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

	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown agent HTTP server", "error", err)
	}
	reconMgr.Stop()
	hcEngine.Stop()

	logger.Info("agent shutdown complete")
}

func newAgentHTTPServer(cfg agentconfig.MetricsSettings, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:         cfg.Address,
		Handler:      newAgentHTTPMux(cfg, logger),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
}

func newAgentHTTPMux(cfg agentconfig.MetricsSettings, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	if cfg.Enabled {
		mux.Handle(cfg.Path, promhttp.Handler())
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			logger.Error("failed to write health response", "error", err)
		}
	})
	return mux
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

	if err := vipvalidation.ValidateDataPlane(vip); err != nil {
		h.logger.Error("invalid VirtualIP spec, ignoring update", "vip", vipKey, "error", err)
		return
	}

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
	rollouts reconciler.RolloutCoordinator,
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
	currentValidByKey := make(map[string]bool, len(currentVIPs))
	currentAddresses := make(map[string]struct{}, len(currentVIPs))
	for i := range currentVIPs {
		currentVIP := &currentVIPs[i]
		key := healthcheck.KeyForVIP(currentVIP)
		currentByKey[key] = currentVIP
		if err := vipvalidation.Validate(currentVIP); err != nil {
			logger.Warn("ignoring invalid current VirtualIP for stale route protection", "vip", key, "error", err)
			continue
		}
		currentValidByKey[key] = true
		currentAddresses[currentVIP.Spec.Address] = struct{}{}
	}

	var firstErr error
	decodedLastConfigs := make(map[string]*v1alpha1.VirtualIP, len(lastConfigs))
	for key, data := range lastConfigs {
		vip, err := virtualIPFromLastConfig(key, data)
		if err != nil {
			logger.Warn("failed to decode stale last-applied config", "vip", key, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		decodedLastConfigs[key] = vip
	}

	for key, vip := range decodedLastConfigs {
		if _, ok := currentByKey[key]; ok && !currentValidByKey[key] && vip.Spec.Address != "" {
			currentAddresses[vip.Spec.Address] = struct{}{}
		}
	}

	for key, vip := range decodedLastConfigs {
		if currentVIP, ok := currentByKey[key]; ok {
			// Invalid current specs are ignored by the event handler; keep the
			// last known good dataplane state for this key until a valid spec arrives.
			if !currentValidByKey[key] {
				logger.Warn("preserving retained VIP for invalid current VirtualIP", "vip", key)
				continue
			}
			if sameRetainedVIPIdentity(vip, currentVIP) {
				continue
			}
		}

		_, addressInUse := currentAddresses[vip.Spec.Address]
		withdrawRoute := !addressInUse
		cleanup := func(ctx context.Context) error {
			return cleanupRetainedVIP(ctx, st, dp, router, key, vip, withdrawRoute, logger)
		}
		var cleanupErr error
		if rollouts != nil {
			cleanupErr = rollouts.RunExclusive(ctx, rolloutKeyForCleanup(key, vip), cleanup)
		} else {
			cleanupErr = cleanup(ctx)
		}
		if cleanupErr != nil {
			logger.Warn("failed to clean stale retained VIP", "vip", key, "error", cleanupErr)
			if firstErr == nil {
				firstErr = cleanupErr
			}
		}
	}

	return firstErr
}

func cleanupRetainedVIP(
	ctx context.Context,
	st *store.Store,
	dp dataplane.DataPlane,
	router routing.Router,
	key string,
	vip *v1alpha1.VirtualIP,
	withdrawRoute bool,
	logger *slog.Logger,
) error {
	logger.Info("cleaning stale retained VIP from dataplane", "vip", key, "address", vip.Spec.Address, "withdraw_route", withdrawRoute)
	if withdrawRoute && router != nil {
		if err := router.WithdrawVIP(ctx, vip.Spec.Address); err != nil {
			return fmt.Errorf("failed to withdraw stale retained VIP route: %w", err)
		}
	}
	if err := dp.RemoveVIP(ctx, vip); err != nil {
		return fmt.Errorf("failed to remove stale retained VIP from dataplane: %w", err)
	}
	if err := st.DeleteLastConfig(key); err != nil {
		return fmt.Errorf("failed to delete stale last-applied config: %w", err)
	}
	if err := st.DeleteHealthStatesForVIP(key); err != nil {
		return fmt.Errorf("failed to delete stale health states: %w", err)
	}
	return nil
}

func rolloutKeyForCleanup(key string, vip *v1alpha1.VirtualIP) string {
	if vip != nil && vip.Spec.Address != "" {
		return "vip-address/" + vip.Spec.Address
	}
	return "virtualip/" + key
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

func retainedVIPTuningDriftConfig(vpp map[string]interface{}, logger *slog.Logger) reconciler.TuningDriftConfig {
	cfg := reconciler.TuningDriftConfig{}
	if vpp == nil {
		return cfg
	}

	if policy, ok := vpp["retained_vip_tuning_drift_policy"].(string); ok && policy != "" {
		cfg.Policy = policy
	}

	for _, key := range []string{"retained_vip_tuning_drift_drain", "rolling_recreate_drain"} {
		value, ok := vpp[key]
		if !ok {
			continue
		}
		drain, err := durationSetting(value)
		if err != nil {
			logger.Warn("invalid retained VIP tuning drift drain setting", "key", key, "value", value, "error", err)
			continue
		}
		cfg.DrainDuration = drain
		break
	}

	return cfg
}

func durationSetting(value interface{}) (time.Duration, error) {
	switch v := value.(type) {
	case time.Duration:
		return v, nil
	case string:
		if v == "" {
			return 0, nil
		}
		return time.ParseDuration(v)
	case int:
		return time.Duration(v) * time.Second, nil
	case int64:
		return time.Duration(v) * time.Second, nil
	case float64:
		return time.Duration(v * float64(time.Second)), nil
	default:
		return 0, fmt.Errorf("unsupported duration type %T", value)
	}
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
