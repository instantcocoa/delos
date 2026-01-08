package grpcutil

import (
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestDefaultServerConfig(t *testing.T) {
	cfg := DefaultServerConfig(9001, "test-service")

	if cfg.Port != 9001 {
		t.Errorf("Port = %v, want %v", cfg.Port, 9001)
	}
	if cfg.ServiceName != "test-service" {
		t.Errorf("ServiceName = %v, want %v", cfg.ServiceName, "test-service")
	}
	if !cfg.EnableReflection {
		t.Error("EnableReflection = false, want true")
	}
	if !cfg.EnableHealthCheck {
		t.Error("EnableHealthCheck = false, want true")
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 30*time.Second)
	}
	if cfg.MaxRecvMsgSize != 16*1024*1024 {
		t.Errorf("MaxRecvMsgSize = %v, want %v", cfg.MaxRecvMsgSize, 16*1024*1024)
	}
	if cfg.MaxSendMsgSize != 16*1024*1024 {
		t.Errorf("MaxSendMsgSize = %v, want %v", cfg.MaxSendMsgSize, 16*1024*1024)
	}
}

func TestNewServer(t *testing.T) {
	logger := slog.Default()
	cfg := DefaultServerConfig(9002, "test-service")

	server := NewServer(cfg, logger)

	if server == nil {
		t.Fatal("NewServer() returned nil")
	}
	if server.grpcServer == nil {
		t.Error("grpcServer is nil")
	}
	if server.healthServer == nil {
		t.Error("healthServer is nil (should be enabled by default)")
	}
	if server.config.Port != cfg.Port {
		t.Errorf("config.Port = %v, want %v", server.config.Port, cfg.Port)
	}
}

func TestNewServerWithoutHealthCheck(t *testing.T) {
	logger := slog.Default()
	cfg := DefaultServerConfig(9003, "test-service")
	cfg.EnableHealthCheck = false

	server := NewServer(cfg, logger)

	if server == nil {
		t.Fatal("NewServer() returned nil")
	}
	if server.healthServer != nil {
		t.Error("healthServer should be nil when EnableHealthCheck is false")
	}
}

func TestNewServerWithoutReflection(t *testing.T) {
	logger := slog.Default()
	cfg := DefaultServerConfig(9004, "test-service")
	cfg.EnableReflection = false

	server := NewServer(cfg, logger)

	if server == nil {
		t.Fatal("NewServer() returned nil")
	}
	// Reflection registration is internal, but server should still work
	if server.grpcServer == nil {
		t.Error("grpcServer is nil")
	}
}

func TestServer_GRPCServer(t *testing.T) {
	logger := slog.Default()
	cfg := DefaultServerConfig(9005, "test-service")

	server := NewServer(cfg, logger)
	grpcServer := server.GRPCServer()

	if grpcServer == nil {
		t.Fatal("GRPCServer() returned nil")
	}
	if grpcServer != server.grpcServer {
		t.Error("GRPCServer() did not return the internal gRPC server")
	}
}

func TestServer_SetServingStatus(t *testing.T) {
	logger := slog.Default()
	cfg := DefaultServerConfig(9006, "test-service")

	server := NewServer(cfg, logger)

	// Should not panic even when called multiple times
	server.SetServingStatus(grpc_health_v1.HealthCheckResponse_SERVING)
	server.SetServingStatus(grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	server.SetServingStatus(grpc_health_v1.HealthCheckResponse_SERVING)
}

func TestServer_SetServingStatusWithoutHealthServer(t *testing.T) {
	logger := slog.Default()
	cfg := DefaultServerConfig(9007, "test-service")
	cfg.EnableHealthCheck = false

	server := NewServer(cfg, logger)

	// Should not panic when health server is nil
	server.SetServingStatus(grpc_health_v1.HealthCheckResponse_SERVING)
	server.SetServingStatus(grpc_health_v1.HealthCheckResponse_NOT_SERVING)
}

func TestNewServerWithCustomInterceptors(t *testing.T) {
	logger := slog.Default()
	cfg := DefaultServerConfig(9008, "test-service")

	// Add custom interceptors
	cfg.UnaryInterceptors = []grpc.UnaryServerInterceptor{
		TimeoutUnaryInterceptor(5 * time.Second),
	}

	server := NewServer(cfg, logger)

	if server == nil {
		t.Fatal("NewServer() returned nil")
	}
	if server.grpcServer == nil {
		t.Error("grpcServer is nil")
	}
}
