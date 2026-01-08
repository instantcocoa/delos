package runtime

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// BedrockProvider implements the Provider interface for AWS Bedrock.
type BedrockProvider struct {
	accessKeyID     string
	secretAccessKey string
	sessionToken    string // Optional, for temporary credentials
	region          string
	httpClient      *http.Client
	models          []string
}

// NewBedrockProvider creates a new AWS Bedrock provider.
func NewBedrockProvider(accessKeyID, secretAccessKey, region string, opts ...BedrockOption) *BedrockProvider {
	if region == "" {
		region = "us-east-1"
	}
	p := &BedrockProvider{
		accessKeyID:     accessKeyID,
		secretAccessKey: secretAccessKey,
		region:          region,
		httpClient:      &http.Client{Timeout: 120 * time.Second},
		models: []string{
			// Anthropic Claude models
			"anthropic.claude-3-5-sonnet-20241022-v2:0",
			"anthropic.claude-3-5-haiku-20241022-v1:0",
			"anthropic.claude-3-sonnet-20240229-v1:0",
			"anthropic.claude-3-haiku-20240307-v1:0",
			"anthropic.claude-3-opus-20240229-v1:0",
			// Meta Llama models
			"meta.llama3-1-405b-instruct-v1:0",
			"meta.llama3-1-70b-instruct-v1:0",
			"meta.llama3-1-8b-instruct-v1:0",
			// Amazon Titan
			"amazon.titan-text-premier-v1:0",
			"amazon.titan-text-express-v1",
			// Mistral
			"mistral.mistral-large-2407-v1:0",
			"mistral.mixtral-8x7b-instruct-v0:1",
			// Cohere
			"cohere.command-r-plus-v1:0",
			"cohere.command-r-v1:0",
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// BedrockOption configures the Bedrock provider.
type BedrockOption func(*BedrockProvider)

// WithSessionToken sets the session token for temporary credentials.
func WithSessionToken(token string) BedrockOption {
	return func(p *BedrockProvider) {
		p.sessionToken = token
	}
}

func (p *BedrockProvider) Name() string {
	return "bedrock"
}

func (p *BedrockProvider) Models() []string {
	return p.models
}

func (p *BedrockProvider) Available(ctx context.Context) bool {
	return p.accessKeyID != "" && p.secretAccessKey != ""
}

func (p *BedrockProvider) CostPer1KTokens() map[string]float64 {
	// Bedrock pricing (approximate, varies by region)
	return map[string]float64{
		"anthropic.claude-3-5-sonnet-20241022-v2:0": 0.003,
		"anthropic.claude-3-5-haiku-20241022-v1:0":  0.0008,
		"anthropic.claude-3-sonnet-20240229-v1:0":   0.003,
		"anthropic.claude-3-haiku-20240307-v1:0":    0.00025,
		"anthropic.claude-3-opus-20240229-v1:0":     0.015,
		"meta.llama3-1-405b-instruct-v1:0":          0.00265,
		"meta.llama3-1-70b-instruct-v1:0":           0.00099,
		"meta.llama3-1-8b-instruct-v1:0":            0.00022,
		"amazon.titan-text-premier-v1:0":            0.0005,
		"amazon.titan-text-express-v1":              0.0002,
		"mistral.mistral-large-2407-v1:0":           0.002,
		"mistral.mixtral-8x7b-instruct-v0:1":        0.00045,
		"cohere.command-r-plus-v1:0":                0.003,
		"cohere.command-r-v1:0":                     0.0005,
	}
}

// Bedrock Converse API types
type bedrockMessage struct {
	Role    string                `json:"role"`
	Content []bedrockContentBlock `json:"content"`
}

type bedrockContentBlock struct {
	Text string `json:"text,omitempty"`
}

type bedrockInferenceConfig struct {
	MaxTokens   int      `json:"maxTokens,omitempty"`
	Temperature float64  `json:"temperature,omitempty"`
	TopP        float64  `json:"topP,omitempty"`
	StopSeqs    []string `json:"stopSequences,omitempty"`
}

type bedrockConverseRequest struct {
	Messages        []bedrockMessage        `json:"messages"`
	System          []bedrockContentBlock   `json:"system,omitempty"`
	InferenceConfig *bedrockInferenceConfig `json:"inferenceConfig,omitempty"`
}

type bedrockConverseResponse struct {
	Output struct {
		Message bedrockMessage `json:"message"`
	} `json:"output"`
	StopReason string `json:"stopReason"`
	Usage      struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
		TotalTokens  int `json:"totalTokens"`
	} `json:"usage"`
}

func (p *BedrockProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	model := params.Model
	if model == "" {
		model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	}

	// Build messages, extracting system message
	var systemBlocks []bedrockContentBlock
	var messages []bedrockMessage

	for _, m := range params.Messages {
		if m.Role == "system" {
			systemBlocks = append(systemBlocks, bedrockContentBlock{Text: m.Content})
			continue
		}
		messages = append(messages, bedrockMessage{
			Role:    m.Role,
			Content: []bedrockContentBlock{{Text: m.Content}},
		})
	}

	reqBody := bedrockConverseRequest{
		Messages: messages,
		System:   systemBlocks,
	}

	// Add inference config if any params set
	if params.MaxTokens > 0 || params.Temperature > 0 || params.TopP > 0 || len(params.Stop) > 0 {
		reqBody.InferenceConfig = &bedrockInferenceConfig{
			MaxTokens:   params.MaxTokens,
			Temperature: params.Temperature,
			TopP:        params.TopP,
			StopSeqs:    params.Stop,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build endpoint URL
	endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse", p.region, model)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Sign request with AWS Signature V4
	if err := p.signRequest(req, body); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bedrock API error: status %d: %s", resp.StatusCode, string(respBody))
	}

	var bedrockResp bedrockConverseResponse
	if err := json.Unmarshal(respBody, &bedrockResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Extract text content
	var content string
	for _, block := range bedrockResp.Output.Message.Content {
		if block.Text != "" {
			content += block.Text
		}
	}

	cost := p.calculateCost(model, bedrockResp.Usage.TotalTokens)

	return &CompletionResult{
		ID:      "", // Bedrock doesn't return an ID
		Content: content,
		Message: Message{
			Role:    bedrockResp.Output.Message.Role,
			Content: content,
		},
		Provider: p.Name(),
		Model:    model,
		Usage: Usage{
			PromptTokens:     bedrockResp.Usage.InputTokens,
			CompletionTokens: bedrockResp.Usage.OutputTokens,
			TotalTokens:      bedrockResp.Usage.TotalTokens,
			CostUSD:          cost,
		},
	}, nil
}

func (p *BedrockProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	model := params.Model
	if model == "" {
		model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	}

	// Build messages
	var systemBlocks []bedrockContentBlock
	var messages []bedrockMessage

	for _, m := range params.Messages {
		if m.Role == "system" {
			systemBlocks = append(systemBlocks, bedrockContentBlock{Text: m.Content})
			continue
		}
		messages = append(messages, bedrockMessage{
			Role:    m.Role,
			Content: []bedrockContentBlock{{Text: m.Content}},
		})
	}

	reqBody := bedrockConverseRequest{
		Messages: messages,
		System:   systemBlocks,
	}

	if params.MaxTokens > 0 || params.Temperature > 0 || params.TopP > 0 || len(params.Stop) > 0 {
		reqBody.InferenceConfig = &bedrockInferenceConfig{
			MaxTokens:   params.MaxTokens,
			Temperature: params.Temperature,
			TopP:        params.TopP,
			StopSeqs:    params.Stop,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Use converse-stream endpoint
	endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse-stream", p.region, model)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if err := p.signRequest(req, body); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Bedrock API error: status %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamChunk, 100)
	go p.streamResponse(ctx, resp, model, ch)
	return ch, nil
}

func (p *BedrockProvider) streamResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
	defer close(ch)
	defer resp.Body.Close()

	var fullContent strings.Builder
	var inputTokens, outputTokens int

	// Bedrock uses AWS event stream format
	// Parse event stream - simplified parser for the text events
	decoder := json.NewDecoder(resp.Body)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var event map[string]interface{}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			// Report decode errors but try to continue
			ch <- StreamChunk{
				Err:      fmt.Errorf("failed to decode stream event: %w", err),
				Provider: p.Name(),
				Model:    model,
			}
			continue
		}

		// Handle content block delta
		if delta, ok := event["contentBlockDelta"].(map[string]interface{}); ok {
			if deltaContent, ok := delta["delta"].(map[string]interface{}); ok {
				if text, ok := deltaContent["text"].(string); ok && text != "" {
					fullContent.WriteString(text)
					ch <- StreamChunk{
						Delta:    text,
						Done:     false,
						Provider: p.Name(),
						Model:    model,
					}
				}
			}
		}

		// Handle metadata (usage)
		if metadata, ok := event["metadata"].(map[string]interface{}); ok {
			if usage, ok := metadata["usage"].(map[string]interface{}); ok {
				if v, ok := usage["inputTokens"].(float64); ok {
					inputTokens = int(v)
				}
				if v, ok := usage["outputTokens"].(float64); ok {
					outputTokens = int(v)
				}
			}
		}
	}

	totalTokens := inputTokens + outputTokens
	cost := p.calculateCost(model, totalTokens)

	ch <- StreamChunk{
		Done:     true,
		Provider: p.Name(),
		Model:    model,
		Message: &Message{
			Role:    "assistant",
			Content: fullContent.String(),
		},
		Usage: &Usage{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      totalTokens,
			CostUSD:          cost,
		},
	}
}

func (p *BedrockProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := params.Model
	if model == "" {
		model = "amazon.titan-embed-text-v2:0"
	}

	embeddings := make([]Embedding, 0, len(params.Texts))

	for _, text := range params.Texts {
		reqBody := map[string]interface{}{
			"inputText": text,
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke", p.region, model)

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		if err := p.signRequest(req, body); err != nil {
			return nil, fmt.Errorf("failed to sign request: %w", err)
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Bedrock API error: status %d: %s", resp.StatusCode, string(respBody))
		}

		var embedResp struct {
			Embedding []float32 `json:"embedding"`
		}

		if err := json.Unmarshal(respBody, &embedResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		embeddings = append(embeddings, Embedding{
			Values:     embedResp.Embedding,
			Dimensions: len(embedResp.Embedding),
		})
	}

	return &EmbedResult{
		Embeddings: embeddings,
		Model:      model,
		Provider:   p.Name(),
	}, nil
}

func (p *BedrockProvider) calculateCost(model string, totalTokens int) float64 {
	costs := p.CostPer1KTokens()
	costPer1K, ok := costs[model]
	if !ok {
		costPer1K = 0.001
	}
	return float64(totalTokens) / 1000 * costPer1K
}

// signRequest signs an HTTP request using AWS Signature V4
func (p *BedrockProvider) signRequest(req *http.Request, payload []byte) error {
	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzdate := now.Format("20060102T150405Z")

	service := "bedrock"
	host := req.URL.Host

	// Create canonical request
	method := req.Method
	canonicalURI := req.URL.Path
	canonicalQuerystring := req.URL.RawQuery

	// Create payload hash
	payloadHash := sha256Hash(payload)

	// Set required headers
	req.Header.Set("Host", host)
	req.Header.Set("X-Amz-Date", amzdate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if p.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", p.sessionToken)
	}

	// Create signed headers list
	signedHeaders := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	if p.sessionToken != "" {
		signedHeaders = append(signedHeaders, "x-amz-security-token")
	}
	sort.Strings(signedHeaders)
	signedHeadersStr := strings.Join(signedHeaders, ";")

	// Create canonical headers
	var canonicalHeaders strings.Builder
	for _, h := range signedHeaders {
		var val string
		switch h {
		case "host":
			val = host
		case "content-type":
			val = req.Header.Get("Content-Type")
		case "x-amz-date":
			val = amzdate
		case "x-amz-content-sha256":
			val = payloadHash
		case "x-amz-security-token":
			val = p.sessionToken
		}
		canonicalHeaders.WriteString(h)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(val)
		canonicalHeaders.WriteString("\n")
	}

	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQuerystring,
		canonicalHeaders.String(),
		signedHeadersStr,
		payloadHash,
	}, "\n")

	// Create string to sign
	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", datestamp, p.region, service)
	stringToSign := strings.Join([]string{
		algorithm,
		amzdate,
		credentialScope,
		sha256Hash([]byte(canonicalRequest)),
	}, "\n")

	// Calculate signature
	kDate := hmacSHA256([]byte("AWS4"+p.secretAccessKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(p.region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	// Create authorization header
	authHeader := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, p.accessKeyID, credentialScope, signedHeadersStr, signature)
	req.Header.Set("Authorization", authHeader)

	return nil
}

func sha256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
