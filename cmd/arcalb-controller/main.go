package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	_ "github.com/akam1o/arca-lb/internal/common/datastore/etcd" // Register etcd datastore
	"github.com/akam1o/arca-lb/internal/controller/api"
	"github.com/akam1o/arca-lb/internal/controller/config"
	grpcserver "github.com/akam1o/arca-lb/internal/controller/grpc"
	"github.com/sirupsen/logrus"
)

var (
	configPath          = flag.String("config", "deploy/config/controller.yaml", "Path to configuration file")
	version             = "dev" // Set by build flags
	grpcShutdownTimeout = 5 * time.Second
)

func main() {
	flag.Parse()

	// Initialize logger
	logger := logrus.New()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		logger.WithError(err).Fatal("Failed to load configuration")
	}

	// Configure logger based on config
	configureLogger(logger, cfg)

	logger.WithFields(logrus.Fields{
		"version": version,
		"config":  *configPath,
	}).Info("Starting arca-lb controller")

	// Initialize datastore
	ctx := context.Background()
	ds, err := datastore.NewDataStore(ctx, cfg.ToDataStoreConfig())
	if err != nil {
		logger.WithError(err).Fatal("Failed to initialize datastore")
	}
	defer func() {
		if err := ds.Close(); err != nil {
			logger.WithError(err).Error("Failed to close datastore")
		}
	}()

	logger.WithField("type", cfg.DataStore.Type).Info("Datastore initialized")

	// Create REST API server
	apiServer := api.NewServer(cfg, ds, logger)

	// Create gRPC server
	grpcSrv := grpcserver.NewServer(cfg, ds, logger)

	// Start REST API server in a goroutine
	go func() {
		if err := apiServer.Start(); err != nil {
			logger.WithError(err).Fatal("REST API server stopped with error")
		}
	}()

	// Start gRPC server in a goroutine
	go func() {
		if err := grpcSrv.Start(); err != nil {
			logger.WithError(err).Fatal("gRPC server stopped with error")
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("Received shutdown signal")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop gRPC server
	grpcShutdownCtx, grpcShutdownCancel := context.WithTimeout(shutdownCtx, grpcShutdownTimeout)
	if err := grpcSrv.Stop(grpcShutdownCtx); err != nil {
		logger.WithError(err).Warn("Failed to shutdown gRPC server gracefully")
	}
	grpcShutdownCancel()

	// Shutdown REST API server
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		logger.WithError(err).Error("Failed to shutdown REST API server gracefully")
	}

	logger.Info("Controller stopped")
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

	logger.SetOutput(os.Stdout)
}
