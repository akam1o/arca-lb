package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/akam1o/arca-lb/internal/common/datastore"
	"github.com/akam1o/arca-lb/internal/controller/config"
	pb "github.com/akam1o/arca-lb/pkg/grpc"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
	return &Server{
		config:    cfg,
		datastore: ds,
		logger:    logger,
	}
}

// Start starts the gRPC server
func (s *Server) Start() error {
	if err := s.initializeGRPCServer(); err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", s.config.GRPC.Host, s.config.GRPC.Port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = listener

	s.logger.WithField("addr", addr).Info("Starting gRPC server")

	if err := s.grpcServer.Serve(listener); err != nil {
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return fmt.Errorf("failed to serve gRPC: %w", err)
	}

	return nil
}

func (s *Server) initializeGRPCServer() error {
	opts, err := s.grpcServerOptions()
	if err != nil {
		return err
	}

	s.grpcServer = grpc.NewServer(opts...)

	configSyncService := NewConfigSyncService(s.datastore, s.logger)
	pb.RegisterConfigSyncServer(s.grpcServer, configSyncService)
	return nil
}

func (s *Server) grpcServerOptions() ([]grpc.ServerOption, error) {
	opts := make([]grpc.ServerOption, 0, 3)

	if s.config.GRPC.APIKey != "" {
		opts = append(opts,
			grpc.UnaryInterceptor(apiKeyUnaryServerInterceptor(s.config.GRPC.APIKey)),
			grpc.StreamInterceptor(apiKeyStreamServerInterceptor(s.config.GRPC.APIKey)),
		)
	}

	if !s.config.GRPC.TLS {
		return opts, nil
	}

	tlsConfig, err := s.loadTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load gRPC TLS config: %w", err)
	}

	opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	return opts, nil
}

func (s *Server) loadTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(s.config.GRPC.CertFile, s.config.GRPC.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if s.config.GRPC.RequireClientCert && s.config.GRPC.ClientCAFile == "" {
		return nil, fmt.Errorf("grpc.client_ca_file is required when grpc.require_client_cert is enabled")
	}

	if s.config.GRPC.ClientCAFile != "" {
		caData, err := os.ReadFile(s.config.GRPC.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read client CA file: %w", err)
		}
		clientCAPool := x509.NewCertPool()
		if !clientCAPool.AppendCertsFromPEM(caData) {
			return nil, fmt.Errorf("failed to parse client CA file")
		}
		tlsConfig.ClientCAs = clientCAPool
		tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		if s.config.GRPC.RequireClientCert {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}

	return tlsConfig, nil
}

// Stop gracefully stops the gRPC server, falling back to a forceful stop when
// active RPCs do not drain before ctx expires.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Stopping gRPC server")
	if s.grpcServer == nil {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	stopped := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		s.logger.WithError(ctx.Err()).Warn("Timed out waiting for graceful gRPC shutdown; forcing stop")
		s.grpcServer.Stop()
		<-stopped
		return ctx.Err()
	}
}
