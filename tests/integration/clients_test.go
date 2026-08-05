// Package integration contains integration tests for the two Delos binaries.
//
// Topology under test:
//
//	delos-gateway  HTTP  http://localhost:8080  (DELOS_GATEWAY_URL)
//	delos          gRPC  localhost:8081         (DELOS_CONTROL_PLANE_ADDR)
//
// The control plane serves observe, prompt, datasets, eval and deploy from a
// single gRPC port, so every control-plane helper below dials the same address.
// The gateway has no gRPC surface at all; it is exercised over plain HTTP.
//
// Run with: go test -tags=integration ./tests/integration/...
//
//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	deployv1 "github.com/instantcocoa/delos/gen/go/deploy/v1"
	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	observev1 "github.com/instantcocoa/delos/gen/go/observe/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// ============================================================================
// CONTROL PLANE (gRPC, one address for all five services)
// ============================================================================

// controlPlaneAddr returns the gRPC address of the delos control plane.
func controlPlaneAddr() string {
	if addr := os.Getenv("DELOS_CONTROL_PLANE_ADDR"); addr != "" {
		return addr
	}
	return "localhost:8081"
}

// dialControlPlane opens a gRPC connection to the control plane.
func dialControlPlane(t *testing.T) (*grpc.ClientConn, func()) {
	t.Helper()
	addr := controlPlaneAddr()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect to control plane at %s: %v", addr, err)
	}
	return conn, func() { conn.Close() }
}

func getObserveClient(t *testing.T) (observev1.ObserveServiceClient, func()) {
	t.Helper()
	conn, cleanup := dialControlPlane(t)
	return observev1.NewObserveServiceClient(conn), cleanup
}

func getPromptClient(t *testing.T) (promptv1.PromptServiceClient, func()) {
	t.Helper()
	conn, cleanup := dialControlPlane(t)
	return promptv1.NewPromptServiceClient(conn), cleanup
}

func getDatasetsClient(t *testing.T) (datasetsv1.DatasetsServiceClient, func()) {
	t.Helper()
	conn, cleanup := dialControlPlane(t)
	return datasetsv1.NewDatasetsServiceClient(conn), cleanup
}

func getEvalClient(t *testing.T) (evalv1.EvalServiceClient, func()) {
	t.Helper()
	conn, cleanup := dialControlPlane(t)
	return evalv1.NewEvalServiceClient(conn), cleanup
}

func getDeployClient(t *testing.T) (deployv1.DeployServiceClient, func()) {
	t.Helper()
	conn, cleanup := dialControlPlane(t)
	return deployv1.NewDeployServiceClient(conn), cleanup
}

// ============================================================================
// GATEWAY (HTTP, OpenAI/Anthropic-compatible surfaces)
// ============================================================================

// gatewayBaseURL returns the base URL of the delos-gateway data plane.
func gatewayBaseURL() string {
	if u := os.Getenv("DELOS_GATEWAY_URL"); u != "" {
		return u
	}
	return "http://localhost:8080"
}

// gatewayClient is a thin HTTP helper for the gateway surfaces. Tests use the
// wire format directly (net/http + encoding/json) so the compatibility
// contract is what is actually asserted.
type gatewayClient struct {
	baseURL string
	http    *http.Client
}

// newGatewayClient returns a gateway HTTP client with the given timeout.
func newGatewayClient(timeout time.Duration) *gatewayClient {
	return &gatewayClient{
		baseURL: gatewayBaseURL(),
		http:    &http.Client{Timeout: timeout},
	}
}

// get issues a GET and returns the status code and raw body.
func (g *gatewayClient) get(ctx context.Context, path string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+path, nil)
	if err != nil {
		return 0, nil, err
	}
	return g.do(req)
}

// postJSON issues a POST with a JSON body and returns the status code and raw body.
func (g *gatewayClient) postJSON(ctx context.Context, path string, payload any) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	return g.postRaw(ctx, path, body)
}

// postRaw issues a POST with an arbitrary (possibly malformed) JSON body.
func (g *gatewayClient) postRaw(ctx context.Context, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return g.do(req)
}

func (g *gatewayClient) do(req *http.Request) (int, []byte, error) {
	resp, err := g.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// postStream issues a POST and returns the open response for SSE consumption.
// The caller must close the returned body.
func (g *gatewayClient) postStream(ctx context.Context, path string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	return g.http.Do(req)
}

// ============================================================================
// GATEWAY WIRE TYPES (mirrors of the OpenAI surface)
// ============================================================================

type gwErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   string `json:"param,omitempty"`
		Code    string `json:"code,omitempty"`
	} `json:"error"`
}

type gwChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type gwChatRequest struct {
	Model       string          `json:"model"`
	Messages    []gwChatMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type gwUsage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type gwChatChoice struct {
	Index   int `json:"index"`
	Message *struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Delta *struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type gwChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []gwChatChoice `json:"choices"`
	Usage   *gwUsage       `json:"usage"`
}

// content returns the assistant content of the first choice.
func (r gwChatResponse) content() string {
	if len(r.Choices) == 0 || r.Choices[0].Message == nil {
		return ""
	}
	return r.Choices[0].Message.Content
}

type gwEmbeddingsRequest struct {
	Model          string `json:"model"`
	Input          any    `json:"input"`
	EncodingFormat string `json:"encoding_format,omitempty"`
}

type gwEmbeddingsResponse struct {
	Object string `json:"object"`
	Model  string `json:"model"`
	Data   []struct {
		Object    string    `json:"object"`
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Usage gwUsage `json:"usage"`
}

type gwModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type gwModelList struct {
	Object string    `json:"object"`
	Data   []gwModel `json:"data"`
}

type gwHealth struct {
	Status    string `json:"status"`
	Providers int    `json:"providers"`
}

// listGatewayModels fetches GET /v1/models and fails the test on transport or
// protocol errors.
func listGatewayModels(t *testing.T, ctx context.Context) gwModelList {
	t.Helper()
	g := newGatewayClient(15 * time.Second)
	status, body, err := g.get(ctx, "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models failed: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("GET /v1/models: expected 200, got %d: %s", status, body)
	}
	var list gwModelList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("GET /v1/models: invalid JSON: %v (body: %s)", err, body)
	}
	return list
}

// decodeGatewayError parses an OpenAI-style error envelope.
func decodeGatewayError(t *testing.T, body []byte) gwErrorEnvelope {
	t.Helper()
	var env gwErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("expected an OpenAI error envelope, got: %s", body)
	}
	return env
}
