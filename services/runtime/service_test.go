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
	completeResult *CompletionResult
	completeErr    error
	streamChunks   []StreamChunk
	streamErr      error
	embedResult    *EmbedResult
	embedErr       error

	lastParams *CompletionParams // captured on Complete/CompleteStream
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Models(ctx context.Context) []string { return m.models }

func (m *mockProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	m.lastParams = &params
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return m.completeResult, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	m.lastParams = &params
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
// ResolveProvider
// =============================================================================

func TestResolveProvider(t *testing.T) {
	openai := &mockProvider{name: "openai", models: []string{"gpt-4o"}}
	anthropic := &mockProvider{name: "anthropic", models: []string{"claude-sonnet-4-5"}}
	custom := &mockProvider{name: "local", models: []string{"my-model"}}
	svc := newTestService(openai, anthropic, custom)
	ctx := context.Background()

	cases := []struct {
		model        string
		wantProvider string
		wantModel    string
	}{
		{"gpt-4o", "openai", "gpt-4o"},                        // exact listing
		{"my-model", "local", "my-model"},                     // exact listing, custom provider
		{"openai/whatever", "openai", "whatever"},             // provider prefix wins
		{"anthropic/claude-x", "anthropic", "claude-x"},       // prefix strips
		{"claude-brand-new", "anthropic", "claude-brand-new"}, // well-known prefix fallback
		{"gpt-brand-new", "openai", "gpt-brand-new"},          // well-known prefix fallback
		{"text-embedding-3-small", "openai", "text-embedding-3-small"},
	}
	for _, tc := range cases {
		p, model, err := svc.ResolveProvider(ctx, tc.model)
		if err != nil {
			t.Errorf("ResolveProvider(%q) error: %v", tc.model, err)
			continue
		}
		if p.Name() != tc.wantProvider || model != tc.wantModel {
			t.Errorf("ResolveProvider(%q) = (%s, %s), want (%s, %s)",
				tc.model, p.Name(), model, tc.wantProvider, tc.wantModel)
		}
	}
}

func TestResolveProviderNotFound(t *testing.T) {
	svc := newTestService(&mockProvider{name: "openai", models: []string{"gpt-4o"}})
	if _, _, err := svc.ResolveProvider(context.Background(), "unknown-model"); err == nil {
		t.Fatal("expected error for unknown model")
	}
	// well-known prefix but provider not registered
	if _, _, err := svc.ResolveProvider(context.Background(), "gemini-2.5-flash"); err == nil {
		t.Fatal("expected error for unregistered provider")
	}
}

// =============================================================================
// Complete / CompleteStream / Embed pass-through
// =============================================================================

func TestServiceComplete(t *testing.T) {
	p := &mockProvider{
		name:   "openai",
		models: []string{"gpt-4o"},
		completeResult: &CompletionResult{
			Message:      TextMessage("assistant", "hello"),
			FinishReason: FinishStop,
			Provider:     "openai",
			Model:        "gpt-4o",
			Usage:        Usage{TotalTokens: 10},
		},
	}
	svc := newTestService(p)

	result, err := svc.Complete(context.Background(), p, CompletionParams{
		Messages: []Message{TextMessage("user", "hi")},
		Model:    "gpt-4o",
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if result.Text() != "hello" {
		t.Errorf("Text() = %q", result.Text())
	}
	if p.lastParams == nil || p.lastParams.Model != "gpt-4o" {
		t.Errorf("params not passed through: %+v", p.lastParams)
	}
}

func TestServiceCompleteError(t *testing.T) {
	wantErr := &ProviderError{Provider: "openai", StatusCode: 429, Message: "rate limited"}
	p := &mockProvider{name: "openai", completeErr: wantErr}
	svc := newTestService(p)

	_, err := svc.Complete(context.Background(), p, CompletionParams{Model: "gpt-4o"})
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ProviderError passthrough, got %T: %v", err, err)
	}
	if !pe.Retryable() {
		t.Error("429 should be retryable")
	}
}

func TestServiceCompleteStream(t *testing.T) {
	p := &mockProvider{
		name: "openai",
		streamChunks: []StreamChunk{
			{Delta: "he"},
			{Delta: "llo"},
			{Done: true, FinishReason: FinishStop},
		},
	}
	svc := newTestService(p)

	chunks, err := svc.CompleteStream(context.Background(), p, CompletionParams{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("CompleteStream failed: %v", err)
	}
	text := ""
	var done bool
	for c := range chunks {
		text += c.Delta
		if c.Done {
			done = true
		}
	}
	if text != "hello" || !done {
		t.Errorf("stream text=%q done=%v", text, done)
	}
}

func TestServiceEmbed(t *testing.T) {
	p := &mockProvider{
		name: "openai",
		embedResult: &EmbedResult{
			Embeddings: []Embedding{{Values: []float32{1, 2}, Dimensions: 2}},
		},
	}
	svc := newTestService(p)

	result, err := svc.Embed(context.Background(), p, EmbedParams{Texts: []string{"x"}, Model: "text-embedding-3-small"})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(result.Embeddings) != 1 {
		t.Errorf("embeddings = %v", result.Embeddings)
	}
}

// =============================================================================
// ListProviders / Registry
// =============================================================================

func TestListProviders(t *testing.T) {
	svc := newTestService(
		&mockProvider{name: "openai", models: []string{"gpt-4o"}},
		&mockProvider{name: "anthropic", models: []string{"claude-sonnet-4-5"}},
	)
	infos := svc.ListProviders(context.Background())
	if len(infos) != 2 {
		t.Fatalf("got %d providers", len(infos))
	}
	// registration order is preserved
	if infos[0].Name != "openai" || infos[1].Name != "anthropic" {
		t.Errorf("order = %s, %s", infos[0].Name, infos[1].Name)
	}
}

func TestRegistryReplace(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockProvider{name: "openai", models: []string{"a"}})
	r.Register(&mockProvider{name: "openai", models: []string{"b"}})
	if len(r.List()) != 1 {
		t.Fatalf("re-registration must replace, got %d providers", len(r.List()))
	}
	p, _ := r.Get("openai")
	if p.Models(context.Background())[0] != "b" {
		t.Error("latest registration should win")
	}
}

// =============================================================================
// Message helpers
// =============================================================================

func TestMessageText(t *testing.T) {
	m := Message{Role: "user", Content: []ContentPart{
		TextPart("a"),
		{Type: "image", ImageURL: "http://x/y.png"},
		TextPart("b"),
	}}
	if m.Text() != "ab" {
		t.Errorf("Text() = %q", m.Text())
	}
}
