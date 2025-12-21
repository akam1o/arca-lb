package metrics

import (
	"net/http"
	"sync"

	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// Registry wraps a Prometheus registry and provides hooks for collectors.
type Registry struct {
	registry *prometheus.Registry
	logger   *logrus.Logger

	mu sync.Mutex

	// Collectors
	healthCheckCollector *HealthCheckCollector
	vppCollector         *VPPCollector
	reconcilerCollector  *ReconcilerCollector
	vipTrafficCollector  *VIPTrafficCollector
}

// NewRegistry creates a new Registry with its own Prometheus registry.
func NewRegistry(logger *logrus.Logger) *Registry {
	if logger == nil {
		logger = logrus.New()
	}

	reg := &Registry{
		registry: prometheus.NewRegistry(),
		logger:   logger,
	}

	// Register default collectors
	reg.vppCollector = NewVPPCollector(logger)
	reg.reconcilerCollector = NewReconcilerCollector(logger)

	reg.registry.MustRegister(reg.vppCollector)
	reg.registry.MustRegister(reg.reconcilerCollector)

	return reg
}

// RegisterHealthCheckCollector registers the health check collector.
func (r *Registry) RegisterHealthCheckCollector(stateTracker *healthcheck.StateTracker) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.healthCheckCollector != nil {
		r.logger.Warn("Health check collector already registered")
		return
	}

	if stateTracker == nil {
		r.logger.Warn("State tracker is nil, skipping health check collector registration")
		return
	}

	r.healthCheckCollector = NewHealthCheckCollector(stateTracker, r.logger)
	r.registry.MustRegister(r.healthCheckCollector)

	r.logger.Info("Health check collector registered")
}

// RegisterVPPCollector registers the VPP collector.
// VPP collector is automatically registered in NewRegistry, this is a no-op for compatibility.
func (r *Registry) RegisterVPPCollector(syncer interface{}, conn interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// VPP collector is already registered in NewRegistry
	r.logger.Debug("VPP collector already registered")
}

// RegisterFRRCollector registers the FRR collector.
// Placeholder for future implementation.
func (r *Registry) RegisterFRRCollector(manager interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.WithField("manager_present", manager != nil).Debug("FRR collector registration (not yet implemented)")
}

// RegisterReconcilerCollector registers the reconciler collector.
// Reconciler collector is automatically registered in NewRegistry, this is a no-op for compatibility.
func (r *Registry) RegisterReconcilerCollector(reconciler interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Reconciler collector is already registered in NewRegistry
	r.logger.Debug("Reconciler collector already registered")
}

// GetHealthCheckCollector returns the health check collector
func (r *Registry) GetHealthCheckCollector() *HealthCheckCollector {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.healthCheckCollector
}

// GetVPPCollector returns the VPP collector
func (r *Registry) GetVPPCollector() *VPPCollector {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.vppCollector
}

// GetReconcilerCollector returns the reconciler collector
func (r *Registry) GetReconcilerCollector() *ReconcilerCollector {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reconcilerCollector
}

// RegisterVIPTrafficCollector registers the VIP traffic collector
func (r *Registry) RegisterVIPTrafficCollector(vipProvider VIPTrafficProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.vipTrafficCollector != nil {
		r.logger.Warn("VIP traffic collector already registered")
		return
	}

	if vipProvider == nil {
		r.logger.Warn("VIP provider is nil, skipping VIP traffic collector registration")
		return
	}

	r.vipTrafficCollector = NewVIPTrafficCollector(vipProvider, r.logger)
	r.registry.MustRegister(r.vipTrafficCollector)

	r.logger.Info("VIP traffic collector registered")
}

// GetVIPTrafficCollector returns the VIP traffic collector
func (r *Registry) GetVIPTrafficCollector() *VIPTrafficCollector {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.vipTrafficCollector
}

// HTTPHandler returns an HTTP handler for the custom registry.
func (r *Registry) HTTPHandler() http.Handler {
	reg := r.registry
	if reg == nil {
		return promhttp.Handler()
	}
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}
