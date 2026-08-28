// Package integration contains comprehensive integration tests for all Delos services.
//
//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	deployv1 "github.com/instantcocoa/delos/gen/go/deploy/v1"
	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	observev1 "github.com/instantcocoa/delos/gen/go/observe/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// Control-plane client helpers (observe, prompt, datasets, eval, deploy) and
// the gateway HTTP helper live in clients_test.go.

// ============================================================================
// CONTROL PLANE HEALTH (all five modules, one address)
// ============================================================================

// TestControlPlane_Health asserts every module hosted by `delos serve` reports
// itself healthy. All five share one gRPC address, so this is one table rather
// than five near-identical tests that only checked err == nil.
func TestControlPlane_Health(t *testing.T) {
	conn, cleanup := dialControlPlane(t)
	defer cleanup()

	type health struct{ status, version string }

	modules := []struct {
		name string
		call func(context.Context) (health, error)
	}{
		{"observe", func(ctx context.Context) (health, error) {
			r, err := observev1.NewObserveServiceClient(conn).Health(ctx, &observev1.HealthRequest{})
			if err != nil {
				return health{}, err
			}
			return health{r.GetStatus(), r.GetVersion()}, nil
		}},
		{"prompt", func(ctx context.Context) (health, error) {
			r, err := promptv1.NewPromptServiceClient(conn).Health(ctx, &promptv1.HealthRequest{})
			if err != nil {
				return health{}, err
			}
			return health{r.GetStatus(), r.GetVersion()}, nil
		}},
		{"datasets", func(ctx context.Context) (health, error) {
			r, err := datasetsv1.NewDatasetsServiceClient(conn).Health(ctx, &datasetsv1.HealthRequest{})
			if err != nil {
				return health{}, err
			}
			return health{r.GetStatus(), r.GetVersion()}, nil
		}},
		{"eval", func(ctx context.Context) (health, error) {
			r, err := evalv1.NewEvalServiceClient(conn).Health(ctx, &evalv1.HealthRequest{})
			if err != nil {
				return health{}, err
			}
			return health{r.GetStatus(), r.GetVersion()}, nil
		}},
		{"deploy", func(ctx context.Context) (health, error) {
			r, err := deployv1.NewDeployServiceClient(conn).Health(ctx, &deployv1.HealthRequest{})
			if err != nil {
				return health{}, err
			}
			return health{r.GetStatus(), r.GetVersion()}, nil
		}},
	}

	for _, m := range modules {
		t.Run(m.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			got, err := m.call(ctx)
			if err != nil {
				t.Fatalf("%s Health failed: %v", m.name, err)
			}
			if got.status != "healthy" {
				t.Errorf("%s: expected status \"healthy\", got %q", m.name, got.status)
			}
			if got.version == "" {
				t.Errorf("%s: expected Health to report a version", m.name)
			}
		})
	}
}

// TestControlPlane_Healthz asserts the plain-HTTP liveness probe that
// orchestrators use, served on the same address as gRPC.
func TestControlPlane_Healthz(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	httpClient := &http.Client{Timeout: 10 * time.Second}
	code, body := getJSON(t, ctx, httpClient, controlPlaneHTTPURL("/healthz"))
	if code != http.StatusOK {
		t.Fatalf("GET /healthz: expected 200, got %d: %s", code, body)
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("GET /healthz: invalid JSON: %v (body: %s)", err, body)
	}
	if health.Status == "" {
		t.Errorf("GET /healthz: expected a status field, got: %s", body)
	}
}

// ============================================================================
// OBSERVE SERVICE TESTS
// ============================================================================

