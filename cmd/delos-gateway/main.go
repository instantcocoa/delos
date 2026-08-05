// Command delos-gateway is the Delos data plane: a stateless LLM gateway that
// serves the OpenAI-compatible and Anthropic-compatible HTTP surfaces and
// forwards to configured providers. It runs with zero knowledge of the Delos
// control plane.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/pkg/database"
	"github.com/instantcocoa/delos/pkg/telemetry"
	"github.com/instantcocoa/delos/services/runtime"
)

const (
	serviceName = "delos-gateway"
	defaultPort = 8080
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// env returns the first set environment variable among names. Standard
// provider variable names (OPENAI_API_KEY) take priority so that a bare
// `docker run -e OPENAI_API_KEY=...` works; DELOS_RUNTIME_* names are kept
// for compatibility.
func env(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Configuration: environment variable > delos.yaml > default. The file is
	// optional; provider credentials are always environment-only.
	cfg, err := config.Load(serviceName)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	port := cfg.GatewayPort
	if port == 0 {
		port = defaultPort
	}

	// Tracing: export OTLP wherever the endpoint points (typically the
	// control plane's port, but any OTLP backend works - the gateway has zero
	// knowledge of the control plane). telemetry.stdout prints spans to
	// stdout for dev.
	otlpEndpoint := cfg.OTLPEndpoint
	otlpProtocol := cfg.OTLPProtocol
	if otlpProtocol == "" {
		otlpProtocol = "http"
	}
	stdoutTraces := cfg.TraceStdout
	tp, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:     serviceName,
		ServiceVersion:  cfg.Version,
		Environment:     cfg.Environment,
		OTLPEndpoint:    otlpEndpoint,
		OTLPProtocol:    otlpProtocol,
		StdoutTraces:    stdoutTraces,
		TracingEnabled:  otlpEndpoint != "" || stdoutTraces,
		TracingSampling: cfg.TracingSampling,
		LogLevel:        cfg.LogLevel,
		LogFormat:       cfg.LogFormat,
	})
	if err != nil {
		return fmt.Errorf("failed to setup telemetry: %w", err)
	}
	defer tp.Shutdown(context.Background())
	logger := tp.Logger()

	registry := runtime.NewRegistry()

	if key := env("OPENAI_API_KEY", "DELOS_RUNTIME_OPENAI_KEY"); key != "" {
		registry.Register(runtime.NewOpenAIProvider(key))
		logger.Info("registered OpenAI provider")
	}
	if key := env("ANTHROPIC_API_KEY", "DELOS_RUNTIME_ANTHROPIC_KEY"); key != "" {
		registry.Register(runtime.NewAnthropicProvider(key))
		logger.Info("registered Anthropic provider")
	}
	if key := env("GEMINI_API_KEY", "GOOGLE_API_KEY", "DELOS_RUNTIME_GEMINI_KEY"); key != "" {
		registry.Register(runtime.NewGeminiProvider(key))
		logger.Info("registered Gemini provider")
	}
	if ak := os.Getenv("AWS_ACCESS_KEY_ID"); ak != "" || os.Getenv("AWS_PROFILE") != "" {
		region := os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
		}
		p, err := runtime.NewBedrockProvider(ctx, region)
		if err != nil {
			logger.Warn("could not configure AWS Bedrock provider", "error", err)
		} else {
			registry.Register(p)
			logger.Info("registered AWS Bedrock provider", "region", region)
		}
	}
	// Ollama is served through its OpenAI-compatible endpoint.
	if url, enabled := os.Getenv("DELOS_RUNTIME_OLLAMA_URL"), os.Getenv("DELOS_RUNTIME_OLLAMA_ENABLED"); enabled == "true" || url != "" {
		if url == "" {
			url = "http://localhost:11434"
		}
		registry.Register(runtime.NewOpenAICompatProvider("ollama", strings.TrimSuffix(url, "/")+"/v1", ""))
		logger.Info("registered Ollama provider (OpenAI-compatible)", "url", url)
	}
	// Any other OpenAI-compatible endpoint (vLLM, SGLang, Together,
	// OpenRouter, ...): DELOS_COMPAT_NAME / DELOS_COMPAT_BASE_URL / DELOS_COMPAT_API_KEY.
	if baseURL := os.Getenv("DELOS_COMPAT_BASE_URL"); baseURL != "" {
		name := os.Getenv("DELOS_COMPAT_NAME")
		if name == "" {
			name = "compat"
		}
		registry.Register(runtime.NewOpenAICompatProvider(name, baseURL, os.Getenv("DELOS_COMPAT_API_KEY")))
		logger.Info("registered OpenAI-compatible provider", "name", name, "base_url", baseURL)
	}

	if len(registry.List()) == 0 {
		logger.Warn("no LLM providers configured - set OPENAI_API_KEY, ANTHROPIC_API_KEY, GEMINI_API_KEY, AWS credentials, DELOS_RUNTIME_OLLAMA_ENABLED=true, or DELOS_COMPAT_BASE_URL")
	}

	svc := runtime.NewRuntimeService(registry, logger)

	// gen_ai.* span emission. Content capture is off by default; redaction
	// rules apply before export when it is on.
	if otlpEndpoint != "" || stdoutTraces {
		var redactor *runtime.Redactor
		if rules := cfg.TraceRedact; rules != "" {
			redactor, err = runtime.ParseRedactionRules(rules)
			if err != nil {
				return fmt.Errorf("invalid DELOS_TRACE_REDACT: %w", err)
			}
		}
		captureContent := cfg.TraceContent
		svc.SetTracer(runtime.NewGatewayTracer(captureContent, redactor))
		logger.Info("gen_ai span emission enabled",
			"otlp_endpoint", otlpEndpoint, "protocol", otlpProtocol,
			"stdout", stdoutTraces, "content_capture", captureContent)
	}

	// Model aliases with fallback chains. delos.yaml's gateway.routes, or
	// DELOS_ROUTES='{"gpt-4":["openai/gpt-4o","anthropic/claude-sonnet-4-5"]}'
	// which takes precedence.
	if raw := cfg.RoutesJSON; raw != "" {
		routes, err := runtime.ParseRoutes(raw)
		if err != nil {
			return fmt.Errorf("invalid DELOS_ROUTES: %w", err)
		}
		svc.SetRoutes(routes)
		logger.Info("configured model routes", "aliases", len(routes), "source", "DELOS_ROUTES")
	} else if len(cfg.Routes) > 0 {
		routes, err := runtime.RoutesFromMap(cfg.Routes)
		if err != nil {
			return fmt.Errorf("invalid gateway.routes in %s: %w", cfg.ConfigPath, err)
		}
		svc.SetRoutes(routes)
		logger.Info("configured model routes", "aliases", len(routes), "source", cfg.ConfigPath)
	}

	// Virtual keys: enforced when Postgres is configured; dev mode otherwise.
	var apiOpts []runtime.HTTPServerOption
	if cfg.UsePostgresStorage() {
		db, err := database.Connect(ctx, &database.Config{
			Host:            cfg.DBHost,
			Port:            cfg.DBPort,
			User:            cfg.DBUser,
			Password:        cfg.DBPassword,
			Database:        cfg.DBName,
			SSLMode:         cfg.DBSSLMode,
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: 5 * time.Minute,
			ConnMaxIdleTime: time.Minute,
		})
		if err != nil {
			return fmt.Errorf("failed to connect to database: %w", err)
		}
		defer db.Close()
		migrator := database.NewMigrator(db, "gateway").WithLogger(logger)
		if err := migrator.LoadMigrations(runtime.Migrations, "migrations"); err != nil {
			return fmt.Errorf("failed to load gateway migrations: %w", err)
		}
		if err := migrator.Up(ctx); err != nil {
			return fmt.Errorf("failed to apply gateway migrations: %w", err)
		}
		apiOpts = append(apiOpts, runtime.WithKeyStore(runtime.NewPostgresKeyStore(db.DB)))
		logger.Info("virtual keys enforced (postgres)")
	} else {
		logger.Info("dev mode: no database configured, virtual keys are NOT enforced - any api_key (e.g. \"delos-dev\") is accepted")
	}

	// Response cache: exact-match, Redis when configured, in-process LRU
	// otherwise. DELOS_CACHE=off (or gateway.cache.enabled: false) disables
	// caching entirely.
	if cfg.CacheEnabled {
		cacheTTL := cfg.CacheTTL
		if url := cfg.CacheRedisURL; url != "" {
			cache, err := runtime.NewRedisCache(url)
			if err != nil {
				return fmt.Errorf("invalid DELOS_REDIS_URL: %w", err)
			}
			defer cache.Close()
			if err := cache.Ping(ctx); err != nil {
				logger.Warn("redis unreachable, falling back to in-process cache", "error", err)
				svc.SetCache(runtime.NewLRUCache(0), cacheTTL)
			} else {
				svc.SetCache(cache, cacheTTL)
				logger.Info("response cache enabled (redis)", "ttl", cacheTTL)
			}
		} else {
			svc.SetCache(runtime.NewLRUCache(0), cacheTTL)
			logger.Info("response cache enabled (in-process LRU)", "ttl", cacheTTL)
		}
	}

	if cfg.RequestTimeout > 0 {
		apiOpts = append(apiOpts, runtime.WithRequestTimeout(cfg.RequestTimeout))
	}

	// Live request stream behind GET /v1/events - what `delos tail` renders.
	// DELOS_TAIL=off turns it off; DELOS_TAIL_BUFFER sizes the replay ring.
	if !strings.EqualFold(os.Getenv("DELOS_TAIL"), "off") {
		ring := 200
		if v := os.Getenv("DELOS_TAIL_BUFFER"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				ring = n
			}
		}
		apiOpts = append(apiOpts, runtime.WithTailBroadcast(runtime.NewTailBroadcaster(ring)))
	}

	api := runtime.NewHTTPServer(svc, logger, apiOpts...)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           api,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting delos-gateway",
			"port", port,
			"env", cfg.Environment,
			"providers", len(registry.List()),
			"config_file", orNone(cfg.ConfigPath),
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger.Info("shutting down delos-gateway")
	return server.Shutdown(shutdownCtx)
}

// orNone renders an unset path as "(none)" in logs.
func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
