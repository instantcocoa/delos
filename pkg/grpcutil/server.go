// Package grpcutil provides gRPC server utilities and interceptors.
package grpcutil

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// ServerConfig holds gRPC server configuration.
type ServerConfig struct {
	Port               int
	ServiceName        string
	EnableReflection   bool
	EnableHealthCheck  bool
	ShutdownTimeout    time.Duration
	MaxRecvMsgSize     int
	MaxSendMsgSize     int
	UnaryInterceptors  []grpc.UnaryServerInterceptor
	StreamInterceptors []grpc.StreamServerInterceptor
}

// DefaultServerConfig returns sensible defaults.
func DefaultServerConfig(port int, serviceName string) ServerConfig {
	return ServerConfig{
		Port:              port,
		ServiceName:       serviceName,
		EnableReflection:  true,
		EnableHealthCheck: true,
		ShutdownTimeout:   30 * time.Second,
		MaxRecvMsgSize:    16 * 1024 * 1024, // 16MB
		MaxSendMsgSize:    16 * 1024 * 1024, // 16MB
	}
}

// Server wraps a gRPC server with lifecycle management.
type Server struct {
	grpcServer   *grpc.Server
	healthServer *health.Server
	config       ServerConfig
	logger       *slog.Logger
}

// NewServer creates a new gRPC server.
func NewServer(cfg ServerConfig, logger *slog.Logger) *Server {
	// Build interceptor chains
	unaryInterceptors := append(
		[]grpc.UnaryServerInterceptor{
			LoggingUnaryInterceptor(logger),
			RecoveryUnaryInterceptor(logger),
		},
		cfg.UnaryInterceptors...,
	)

	streamInterceptors := append(
		[]grpc.StreamServerInterceptor{
			LoggingStreamInterceptor(logger),
			RecoveryStreamInterceptor(logger),
		},
		cfg.StreamInterceptors...,
	)

	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(cfg.MaxRecvMsgSize),
		grpc.MaxSendMsgSize(cfg.MaxSendMsgSize),
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	}

	grpcServer := grpc.NewServer(opts...)

	s := &Server{
		grpcServer: grpcServer,
		config:     cfg,
		logger:     logger,
	}

	// Enable reflection for development
	if cfg.EnableReflection {
		reflection.Register(grpcServer)
	}

	// Enable health checks
	if cfg.EnableHealthCheck {
		s.healthServer = health.NewServer()
		grpc_health_v1.RegisterHealthServer(grpcServer, s.healthServer)
		s.healthServer.SetServingStatus(cfg.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	}

	return s
}

// GRPCServer returns the underlying gRPC server for service registration.
func (s *Server) GRPCServer() *grpc.Server {
	return s.grpcServer
}

// SetServingStatus sets the health check status.
func (s *Server) SetServingStatus(status grpc_health_v1.HealthCheckResponse_ServingStatus) {
	if s.healthServer != nil {
		s.healthServer.SetServingStatus(s.config.ServiceName, status)
	}
}

// Run starts the server and blocks until shutdown.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	// Handle graceful shutdown
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("gRPC server starting", "addr", addr, "service", s.config.ServiceName)
		if err := s.grpcServer.Serve(lis); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		s.logger.Info("context cancelled, shutting down")
	case sig := <-shutdownCh:
		s.logger.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		return err
	}

	return s.shutdown()
}

func (s *Server) shutdown() error {
	s.logger.Info("initiating graceful shutdown", "timeout", s.config.ShutdownTimeout)

	// Mark as not serving
	s.SetServingStatus(grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	// Graceful stop with timeout
	done := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("graceful shutdown completed")
	case <-ctx.Done():
		s.logger.Warn("graceful shutdown timed out, forcing stop")
		s.grpcServer.Stop()
	}

	return nil
}
