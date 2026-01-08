package main

import (
	"context"
	"fmt"
	"os"

	"github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/pkg/grpcutil"
	"github.com/instantcocoa/delos/pkg/telemetry"
	"github.com/instantcocoa/delos/services/eval"
)

const (
	serviceName = "eval"
	defaultPort = 9004
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	cfg, err := config.Load(serviceName)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cfg.GRPCPort = defaultPort

	tp, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:     serviceName,
		ServiceVersion:  cfg.Version,
		Environment:     cfg.Environment,
		OTLPEndpoint:    cfg.ObserveEndpoint,
		TracingEnabled:  cfg.TracingEnabled,
		TracingSampling: cfg.TracingSampling,
		LogLevel:        cfg.LogLevel,
		LogFormat:       cfg.LogFormat,
	})
	if err != nil {
		return fmt.Errorf("failed to setup telemetry: %w", err)
	}
	defer tp.Shutdown(ctx)

	logger := tp.Logger()

	// Initialize store and service
	store := eval.NewMemoryStore()
	svc := eval.NewEvalService(store)

	serverCfg := grpcutil.DefaultServerConfig(cfg.GRPCPort, serviceName)
	server := grpcutil.NewServer(serverCfg, logger)

	handler := eval.NewHandler(logger, svc)
	handler.Register(server.GRPCServer())

	logger.Info("starting eval service", "port", cfg.GRPCPort, "env", cfg.Environment)

	return server.Run(ctx)
}
