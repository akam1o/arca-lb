package metrics

import (
	"sync"

	"github.com/akam1o/arca-lb/internal/agent/healthcheck"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

// HealthCheckCollector collects health check metrics
type HealthCheckCollector struct {
	stateTracker *healthcheck.StateTracker
	logger       *logrus.Logger

	// Metrics
	healthCheckTotal     *prometheus.CounterVec
	backendState         *prometheus.GaugeVec
	healthCheckDuration  *prometheus.HistogramVec
	
	// Track previous collection state to detect removed backends
	mu              sync.Mutex
	previousBackends map[string]bool // "vipID:backendID" -> exists
}

// NewHealthCheckCollector creates a new health check collector
func NewHealthCheckCollector(stateTracker *healthcheck.StateTracker, logger *logrus.Logger) *HealthCheckCollector {
	if logger == nil {
		logger = logrus.New()
	}

	return &HealthCheckCollector{
		stateTracker: stateTracker,
		logger:       logger,
		healthCheckTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arca_lb_healthcheck_total",
				Help: "Total number of health checks performed",
			},
			[]string{"vip_id", "backend_id", "result"}, // result: success|failure
		),
		backendState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "arca_lb_backend_up",
				Help: "Backend health state (1 = up, 0 = down, -1 = unknown)",
			},
			[]string{"vip_id", "backend_id"},
		),
		healthCheckDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "arca_lb_healthcheck_duration_seconds",
				Help:    "Health check duration in seconds",
				Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~4s
			},
			[]string{"vip_id", "backend_id", "result"},
		),
		previousBackends: make(map[string]bool),
	}
}

// Describe implements prometheus.Collector
func (c *HealthCheckCollector) Describe(ch chan<- *prometheus.Desc) {
	c.healthCheckTotal.Describe(ch)
	c.backendState.Describe(ch)
	c.healthCheckDuration.Describe(ch)
}

// Collect implements prometheus.Collector
func (c *HealthCheckCollector) Collect(ch chan<- prometheus.Metric) {
	// Collect current backend states
	states := c.stateTracker.GetAllStates()
	
	c.mu.Lock()
	// Track current backends
	currentBackends := make(map[string]bool)
	
	// Update backend state gauges for all current states
	for key, state := range states {
		// Parse key: "vipID:backendID"
		vipID, backendID := parseKey(key)
		if vipID == "" || backendID == "" {
			c.logger.WithField("key", key).Warn("Invalid state key format")
			continue
		}

		// Mark as current
		currentBackends[key] = true

		// Update backend state gauge
		// Use -1 for unknown state as per help text and PLAN.md requirements
		switch state.State {
		case healthcheck.StateUp:
			c.backendState.WithLabelValues(vipID, backendID).Set(1.0)
		case healthcheck.StateDown:
			c.backendState.WithLabelValues(vipID, backendID).Set(0.0)
		case healthcheck.StateUnknown:
			c.backendState.WithLabelValues(vipID, backendID).Set(-1.0)
		}
	}
	
	// Remove metrics for backends that no longer exist
	for key := range c.previousBackends {
		if !currentBackends[key] {
			vipID, backendID := parseKey(key)
			if vipID != "" && backendID != "" {
				// Delete the metric for removed backend
				c.backendState.DeleteLabelValues(vipID, backendID)
			}
		}
	}
	
	// Update previous backends for next collection
	c.previousBackends = currentBackends
	c.mu.Unlock()

	// Export metrics
	c.healthCheckTotal.Collect(ch)
	c.backendState.Collect(ch)
	c.healthCheckDuration.Collect(ch)
}

// RecordProbeResult records a health check probe result
func (c *HealthCheckCollector) RecordProbeResult(vipID, backendID string, success bool, duration float64) {
	result := "failure"
	if success {
		result = "success"
	}

	c.healthCheckTotal.WithLabelValues(vipID, backendID, result).Inc()
	c.healthCheckDuration.WithLabelValues(vipID, backendID, result).Observe(duration)
}

