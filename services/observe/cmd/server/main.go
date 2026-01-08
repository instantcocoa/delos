package main

import (
	"context"
	"fmt"
	"os"

	"github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/pkg/grpcutil"
	"github.com/instantcocoa/delos/pkg/telemetry"
	"github.com/instantcocoa/delos/services/observe"
)

const (
	serviceName = "observe"
	defaultPort = 9000
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load(serviceName)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cfg.GRPCPort = defaultPort

	// Setup telemetry (but don't send to self to avoid loops)
	tp, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:     serviceName,
		ServiceVersion:  cfg.Version,
		Environment:     cfg.Environment,
		OTLPEndpoint:    "", // Disable tracing for observe service itself
		TracingEnabled:  false,
		TracingSampling: cfg.TracingSampling,
		LogLevel:        cfg.LogLevel,
		LogFormat:       cfg.LogFormat,
	})
	if err != nil {
		return fmt.Errorf("failed to setup telemetry: %w", err)
	}
	defer tp.Shutdown(ctx)

	logger := tp.Logger()

	// Initialize stores (using in-memory for now)
	spanStore := observe.NewMemorySpanStore()
	metricStore := observe.NewMemoryMetricStore()

	// Create gRPC server
	serverCfg := grpcutil.DefaultServerConfig(cfg.GRPCPort, serviceName)
	server := grpcutil.NewServer(serverCfg, logger)

	// Register service handlers
	handler := observe.NewHandler(spanStore, metricStore, logger)
	handler.Register(server.GRPCServer())

	logger.Info("starting observe service",
		"port", cfg.GRPCPort,
		"env", cfg.Environment,
	)

	// Run server (blocks until shutdown)
	return server.Run(ctx)
}
