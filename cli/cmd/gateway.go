package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/instantcocoa/delos/cli/internal/output"
)

var gatewayCmd = &cobra.Command{
	Use:     "gateway",
	Aliases: []string{"runtime"},
	Short:   "Gateway (data plane) operations",
	Long: `Commands for completions, embeddings and model discovery against the
delos-gateway OpenAI-compatible HTTP API (DELOS_GATEWAY_URL).`,
}

// ---- HTTP plumbing ----

// gatewayURL joins the configured gateway base URL with an API path.
func gatewayURL(path string) string {
	return strings.TrimRight(cfg.GatewayURL, "/") + path
}

// openAIError is the error envelope returned by the gateway on failures.
type openAIError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// errorFromResponse turns a non-200 gateway response into an error, preferring
// the message carried in the OpenAI error envelope.
func errorFromResponse(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env openAIError
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		if env.Error.Code != "" {
			return fmt.Errorf("gateway error (%s): %s", env.Error.Code, env.Error.Message)
		}
		return fmt.Errorf("gateway error: %s", env.Error.Message)
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fmt.Errorf("gateway returned %s", resp.Status)
	}
	return fmt.Errorf("gateway returned %s: %s", resp.Status, trimmed)
}

// gatewayRequest issues a request to the gateway and returns the response. The
// caller owns resp.Body. Non-2xx responses are returned as errors.
func gatewayRequest(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, gatewayURL(path), body)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach gateway at %s: %w", cfg.GatewayURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, errorFromResponse(resp)
	}
	return resp, nil
}

// gatewayJSON issues a request and decodes the JSON response into out.
func gatewayJSON(ctx context.Context, method, path string, payload, out any) error {
	resp, err := gatewayRequest(ctx, method, path, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode gateway response: %w", err)
	}
	return nil
}

// ---- wire types ----

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

type chatUsage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
}

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

type modelsResponse struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Usage *chatUsage `json:"usage"`
}

// ---- commands ----

var gatewayCompleteCmd = &cobra.Command{
	Use:   "complete <message>",
	Short: "Generate a completion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		model, _ := cmd.Flags().GetString("model")
		system, _ := cmd.Flags().GetString("system")
		temperature, _ := cmd.Flags().GetFloat64("temperature")
		maxTokens, _ := cmd.Flags().GetInt("max-tokens")
		stream, _ := cmd.Flags().GetBool("stream")

		ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
		defer cancel()

		messages := make([]chatMessage, 0, 2)
		if system != "" {
			messages = append(messages, chatMessage{Role: "system", Content: system})
		}
		messages = append(messages, chatMessage{Role: "user", Content: args[0]})

		req := &chatRequest{
			Model:       model,
			Messages:    messages,
			Temperature: temperature,
			MaxTokens:   maxTokens,
			Stream:      stream,
		}

		if stream {
			return streamCompletion(ctx, req)
		}

		var resp chatResponse
		if err := gatewayJSON(ctx, http.MethodPost, "/v1/chat/completions", req, &resp); err != nil {
			return err
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp)
		}

		if len(resp.Choices) == 0 {
			return fmt.Errorf("gateway returned no choices")
		}
		fmt.Println(resp.Choices[0].Message.Content)
		if cfg.Verbose && resp.Usage != nil {
			fmt.Printf("\n---\nModel: %s | Tokens: %d (prompt %d, completion %d) | Cost: $%.4f\n",
				resp.Model, resp.Usage.TotalTokens, resp.Usage.PromptTokens,
				resp.Usage.CompletionTokens, resp.Usage.CostUSD)
		}
		return nil
	},
}

// streamCompletion posts a streaming completion and prints deltas as they
// arrive, parsing the OpenAI SSE framing.
func streamCompletion(ctx context.Context, req *chatRequest) error {
	resp, err := gatewayRequest(ctx, http.MethodPost, "/v1/chat/completions", req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk chatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip frames we cannot parse rather than aborting the stream.
			continue
		}
		for _, choice := range chunk.Choices {
			fmt.Print(choice.Delta.Content)
		}
	}
	fmt.Println()

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream error: %w", err)
	}
	return nil
}

var gatewayModelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List available models",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		var resp modelsResponse
		if err := gatewayJSON(ctx, http.MethodGet, "/v1/models", nil, &resp); err != nil {
			return err
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp.Data)
		}

		table := output.Table{
			Headers: []string{"ID", "OWNED BY"},
			Rows:    make([][]string, len(resp.Data)),
		}
		for i, m := range resp.Data {
			table.Rows[i] = []string{m.ID, m.OwnedBy}
		}

		w := output.NewWriter("table")
		return w.Print(table)
	},
}

var gatewayEmbedCmd = &cobra.Command{
	Use:   "embed <text>",
	Short: "Generate embeddings for text",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		model, _ := cmd.Flags().GetString("model")

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		var resp embeddingsResponse
		req := &embeddingsRequest{Model: model, Input: args}
		if err := gatewayJSON(ctx, http.MethodPost, "/v1/embeddings", req, &resp); err != nil {
			return err
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp)
		}

		output.Info("Generated %d embeddings", len(resp.Data))
		output.Info("Model: %s", resp.Model)
		if resp.Usage != nil {
			output.Info("Tokens: %d | Cost: $%.4f", resp.Usage.TotalTokens, resp.Usage.CostUSD)
		}
		for i, e := range resp.Data {
			output.Info("Embedding %d: %d dimensions %s", i+1, len(e.Embedding), previewValues(e.Embedding, 5))
		}
		return nil
	},
}

// previewValues renders the first n values of an embedding vector.
func previewValues(values []float64, n int) string {
	if len(values) == 0 {
		return "[]"
	}
	if n > len(values) {
		n = len(values)
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("%.6f", values[i])
	}
	suffix := ""
	if len(values) > n {
		suffix = ", ..."
	}
	return "[" + strings.Join(parts, ", ") + suffix + "]"
}

var gatewayHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check gateway health",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()

		var resp map[string]any
		if err := gatewayJSON(ctx, http.MethodGet, "/healthz", nil, &resp); err != nil {
			return err
		}

		if cfg.Format == "json" || cfg.Format == "yaml" {
			w := output.NewWriter(cfg.Format)
			return w.Print(resp)
		}

		status, _ := resp["status"].(string)
		if status == "" {
			status = "unknown"
		}
		output.Success("Gateway at %s is %s", cfg.GatewayURL, status)
		if providers, ok := resp["providers"].(float64); ok {
			output.Info("Providers configured: %d", int(providers))
		}
		return nil
	},
}

func init() {
	// Complete flags
	gatewayCompleteCmd.Flags().String("model", "", "Model to use, e.g. gpt-4o or openai/gpt-4o (required)")
	gatewayCompleteCmd.Flags().String("system", "", "System prompt")
	gatewayCompleteCmd.Flags().Float64("temperature", 0.7, "Temperature")
	gatewayCompleteCmd.Flags().Int("max-tokens", 1024, "Max tokens")
	gatewayCompleteCmd.Flags().Bool("stream", false, "Stream response")
	_ = gatewayCompleteCmd.MarkFlagRequired("model")

	// Embed flags
	gatewayEmbedCmd.Flags().String("model", "text-embedding-3-small", "Embedding model to use")

	gatewayCmd.AddCommand(gatewayCompleteCmd)
	gatewayCmd.AddCommand(gatewayModelsCmd)
	gatewayCmd.AddCommand(gatewayEmbedCmd)
	gatewayCmd.AddCommand(gatewayHealthCmd)
}
