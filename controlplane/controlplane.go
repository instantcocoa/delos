// Package controlplane runs the Delos control plane: observe, prompt,
// datasets, eval, and deploy linked into one process behind one port. The
// modules stay separate Go packages; calls between them are direct function
// calls.
package controlplane

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
	"github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/pkg/database"
	"github.com/instantcocoa/delos/pkg/grpcutil"
	"github.com/instantcocoa/delos/pkg/telemetry"
	"github.com/instantcocoa/delos/services/datasets"
	"github.com/instantcocoa/delos/services/deploy"
	"github.com/instantcocoa/delos/services/eval"
	"github.com/instantcocoa/delos/services/observe"
	"github.com/instantcocoa/delos/services/prompt"
)

const (
	serviceName = "delos"
	defaultPort = 8081
)

// Run starts the control plane and blocks until the context is cancelled or
// the process receives SIGINT/SIGTERM.
func Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(serviceName)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	// Port comes from DELOS_PORT, else delos.yaml's `port`, else the default.
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}

	// The control plane hosts observe itself, so it does not export traces
	// over the network to avoid a self-loop.
	tp, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:    serviceName,
		ServiceVersion: cfg.Version,
		Environment:    cfg.Environment,
		OTLPEndpoint:   "",
		TracingEnabled: false,
		LogLevel:       cfg.LogLevel,
		LogFormat:      cfg.LogFormat,
	})
	if err != nil {
		return fmt.Errorf("failed to setup telemetry: %w", err)
	}
	defer tp.Shutdown(context.Background())
	logger := tp.Logger()

	// Storage: Postgres when configured, in-memory otherwise. Migrations run
	// at startup so a fresh database is usable immediately.
	var db *database.DB
	if cfg.UsePostgresStorage() {
		db, err = database.Connect(ctx, &database.Config{
			Host:            cfg.DBHost,
			Port:            cfg.DBPort,
			User:            cfg.DBUser,
			Password:        cfg.DBPassword,
			Database:        cfg.DBName,
			SSLMode:         cfg.DBSSLMode,
			MaxOpenConns:    25,
			MaxIdleConns:    5,
			ConnMaxLifetime: 5 * time.Minute,
			ConnMaxIdleTime: time.Minute,
		})
		if err != nil {
			return fmt.Errorf("failed to connect to database: %w", err)
		}
		defer db.Close()

		for _, m := range []struct {
			schema string
			fs     embed.FS
		}{
			{"prompt", prompt.Migrations},
			{"observe", observe.Migrations},
		} {
			migrator := database.NewMigrator(db, m.schema).WithLogger(logger)
			if err := migrator.LoadMigrations(m.fs, "migrations"); err != nil {
				return fmt.Errorf("failed to load %s migrations: %w", m.schema, err)
			}
			if err := migrator.Up(ctx); err != nil {
				return fmt.Errorf("failed to apply %s migrations: %w", m.schema, err)
			}
		}
		logger.Info("connected to postgres and applied migrations")
	}

	// ---- module construction (direct wiring, no network) ----

	var spanStore observe.SpanStore = observe.NewMemorySpanStore()
	var metricStore observe.MetricStore = observe.NewMemoryMetricStore()
	if db != nil {
		spanStore = observe.NewPostgresSpanStore(db.DB)
		metricStore = observe.NewPostgresMetricStore(db.DB)
	}
	observeHandler := observe.NewHandler(spanStore, metricStore, logger)

	promptStore, err := prompt.NewStore(prompt.StoreOptions{
		Backend: cfg.StorageBackend,
		DB:      sqlDB(db),
	})
	if err != nil {
		return fmt.Errorf("failed to create prompt store: %w", err)
	}
	promptHandler := prompt.NewHandler(promptStore, logger)

	datasetsStore := datasets.NewMemoryStore()
	datasetsService := datasets.NewDatasetsService(datasetsStore)
	datasetsHandler := datasets.NewHandler(logger, datasetsService)

	evalStore := eval.NewMemoryStore()
	evalService := eval.NewEvalService(evalStore)
	evalHandler := eval.NewHandler(logger, evalService)

	deployStore := deploy.NewMemoryStore()
	deployService := deploy.NewDeployService(deployStore, evalResults{store: evalStore})
	deployHandler := deploy.NewHandler(logger, deployService)

	// Eval runner: prompts and datasets are direct in-process calls; LLM
	// traffic goes through delos-gateway over its public OpenAI surface.
	gatewayURL := cfg.GatewayURL
	if gatewayURL == "" {
		gatewayURL = "http://localhost:8080"
	}
	runner := eval.NewRunner(
		logger,
		evalStore,
		eval.NewGatewayClient(gatewayURL, os.Getenv("DELOS_GATEWAY_API_KEY")),
		promptSource{h: promptHandler},
		exampleSource{h: datasetsHandler},
		eval.RunnerConfig{PollInterval: 5 * time.Second, Concurrency: 2},
	)
	runner.Start(ctx)
	defer runner.Stop()

	// ---- one port: gRPC (h2c) + HTTP on :8081 ----

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(16*1024*1024),
		grpc.MaxSendMsgSize(16*1024*1024),
		grpc.ChainUnaryInterceptor(
			grpcutil.RecoveryUnaryInterceptor(logger),
			grpcutil.LoggingUnaryInterceptor(logger),
		),
		grpc.ChainStreamInterceptor(
			grpcutil.RecoveryStreamInterceptor(logger),
			grpcutil.LoggingStreamInterceptor(logger),
		),
	)
	observeHandler.Register(grpcServer)
	promptHandler.Register(grpcServer)
	datasetsHandler.Register(grpcServer)
	evalHandler.Register(grpcServer)
	deployHandler.Register(grpcServer)

	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	for _, name := range []string{"", "observe", "prompt", "datasets", "eval", "deploy"} {
		healthServer.SetServingStatus(name, healthpb.HealthCheckResponse_SERVING)
	}
	reflection.Register(grpcServer)

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	// OTLP/HTTP trace ingest (protobuf and JSON) - accepts spans from any
	// gen_ai.* source, not just delos-gateway.
	httpMux.Handle("POST /v1/traces", observeHandler.OTLPHandler())
	// Quality gate verdicts for CI: GET /v1/gates/{gate}/verdict.
	httpMux.Handle("GET /v1/gates/{gate}/verdict", deployService.VerdictHandler())

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
			return
		}
		httpMux.ServeHTTP(w, r)
	})

	// gRPC clients speak HTTP/2 without TLS (h2c), so enable unencrypted
	// HTTP/2 alongside HTTP/1 on the shared port.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           root,
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting delos control plane",
			"port", port,
			"env", cfg.Environment,
			"storage", storageName(cfg),
			"gateway_url", gatewayURL,
		)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down delos control plane")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	grpcServer.GracefulStop()
	return server.Shutdown(shutdownCtx)
}

