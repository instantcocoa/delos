// Package integration contains Ollama-specific integration tests, exercised
// through the delos-gateway OpenAI-compatible HTTP surface.
// These tests require a running Ollama instance with the gemma3:4b model.
// Run with: go test -tags=integration ./tests/integration/... -run Ollama
//
//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

const (
	ollamaProvider = "ollama"
	ollamaModel    = "gemma3:4b"
)

// isOllamaAvailable checks whether the gateway lists any ollama-owned model.
func isOllamaAvailable(t *testing.T) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	g := newGatewayClient(10 * time.Second)
	status, body, err := g.get(ctx, "/v1/models")
	if err != nil || status != http.StatusOK {
		return false
	}
	var list gwModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return false
	}
	for _, m := range list.Data {
		if m.OwnedBy == ollamaProvider {
			return true
		}
	}
	return false
}

// skipIfOllamaUnavailable skips the test if Ollama is not available
func skipIfOllamaUnavailable(t *testing.T) {
	t.Helper()
	if !isOllamaAvailable(t) {
		t.Skip("Ollama provider not available - skipping test")
	}
}

// ollamaComplete runs a non-streaming completion against the gateway using the
// ollama provider prefix and fails the test on transport errors.
func ollamaComplete(t *testing.T, ctx context.Context, req gwChatRequest) gwChatResponse {
	t.Helper()
	req.Model = ollamaProvider + "/" + ollamaModel
	g := newGatewayClient(120 * time.Second)
	status, body, err := g.postJSON(ctx, "/v1/chat/completions", req)
	if err != nil {
		t.Fatalf("gateway completion failed: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("gateway completion: expected 200, got %d: %s", status, body)
	}
	var resp gwChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("gateway completion: invalid JSON: %v (body: %s)", err, body)
	}
	return resp
}

// ============================================================================
// OLLAMA PROVIDER TESTS
// ============================================================================

func TestOllama_Available(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)

	var ollamaFound, hasModel bool
	for _, m := range list.Data {
		if m.OwnedBy == ollamaProvider {
			ollamaFound = true
			if strings.Contains(m.ID, "gemma3") {
				hasModel = true
			}
		}
	}
	if !ollamaFound {
		t.Skip("Ollama provider not registered on the gateway - skipping")
	}
	if !hasModel {
		t.Logf("Warning: gemma3 model not found in Ollama model list")
	}
}

func TestOllama_Complete_SimpleQuestion(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp := ollamaComplete(t, ctx, gwChatRequest{
		Messages:  []gwChatMessage{{Role: "user", Content: "What is 2 + 2? Answer with just the number."}},
		MaxTokens: 20,
	})

	t.Logf("Response: %s", resp.content())
	if resp.Usage != nil {
		t.Logf("Usage: prompt=%d, completion=%d, total=%d",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
		if resp.Usage.CostUSD != 0 {
			t.Logf("Note: Cost reported as %f (expected 0 for local models)", resp.Usage.CostUSD)
		}
	}

	if !strings.Contains(resp.content(), "4") {
		t.Errorf("Expected response to contain '4', got: %s", resp.content())
	}
}

func TestOllama_Complete_SystemPrompt(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp := ollamaComplete(t, ctx, gwChatRequest{
		Messages: []gwChatMessage{
			{Role: "system", Content: "You are a pirate. Always respond in pirate speak, starting with 'Arrr'."},
			{Role: "user", Content: "Say hello"},
		},
		MaxTokens: 50,
	})

	t.Logf("Response: %s", resp.content())
	if !strings.Contains(strings.ToLower(resp.content()), "arr") {
		t.Logf("Warning: Response may not follow pirate system prompt: %s", resp.content())
	}
}

func TestOllama_Complete_Temperature(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	temp := 0.1
	resp := ollamaComplete(t, ctx, gwChatRequest{
		Messages:    []gwChatMessage{{Role: "user", Content: "Complete this sequence: 1, 2, 3, "}},
		Temperature: &temp,
		MaxTokens:   10,
	})

	t.Logf("Low temperature response: %s", resp.content())
	if !strings.Contains(resp.content(), "4") {
		t.Logf("Note: Expected '4' in response, got: %s", resp.content())
	}
}

func TestOllama_CompleteStream(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	g := newGatewayClient(120 * time.Second)
	resp, err := g.postStream(ctx, "/v1/chat/completions", gwChatRequest{
		Model:     ollamaProvider + "/" + ollamaModel,
		Messages:  []gwChatMessage{{Role: "user", Content: "Count from 1 to 5, one number per line."}},
		MaxTokens: 50,
		Stream:    true,
	})
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("stream: expected text/event-stream, got %s", ct)
	}

	var chunks int
	var sawDone bool
	var fullContent strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			sawDone = true
			break
		}
		var chunk gwChatResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("bad SSE chunk %q: %v", payload, err)
		}
		chunks++
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			fullContent.WriteString(chunk.Choices[0].Delta.Content)
		}
		if chunks <= 3 {
			t.Logf("Chunk %d: %q", chunks, payload)
		}
	}

	t.Logf("Received %d chunks", chunks)
	t.Logf("Full content: %s", fullContent.String())

	if chunks == 0 {
		t.Error("Expected to receive at least one chunk")
	}
	if !sawDone {
		t.Error("Expected stream to terminate with [DONE]")
	}
}

