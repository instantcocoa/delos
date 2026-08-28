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

// TestOllama_Available asserts the gateway advertises the model the rest of
// this file completes against. Without it every other Ollama test would fail
// deep inside a completion with a confusing provider error.
func TestOllama_Available(t *testing.T) {
	skipIfOllamaUnavailable(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)

	var ollamaModels []string
	hasModel := false
	for _, m := range list.Data {
		if m.OwnedBy != ollamaProvider {
			continue
		}
		ollamaModels = append(ollamaModels, m.ID)
		if m.ID == "" {
			t.Error("ollama model advertised with an empty id")
		}
		if strings.Contains(m.ID, "gemma3") {
			hasModel = true
		}
	}
	if len(ollamaModels) == 0 {
		t.Fatal("ollama is registered but advertises no models")
	}
	if !hasModel {
		t.Errorf("the %s model these tests require is not advertised; ollama serves %v",
			ollamaModel, ollamaModels)
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

	if !strings.Contains(resp.content(), "4") {
		t.Errorf("Expected response to contain '4', got: %s", resp.content())
	}
	if resp.Usage == nil {
		t.Fatal("expected usage accounting on a completion")
	}
	if resp.Usage.PromptTokens <= 0 || resp.Usage.CompletionTokens <= 0 {
		t.Errorf("expected non-zero token counts, got prompt=%d completion=%d",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}
	// A locally hosted model costs nothing; a non-zero cost means the pricing
	// table is being applied to a provider it does not describe.
	if resp.Usage.CostUSD != 0 {
		t.Errorf("expected cost_usd 0 for a local ollama model, got %f", resp.Usage.CostUSD)
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

	// Whether the model plays along with the persona is not deterministic, so
	// assert what a system message must always produce: a well-formed assistant
	// reply with content and token accounting that includes the system message.
	if len(resp.Choices) == 0 {
		t.Fatal("expected a choice in the response")
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Role != "assistant" {
		t.Fatalf("expected an assistant message, got %+v", resp.Choices[0])
	}
	if strings.TrimSpace(resp.content()) == "" {
		t.Error("expected non-empty content when a system prompt is supplied")
	}
	if resp.Usage == nil {
		t.Fatal("expected usage accounting")
	}
	// The system message is part of the prompt, so it must be billed for.
	if resp.Usage.PromptTokens <= 0 {
		t.Errorf("expected the system message to count toward prompt tokens, got %d",
			resp.Usage.PromptTokens)
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

	// At temperature 0.1 the continuation of "1, 2, 3, " is 4.
	if !strings.Contains(resp.content(), "4") {
		t.Errorf("expected the low-temperature continuation of \"1, 2, 3, \" to contain \"4\", got: %s",
			resp.content())
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
	}

	if strings.TrimSpace(fullContent.String()) == "" {
		t.Errorf("expected the streamed deltas to carry content, got %q", fullContent.String())
	}
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

	// Second turn - reference previous context
	resp2 := ollamaComplete(t, ctx, gwChatRequest{
		Messages: []gwChatMessage{
			{Role: "user", Content: "My name is Alice."},
			{Role: "assistant", Content: resp1.content()},
			{Role: "user", Content: "What did I tell you my name was?"},
		},
		MaxTokens: 30,
	})
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

	// max_tokens is a hard cap the gateway must pass through to the provider.
	// A prompt that invites a long answer is the case that exposes it being
	// dropped. Allow modest slack for tokenizer accounting, not 10x.
	const requested = 10
	const tolerated = 2 * requested

	if resp.Usage == nil {
		t.Fatal("expected usage accounting to verify the max_tokens cap")
	}
	if resp.Usage.CompletionTokens > tolerated {
		t.Errorf("max_tokens=%d was not enforced: the response used %d completion tokens",
			requested, resp.Usage.CompletionTokens)
	}
	if resp.Usage.CompletionTokens <= 0 {
		t.Errorf("expected a non-zero completion token count, got %d", resp.Usage.CompletionTokens)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("expected a choice in the response")
	}
	// Being cut off by the cap is reported as finish_reason "length".
	if fr := resp.Choices[0].FinishReason; fr == nil || *fr != "length" {
		got := "<nil>"
		if fr != nil {
			got = *fr
		}
		t.Errorf("expected finish_reason \"length\" when max_tokens truncates the answer, got %q", got)
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

	// The rendered prompt asks for "Hello" in Spanish and nothing else.
	if !strings.Contains(strings.ToLower(resp.content()), "hola") {
		t.Errorf("expected the rendered translation prompt to yield \"hola\", got: %s", resp.content())
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

	// Which words a summary picks is not deterministic. That it is a summary -
	// non-empty, shorter than its input, and billed for the long prompt - is.
	summary := strings.TrimSpace(resp.content())
	if summary == "" {
		t.Fatal("expected a non-empty summary")
	}
	if len(summary) >= len(longText) {
		t.Errorf("expected the summary (%d chars) to be shorter than the input (%d chars): %s",
			len(summary), len(longText), summary)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage accounting")
	}
	if resp.Usage.CompletionTokens > 100 {
		t.Errorf("max_tokens=100 was not enforced: the summary used %d completion tokens",
			resp.Usage.CompletionTokens)
	}
	if resp.Usage.PromptTokens <= resp.Usage.CompletionTokens {
		t.Errorf("summarizing a long passage should cost more prompt than completion tokens, got prompt=%d completion=%d",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
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

	// "Write a Python function ... just the function" has exactly one shape.
	code := resp.content()
	if !strings.Contains(code, "def ") {
		t.Errorf("expected a Python function definition (\"def \") in the generated code, got:\n%s", code)
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