// sqlDB unwraps the database handle, tolerating the in-memory case.
func sqlDB(db *database.DB) *sql.DB {
	if db == nil {
		return nil
	}
	return db.DB
}

func storageName(cfg *config.Base) string {
	if cfg.UsePostgresStorage() {
		return "postgres"
	}
	return "memory"
}

// ---- in-process adapters for the eval runner ----

type promptSource struct{ h *prompt.Handler }

func (p promptSource) GetPrompt(ctx context.Context, id string) (*promptv1.Prompt, error) {
	resp, err := p.h.GetPrompt(ctx, &promptv1.GetPromptRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return resp.GetPrompt(), nil
}

type exampleSource struct{ h *datasets.Handler }

func (e exampleSource) GetExamples(ctx context.Context, datasetID string, limit int, shuffle bool) ([]*datasetsv1.Example, error) {
	resp, err := e.h.GetExamples(ctx, &datasetsv1.GetExamplesRequest{
		DatasetId: datasetID,
		Limit:     int32(limit),
		Shuffle:   shuffle,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetExamples(), nil
}

// evalResults adapts the eval store to deploy's EvalResults interface.
type evalResults struct{ store eval.Store }

func (e evalResults) LatestCompletedRun(ctx context.Context, promptID string) (*deploy.RunSummary, error) {
	run, err := eval.LatestCompletedRunForPrompt(ctx, e.store, promptID)
	if err != nil || run == nil {
		return nil, err
	}
	return &deploy.RunSummary{
		RunID:        run.RunID,
		PromptID:     run.PromptID,
		OverallScore: run.OverallScore,
		PassRate:     run.PassRate,
		AvgLatencyMs: run.AvgLatencyMs,
		TotalCostUSD: run.TotalCostUSD,
		CompletedAt:  run.CompletedAt,
	}, nil
}