// parseKey parses a "vipID:backendID" key into its components
func parseKey(key string) (vipID, backendID string) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[:i], key[i+1:]
		}
	}
	return "", ""
}

// VPPCollector collects VPP-related metrics
type VPPCollector struct {
	logger *logrus.Logger

	// Metrics
	vppErrorsTotal    *prometheus.CounterVec
	vppReconnectsTotal prometheus.Counter
}

// NewVPPCollector creates a new VPP collector
func NewVPPCollector(logger *logrus.Logger) *VPPCollector {
	if logger == nil {
		logger = logrus.New()
	}

	return &VPPCollector{
		logger: logger,
		vppErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arca_lb_vpp_errors_total",
				Help: "Total number of VPP errors",
			},
			[]string{"component", "operation"}, // component: connection|sync|lb, operation: connect|add_vip|add_backend|etc
		),
		vppReconnectsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "arca_lb_vpp_reconnects_total",
				Help: "Total number of VPP reconnections",
			},
		),
	}
}

// Describe implements prometheus.Collector
func (c *VPPCollector) Describe(ch chan<- *prometheus.Desc) {
	c.vppErrorsTotal.Describe(ch)
	c.vppReconnectsTotal.Describe(ch)
}

// Collect implements prometheus.Collector
func (c *VPPCollector) Collect(ch chan<- prometheus.Metric) {
	c.vppErrorsTotal.Collect(ch)
	c.vppReconnectsTotal.Collect(ch)
}

// RecordError records a VPP error
func (c *VPPCollector) RecordError(component, operation string) {
	c.vppErrorsTotal.WithLabelValues(component, operation).Inc()
}

// RecordReconnect records a VPP reconnection
func (c *VPPCollector) RecordReconnect() {
	c.vppReconnectsTotal.Inc()
}

// ReconcilerCollector collects reconciler metrics
type ReconcilerCollector struct {
	logger *logrus.Logger

	// Metrics
	reconcileTotal *prometheus.CounterVec
}

// VIPTrafficCollector collects VIP traffic statistics
type VIPTrafficCollector struct {
	logger *logrus.Logger
	
	// VIP provider interface to get current VIPs
	vipProvider VIPTrafficProvider
	
	// Metrics
	vipBytesTotal   *prometheus.CounterVec
	vipPacketsTotal *prometheus.CounterVec
	
	// Track previous VIPs to detect removed VIPs
	mu            sync.Mutex
	previousVIPs  map[string]VIPInfo // "vipID" -> VIPInfo (to get label values for deletion)
}

// VIPTrafficProvider is an interface for getting VIP information
type VIPTrafficProvider interface {
	GetVIPs() map[string]VIPInfo
}

// VIPInfo contains information about a VIP for traffic statistics
type VIPInfo struct {
	ID       string
	IP       string
	Port     uint16
	Protocol string
}


// NewVIPTrafficCollector creates a new VIP traffic collector
func NewVIPTrafficCollector(vipProvider VIPTrafficProvider, logger *logrus.Logger) *VIPTrafficCollector {
	if logger == nil {
		logger = logrus.New()
	}

	return &VIPTrafficCollector{
		logger:      logger,
		vipProvider: vipProvider,
		vipBytesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arca_lb_vip_bytes_total",
				Help: "Total number of bytes processed by VIP",
			},
			[]string{"vip_id", "vip_ip", "protocol", "direction"}, // direction: in|out
		),
		vipPacketsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arca_lb_vip_packets_total",
				Help: "Total number of packets processed by VIP",
			},
			[]string{"vip_id", "vip_ip", "protocol", "direction"}, // direction: in|out
		),
		previousVIPs: make(map[string]VIPInfo),
	}
}

// Describe implements prometheus.Collector
func (c *VIPTrafficCollector) Describe(ch chan<- *prometheus.Desc) {
	c.vipBytesTotal.Describe(ch)
	c.vipPacketsTotal.Describe(ch)
}

