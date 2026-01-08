package runtime

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
)

// =============================================================================
// Mock Provider for Testing
// =============================================================================

type mockProvider struct {
	name           string
	models         []string
	available      bool
	costPer1K      map[string]float64
	completeResult *CompletionResult
	completeErr    error
	streamChunks   []StreamChunk
	streamErr      error
	embedResult    *EmbedResult
	embedErr       error
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Models() []string { return m.models }

func (m *mockProvider) Available(ctx context.Context) bool { return m.available }

func (m *mockProvider) CostPer1KTokens() map[string]float64 { return m.costPer1K }

func (m *mockProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return m.completeResult, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	ch := make(chan StreamChunk, len(m.streamChunks))
	for _, chunk := range m.streamChunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	if m.embedErr != nil {
		return nil, m.embedErr
	}
	return m.embedResult, nil
}

// =============================================================================
// Test Helpers
// =============================================================================

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestService(providers ...Provider) *RuntimeService {
	registry := NewRegistry()
	for _, p := range providers {
		registry.Register(p)
	}
	return NewRuntimeService(registry, newTestLogger())
}

// =============================================================================
// RuntimeService Creation Tests
// =============================================================================

func TestNewRuntimeService(t *testing.T) {
	registry := NewRegistry()
	logger := newTestLogger()

	svc := NewRuntimeService(registry, logger)

	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.registry != registry {
		t.Error("registry not set correctly")
	}
}

// =============================================================================
// Complete Tests
// =============================================================================

func TestComplete_Success(t *testing.T) {
	mockP := &mockProvider{
		name:      "test-provider",
		models:    []string{"test-model"},
		available: true,
		costPer1K: map[string]float64{"test-model": 0.001},
		completeResult: &CompletionResult{
			ID:       "test-id",
			Content:  "Hello, world!",
			Provider: "test-provider",
			Model:    "test-model",
			Usage:    Usage{TotalTokens: 10, CostUSD: 0.00001},
		},
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Provider: "test-provider",
	}

	result, err := svc.Complete(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Content != "Hello, world!" {
		t.Errorf("expected content 'Hello, world!', got '%s'", result.Content)
	}
	if result.Provider != "test-provider" {
		t.Errorf("expected provider 'test-provider', got '%s'", result.Provider)
	}
}

func TestComplete_ProviderError(t *testing.T) {
	mockP := &mockProvider{
		name:        "test-provider",
		available:   true,
		costPer1K:   map[string]float64{},
		completeErr: errors.New("API rate limit exceeded"),
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Provider: "test-provider",
	}

	_, err := svc.Complete(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, mockP.completeErr) && err.Error() != "completion failed: API rate limit exceeded" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestComplete_ProviderNotFound(t *testing.T) {
	svc := newTestService() // no providers

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Provider: "nonexistent",
	}

	_, err := svc.Complete(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "provider not found: nonexistent" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestComplete_NoProvidersAvailable(t *testing.T) {
	mockP := &mockProvider{
		name:      "test-provider",
		available: false, // not available
		costPer1K: map[string]float64{},
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		// No provider specified, will try to auto-select
	}

	_, err := svc.Complete(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "no providers available" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestComplete_SpecificProviderNotAvailable(t *testing.T) {
	mockP := &mockProvider{
		name:      "test-provider",
		available: false,
		costPer1K: map[string]float64{},
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Provider: "test-provider",
		Routing:  RoutingSpecificProvider,
	}

	_, err := svc.Complete(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "provider not available: test-provider" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestComplete_SpecificProviderNoName(t *testing.T) {
	svc := newTestService()

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Routing:  RoutingSpecificProvider,
		// Provider name not set
	}

	_, err := svc.Complete(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "provider name required for specific provider routing" {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// CompleteStream Tests
// =============================================================================

func TestCompleteStream_Success(t *testing.T) {
	chunks := []StreamChunk{
		{Delta: "Hello", Done: false},
		{Delta: ", world!", Done: false},
		{Delta: "", Done: true, Message: &Message{Role: "assistant", Content: "Hello, world!"}},
	}

	mockP := &mockProvider{
		name:         "test-provider",
		available:    true,
		costPer1K:    map[string]float64{},
		streamChunks: chunks,
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Provider: "test-provider",
	}

	ch, err := svc.CompleteStream(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var received []StreamChunk
	for chunk := range ch {
		received = append(received, chunk)
	}

	if len(received) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(received))
	}
	if received[0].Delta != "Hello" {
		t.Errorf("expected first chunk 'Hello', got '%s'", received[0].Delta)
	}
}

func TestCompleteStream_ProviderError(t *testing.T) {
	mockP := &mockProvider{
		name:      "test-provider",
		available: true,
		costPer1K: map[string]float64{},
		streamErr: errors.New("stream initialization failed"),
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Provider: "test-provider",
	}

	_, err := svc.CompleteStream(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// =============================================================================
// Embed Tests
// =============================================================================

func TestEmbed_Success(t *testing.T) {
	mockP := &mockProvider{
		name:      "openai",
		available: true,
		costPer1K: map[string]float64{},
		embedResult: &EmbedResult{
			Embeddings: []Embedding{
				{Values: []float32{0.1, 0.2, 0.3}, Dimensions: 3},
			},
			Model:    "text-embedding-3-small",
			Provider: "openai",
		},
	}

	svc := newTestService(mockP)

	params := EmbedParams{
		Texts: []string{"Hello, world!"},
	}

	result, err := svc.Embed(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Embeddings) != 1 {
		t.Errorf("expected 1 embedding, got %d", len(result.Embeddings))
	}
	if result.Embeddings[0].Dimensions != 3 {
		t.Errorf("expected 3 dimensions, got %d", result.Embeddings[0].Dimensions)
	}
}

func TestEmbed_SpecificProvider(t *testing.T) {
	mockP := &mockProvider{
		name:      "gemini",
		available: true,
		costPer1K: map[string]float64{},
		embedResult: &EmbedResult{
			Provider: "gemini",
		},
	}

	svc := newTestService(mockP)

	params := EmbedParams{
		Texts:    []string{"Test"},
		Provider: "gemini",
	}

	result, err := svc.Embed(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "gemini" {
		t.Errorf("expected provider 'gemini', got '%s'", result.Provider)
	}
}

func TestEmbed_ProviderNotFound(t *testing.T) {
	svc := newTestService() // no providers

	params := EmbedParams{
		Texts:    []string{"Test"},
		Provider: "nonexistent",
	}

	_, err := svc.Embed(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "provider not found: nonexistent" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEmbed_NoEmbeddingProvider(t *testing.T) {
	// No openai provider registered (default for embeddings)
	mockP := &mockProvider{
		name:      "anthropic", // doesn't support embeddings
		available: true,
		costPer1K: map[string]float64{},
	}

	svc := newTestService(mockP)

	params := EmbedParams{
		Texts: []string{"Test"},
		// No provider specified, will try to default to openai
	}

	_, err := svc.Embed(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "no embedding provider available" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEmbed_ProviderError(t *testing.T) {
	mockP := &mockProvider{
		name:      "openai",
		available: true,
		costPer1K: map[string]float64{},
		embedErr:  errors.New("embedding failed"),
	}

	svc := newTestService(mockP)

	params := EmbedParams{
		Texts: []string{"Test"},
	}

	_, err := svc.Embed(context.Background(), params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// =============================================================================
// ListProviders Tests
// =============================================================================

func TestListProviders_Empty(t *testing.T) {
	svc := newTestService()

	providers := svc.ListProviders(context.Background())

	if len(providers) != 0 {
		t.Errorf("expected 0 providers, got %d", len(providers))
	}
}

func TestListProviders_Multiple(t *testing.T) {
	mock1 := &mockProvider{
		name:      "provider1",
		models:    []string{"model1", "model2"},
		available: true,
		costPer1K: map[string]float64{"model1": 0.001},
	}
	mock2 := &mockProvider{
		name:      "provider2",
		models:    []string{"model3"},
		available: false,
		costPer1K: map[string]float64{"model3": 0.002},
	}

	svc := newTestService(mock1, mock2)

	providers := svc.ListProviders(context.Background())

	if len(providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(providers))
	}

	// Check first provider
	found := false
	for _, p := range providers {
		if p.Name == "provider1" {
			found = true
			if !p.Available {
				t.Error("provider1 should be available")
			}
			if len(p.Models) != 2 {
				t.Errorf("provider1 should have 2 models, got %d", len(p.Models))
			}
		}
	}
	if !found {
		t.Error("provider1 not found in list")
	}
}

// =============================================================================
// Provider Selection Tests
// =============================================================================

func TestSelectProvider_CostOptimized(t *testing.T) {
	expensive := &mockProvider{
		name:      "expensive",
		available: true,
		costPer1K: map[string]float64{"model": 0.01},
	}
	cheap := &mockProvider{
		name:      "cheap",
		available: true,
		costPer1K: map[string]float64{"model": 0.001},
	}

	svc := newTestService(expensive, cheap)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Routing:  RoutingCostOptimized,
	}

	// Use Complete to trigger provider selection
	cheap.completeResult = &CompletionResult{Provider: "cheap"}

	result, err := svc.Complete(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "cheap" {
		t.Errorf("expected cheap provider, got '%s'", result.Provider)
	}
}

func TestSelectProvider_QualityOptimized_PrefersAnthropic(t *testing.T) {
	openai := &mockProvider{
		name:           "openai",
		available:      true,
		costPer1K:      map[string]float64{},
		completeResult: &CompletionResult{Provider: "openai"},
	}
	anthropic := &mockProvider{
		name:           "anthropic",
		available:      true,
		costPer1K:      map[string]float64{},
		completeResult: &CompletionResult{Provider: "anthropic"},
	}

	svc := newTestService(openai, anthropic)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Routing:  RoutingQualityOptimized,
	}

	result, err := svc.Complete(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("expected anthropic for quality routing, got '%s'", result.Provider)
	}
}

func TestSelectProvider_QualityOptimized_FallbackToOpenAI(t *testing.T) {
	openai := &mockProvider{
		name:           "openai",
		available:      true,
		costPer1K:      map[string]float64{},
		completeResult: &CompletionResult{Provider: "openai"},
	}

	svc := newTestService(openai)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Routing:  RoutingQualityOptimized,
	}

	result, err := svc.Complete(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "openai" {
		t.Errorf("expected openai fallback, got '%s'", result.Provider)
	}
}

func TestSelectProvider_LatencyOptimized(t *testing.T) {
	mockP := &mockProvider{
		name:           "fast-provider",
		available:      true,
		costPer1K:      map[string]float64{},
		completeResult: &CompletionResult{Provider: "fast-provider"},
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Routing:  RoutingLatencyOptimized,
	}

	result, err := svc.Complete(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "fast-provider" {
		t.Errorf("expected fast-provider, got '%s'", result.Provider)
	}
}

func TestSelectCheapest_WithSpecificModel(t *testing.T) {
	p1 := &mockProvider{
		name:      "p1",
		available: true,
		costPer1K: map[string]float64{"gpt-4": 0.03, "gpt-3.5": 0.001},
	}
	p2 := &mockProvider{
		name:      "p2",
		available: true,
		costPer1K: map[string]float64{"gpt-4": 0.02}, // cheaper for gpt-4
	}

	svc := newTestService(p1, p2)
	p2.completeResult = &CompletionResult{Provider: "p2"}

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Model:    "gpt-4",
		Routing:  RoutingCostOptimized,
	}

	result, err := svc.Complete(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "p2" {
		t.Errorf("expected p2 (cheaper for gpt-4), got '%s'", result.Provider)
	}
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestComplete_ContextCancelled(t *testing.T) {
	mockP := &mockProvider{
		name:        "test-provider",
		available:   true,
		costPer1K:   map[string]float64{},
		completeErr: context.Canceled,
	}

	svc := newTestService(mockP)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Provider: "test-provider",
	}

	_, err := svc.Complete(ctx, params)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestComplete_EmptyMessages(t *testing.T) {
	mockP := &mockProvider{
		name:           "test-provider",
		available:      true,
		costPer1K:      map[string]float64{},
		completeResult: &CompletionResult{Content: "response"},
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{}, // empty
		Provider: "test-provider",
	}

	// Should still work - provider handles validation
	_, err := svc.Complete(context.Background(), params)
	if err != nil {
		t.Errorf("unexpected error with empty messages: %v", err)
	}
}

func TestSelectCheapest_NoCostsConfigured(t *testing.T) {
	mockP := &mockProvider{
		name:           "test-provider",
		available:      true,
		costPer1K:      map[string]float64{}, // empty costs
		completeResult: &CompletionResult{Provider: "test-provider"},
	}

	svc := newTestService(mockP)

	params := CompletionParams{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Routing:  RoutingCostOptimized,
	}

	// Should fall back to first provider
	result, err := svc.Complete(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "test-provider" {
		t.Errorf("expected test-provider fallback, got '%s'", result.Provider)
	}
}