// TestObserveService_IngestQueryAndGetTrace is a round trip: spans go in, and
// the same spans must come back out by trace id and through the service-name
// filter. Ingest, QueryTraces and GetTrace used to be three tests that only
// logged whatever the server returned.
func TestObserveService_IngestQueryAndGetTrace(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	traceID := fmt.Sprintf("itest-trace-%d", time.Now().UnixNano())
	service := fmt.Sprintf("itest-svc-%d", time.Now().UnixNano())
	start := timestamppb.Now()

	spans := []*observev1.Span{
		{
			TraceId:     traceID,
			SpanId:      "span-root",
			Name:        "itest-root",
			ServiceName: service,
			StartTime:   start,
			Duration:    durationpb.New(50 * time.Millisecond),
		},
		{
			TraceId:      traceID,
			SpanId:       "span-child",
			ParentSpanId: "span-root",
			Name:         "itest-child",
			ServiceName:  service,
			StartTime:    start,
			Duration:     durationpb.New(20 * time.Millisecond),
		},
	}

	ingestResp, err := client.IngestTraces(ctx, &observev1.IngestTracesRequest{Spans: spans})
	if err != nil {
		t.Fatalf("IngestTraces failed: %v", err)
	}
	if got := ingestResp.AcceptedCount; got != int32(len(spans)) {
		t.Fatalf("IngestTraces accepted %d spans, sent %d", got, len(spans))
	}

	// GetTrace must return exactly what was ingested, assembled into one trace.
	getResp, err := client.GetTrace(ctx, &observev1.GetTraceRequest{TraceId: traceID})
	if err != nil {
		t.Fatalf("GetTrace(%s) failed after ingesting it: %v", traceID, err)
	}
	if getResp.Trace == nil {
		t.Fatalf("GetTrace(%s) returned no trace after ingesting 2 spans", traceID)
	}
	if getResp.Trace.TraceId != traceID {
		t.Errorf("GetTrace returned trace %q, want %q", getResp.Trace.TraceId, traceID)
	}
	if got := len(getResp.Trace.Spans); got != len(spans) {
		t.Errorf("GetTrace returned %d spans, want %d", got, len(spans))
	}
	names := map[string]bool{}
	for _, s := range getResp.Trace.Spans {
		names[s.Name] = true
		if s.TraceId != traceID {
			t.Errorf("span %s carries trace id %q, want %q", s.SpanId, s.TraceId, traceID)
		}
	}
	for _, want := range []string{"itest-root", "itest-child"} {
		if !names[want] {
			t.Errorf("GetTrace did not return the %q span", want)
		}
	}

	// The service-name filter must be honoured, not ignored.
	queryResp, err := client.QueryTraces(ctx, &observev1.QueryTracesRequest{
		ServiceName: service,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("QueryTraces failed: %v", err)
	}
	found := false
	for _, tr := range queryResp.Traces {
		if tr.TraceId == traceID {
			found = true
		}
		for _, s := range tr.Spans {
			if s.ServiceName != "" && s.ServiceName != service {
				t.Errorf("QueryTraces(service=%q) returned a span from service %q",
					service, s.ServiceName)
			}
		}
	}
	if !found {
		t.Errorf("QueryTraces(service=%q) did not return the trace just ingested (%s); got %d traces",
			service, traceID, len(queryResp.Traces))
	}
}

// TestObserveService_GetTrace_NotFound pins the error for an unknown trace id.
func TestObserveService_GetTrace_NotFound(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetTrace(ctx, &observev1.GetTraceRequest{
		TraceId: fmt.Sprintf("no-such-trace-%d", time.Now().UnixNano()),
	})
	if err == nil {
		if resp.GetTrace() != nil {
			t.Fatalf("expected no trace for an unknown id, got %+v", resp.GetTrace())
		}
		return
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("expected NOT_FOUND for an unknown trace id, got %s: %s", st.Code(), st.Message())
	}
}

// TestObserveService_QueryMetrics asserts the returned series respects the
// requested time window, rather than just that the call did not error.
func TestObserveService_QueryMetrics(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now().Add(-time.Hour)
	end := time.Now()

	resp, err := client.QueryMetrics(ctx, &observev1.QueryMetricsRequest{
		MetricName: "request_count",
		StartTime:  timestamppb.New(start),
		EndTime:    timestamppb.New(end),
	})
	if err != nil {
		t.Fatalf("QueryMetrics failed: %v", err)
	}
	for i, dp := range resp.DataPoints {
		if dp.Timestamp == nil {
			t.Errorf("data point %d has no timestamp", i)
			continue
		}
		ts := dp.Timestamp.AsTime()
		if ts.Before(start.Add(-time.Minute)) || ts.After(end.Add(time.Minute)) {
			t.Errorf("data point %d at %s falls outside the requested window [%s, %s]",
				i, ts, start, end)
		}
	}
}

// ============================================================================
// GATEWAY TESTS (HTTP data plane: OpenAI + Anthropic surfaces)
// ============================================================================

func TestGateway_Healthz(t *testing.T) {
	g := newGatewayClient(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, body, err := g.get(ctx, "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("GET /healthz: expected 200, got %d: %s", status, body)
	}

	var health gwHealth
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("GET /healthz: invalid JSON: %v (body: %s)", err, body)
	}
	if health.Status == "" {
		t.Errorf("expected a status field, got: %s", body)
	}

	// The provider count must agree with what /v1/models advertises: zero
	// providers means zero models, and vice versa.
	list := listGatewayModels(t, ctx)
	if (health.Providers == 0) != (len(list.Data) == 0) {
		t.Errorf("/healthz reports %d providers but /v1/models returns %d models",
			health.Providers, len(list.Data))
	}
}

func TestGateway_ListModels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)
	if list.Object != "list" {
		t.Errorf("expected object=list, got %q", list.Object)
	}
	seen := map[string]bool{}
	for _, m := range list.Data {
		if m.ID == "" {
			t.Error("model entry with an empty id")
		}
		if seen[m.ID] {
			t.Errorf("model %s advertised more than once", m.ID)
		}
		seen[m.ID] = true
		if m.Object != "model" {
			t.Errorf("model %s: expected object=model, got %q", m.ID, m.Object)
		}
		if m.OwnedBy == "" {
			t.Errorf("model %s: expected owned_by to name the provider", m.ID)
		}
	}
}

