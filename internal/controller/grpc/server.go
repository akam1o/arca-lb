package grpc

import (
	"fmt"
	"net"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/controller/config"
	pb "github.com/akam1o/arca-lb/pkg/grpc"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// Server represents the gRPC server
type Server struct {
	config     *config.Config
	grpcServer *grpc.Server
	datastore  datastore.DataStore
	logger     *logrus.Logger
	listener   net.Listener
}

// NewServer creates a new gRPC server instance
func NewServer(cfg *config.Config, ds datastore.DataStore, logger *logrus.Logger) *Server {
	server := &Server{
		config:    cfg,
		datastore: ds,
		logger:    logger,
	}

	// Create gRPC server
	server.grpcServer = grpc.NewServer()

	// Register ConfigSync service
	configSyncService := NewConfigSyncService(ds, logger)
	pb.RegisterConfigSyncServer(server.grpcServer, configSyncService)

	return server
}

// Start starts the gRPC server
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.config.GRPC.Host, s.config.GRPC.Port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = listener

	s.logger.WithField("addr", addr).Info("Starting gRPC server")

	if err := s.grpcServer.Serve(listener); err != nil {
		return fmt.Errorf("failed to serve gRPC: %w", err)
	}

	return nil
}

// Stop gracefully stops the gRPC server
func (s *Server) Stop() {
	s.logger.Info("Stopping gRPC server")
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}
}
