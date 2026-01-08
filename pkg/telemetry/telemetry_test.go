package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestSetup_TracingDisabled(t *testing.T) {
	cfg := Config{
		ServiceName:     "test-service",
		ServiceVersion:  "1.0.0",
		Environment:     "test",
		TracingEnabled:  false,
		LogLevel:        "info",
		LogFormat:       "json",
	}

	provider, err := Setup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer provider.Shutdown(context.Background())

	if provider == nil {
		t.Fatal("Setup() returned nil provider")
	}

	if provider.Logger() == nil {
		t.Error("Logger() returned nil")
	}

	if provider.tracerProvider != nil {
		t.Error("tracerProvider should be nil when tracing is disabled")
	}
}

func TestProvider_Logger(t *testing.T) {
	tests := []struct {
		name      string
		logLevel  string
		logFormat string
	}{
		{"debug level json", "debug", "json"},
		{"info level json", "info", "json"},
		{"warn level json", "warn", "json"},
		{"error level json", "error", "json"},
		{"info level text", "info", "text"},
		{"unknown level", "unknown", "json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Environment:    "test",
				TracingEnabled: false,
				LogLevel:       tt.logLevel,
				LogFormat:      tt.logFormat,
			}

			provider, err := Setup(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Setup() error = %v", err)
			}
			defer provider.Shutdown(context.Background())

			logger := provider.Logger()
			if logger == nil {
				t.Fatal("Logger() returned nil")
			}
		})
	}
}

func TestProvider_Tracer(t *testing.T) {
	cfg := Config{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Environment:    "test",
		TracingEnabled: false, // Don't need actual OTLP endpoint for this test
		LogLevel:       "info",
		LogFormat:      "json",
	}

	provider, err := Setup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer provider.Shutdown(context.Background())

	tracer := provider.Tracer("test-tracer")
	if tracer == nil {
		t.Fatal("Tracer() returned nil")
	}
}

func TestProvider_Shutdown(t *testing.T) {
	t.Run("with tracing disabled", func(t *testing.T) {
		cfg := Config{
			ServiceName:    "test-service",
			ServiceVersion: "1.0.0",
			Environment:    "test",
			TracingEnabled: false,
			LogLevel:       "info",
			LogFormat:      "json",
		}

		provider, err := Setup(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Setup() error = %v", err)
		}

		err = provider.Shutdown(context.Background())
		if err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})

	t.Run("nil tracer provider", func(t *testing.T) {
		provider := &Provider{
			tracerProvider: nil,
		}

		err := provider.Shutdown(context.Background())
		if err != nil {
			t.Errorf("Shutdown() with nil tracerProvider error = %v", err)
		}
	})
}

func TestSpanFromContext(t *testing.T) {
	ctx := context.Background()
	span := SpanFromContext(ctx)

	// Should return a no-op span for empty context
	if span == nil {
		t.Fatal("SpanFromContext() returned nil")
	}

	// The span should not be recording (no-op span)
	if span.IsRecording() {
		t.Error("expected non-recording span from empty context")
	}
}

func TestTraceIDFromContext(t *testing.T) {
	t.Run("empty context", func(t *testing.T) {
		ctx := context.Background()
		traceID := TraceIDFromContext(ctx)

		if traceID != "" {
			t.Errorf("TraceIDFromContext() = %v, want empty string", traceID)
		}
	})

	t.Run("context with invalid span", func(t *testing.T) {
		// Create a context with a span that has no valid trace ID
		ctx := context.Background()
		span := trace.SpanFromContext(ctx)
		ctx = trace.ContextWithSpan(ctx, span)

		traceID := TraceIDFromContext(ctx)
		if traceID != "" {
			t.Errorf("TraceIDFromContext() = %v, want empty string", traceID)
		}
	})
}

func TestSetupLogger_Levels(t *testing.T) {
	tests := []struct {
		level string
	}{
		{"debug"},
		{"info"},
		{"warn"},
		{"error"},
		{"invalid"},
		{""},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			cfg := Config{
				ServiceName:    "test",
				ServiceVersion: "1.0",
				Environment:    "test",
				LogLevel:       tt.level,
				LogFormat:      "json",
			}

			logger := setupLogger(cfg)
			if logger == nil {
				t.Fatal("setupLogger() returned nil")
			}
		})
	}
}

func TestSetupLogger_Formats(t *testing.T) {
	tests := []struct {
		format string
	}{
		{"json"},
		{"text"},
		{"invalid"},
		{""},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			cfg := Config{
				ServiceName:    "test",
				ServiceVersion: "1.0",
				Environment:    "test",
				LogLevel:       "info",
				LogFormat:      tt.format,
			}

			logger := setupLogger(cfg)
			if logger == nil {
				t.Fatal("setupLogger() returned nil")
			}
		})
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		ServiceName:     "my-service",
		ServiceVersion:  "2.0.0",
		Environment:     "production",
		OTLPEndpoint:    "localhost:4317",
		TracingEnabled:  true,
		TracingSampling: 0.5,
		LogLevel:        "debug",
		LogFormat:       "json",
	}

	if cfg.ServiceName != "my-service" {
		t.Errorf("ServiceName = %v, want %v", cfg.ServiceName, "my-service")
	}
	if cfg.ServiceVersion != "2.0.0" {
		t.Errorf("ServiceVersion = %v, want %v", cfg.ServiceVersion, "2.0.0")
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment = %v, want %v", cfg.Environment, "production")
	}
	if cfg.OTLPEndpoint != "localhost:4317" {
		t.Errorf("OTLPEndpoint = %v, want %v", cfg.OTLPEndpoint, "localhost:4317")
	}
	if !cfg.TracingEnabled {
		t.Error("TracingEnabled = false, want true")
	}
	if cfg.TracingSampling != 0.5 {
		t.Errorf("TracingSampling = %v, want %v", cfg.TracingSampling, 0.5)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, "debug")
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %v, want %v", cfg.LogFormat, "json")
	}
}