func TestOllama_Complete_MultiTurn(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// First turn - establish context
	resp1 := ollamaComplete(t, ctx, gwChatRequest{
		Messages:  []gwChatMessage{{Role: "user", Content: "My name is Alice. What is my name?"}},
		MaxTokens: 30,
	})
	t.Logf("Turn 1 response: %s", resp1.content())

	// Second turn - reference previous context
	resp2 := ollamaComplete(t, ctx, gwChatRequest{
		Messages: []gwChatMessage{
			{Role: "user", Content: "My name is Alice."},
			{Role: "assistant", Content: resp1.content()},
			{Role: "user", Content: "What did I tell you my name was?"},
		},
		MaxTokens: 30,
	})
	t.Logf("Turn 2 response: %s", resp2.content())

	if !strings.Contains(strings.ToLower(resp2.content()), "alice") {
		t.Errorf("Expected response to contain 'Alice', got: %s", resp2.content())
	}
}

func TestOllama_Complete_MaxTokens(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp := ollamaComplete(t, ctx, gwChatRequest{
		Messages:  []gwChatMessage{{Role: "user", Content: "Write a very long story about a dragon."}},
		MaxTokens: 10,
	})

	t.Logf("Response with max_tokens=10: %s", resp.content())
	if resp.Usage != nil {
		t.Logf("Completion tokens used: %d", resp.Usage.CompletionTokens)
		if resp.Usage.CompletionTokens > 20 {
			t.Logf("Note: Got %d completion tokens, expected closer to 10", resp.Usage.CompletionTokens)
		}
	}
}

// ============================================================================
// OLLAMA + PROMPT SERVICE INTEGRATION TESTS
// ============================================================================

func TestOllama_WithPromptService(t *testing.T) {
	skipIfOllamaUnavailable(t)

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Create a prompt with variables in the control plane
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

	// Render the prompt's messages with variables and run them through the
	// gateway - the same flow the eval runner uses.
	variables := map[string]string{"text": "Hello", "language": "Spanish"}
	var messages []gwChatMessage
	for _, m := range createResp.Prompt.Messages {
		content := m.Content
		for k, v := range variables {
			content = strings.ReplaceAll(content, "{{"+k+"}}", v)
		}
		messages = append(messages, gwChatMessage{Role: m.Role, Content: content})
	}

	resp := ollamaComplete(t, ctx, gwChatRequest{Messages: messages, MaxTokens: 20})
	t.Logf("Translation response: %s", resp.content())

	if !strings.Contains(strings.ToLower(resp.content()), "hola") {
		t.Logf("Note: Expected 'hola' in response, got: %s", resp.content())
	}
}

func TestOllama_Summarization(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	longText := `The Industrial Revolution, which took place from the 18th to 19th centuries,
was a period during which predominantly agrarian, rural societies in Europe and America became
industrial and urban. Prior to the Industrial Revolution, which began in Britain in the late 1700s,
manufacturing was often done in people's homes, using hand tools or basic machines.
Industrialization marked a shift to powered, special-purpose machinery, factories and mass production.`

	temp := 0.3
	resp := ollamaComplete(t, ctx, gwChatRequest{
		Messages: []gwChatMessage{
			{Role: "system", Content: "You are a helpful assistant that summarizes text concisely."},
			{Role: "user", Content: "Summarize this in one sentence:\n\n" + longText},
		},
		MaxTokens:   100,
		Temperature: &temp,
	})

	t.Logf("Summary: %s", resp.content())

	if len(resp.content()) > len(longText) {
		t.Logf("Note: Summary longer than original text")
	}

	lowered := strings.ToLower(resp.content())
	hasKeyword := strings.Contains(lowered, "industrial") ||
		strings.Contains(lowered, "revolution") ||
		strings.Contains(lowered, "manufacturing")
	if !hasKeyword {
		t.Logf("Note: Summary may not capture key concepts: %s", resp.content())
	}
}

func TestOllama_CodeGeneration(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	temp := 0.2
	resp := ollamaComplete(t, ctx, gwChatRequest{
		Messages: []gwChatMessage{
			{Role: "user", Content: "Write a Python function that checks if a number is prime. Just the function, no explanation."},
		},
		MaxTokens:   200,
		Temperature: &temp,
	})

	t.Logf("Generated code:\n%s", resp.content())

	if !strings.Contains(resp.content(), "def ") {
		t.Logf("Note: Response may not contain Python function definition")
	}
}

// ============================================================================
// OLLAMA BENCHMARKS (optional, for performance testing)
// ============================================================================

func BenchmarkOllama_Complete(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	g := newGatewayClient(120 * time.Second)
	status, body, err := g.get(ctx, "/v1/models")
	cancel()
	if err != nil || status != http.StatusOK {
		b.Skipf("Cannot reach gateway: %v (status %d)", err, status)
	}
	var list gwModelList
	if err := json.Unmarshal(body, &list); err != nil {
		b.Skipf("Bad models response: %v", err)
	}
	available := false
	for _, m := range list.Data {
		if m.OwnedBy == ollamaProvider {
			available = true
			break
		}
	}
	if !available {
		b.Skip("Ollama not available")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		status, body, err := g.postJSON(ctx, "/v1/chat/completions", gwChatRequest{
			Model:     ollamaProvider + "/" + ollamaModel,
			Messages:  []gwChatMessage{{Role: "user", Content: "Say hello"}},
			MaxTokens: 10,
		})
		cancel()
		if err != nil || status != http.StatusOK {
			b.Fatalf("Complete failed: %v (status %d, body %s)", err, status, body)
		}
	}
}