// TestGateway_GetModel asserts the single-model endpoint's own contract: a
// known id round-trips with a complete model object, and an unknown id is a 404
// carrying the OpenAI-style model_not_found envelope.
func TestGateway_GetModel(t *testing.T) {
	g := newGatewayClient(15 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)
	if len(list.Data) > 0 {
		want := list.Data[0]
		code, body, err := g.get(ctx, "/v1/models/"+want.ID)
		if err != nil {
			t.Fatalf("GET /v1/models/%s failed: %v", want.ID, err)
		}
		if code != http.StatusOK {
			t.Fatalf("GET /v1/models/%s: expected 200, got %d: %s", want.ID, code, body)
		}
		var got gwModel
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("invalid JSON: %v (body: %s)", err, body)
		}
		if got.ID != want.ID {
			t.Errorf("expected model id %q, got %q", want.ID, got.ID)
		}
		if got.Object != "model" {
			t.Errorf("expected object=model, got %q", got.Object)
		}
		if got.OwnedBy == "" {
			t.Errorf("expected owned_by to name the provider, got an empty string")
		}
		if got.Created <= 0 {
			t.Errorf("expected a positive created timestamp, got %d", got.Created)
		}
	}

	// Unknown model: 404 with the OpenAI error envelope. This holds whether or
	// not any provider is configured.
	unknown := fmt.Sprintf("no-such-model-%d", time.Now().UnixNano())
	code, body, err := g.get(ctx, "/v1/models/"+unknown)
	if err != nil {
		t.Fatalf("GET /v1/models/%s failed: %v", unknown, err)
	}
	if code != http.StatusNotFound {
		t.Fatalf("GET /v1/models/%s: expected 404, got %d: %s", unknown, code, body)
	}
	if env := decodeGatewayError(t, body); env.Error.Code != "model_not_found" {
		t.Errorf("expected code model_not_found, got %q", env.Error.Code)
	}
}

