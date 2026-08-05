package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// PromptSource supplies prompts to the eval runner. In the merged control
// plane this is a direct call into the prompt module, not a network hop.
type PromptSource interface {
	GetPrompt(ctx context.Context, id string) (*promptv1.Prompt, error)
}

// ExampleSource supplies dataset examples to the eval runner.
type ExampleSource interface {
	GetExamples(ctx context.Context, datasetID string, limit int, shuffle bool) ([]*datasetsv1.Example, error)
}

// ChatMessage is a single chat message sent to the gateway.
type ChatMessage struct {
	Role    string
	Content string
}

// CompletionRequest is what the eval runner asks of the LLM gateway.
type CompletionRequest struct {
	Messages    []ChatMessage
	Provider    string // optional; forces a specific provider
	Model       string
	Temperature float64
	MaxTokens   int
}

// CompletionResponse is the reduced completion result the runner consumes.
type CompletionResponse struct {
	Content     string
	Role        string
	TotalTokens int
	CostUSD     float64
}

// CompletionClient runs completions and embeddings. The only production
// implementation talks to delos-gateway over its OpenAI-compatible surface;
// the gateway never learns the control plane exists.
type CompletionClient interface {
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	Embed(ctx context.Context, texts []string, model string) ([][]float32, error)
}

// GatewayClient is a CompletionClient speaking the gateway's OpenAI surface.
type GatewayClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewGatewayClient creates a client for delos-gateway at baseURL
// (e.g. "http://localhost:8080").
func NewGatewayClient(baseURL, apiKey string) *GatewayClient {
	return &GatewayClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (g *GatewayClient) post(ctx context.Context, path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.apiKey)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error.Message != "" {
			return fmt.Errorf("gateway %s: %s (status %d)", path, envelope.Error.Message, resp.StatusCode)
		}
		return fmt.Errorf("gateway %s: status %d: %s", path, resp.StatusCode, data)
	}
	return json.Unmarshal(data, out)
}

func (g *GatewayClient) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	model := req.Model
	if req.Provider != "" {
		model = req.Provider + "/" + model
	}
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	messages := make([]msg, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = msg{Role: m.Role, Content: m.Content}
	}
	body := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": req.Temperature,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	var out struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int     `json:"total_tokens"`
			CostUSD     float64 `json:"cost_usd"`
		} `json:"usage"`
	}
	if err := g.post(ctx, "/v1/chat/completions", body, &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("gateway returned no choices")
	}
	return &CompletionResponse{
		Content:     out.Choices[0].Message.Content,
		Role:        out.Choices[0].Message.Role,
		TotalTokens: out.Usage.TotalTokens,
		CostUSD:     out.Usage.CostUSD,
	}, nil
}

func (g *GatewayClient) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	body := map[string]any{"model": model, "input": texts}
	if err := g.post(ctx, "/v1/embeddings", body, &out); err != nil {
		return nil, err
	}
	embeddings := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		embeddings[i] = d.Embedding
	}
	return embeddings, nil
}