// Collect implements prometheus.Collector
func (c *VIPTrafficCollector) Collect(ch chan<- prometheus.Metric) {
	if c.vipProvider == nil {
		return
	}

	vips := c.vipProvider.GetVIPs()
	
	c.mu.Lock()
	
	// Update metrics for current VIPs
	// Note: Actual traffic statistics from VPP stats API will be implemented later
	// For now, we initialize counters at 0 for all VIPs
	for vipID, vipInfo := range vips {
		// Initialize counters for in/out directions
		// Actual values will be updated when VPP stats API integration is complete
		c.vipBytesTotal.WithLabelValues(vipID, vipInfo.IP, vipInfo.Protocol, "in").Add(0)
		c.vipBytesTotal.WithLabelValues(vipID, vipInfo.IP, vipInfo.Protocol, "out").Add(0)
		c.vipPacketsTotal.WithLabelValues(vipID, vipInfo.IP, vipInfo.Protocol, "in").Add(0)
		c.vipPacketsTotal.WithLabelValues(vipID, vipInfo.IP, vipInfo.Protocol, "out").Add(0)
	}
	
	// Remove metrics for VIPs that no longer exist
	// Use previous VIP info to get label values for deletion
	for vipID, prevVIPInfo := range c.previousVIPs {
		if _, exists := vips[vipID]; !exists {
			// VIP was removed, delete its metrics using previous label values
			c.vipBytesTotal.DeleteLabelValues(vipID, prevVIPInfo.IP, prevVIPInfo.Protocol, "in")
			c.vipBytesTotal.DeleteLabelValues(vipID, prevVIPInfo.IP, prevVIPInfo.Protocol, "out")
			c.vipPacketsTotal.DeleteLabelValues(vipID, prevVIPInfo.IP, prevVIPInfo.Protocol, "in")
			c.vipPacketsTotal.DeleteLabelValues(vipID, prevVIPInfo.IP, prevVIPInfo.Protocol, "out")
		}
	}
	
	// Update previous VIPs for next collection (store full VIP info for deletion)
	c.previousVIPs = make(map[string]VIPInfo, len(vips))
	for vipID, vipInfo := range vips {
		c.previousVIPs[vipID] = vipInfo
	}
	c.mu.Unlock()

	// Export metrics
	c.vipBytesTotal.Collect(ch)
	c.vipPacketsTotal.Collect(ch)
}

// UpdateTrafficStats updates traffic statistics for a VIP
// This will be called when VPP stats API integration is complete
func (c *VIPTrafficCollector) UpdateTrafficStats(vipID, vipIP, protocol, direction string, bytes, packets uint64) {
	c.vipBytesTotal.WithLabelValues(vipID, vipIP, protocol, direction).Add(float64(bytes))
	c.vipPacketsTotal.WithLabelValues(vipID, vipIP, protocol, direction).Add(float64(packets))
}

// NewReconcilerCollector creates a new reconciler collector
func NewReconcilerCollector(logger *logrus.Logger) *ReconcilerCollector {
	if logger == nil {
		logger = logrus.New()
	}

	return &ReconcilerCollector{
		logger: logger,
		reconcileTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arca_lb_reconcile_total",
				Help: "Total number of reconciliation cycles",
			},
			[]string{"result"}, // result: success|failure|no_change
		),
	}
}

// Describe implements prometheus.Collector
func (c *ReconcilerCollector) Describe(ch chan<- *prometheus.Desc) {
	c.reconcileTotal.Describe(ch)
}

// Collect implements prometheus.Collector
func (c *ReconcilerCollector) Collect(ch chan<- prometheus.Metric) {
	c.reconcileTotal.Collect(ch)
}

// RecordReconcile records a reconciliation cycle
func (c *ReconcilerCollector) RecordReconcile(result string) {
	c.reconcileTotal.WithLabelValues(result).Inc()
}

