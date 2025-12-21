package healthcheck

import (
	"context"

	"github.com/sirupsen/logrus"
)

// ProbeJobResult represents the result of a probe job
type ProbeJobResult struct {
	VIPID     string
	BackendID string
	Result    ProbeResult
	RiseCount int
	FallCount int
}

// Worker represents a health check worker
type Worker struct {
	id       int
	logger   *logrus.Logger
	jobCh    <-chan *ProbeJob
	resultCh chan<- *ProbeJobResult
	stopCh   chan struct{}
}

// NewWorker creates a new health check worker
func NewWorker(id int, jobCh <-chan *ProbeJob, resultCh chan<- *ProbeJobResult, logger *logrus.Logger) *Worker {
	return &Worker{
		id:       id,
		logger:   logger,
		jobCh:    jobCh,
		resultCh: resultCh,
		stopCh:   make(chan struct{}),
	}
}

// Start starts the worker goroutine
func (w *Worker) Start(ctx context.Context) {
	w.logger.WithField("worker_id", w.id).Debug("Worker started")

	for {
		select {
		case <-w.stopCh:
			w.logger.WithField("worker_id", w.id).Debug("Worker stopped")
			return

		case <-ctx.Done():
			w.logger.WithField("worker_id", w.id).Debug("Worker stopped (context done)")
			return

		case job, ok := <-w.jobCh:
			if !ok {
				w.logger.WithField("worker_id", w.id).Debug("Worker stopped (job channel closed)")
				return
			}

			// Execute probe
			w.executeJob(ctx, job)
		}
	}
}

// Stop stops the worker
func (w *Worker) Stop() {
	select {
	case <-w.stopCh:
		// Already stopped
		return
	default:
		close(w.stopCh)
	}
}

// executeJob executes a probe job and sends the result
func (w *Worker) executeJob(ctx context.Context, job *ProbeJob) {
	// Execute probe
	result := job.Prober.Probe(ctx, job.BackendIP)

	// Log probe result
	w.logger.WithFields(logrus.Fields{
		"worker_id":  w.id,
		"vip_id":     job.VIPID,
		"backend_id": job.BackendID,
		"backend_ip": job.BackendIP,
		"success":    result.Success,
		"latency":    result.Latency,
	}).Debug("Health check probe completed")

	// Send result (non-blocking)
	jobResult := &ProbeJobResult{
		VIPID:     job.VIPID,
		BackendID: job.BackendID,
		Result:    result,
		RiseCount: job.RiseCount,
		FallCount: job.FallCount,
	}

	select {
	case w.resultCh <- jobResult:
		// Result sent successfully
	default:
		// Result channel full - log warning
		w.logger.WithFields(logrus.Fields{
			"worker_id":  w.id,
			"vip_id":     job.VIPID,
			"backend_id": job.BackendID,
		}).Warn("Result channel full, dropping health check result")
	}
}
