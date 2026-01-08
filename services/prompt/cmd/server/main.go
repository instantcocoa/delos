package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/lib/pq"

	"github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/pkg/grpcutil"
	"github.com/instantcocoa/delos/pkg/telemetry"
	"github.com/instantcocoa/delos/services/prompt"
)

const (
	serviceName = "prompt"
	defaultPort = 9002
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

	// Setup telemetry
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

	// Initialize database connection if using postgres
	var db *sql.DB
	if cfg.UsePostgresStorage() {
		var err error
		db, err = sql.Open("postgres", cfg.DatabaseDSN())
		if err != nil {
			return fmt.Errorf("failed to connect to database: %w", err)
		}
		defer db.Close()

		if err := db.PingContext(ctx); err != nil {
			return fmt.Errorf("failed to ping database: %w", err)
		}
		logger.Info("connected to postgres database")
	}

	// Initialize store
	store, err := prompt.NewStore(prompt.StoreOptions{
		Backend: cfg.StorageBackend,
		DB:      db,
	})
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}

	logger.Info("initialized storage backend", "backend", cfg.StorageBackend)

	// Create gRPC server
	serverCfg := grpcutil.DefaultServerConfig(cfg.GRPCPort, serviceName)
	server := grpcutil.NewServer(serverCfg, logger)

	// Register service handlers
	handler := prompt.NewHandler(store, logger)
	handler.Register(server.GRPCServer())

	logger.Info("starting prompt service",
		"port", cfg.GRPCPort,
		"env", cfg.Environment,
	)

	// Run server (blocks until shutdown)
	return server.Run(ctx)
}