func TestGateway_ChatCompletions(t *testing.T) {
	model := requireGatewayModel(t)

	g := newGatewayClient(90 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	code, body, err := g.postJSON(ctx, "/v1/chat/completions", gwChatRequest{
		Model:     model,
		Messages:  []gwChatMessage{{Role: "user", Content: "Say hello"}},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("POST /v1/chat/completions failed: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions with configured model %q: expected 200, got %d: %s",
			model, code, body)
	}

	var resp gwChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v (body: %s)", err, body)
	}
	if resp.ID == "" {
		t.Error("expected a completion id")
	}
	if resp.Object != "chat.completion" {
		t.Errorf("expected object=chat.completion, got %q", resp.Object)
	}
	if len(resp.Choices) == 0 {
		t.Fatalf("expected at least one choice, got: %s", body)
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Role != "assistant" {
		t.Fatalf("expected an assistant message, got: %s", body)
	}
	if strings.TrimSpace(resp.content()) == "" {
		t.Error("expected non-empty assistant content")
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason == "" {
		t.Error("expected a finish_reason on the completed choice")
	}
	if resp.Usage == nil {
		t.Fatal("expected usage accounting on a non-streaming completion")
	}
	if resp.Usage.PromptTokens <= 0 || resp.Usage.CompletionTokens <= 0 {
		t.Errorf("expected non-zero prompt and completion tokens, got prompt=%d completion=%d",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}
	if want := resp.Usage.PromptTokens + resp.Usage.CompletionTokens; resp.Usage.TotalTokens != want {
		t.Errorf("total_tokens=%d does not equal prompt+completion=%d", resp.Usage.TotalTokens, want)
	}
}

func TestGateway_ChatCompletions_Stream(t *testing.T) {
	model := requireGatewayModel(t)

	g := newGatewayClient(90 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := g.postStream(ctx, "/v1/chat/completions", gwChatRequest{
		Model:     model,
		Messages:  []gwChatMessage{{Role: "user", Content: "Say hello"}},
		MaxTokens: 20,
		Stream:    true,
	})
	if err != nil {
		t.Fatalf("streaming POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("streaming completion with configured model %q: expected 200, got %d: %s",
			model, resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	content, chunks, done := readSSEChatStream(t, resp)
	if chunks == 0 {
		t.Error("expected at least one SSE chunk")
	}
	if !done {
		t.Error("expected the stream to terminate with the [DONE] sentinel")
	}
	if strings.TrimSpace(content) == "" {
		t.Errorf("expected the streamed deltas to carry content, got %q", content)
	}
}

func TestGateway_Embeddings(t *testing.T) {
	model := gatewayEmbeddingModel(t)
	if model == "" {
		t.Skip("no embedding-capable model advertised by the gateway - " +
			"configure an embedding provider to exercise /v1/embeddings")
	}

	g := newGatewayClient(60 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	code, body, err := g.postJSON(ctx, "/v1/embeddings", gwEmbeddingsRequest{
		Model: model,
		Input: "hello world",
	})
	if err != nil {
		t.Fatalf("POST /v1/embeddings failed: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("POST /v1/embeddings with advertised model %q: expected 200, got %d: %s",
			model, code, body)
	}

	var resp gwEmbeddingsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v (body: %s)", err, body)
	}
	if resp.Object != "list" {
		t.Errorf("expected object=list, got %q", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 embedding for a single input, got %d", len(resp.Data))
	}
	if len(resp.Data[0].Embedding) == 0 {
		t.Error("expected a non-empty embedding vector")
	}
	if resp.Data[0].Object != "embedding" {
		t.Errorf("expected object=embedding on the data entry, got %q", resp.Data[0].Object)
	}
	if resp.Usage.PromptTokens <= 0 {
		t.Errorf("expected prompt token accounting, got %d", resp.Usage.PromptTokens)
	}
}

// ---- gateway test helpers ----

// gatewayEmbeddingModel returns a model that looks like an embedding model.
func gatewayEmbeddingModel(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)
	for _, m := range list.Data {
		if strings.Contains(strings.ToLower(m.ID), "embed") {
			return m.ID
		}
	}
	return ""
}

// readSSEChatStream consumes an OpenAI-style SSE chat stream, returning the
// concatenated delta content, the number of data chunks, and whether the
// terminating [DONE] sentinel was seen.
func readSSEChatStream(t *testing.T, resp *http.Response) (string, int, bool) {
	t.Helper()
	var content strings.Builder
	var chunks int
	var done bool

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk gwChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Errorf("malformed SSE chunk %q: %v", data, err)
			continue
		}
		chunks++
		for _, c := range chunk.Choices {
			if c.Delta != nil {
				content.WriteString(c.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Errorf("SSE stream ended with a read error: %v", err)
	}
	return content.String(), chunks, done
}

// ============================================================================
// DEPLOY SERVICE TESTS (quality gates only)
// ============================================================================
//
// Delos does not deploy anything. The deploy service holds quality gates and
// answers one question: does the latest completed eval run for a prompt satisfy
// this gate's conditions? CI systems act on the verdict.

// TestDeployService_GateLifecycle covers create -> list (unfiltered and
// filtered) -> verdict for a prompt that has never been evaluated.
func TestDeployService_GateLifecycle(t *testing.T) {
	deployClient, deployCleanup := getDeployClient(t)
	defer deployCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	timestamp := time.Now().UnixNano()

	// A gate names a prompt; create one to point at.
	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Gate Test Prompt",
		Slug: fmt.Sprintf("gate-test-%d", timestamp),
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Test prompt"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	promptID := promptResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})

	// CreateQualityGate
	gateName := fmt.Sprintf("gate-lifecycle-%d", timestamp)
	createResp, err := deployClient.CreateQualityGate(ctx, &deployv1.CreateQualityGateRequest{
		Name:        gateName,
		Description: "Gate created by TestDeployService_GateLifecycle",
		PromptId:    promptID,
		Conditions: []*deployv1.GateCondition{
			{
				Metric:    "overall_score",
				Operator:  deployv1.GateOperator_GATE_OPERATOR_GTE,
				Threshold: 0.8,
			},
			{
				Metric:    "avg_latency_ms",
				Operator:  deployv1.GateOperator_GATE_OPERATOR_LTE,
				Threshold: 5000,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateQualityGate failed: %v", err)
	}
	gate := createResp.QualityGate
	if gate.GetId() == "" {
		t.Error("expected the created gate to have an id")
	}
	if gate.GetName() != gateName {
		t.Errorf("expected gate name %q, got %q", gateName, gate.GetName())
	}
	if got := len(gate.GetConditions()); got != 2 {
		t.Errorf("expected 2 conditions on the created gate, got %d", got)
	}

	// ListQualityGates filtered by prompt - must contain exactly our gate.
	filtered, err := deployClient.ListQualityGates(ctx, &deployv1.ListQualityGatesRequest{
		PromptId: promptID,
	})
	if err != nil {
		t.Fatalf("ListQualityGates (filtered) failed: %v", err)
	}
	found := false
	for _, g := range filtered.QualityGates {
		if g.GetPromptId() != promptID {
			t.Errorf("filtered list returned a gate for prompt %q, want %q", g.GetPromptId(), promptID)
		}
		if g.GetName() == gateName {
			found = true
		}
	}
	if !found {
		t.Errorf("gate %q not returned by ListQualityGates(prompt_id=%s)", gateName, promptID)
	}

	// ListQualityGates without a filter - must be a superset.
	all, err := deployClient.ListQualityGates(ctx, &deployv1.ListQualityGatesRequest{})
	if err != nil {
		t.Fatalf("ListQualityGates (unfiltered) failed: %v", err)
	}
	if len(all.QualityGates) < len(filtered.QualityGates) {
		t.Errorf("unfiltered list (%d) is smaller than the filtered list (%d)",
			len(all.QualityGates), len(filtered.QualityGates))
	}
	foundInAll := false
	for _, g := range all.QualityGates {
		if g.GetName() == gateName {
			foundInAll = true
			break
		}
	}
	if !foundInAll {
		t.Errorf("gate %q not returned by an unfiltered ListQualityGates", gateName)
	}

	// GetGateVerdict with no eval runs: not a pass, and the reason says why.
	verdict, err := deployClient.GetGateVerdict(ctx, &deployv1.GetGateVerdictRequest{Name: gateName})
	if err != nil {
		t.Fatalf("GetGateVerdict failed: %v", err)
	}
	if verdict.Pass {
		t.Error("expected pass=false for a gate whose prompt has no eval runs")
	}
	if len(verdict.Reasons) == 0 {
		t.Error("expected an explanatory reason when no eval run exists")
	}
	if verdict.EvalRunId != "" {
		t.Errorf("expected an empty eval_run_id with no runs, got %q", verdict.EvalRunId)
	}
	joined := strings.ToLower(strings.Join(verdict.Reasons, " "))
	if !strings.Contains(joined, "eval run") {
		t.Errorf("expected the reason to mention the missing eval run, got %v", verdict.Reasons)
	}
	if verdict.Gate.GetName() != gateName {
		t.Errorf("verdict echoed gate %q, want %q", verdict.Gate.GetName(), gateName)
	}
}

// TestDeployService_CreateQualityGate_Validation rejects gates that cannot be
// evaluated: no conditions, no prompt, unknown metric.
func TestDeployService_CreateQualityGate_Validation(t *testing.T) {
	client, cleanup := getDeployClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	timestamp := time.Now().UnixNano()

	tests := []struct {
		name string
		req  *deployv1.CreateQualityGateRequest
	}{
		{
			name: "no conditions",
			req: &deployv1.CreateQualityGateRequest{
				Name:     fmt.Sprintf("invalid-no-conditions-%d", timestamp),
				PromptId: "some-prompt",
			},
		},
		{
			name: "no prompt",
			req: &deployv1.CreateQualityGateRequest{
				Name: fmt.Sprintf("invalid-no-prompt-%d", timestamp),
				Conditions: []*deployv1.GateCondition{
					{Metric: "overall_score", Operator: deployv1.GateOperator_GATE_OPERATOR_GTE, Threshold: 0.8},
				},
			},
		},
		{
			name: "unknown metric",
			req: &deployv1.CreateQualityGateRequest{
				Name:     fmt.Sprintf("invalid-metric-%d", timestamp),
				PromptId: "some-prompt",
				Conditions: []*deployv1.GateCondition{
					{Metric: "vibes", Operator: deployv1.GateOperator_GATE_OPERATOR_GTE, Threshold: 0.8},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.CreateQualityGate(ctx, tt.req)
			if err == nil {
				t.Fatalf("expected CreateQualityGate to reject %s", tt.name)
			}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected a gRPC status error, got %v", err)
			}
			if st.Code() != codes.InvalidArgument {
				t.Errorf("expected INVALID_ARGUMENT, got %s: %s", st.Code(), st.Message())
			}
		})
	}
}

// TestDeployService_GetGateVerdict_NotFound asserts the CI-visible error for a
// gate name that does not exist.
func TestDeployService_GetGateVerdict_NotFound(t *testing.T) {
	client, cleanup := getDeployClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.GetGateVerdict(ctx, &deployv1.GetGateVerdictRequest{
		Name: fmt.Sprintf("no-such-gate-%d", time.Now().UnixNano()),
	})
	if err == nil {
		t.Fatal("expected an error for an unknown gate name")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("expected NOT_FOUND, got %s: %s", st.Code(), st.Message())
	}
}

// TestDeployService_VerdictHTTP exercises GET /v1/gates/{gate}/verdict, the
// endpoint CI systems and dashboards hit without a gRPC client. The control
// plane serves it on the same address as gRPC.
func TestDeployService_VerdictHTTP(t *testing.T) {
	deployClient, deployCleanup := getDeployClient(t)
	defer deployCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	timestamp := time.Now().UnixNano()

	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Gate HTTP Test Prompt",
		Slug: fmt.Sprintf("gate-http-test-%d", timestamp),
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Test prompt"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	promptID := promptResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})

	gateName := fmt.Sprintf("gate-http-%d", timestamp)
	if _, err := deployClient.CreateQualityGate(ctx, &deployv1.CreateQualityGateRequest{
		Name:     gateName,
		PromptId: promptID,
		Conditions: []*deployv1.GateCondition{
			{Metric: "overall_score", Operator: deployv1.GateOperator_GATE_OPERATOR_GTE, Threshold: 0.8},
		},
	}); err != nil {
		t.Fatalf("CreateQualityGate failed: %v", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}

	// Known gate: 200 with a JSON verdict body.
	code, body := getJSON(t, ctx, httpClient, controlPlaneHTTPURL("/v1/gates/"+gateName+"/verdict"))
	if code != http.StatusOK {
		t.Fatalf("expected 200 for a known gate, got %d: %s", code, body)
	}
	var verdict struct {
		Gate        string   `json:"gate"`
		Pass        bool     `json:"pass"`
		Reasons     []string `json:"reasons"`
		EvalRunID   string   `json:"eval_run_id"`
		EvaluatedAt string   `json:"evaluated_at"`
	}
	if err := json.Unmarshal(body, &verdict); err != nil {
		t.Fatalf("verdict body is not valid JSON: %v (body: %s)", err, body)
	}
	if verdict.Gate != gateName {
		t.Errorf("expected gate %q in the verdict body, got %q", gateName, verdict.Gate)
	}
	if verdict.Pass {
		t.Error("expected pass=false for a gate whose prompt has no eval runs")
	}
	if len(verdict.Reasons) == 0 {
		t.Error("expected reasons in the verdict body")
	}
	if verdict.EvaluatedAt == "" {
		t.Error("expected evaluated_at in the verdict body")
	}

	// Unknown gate: 404 with a JSON error body.
	code, body = getJSON(t, ctx, httpClient,
		controlPlaneHTTPURL(fmt.Sprintf("/v1/gates/no-such-gate-%d/verdict", timestamp)))
	if code != http.StatusNotFound {
		t.Errorf("expected 404 for an unknown gate, got %d: %s", code, body)
	}
	var errBody struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Errorf("404 body is not valid JSON: %v (body: %s)", err, body)
	} else if errBody.Error == "" {
		t.Errorf("expected an error message in the 404 body, got: %s", body)
	}
}

// controlPlaneHTTPURL builds a control-plane HTTP URL. gRPC (h2c) and HTTP are
// served on the same address, so the gRPC target doubles as the HTTP host.
func controlPlaneHTTPURL(path string) string {
	return "http://" + controlPlaneAddr() + path
}

// getJSON issues a GET and returns the status code and raw body.
func getJSON(t *testing.T, ctx context.Context, c *http.Client, url string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to build request for %s: %v", url, err)
	}
	if token := controlPlaneToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read %s response: %v", url, err)
	}
	return resp.StatusCode, body
}
