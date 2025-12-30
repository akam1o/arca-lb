package healthcheck

import (
	"context"
	"sync"
	"time"

	"github.com/akam1o/arca-lb/internal/common/models"
	"github.com/sirupsen/logrus"
)

// ProbeJob represents a health check job for a single backend
type ProbeJob struct {
	// VIPID is the VIP this backend belongs to
	VIPID string

	// BackendID is the backend to probe
	BackendID string

	// BackendIP is the IP address to probe
	BackendIP string

	// Prober is the health check prober to use
	Prober Prober

	// RiseCount is the number of consecutive successes needed to mark UP
	RiseCount int

	// FallCount is the number of consecutive failures needed to mark DOWN
	FallCount int
}

// JobScheduler manages health check job scheduling for all VIPs
type JobScheduler struct {
	logger *logrus.Logger

	mu sync.RWMutex
	// vipSchedules maps vipID to its schedule
	vipSchedules map[string]*VIPSchedule

	// jobCh is the channel where jobs are sent
	jobCh chan<- *ProbeJob

	// stopCh is used to signal shutdown
	stopCh chan struct{}

	// wg tracks active schedulers
	wg sync.WaitGroup
}

// VIPSchedule represents the health check schedule for a single VIP
type VIPSchedule struct {
	vipID     string
	vipConfig *models.VIPConfig
	prober    Prober
	interval  time.Duration
	riseCount int
	fallCount int
	ticker    *time.Ticker
	stopCh    chan struct{}
	logger    *logrus.Logger
}

// NewJobScheduler creates a new job scheduler
func NewJobScheduler(jobCh chan<- *ProbeJob, logger *logrus.Logger) *JobScheduler {
	return &JobScheduler{
		logger:       logger,
		vipSchedules: make(map[string]*VIPSchedule),
		jobCh:        jobCh,
		stopCh:       make(chan struct{}),
	}
}

// StartVIP starts health check scheduling for a VIP
func (js *JobScheduler) StartVIP(ctx context.Context, vipConfig *models.VIPConfig, prober Prober) error {
	if vipConfig.HealthCheck == nil {
		return nil // No health check configured
	}

	js.mu.Lock()
	defer js.mu.Unlock()

	vipID := vipConfig.VIP.ID

	// Stop existing schedule if any
	if existing, ok := js.vipSchedules[vipID]; ok {
		js.logger.WithField("vip_id", vipID).Debug("Stopping existing VIP schedule")
		existing.stop()
		delete(js.vipSchedules, vipID)
	}

	// Create new schedule
	schedule := &VIPSchedule{
		vipID:     vipID,
		vipConfig: vipConfig,
		prober:    prober,
		interval:  time.Duration(vipConfig.HealthCheck.IntervalSec) * time.Second,
		riseCount: vipConfig.HealthCheck.RiseCount,
		fallCount: vipConfig.HealthCheck.FallCount,
		ticker:    time.NewTicker(time.Duration(vipConfig.HealthCheck.IntervalSec) * time.Second),
		stopCh:    make(chan struct{}),
		logger:    js.logger,
	}

	js.vipSchedules[vipID] = schedule

	// Start scheduler goroutine
	js.wg.Add(1)
	go js.runScheduler(ctx, schedule)

	js.logger.WithFields(logrus.Fields{
		"vip_id":   vipID,
		"interval": schedule.interval,
		"backends": len(vipConfig.Backends),
	}).Info("Started health check schedule for VIP")

	return nil
}

// StopVIP stops health check scheduling for a VIP
func (js *JobScheduler) StopVIP(vipID string) {
	js.mu.Lock()
	defer js.mu.Unlock()

	if schedule, ok := js.vipSchedules[vipID]; ok {
		js.logger.WithField("vip_id", vipID).Debug("Stopping VIP schedule")
		schedule.stop()
		delete(js.vipSchedules, vipID)
	}
}

// Stop stops all health check scheduling
func (js *JobScheduler) Stop() {
	js.logger.Info("Stopping job scheduler")

	// Signal shutdown
	close(js.stopCh)

	// Stop all VIP schedules
	js.mu.Lock()
	for vipID, schedule := range js.vipSchedules {
		js.logger.WithField("vip_id", vipID).Debug("Stopping VIP schedule")
		schedule.stop()
	}
	js.vipSchedules = make(map[string]*VIPSchedule)
	js.mu.Unlock()

	// Wait for all schedulers to finish
	js.wg.Wait()

	js.logger.Info("Job scheduler stopped")
}

// runScheduler runs the health check scheduler for a single VIP
func (js *JobScheduler) runScheduler(ctx context.Context, schedule *VIPSchedule) {
	defer js.wg.Done()
	defer schedule.ticker.Stop()

	// Emit initial jobs immediately
	js.emitJobs(schedule)

	for {
		select {
		case <-schedule.stopCh:
			js.logger.WithField("vip_id", schedule.vipID).Debug("VIP scheduler stopped")
			return

		case <-js.stopCh:
			js.logger.WithField("vip_id", schedule.vipID).Debug("VIP scheduler stopped (global stop)")
			return

		case <-ctx.Done():
			js.logger.WithField("vip_id", schedule.vipID).Debug("VIP scheduler stopped (context done)")
			return

		case <-schedule.ticker.C:
			// Emit jobs for all backends
			js.emitJobs(schedule)
		}
	}
}

// emitJobs emits health check jobs for all backends in a VIP
func (js *JobScheduler) emitJobs(schedule *VIPSchedule) {
	for i := range schedule.vipConfig.Backends {
		backend := &schedule.vipConfig.Backends[i]

		job := &ProbeJob{
			VIPID:     schedule.vipID,
			BackendID: backend.ID,
			BackendIP: backend.IP,
			Prober:    schedule.prober,
			RiseCount: schedule.riseCount,
			FallCount: schedule.fallCount,
		}

		// Non-blocking send to avoid blocking scheduler
		select {
		case js.jobCh <- job:
			// Job sent successfully
		default:
			// Channel full - log warning and skip this round
			js.logger.WithFields(logrus.Fields{
				"vip_id":     schedule.vipID,
				"backend_id": backend.ID,
			}).Warn("Job channel full, skipping health check")
		}
	}
}

// stop stops the VIP schedule
func (vs *VIPSchedule) stop() {
	select {
	case <-vs.stopCh:
		// Already stopped
		return
	default:
		close(vs.stopCh)
	}
}
