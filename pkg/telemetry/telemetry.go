// Package telemetry provides OpenTelemetry tracing and logging setup.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds telemetry configuration.
type Config struct {
	ServiceName     string
	ServiceVersion  string
	Environment     string
	OTLPEndpoint    string
	TracingEnabled  bool
	TracingSampling float64
	LogLevel        string
	LogFormat       string

	// OTLPProtocol selects the OTLP transport when OTLPEndpoint is set:
	// "grpc" (default) or "http" (OTLP/HTTP protobuf).
	OTLPProtocol string

	// StdoutTraces additionally writes spans to stdout. This is the
	// zero-config dev mode: traces are visible without any collector.
	StdoutTraces bool
}

// Provider manages telemetry resources.
type Provider struct {
	tracerProvider *sdktrace.TracerProvider
	logger         *slog.Logger
}

// Setup initializes OpenTelemetry tracing and logging.
func Setup(ctx context.Context, cfg Config) (*Provider, error) {
	p := &Provider{}

	// Setup logging
	p.logger = setupLogger(cfg)

	// Setup tracing
	if cfg.TracingEnabled {
		tp, err := setupTracing(ctx, cfg)
		if err != nil {
			return nil, err
		}
		p.tracerProvider = tp
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	return p, nil
}

// Shutdown gracefully shuts down telemetry providers.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.tracerProvider != nil {
		return p.tracerProvider.Shutdown(ctx)
	}
	return nil
}

// Logger returns the configured logger.
func (p *Provider) Logger() *slog.Logger {
	return p.logger
}

// Tracer returns a tracer for the given name.
func (p *Provider) Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

func setupLogger(cfg Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level == slog.LevelDebug,
	}

	var handler slog.Handler
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler).With(
		"service", cfg.ServiceName,
		"version", cfg.ServiceVersion,
		"env", cfg.Environment,
	)

	slog.SetDefault(logger)
	return logger
}

// newExporters builds the span exporters implied by cfg. It returns an empty
// slice when no exporter is configured, in which case the TracerProvider is
// still installed so spans can be collected in-process (tests, and any
// SpanProcessor added later).
func newExporters(ctx context.Context, cfg Config) ([]sdktrace.SpanExporter, error) {
	var exporters []sdktrace.SpanExporter
	if cfg.OTLPEndpoint != "" {
		switch cfg.OTLPProtocol {
		case "http":
			exp, err := otlptracehttp.New(ctx,
				otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
				otlptracehttp.WithInsecure(), // TODO: configure TLS for production
			)
			if err != nil {
				return nil, err
			}
			exporters = append(exporters, exp)
		case "", "grpc":
			exp, err := otlptracegrpc.New(ctx,
				otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
				otlptracegrpc.WithInsecure(), // TODO: configure TLS for production
			)
			if err != nil {
				return nil, err
			}
			exporters = append(exporters, exp)
		default:
			return nil, fmt.Errorf("unsupported OTLP protocol %q (want \"grpc\" or \"http\")", cfg.OTLPProtocol)
		}
	}
	if cfg.StdoutTraces {
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, err
		}
		exporters = append(exporters, exp)
	}
	return exporters, nil
}

func setupTracing(ctx context.Context, cfg Config) (*sdktrace.TracerProvider, error) {
	exporters, err := newExporters(ctx, cfg)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String(cfg.ServiceVersion),
			semconv.DeploymentEnvironmentKey.String(cfg.Environment),
		),
	)
	if err != nil {
		return nil, err
	}

	sampler := sdktrace.ParentBased(
		sdktrace.TraceIDRatioBased(cfg.TracingSampling),
	)

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}
	for _, exp := range exporters {
		opts = append(opts, sdktrace.WithBatcher(exp,
			sdktrace.WithBatchTimeout(5*time.Second),
		))
	}

	return sdktrace.NewTracerProvider(opts...), nil
}

// SpanFromContext returns the span from the context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// TraceIDFromContext returns the trace ID from the context.
func TraceIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasTraceID() {
		return span.SpanContext().TraceID().String()
	}
	return ""
}
