package runtime

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"

	runtimev1 "github.com/instantcocoa/delos/gen/go/runtime/v1"
)

// Handler implements the RuntimeService gRPC interface.
type Handler struct {
	runtimev1.UnimplementedRuntimeServiceServer
	svc    *RuntimeService
	logger *slog.Logger
}

// NewHandler creates a new runtime service handler.
func NewHandler(svc *RuntimeService, logger *slog.Logger) *Handler {
	return &Handler{
		svc:    svc,
		logger: logger.With("component", "handler"),
	}
}

// Register registers the handler with a gRPC server.
func (h *Handler) Register(s *grpc.Server) {
	runtimev1.RegisterRuntimeServiceServer(s, h)
}

// Complete performs a completion request.
func (h *Handler) Complete(ctx context.Context, req *runtimev1.CompleteRequest) (*runtimev1.CompleteResponse, error) {
	params := protoToCompletionParams(req.Params)

	result, err := h.svc.Complete(ctx, params)
	if err != nil {
		return nil, err
	}

	return completionResultToProto(result), nil
}

// CompleteStream performs a streaming completion request.
func (h *Handler) CompleteStream(req *runtimev1.CompleteStreamRequest, stream runtimev1.RuntimeService_CompleteStreamServer) error {
	params := protoToCompletionParams(req.Params)

	chunks, err := h.svc.CompleteStream(stream.Context(), params)
	if err != nil {
		return err
	}

	for chunk := range chunks {
		resp := &runtimev1.CompleteStreamResponse{
			Id:       chunk.ID,
			Delta:    chunk.Delta,
			Done:     chunk.Done,
			Provider: chunk.Provider,
			Model:    chunk.Model,
			TraceId:  chunk.TraceID,
		}

		if chunk.Message != nil {
			resp.Message = &runtimev1.Message{
				Role:    chunk.Message.Role,
				Content: chunk.Message.Content,
			}
		}

		if chunk.Usage != nil {
			resp.Usage = &runtimev1.Usage{
				PromptTokens:     int32(chunk.Usage.PromptTokens),
				CompletionTokens: int32(chunk.Usage.CompletionTokens),
				TotalTokens:      int32(chunk.Usage.TotalTokens),
				CostUsd:          chunk.Usage.CostUSD,
			}
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}

	return nil
}

// Embed generates embeddings for text.
func (h *Handler) Embed(ctx context.Context, req *runtimev1.EmbedRequest) (*runtimev1.EmbedResponse, error) {
	params := EmbedParams{
		Texts:    req.Texts,
		Model:    req.Model,
		Provider: req.Provider,
	}

	result, err := h.svc.Embed(ctx, params)
	if err != nil {
		return nil, err
	}

	embeddings := make([]*runtimev1.Embedding, len(result.Embeddings))
	for i, e := range result.Embeddings {
		embeddings[i] = &runtimev1.Embedding{
			Values:     e.Values,
			Dimensions: int32(e.Dimensions),
		}
	}

	return &runtimev1.EmbedResponse{
		Embeddings: embeddings,
		Model:      result.Model,
		Provider:   result.Provider,
		Usage: &runtimev1.Usage{
			PromptTokens: int32(result.Usage.PromptTokens),
			TotalTokens:  int32(result.Usage.TotalTokens),
		},
	}, nil
}

// ListProviders returns available LLM providers.
func (h *Handler) ListProviders(ctx context.Context, req *runtimev1.ListProvidersRequest) (*runtimev1.ListProvidersResponse, error) {
	providers := h.svc.ListProviders(ctx)

	protoProviders := make([]*runtimev1.Provider, len(providers))
	for i, p := range providers {
		protoProviders[i] = &runtimev1.Provider{
			Name:             p.Name,
			Models:           p.Models,
			Available:        p.Available,
			CostPer_1KTokens: p.CostPer1KTokens,
		}
	}

	return &runtimev1.ListProvidersResponse{
		Providers: protoProviders,
	}, nil
}

// Health returns the service health status.
func (h *Handler) Health(ctx context.Context, req *runtimev1.HealthRequest) (*runtimev1.HealthResponse, error) {
	providers := h.svc.ListProviders(ctx)
	status := make(map[string]bool)
	for _, p := range providers {
		status[p.Name] = p.Available
	}

	return &runtimev1.HealthResponse{
		Status:         "healthy",
		Version:        "0.1.0",
		ProviderStatus: status,
	}, nil
}

// Conversion helpers

func protoToCompletionParams(p *runtimev1.CompletionParams) CompletionParams {
	if p == nil {
		return CompletionParams{}
	}

	messages := make([]Message, len(p.Messages))
	for i, m := range p.Messages {
		messages[i] = Message{
			Role:    m.Role,
			Content: m.Content,
			Name:    m.Name,
		}
	}

	routing := RoutingUnspecified
	switch p.Routing {
	case runtimev1.RoutingStrategy_ROUTING_STRATEGY_COST_OPTIMIZED:
		routing = RoutingCostOptimized
	case runtimev1.RoutingStrategy_ROUTING_STRATEGY_LATENCY_OPTIMIZED:
		routing = RoutingLatencyOptimized
	case runtimev1.RoutingStrategy_ROUTING_STRATEGY_QUALITY_OPTIMIZED:
		routing = RoutingQualityOptimized
	case runtimev1.RoutingStrategy_ROUTING_STRATEGY_SPECIFIC_PROVIDER:
		routing = RoutingSpecificProvider
	}

	return CompletionParams{
		PromptRef:    p.PromptRef,
		Messages:     messages,
		Variables:    p.Variables,
		Routing:      routing,
		Provider:     p.Provider,
		Model:        p.Model,
		Temperature:  p.Temperature,
		MaxTokens:    int(p.MaxTokens),
		TopP:         p.TopP,
		Stop:         p.Stop,
		OutputSchema: p.OutputSchema,
		UseCache:     p.UseCache,
		CacheTTL:     int(p.CacheTtlSeconds),
		Metadata:     p.Metadata,
	}
}

func completionResultToProto(r *CompletionResult) *runtimev1.CompleteResponse {
	return &runtimev1.CompleteResponse{
		Id:      r.ID,
		Content: r.Content,
		Message: &runtimev1.Message{
			Role:    r.Message.Role,
			Content: r.Message.Content,
		},
		Provider: r.Provider,
		Model:    r.Model,
		Usage: &runtimev1.Usage{
			PromptTokens:     int32(r.Usage.PromptTokens),
			CompletionTokens: int32(r.Usage.CompletionTokens),
			TotalTokens:      int32(r.Usage.TotalTokens),
			CostUsd:          r.Usage.CostUSD,
		},
		Cached:  r.Cached,
		TraceId: r.TraceID,
	}
}
