// Package integration contains Ollama-specific integration tests.
// These tests require a running Ollama instance with the gemma3:4b model.
// Run with: go test -tags=integration ./tests/integration/... -run Ollama
//
//go:build integration

package integration

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
	runtimev1 "github.com/instantcocoa/delos/gen/go/runtime/v1"
)

const (
	ollamaProvider = "ollama"
	ollamaModel    = "gemma3:4b"
)

// isOllamaAvailable checks if Ollama provider is available in the runtime service
func isOllamaAvailable(t *testing.T) bool {
	t.Helper()
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &runtimev1.HealthRequest{})
	if err != nil {
		return false
	}

	available, ok := resp.ProviderStatus[ollamaProvider]
	return ok && available
}

// skipIfOllamaUnavailable skips the test if Ollama is not available
func skipIfOllamaUnavailable(t *testing.T) {
	t.Helper()
	if !isOllamaAvailable(t) {
		t.Skip("Ollama provider not available - skipping test")
	}
}

// ============================================================================
// OLLAMA PROVIDER TESTS
// ============================================================================

func TestOllama_Available(t *testing.T) {
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListProviders(ctx, &runtimev1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}

	var ollamaFound bool
	for _, p := range resp.Providers {
		if p.Name == ollamaProvider {
			ollamaFound = true
			t.Logf("Ollama provider: available=%v, models=%v", p.Available, p.Models)
			if !p.Available {
				t.Error("Ollama provider found but not available")
			}
			// Check for our expected model
			var hasModel bool
			for _, m := range p.Models {
				if strings.Contains(m, "gemma3") {
					hasModel = true
					break
				}
			}
			if !hasModel {
				t.Logf("Warning: gemma3 model not found in Ollama, available models: %v", p.Models)
			}
			break
		}
	}

	if !ollamaFound {
		t.Error("Ollama provider not found in provider list")
	}
}

func TestOllama_Complete_SimpleQuestion(t *testing.T) {
	skipIfOllamaUnavailable(t)
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "What is 2 + 2? Answer with just the number."},
			},
			Provider:  ollamaProvider,
			Model:     ollamaModel,
			MaxTokens: 20,
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	t.Logf("Response: %s", resp.Content)
	t.Logf("Usage: prompt=%d, completion=%d, total=%d",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)

	// Basic sanity check - response should contain "4"
	if !strings.Contains(resp.Content, "4") {
		t.Errorf("Expected response to contain '4', got: %s", resp.Content)
	}

	// Cost should be 0 for local models
	if resp.Usage.CostUsd != 0 {
		t.Logf("Note: Cost reported as %f (expected 0 for local models)", resp.Usage.CostUsd)
	}
}

func TestOllama_Complete_SystemPrompt(t *testing.T) {
	skipIfOllamaUnavailable(t)
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "system", Content: "You are a pirate. Always respond in pirate speak, starting with 'Arrr'."},
				{Role: "user", Content: "Say hello"},
			},
			Provider:  ollamaProvider,
			Model:     ollamaModel,
			MaxTokens: 50,
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	t.Logf("Response: %s", resp.Content)

	// Check that it follows the system prompt
	lowered := strings.ToLower(resp.Content)
	if !strings.Contains(lowered, "arr") {
		t.Logf("Warning: Response may not follow pirate system prompt: %s", resp.Content)
	}
}

func TestOllama_Complete_Temperature(t *testing.T) {
	skipIfOllamaUnavailable(t)
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Low temperature should give more deterministic responses
	resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "Complete this sequence: 1, 2, 3, "},
			},
			Provider:    ollamaProvider,
			Model:       ollamaModel,
			Temperature: 0.1,
			MaxTokens:   10,
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	t.Logf("Low temperature response: %s", resp.Content)

	// Should likely contain "4"
	if !strings.Contains(resp.Content, "4") {
		t.Logf("Note: Expected '4' in response, got: %s", resp.Content)
	}
}

