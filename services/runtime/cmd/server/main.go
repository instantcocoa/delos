package main

import (
	"context"
	"fmt"
	"os"

	"github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/pkg/grpcutil"
	"github.com/instantcocoa/delos/pkg/telemetry"
	"github.com/instantcocoa/delos/services/runtime"
)

const (
	serviceName = "runtime"
	defaultPort = 9001
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

	// Initialize provider registry
	registry := runtime.NewRegistry()

	// Register OpenAI provider
	openAIKey := os.Getenv("DELOS_RUNTIME_OPENAI_KEY")
	if openAIKey != "" {
		registry.Register(runtime.NewOpenAIProvider(openAIKey))
		logger.Info("registered OpenAI provider")
	}

	// Register Anthropic provider
	anthropicKey := os.Getenv("DELOS_RUNTIME_ANTHROPIC_KEY")
	if anthropicKey != "" {
		registry.Register(runtime.NewAnthropicProvider(anthropicKey))
		logger.Info("registered Anthropic provider")
	}

	// Register Gemini provider
	geminiKey := os.Getenv("DELOS_RUNTIME_GEMINI_KEY")
	if geminiKey != "" {
		registry.Register(runtime.NewGeminiProvider(geminiKey))
		logger.Info("registered Gemini provider")
	}

	// Register Ollama provider (local)
	ollamaURL := os.Getenv("DELOS_RUNTIME_OLLAMA_URL")
	ollamaEnabled := os.Getenv("DELOS_RUNTIME_OLLAMA_ENABLED")
	if ollamaEnabled == "true" || ollamaURL != "" {
		ollamaProvider := runtime.NewOllamaProvider(ollamaURL)
		if ollamaProvider.Available(ctx) {
			registry.Register(ollamaProvider)
			logger.Info("registered Ollama provider", "url", ollamaURL)
		} else {
			logger.Warn("Ollama not available", "url", ollamaURL)
		}
	}

	// Register OpenRouter provider (multi-model gateway)
	openRouterKey := os.Getenv("DELOS_RUNTIME_OPENROUTER_KEY")
	if openRouterKey != "" {
		siteURL := os.Getenv("DELOS_RUNTIME_OPENROUTER_SITE_URL")
		siteName := os.Getenv("DELOS_RUNTIME_OPENROUTER_SITE_NAME")
		var opts []runtime.OpenRouterOption
		if siteURL != "" || siteName != "" {
			opts = append(opts, runtime.WithSiteInfo(siteURL, siteName))
		}
		registry.Register(runtime.NewOpenRouterProvider(openRouterKey, opts...))
		logger.Info("registered OpenRouter provider")
	}

	// Register AWS Bedrock provider
	bedrockAccessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	bedrockSecretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	bedrockRegion := os.Getenv("AWS_REGION")
	if bedrockAccessKey != "" && bedrockSecretKey != "" {
		var opts []runtime.BedrockOption
		if sessionToken := os.Getenv("AWS_SESSION_TOKEN"); sessionToken != "" {
			opts = append(opts, runtime.WithSessionToken(sessionToken))
		}
		registry.Register(runtime.NewBedrockProvider(bedrockAccessKey, bedrockSecretKey, bedrockRegion, opts...))
		logger.Info("registered AWS Bedrock provider", "region", bedrockRegion)
	}

	// Register Vertex AI provider
	vertexProject := os.Getenv("GOOGLE_CLOUD_PROJECT")
	vertexLocation := os.Getenv("GOOGLE_CLOUD_LOCATION")
	vertexAccessToken := os.Getenv("GOOGLE_CLOUD_ACCESS_TOKEN")
	if vertexProject != "" && vertexAccessToken != "" {
		registry.Register(runtime.NewVertexAIProvider(vertexProject, vertexLocation, vertexAccessToken))
		logger.Info("registered Vertex AI provider", "project", vertexProject, "location", vertexLocation)
	}

	// Register Together AI provider
	togetherKey := os.Getenv("DELOS_RUNTIME_TOGETHER_KEY")
	if togetherKey != "" {
		registry.Register(runtime.NewTogetherProvider(togetherKey))
		logger.Info("registered Together AI provider")
	}

	if len(registry.List()) == 0 {
		logger.Warn("no LLM providers configured - set API keys for: OpenAI, Anthropic, Gemini, OpenRouter, Together, AWS Bedrock, Vertex AI, or DELOS_RUNTIME_OLLAMA_ENABLED=true")
	}

	// Initialize service
	svc := runtime.NewRuntimeService(registry, logger)

	// Create gRPC server
	serverCfg := grpcutil.DefaultServerConfig(cfg.GRPCPort, serviceName)
	server := grpcutil.NewServer(serverCfg, logger)

	// Register service handlers
	handler := runtime.NewHandler(svc, logger)
	handler.Register(server.GRPCServer())

	logger.Info("starting runtime service",
		"port", cfg.GRPCPort,
		"env", cfg.Environment,
		"providers", len(registry.List()),
	)

	// Run server (blocks until shutdown)
	return server.Run(ctx)
}
