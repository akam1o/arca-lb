package metrics

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/akam1o/arca-lb/internal/agent/config"
	"github.com/sirupsen/logrus"
)

// Server exposes metrics and health endpoints.
type Server struct {
	cfg     *config.MetricsConfig
	handler http.Handler
	logger  *logrus.Logger

	mu         sync.Mutex
	httpServer *http.Server
	started    bool
}

// NewServer creates a new metrics server with the provided configuration and handler.
func NewServer(cfg *config.MetricsConfig, handler http.Handler, logger *logrus.Logger) *Server {
	if cfg == nil {
		cfg = &config.MetricsConfig{}
	}
	if logger == nil {
		logger = logrus.New()
	}

	return &Server{
		cfg:     cfg,
		handler: handler,
		logger:  logger,
	}
}

// Start starts the metrics HTTP server.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return errors.New("metrics server already started")
	}

	if s.cfg == nil {
		s.logger.Warn("Metrics configuration not provided, skipping metrics server start")
		return nil
	}

	if !s.cfg.Enabled {
		s.logger.WithField("enabled", false).Info("Metrics server disabled, skipping start")
		return nil
	}

	mux := http.NewServeMux()

	metricsPath := s.cfg.Path
	if metricsPath == "" {
		metricsPath = "/metrics"
	}

	if s.handler == nil {
		s.logger.WithField("metrics_path", metricsPath).Warn("Metrics handler not provided, endpoint will return 404")
		mux.Handle(metricsPath, http.NotFoundHandler())
	} else {
		mux.Handle(metricsPath, s.handler)
	}

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	})

	serverTimeout := s.cfg.Timeout
	if serverTimeout <= 0 {
		serverTimeout = 10 * time.Second
	}

	s.httpServer = &http.Server{
		Addr:              s.cfg.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: serverTimeout,
		ReadTimeout:       serverTimeout,
		WriteTimeout:      serverTimeout,
		IdleTimeout:       serverTimeout,
	}

	listener, err := net.Listen("tcp", s.cfg.ListenAddress)
	if err != nil {
		s.logger.WithFields(logrus.Fields{
			"listen_address": s.cfg.ListenAddress,
			"error":          err,
		}).Error("Failed to bind metrics server listener")
		return err
	}

	s.started = true

	logger := s.logger.WithFields(logrus.Fields{
		"listen_address": s.cfg.ListenAddress,
		"metrics_path":   metricsPath,
	})
	logger.Info("Starting metrics server")

	go func() {
		if serveErr := s.httpServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.WithError(serveErr).Error("Metrics server exited with error")
		} else {
			logger.Debug("Metrics server stopped")
		}

		s.mu.Lock()
		s.started = false
		s.httpServer = nil
		s.mu.Unlock()
	}()

	return nil
}

// Stop gracefully stops the metrics HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		s.logger.Debug("Metrics server not started, skipping stop")
		return nil
	}

	server := s.httpServer
	timeout := s.cfg.Timeout
	s.mu.Unlock()

	if server == nil {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	if cancel != nil {
		defer cancel()
	}

	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.logger.WithError(err).Warn("Metrics server shutdown encountered error")
		return err
	}

	s.mu.Lock()
	s.started = false
	s.httpServer = nil
	s.mu.Unlock()

	s.logger.Info("Metrics server stopped")
	return nil
}