func TestOllama_CompleteStream(t *testing.T) {
	skipIfOllamaUnavailable(t)
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	stream, err := client.CompleteStream(ctx, &runtimev1.CompleteStreamRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "Count from 1 to 5, one number per line."},
			},
			Provider:  ollamaProvider,
			Model:     ollamaModel,
			MaxTokens: 50,
		},
	})
	if err != nil {
		t.Fatalf("CompleteStream failed: %v", err)
	}

	var chunks int
	var fullContent strings.Builder

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Stream recv failed: %v", err)
		}
		chunks++
		fullContent.WriteString(chunk.Delta)

		// Log first few chunks for debugging
		if chunks <= 3 {
			t.Logf("Chunk %d: %q", chunks, chunk.Delta)
		}
	}

	t.Logf("Received %d chunks", chunks)
	t.Logf("Full content: %s", fullContent.String())

	if chunks == 0 {
		t.Error("Expected to receive at least one chunk")
	}
}

func TestOllama_Complete_MultiTurn(t *testing.T) {
	skipIfOllamaUnavailable(t)
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// First turn - establish context
	resp1, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "My name is Alice. What is my name?"},
			},
			Provider:  ollamaProvider,
			Model:     ollamaModel,
			MaxTokens: 30,
		},
	})
	if err != nil {
		t.Fatalf("First turn failed: %v", err)
	}
	t.Logf("Turn 1 response: %s", resp1.Content)

	// Second turn - reference previous context
	resp2, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "My name is Alice."},
				{Role: "assistant", Content: resp1.Content},
				{Role: "user", Content: "What did I tell you my name was?"},
			},
			Provider:  ollamaProvider,
			Model:     ollamaModel,
			MaxTokens: 30,
		},
	})
	if err != nil {
		t.Fatalf("Second turn failed: %v", err)
	}
	t.Logf("Turn 2 response: %s", resp2.Content)

	// Should mention Alice
	if !strings.Contains(strings.ToLower(resp2.Content), "alice") {
		t.Errorf("Expected response to contain 'Alice', got: %s", resp2.Content)
	}
}

func TestOllama_Complete_MaxTokens(t *testing.T) {
	skipIfOllamaUnavailable(t)
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Request with very low max tokens
	resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "Write a very long story about a dragon."},
			},
			Provider:  ollamaProvider,
			Model:     ollamaModel,
			MaxTokens: 10,
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	t.Logf("Response with max_tokens=10: %s", resp.Content)
	t.Logf("Completion tokens used: %d", resp.Usage.CompletionTokens)

	// Should have limited output (though exact behavior depends on model)
	if resp.Usage.CompletionTokens > 20 {
		t.Logf("Note: Got %d completion tokens, expected closer to 10", resp.Usage.CompletionTokens)
	}
}

// ============================================================================
// OLLAMA + PROMPT SERVICE INTEGRATION TESTS
// ============================================================================

func TestOllama_WithPromptService(t *testing.T) {
	skipIfOllamaUnavailable(t)

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	runtimeClient, runtimeCleanup := getRuntimeClient(t)
	defer runtimeCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Create a prompt with variables
	timestamp := time.Now().Format("150405")
	createResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name:        "Ollama Test Prompt",
		Slug:        "ollama-test-" + timestamp,
		Description: "A test prompt for Ollama integration",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are a helpful assistant that gives brief answers."},
			{Role: "user", Content: "Translate '{{text}}' to {{language}}. Just give the translation, nothing else."},
		},
		Tags: []string{"ollama-test"},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}

	promptID := createResp.Prompt.Id
	t.Logf("Created prompt: %s (slug: %s)", promptID, createResp.Prompt.Slug)

	defer func() {
		promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	}()

	// Use the prompt with Ollama via prompt_ref
	resp, err := runtimeClient.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			PromptRef: createResp.Prompt.Slug + ":v1",
			Variables: map[string]string{
				"text":     "Hello",
				"language": "Spanish",
			},
			Provider:  ollamaProvider,
			Model:     ollamaModel,
			MaxTokens: 20,
		},
	})
	if err != nil {
		t.Fatalf("Complete with prompt_ref failed: %v", err)
	}

	t.Logf("Translation response: %s", resp.Content)

	// Should contain Spanish word for hello
	lowered := strings.ToLower(resp.Content)
	if !strings.Contains(lowered, "hola") {
		t.Logf("Note: Expected 'hola' in response, got: %s", resp.Content)
	}
}

