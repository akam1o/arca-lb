package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	_ "github.com/akam1o/arca-lb/internal/common/datastore/etcd"  // Register etcd datastore
	_ "github.com/akam1o/arca-lb/internal/common/datastore/mysql" // Register mysql datastore
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
	os.Exit(runController(*configPath))
}

func runController(configPath string) int {
	// Initialize logger
	logger := logrus.New()

	// Load configuration
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.WithError(err).Error("Failed to load configuration")
		return 1
	}

	// Configure logger based on config
	configureLogger(logger, cfg)

	logger.WithFields(logrus.Fields{
		"version": version,
		"config":  configPath,
	}).Info("Starting arca-lb controller")

	// Initialize datastore
	ctx := context.Background()
	ds, err := datastore.NewDataStore(ctx, cfg.ToDataStoreConfig())
	if err != nil {
		logger.WithError(err).Error("Failed to initialize datastore")
		return 1
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

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	return runControllerServers(apiServer, grpcSrv, sigChan, logger)
}

type controllerAPIServer interface {
	Start() error
	Shutdown(context.Context) error
}

type controllerGRPCServer interface {
	Start() error
	Stop(context.Context) error
}

func runControllerServers(
	apiServer controllerAPIServer,
	grpcSrv controllerGRPCServer,
	sigChan <-chan os.Signal,
	logger *logrus.Logger,
) int {
	serverErrCh := make(chan error, 2)

	go func() {
		if err := apiServer.Start(); err != nil {
			serverErrCh <- fmt.Errorf("REST API server stopped with error: %w", err)
		}
	}()

	go func() {
		if err := grpcSrv.Start(); err != nil {
			serverErrCh <- fmt.Errorf("gRPC server stopped with error: %w", err)
		}
	}()

	exitCode := 0
	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
	case err := <-serverErrCh:
		logger.WithError(err).Error("Controller server stopped with error")
		exitCode = 1
	}

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
	return exitCode
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
