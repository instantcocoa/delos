package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
	runtimev1 "github.com/instantcocoa/delos/gen/go/runtime/v1"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	// Connect to dependent services
	runtimeConn, runtimeClient, err := connectToRuntime(cfg.RuntimeAddr)
	if err != nil {
		logger.Warn("failed to connect to runtime service - eval runner will not work", "error", err)
	} else {
		defer runtimeConn.Close()
	}

	promptConn, promptClient, err := connectToPrompt(cfg.PromptAddr)
	if err != nil {
		logger.Warn("failed to connect to prompt service - eval runner will not work", "error", err)
	} else {
		defer promptConn.Close()
	}

	datasetsConn, datasetsClient, err := connectToDatasets(cfg.DatasetsAddr)
	if err != nil {
		logger.Warn("failed to connect to datasets service - eval runner will not work", "error", err)
	} else {
		defer datasetsConn.Close()
	}

	// Start the eval runner if all clients connected
	var runner *eval.Runner
	if runtimeClient != nil && promptClient != nil && datasetsClient != nil {
		runner = eval.NewRunner(
			logger,
			store,
			runtimeClient,
			promptClient,
			datasetsClient,
			eval.RunnerConfig{
				PollInterval: 5 * time.Second,
				Concurrency:  2,
			},
		)
		runner.Start(ctx)
		defer runner.Stop()
		logger.Info("eval runner started")
	} else {
		logger.Warn("eval runner not started - missing service connections")
	}

	serverCfg := grpcutil.DefaultServerConfig(cfg.GRPCPort, serviceName)
	server := grpcutil.NewServer(serverCfg, logger)

	handler := eval.NewHandler(logger, svc)
	handler.Register(server.GRPCServer())

	logger.Info("starting eval service", "port", cfg.GRPCPort, "env", cfg.Environment)

	// Handle shutdown gracefully
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down...")
		cancel()
	}()

	return server.Run(ctx)
}

func connectToRuntime(addr string) (*grpc.ClientConn, runtimev1.RuntimeServiceClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to runtime at %s: %w", addr, err)
	}
	return conn, runtimev1.NewRuntimeServiceClient(conn), nil
}

func connectToPrompt(addr string) (*grpc.ClientConn, promptv1.PromptServiceClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to prompt at %s: %w", addr, err)
	}
	return conn, promptv1.NewPromptServiceClient(conn), nil
}

func connectToDatasets(addr string) (*grpc.ClientConn, datasetsv1.DatasetsServiceClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to datasets at %s: %w", addr, err)
	}
	return conn, datasetsv1.NewDatasetsServiceClient(conn), nil
}