func TestOllama_Summarization(t *testing.T) {
	skipIfOllamaUnavailable(t)
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	longText := `The Industrial Revolution, which took place from the 18th to 19th centuries,
was a period during which predominantly agrarian, rural societies in Europe and America became
industrial and urban. Prior to the Industrial Revolution, which began in Britain in the late 1700s,
manufacturing was often done in people's homes, using hand tools or basic machines.
Industrialization marked a shift to powered, special-purpose machinery, factories and mass production.`

	resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "system", Content: "You are a helpful assistant that summarizes text concisely."},
				{Role: "user", Content: "Summarize this in one sentence:\n\n" + longText},
			},
			Provider:    ollamaProvider,
			Model:       ollamaModel,
			MaxTokens:   100,
			Temperature: 0.3,
		},
	})
	if err != nil {
		t.Fatalf("Summarization failed: %v", err)
	}

	t.Logf("Summary: %s", resp.Content)

	// Summary should be shorter than original
	if len(resp.Content) > len(longText) {
		t.Logf("Note: Summary longer than original text")
	}

	// Should mention key concepts
	lowered := strings.ToLower(resp.Content)
	hasKeyword := strings.Contains(lowered, "industrial") ||
		strings.Contains(lowered, "revolution") ||
		strings.Contains(lowered, "manufacturing")
	if !hasKeyword {
		t.Logf("Note: Summary may not capture key concepts: %s", resp.Content)
	}
}

func TestOllama_CodeGeneration(t *testing.T) {
	skipIfOllamaUnavailable(t)
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "Write a Python function that checks if a number is prime. Just the function, no explanation."},
			},
			Provider:    ollamaProvider,
			Model:       ollamaModel,
			MaxTokens:   200,
			Temperature: 0.2,
		},
	})
	if err != nil {
		t.Fatalf("Code generation failed: %v", err)
	}

	t.Logf("Generated code:\n%s", resp.Content)

	// Should contain Python function definition
	if !strings.Contains(resp.Content, "def ") {
		t.Logf("Note: Response may not contain Python function definition")
	}
}

// ============================================================================
// OLLAMA BENCHMARKS (optional, for performance testing)
// ============================================================================

func BenchmarkOllama_Complete(b *testing.B) {
	// Skip if not running benchmarks or Ollama unavailable
	addr := os.Getenv("DELOS_RUNTIME_ADDR")
	if addr == "" {
		addr = "localhost:9001"
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		b.Skipf("Cannot connect to runtime: %v", err)
	}
	defer conn.Close()
	client := runtimev1.NewRuntimeServiceClient(conn)

	// Check Ollama availability
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	resp, err := client.Health(ctx, &runtimev1.HealthRequest{})
	cancel()
	if err != nil || !resp.ProviderStatus[ollamaProvider] {
		b.Skip("Ollama not available")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		_, err := client.Complete(ctx, &runtimev1.CompleteRequest{
			Params: &runtimev1.CompletionParams{
				Messages: []*runtimev1.Message{
					{Role: "user", Content: "Say hello"},
				},
				Provider:  ollamaProvider,
				Model:     ollamaModel,
				MaxTokens: 10,
			},
		})
		cancel()
		if err != nil {
			b.Fatalf("Complete failed: %v", err)
		}
	}
}
