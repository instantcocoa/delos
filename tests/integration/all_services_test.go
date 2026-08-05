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
// OBSERVE SERVICE TESTS (5 endpoints)
// ============================================================================

func TestObserveService_Health(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &observev1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Observe health: %s", resp.Status)
}

func TestObserveService_IngestTraces(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := timestamppb.Now()
	resp, err := client.IngestTraces(ctx, &observev1.IngestTracesRequest{
		Spans: []*observev1.Span{
			{
				TraceId:   "test-trace-123",
				SpanId:    "span-1",
				Name:      "test-operation",
				StartTime: now,
			},
		},
	})
	if err != nil {
		t.Fatalf("IngestTraces failed: %v", err)
	}
	t.Logf("Ingested %d spans", resp.AcceptedCount)
}

func TestObserveService_QueryTraces(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.QueryTraces(ctx, &observev1.QueryTracesRequest{
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryTraces failed: %v", err)
	}
	t.Logf("Found %d traces", len(resp.Traces))
}

func TestObserveService_GetTrace(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetTrace(ctx, &observev1.GetTraceRequest{
		TraceId: "test-trace-123",
	})
	if err != nil {
		t.Logf("GetTrace: %v (trace may not exist)", err)
		return
	}
	if resp.Trace != nil {
		t.Logf("Got trace: %s with %d spans", resp.Trace.TraceId, len(resp.Trace.Spans))
	}
}

func TestObserveService_QueryMetrics(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.QueryMetrics(ctx, &observev1.QueryMetricsRequest{
		MetricName: "request_count",
		StartTime:  timestamppb.New(time.Now().Add(-time.Hour)),
		EndTime:    timestamppb.Now(),
	})
	if err != nil {
		t.Fatalf("QueryMetrics failed: %v", err)
	}
	t.Logf("QueryMetrics returned successfully")
	_ = resp
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
	t.Logf("Gateway health: status=%s providers=%d", health.Status, health.Providers)
}

func TestGateway_ListModels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)
	if list.Object != "list" {
		t.Errorf("expected object=list, got %q", list.Object)
	}
	t.Logf("Gateway serves %d models", len(list.Data))
	for _, m := range list.Data {
		if m.Object != "model" {
			t.Errorf("model %s: expected object=model, got %q", m.ID, m.Object)
		}
		if m.OwnedBy == "" {
			t.Errorf("model %s: expected owned_by to name the provider", m.ID)
		}
	}
}

func TestGateway_GetModel(t *testing.T) {
	g := newGatewayClient(15 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)
	if len(list.Data) == 0 {
		t.Skip("gateway has no models configured - skipping model lookup")
	}

	want := list.Data[0]
	status, body, err := g.get(ctx, "/v1/models/"+want.ID)
	if err != nil {
		t.Fatalf("GET /v1/models/%s failed: %v", want.ID, err)
	}
	if status != http.StatusOK {
		t.Fatalf("GET /v1/models/%s: expected 200, got %d: %s", want.ID, status, body)
	}

	var got gwModel
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("invalid JSON: %v (body: %s)", err, body)
	}
	if got.ID != want.ID {
		t.Errorf("expected model id %q, got %q", want.ID, got.ID)
	}
	t.Logf("Model %s owned by %s", got.ID, got.OwnedBy)
}

func TestGateway_ChatCompletions(t *testing.T) {
	model := anyGatewayModel(t)
	if model == "" {
		t.Skip("gateway has no models configured - skipping chat completion")
	}

	g := newGatewayClient(90 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	status, body, err := g.postJSON(ctx, "/v1/chat/completions", gwChatRequest{
		Model:     model,
		Messages:  []gwChatMessage{{Role: "user", Content: "Say hello"}},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatalf("POST /v1/chat/completions failed: %v", err)
	}
	if status != http.StatusOK {
		// A provider-side failure (missing credentials, model not pulled) is
		// not a gateway contract violation - report it and move on.
		t.Logf("chat completion returned %d: %s (expected without provider credentials)", status, body)
		return
	}

	var resp gwChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v (body: %s)", err, body)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("expected object=chat.completion, got %q", resp.Object)
	}
	if len(resp.Choices) == 0 {
		t.Fatalf("expected at least one choice, got: %s", body)
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("expected an assistant message, got: %s", body)
	}
	t.Logf("Chat completion (%s): %s", resp.Model, resp.content())
	if resp.Usage != nil {
		t.Logf("Usage: prompt=%d completion=%d total=%d cost_usd=%f",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens,
			resp.Usage.TotalTokens, resp.Usage.CostUSD)
	}
}

func TestGateway_ChatCompletions_Stream(t *testing.T) {
	model := anyGatewayModel(t)
	if model == "" {
		t.Skip("gateway has no models configured - skipping streaming chat completion")
	}

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
		t.Logf("streaming chat completion returned %d (expected without provider credentials)", resp.StatusCode)
		return
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	content, chunks, done := readSSEChatStream(t, resp)
	t.Logf("Received %d SSE chunks, done=%v, content=%q", chunks, done, content)
	if chunks == 0 {
		t.Error("expected at least one SSE chunk")
	}
}

func TestGateway_Embeddings(t *testing.T) {
	model := gatewayEmbeddingModel(t)
	if model == "" {
		t.Skip("no embedding-capable model advertised by the gateway")
	}

	g := newGatewayClient(60 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	status, body, err := g.postJSON(ctx, "/v1/embeddings", gwEmbeddingsRequest{
		Model: model,
		Input: "hello world",
	})
	if err != nil {
		t.Fatalf("POST /v1/embeddings failed: %v", err)
	}
	if status != http.StatusOK {
		t.Logf("embeddings returned %d: %s (expected without provider credentials)", status, body)
		return
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
	t.Logf("Embedding (%s): %d dimensions", resp.Model, len(resp.Data[0].Embedding))
}

// ---- gateway test helpers ----

// anyGatewayModel returns a model the gateway can serve, preferring a local
// Ollama model so the test does not depend on cloud credentials.
func anyGatewayModel(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)
	for _, m := range list.Data {
		if m.OwnedBy == ollamaProvider {
			return m.ID
		}
	}
	if len(list.Data) > 0 {
		return list.Data[0].ID
	}
	return ""
}

// gatewayEmbeddingModel returns a model that looks like an embedding model.
func gatewayEmbeddingModel(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list := listGatewayModels(t, ctx)
	for _, m := range list.Data {
		id := strings.ToLower(m.ID)
		if strings.Contains(id, "embed") {
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
		t.Logf("SSE read stopped: %v", err)
	}
	return content.String(), chunks, done
}

// ============================================================================
// PROMPT SERVICE TESTS (8 endpoints) - Health test
// ============================================================================

func TestPromptService_Health(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &promptv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Prompt health: %s", resp.Status)
}

// ============================================================================
// DATASETS SERVICE TESTS (10 endpoints)
// ============================================================================

func TestDatasetsService_Health(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &datasetsv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Datasets health: %s", resp.Status)
}

func TestDatasetsService_FullCRUD(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. CreateDataset
	createResp, err := client.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:        "CRUD Test Dataset",
		Description: "Testing all CRUD operations",
		Tags:        []string{"crud-test"},
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}
	datasetID := createResp.Dataset.Id
	t.Logf("1. Created dataset: %s", datasetID)

	defer func() {
		client.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})
	}()

	// 2. GetDataset
	getResp, err := client.GetDataset(ctx, &datasetsv1.GetDatasetRequest{Id: datasetID})
	if err != nil {
		t.Fatalf("GetDataset failed: %v", err)
	}
	t.Logf("2. Got dataset: %s", getResp.Dataset.Name)

	// 3. UpdateDataset
	updateResp, err := client.UpdateDataset(ctx, &datasetsv1.UpdateDatasetRequest{
		Id:          datasetID,
		Description: "Updated description",
	})
	if err != nil {
		t.Fatalf("UpdateDataset failed: %v", err)
	}
	t.Logf("3. Updated dataset: %s", updateResp.Dataset.Description)

	// 4. ListDatasets
	listResp, err := client.ListDatasets(ctx, &datasetsv1.ListDatasetsRequest{
		Tags:  []string{"crud-test"},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListDatasets failed: %v", err)
	}
	t.Logf("4. Listed %d datasets", len(listResp.Datasets))

	// 5. AddExamples
	addResp, err := client.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: datasetID,
		Examples: []*datasetsv1.ExampleInput{
			{
				Input:          toStruct(t, map[string]interface{}{"q": "What is 2+2?"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"a": "4"}),
			},
			{
				Input:          toStruct(t, map[string]interface{}{"q": "What is 3+3?"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"a": "6"}),
			},
		},
	})
	if err != nil {
		t.Fatalf("AddExamples failed: %v", err)
	}
	t.Logf("5. Added %d examples", addResp.AddedCount)

	// 6. GetExamples
	getExResp, err := client.GetExamples(ctx, &datasetsv1.GetExamplesRequest{
		DatasetId: datasetID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetExamples failed: %v", err)
	}
	t.Logf("6. Got %d examples (total: %d)", len(getExResp.Examples), getExResp.TotalCount)

	// 7. RemoveExamples
	if len(getExResp.Examples) > 0 {
		removeResp, err := client.RemoveExamples(ctx, &datasetsv1.RemoveExamplesRequest{
			DatasetId:  datasetID,
			ExampleIds: []string{getExResp.Examples[0].Id},
		})
		if err != nil {
			t.Fatalf("RemoveExamples failed: %v", err)
		}
		t.Logf("7. Removed %d examples", removeResp.RemovedCount)
	}

	// 8. GenerateExamples (may require LLM)
	genResp, err := client.GenerateExamples(ctx, &datasetsv1.GenerateExamplesRequest{
		DatasetId: datasetID,
		Count:     2,
	})
	if err != nil {
		t.Logf("8. GenerateExamples: %v (may require LLM)", err)
	} else {
		t.Logf("8. Generated %d examples", genResp.GeneratedCount)
	}

	// 9. DeleteDataset (in defer)
	t.Logf("9. DeleteDataset will run in defer")
}

// ============================================================================
// EVAL SERVICE TESTS (8 endpoints)
// ============================================================================

func TestEvalService_Health(t *testing.T) {
	client, cleanup := getEvalClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &evalv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Eval health: %s", resp.Status)
}

func TestEvalService_ListEvaluators(t *testing.T) {
	client, cleanup := getEvalClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListEvaluators(ctx, &evalv1.ListEvaluatorsRequest{})
	if err != nil {
		t.Fatalf("ListEvaluators failed: %v", err)
	}
	t.Logf("Found %d evaluators:", len(resp.Evaluators))
	for _, e := range resp.Evaluators {
		t.Logf("  %s: %s", e.Type, e.Name)
	}
}

func TestEvalService_FullWorkflow(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create a prompt
	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Eval Test Prompt",
		Slug: "eval-test-" + time.Now().Format("150405"),
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Echo back the input"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	promptID := promptResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	t.Logf("Created prompt: %s", promptID)

	// Create a dataset
	datasetResp, err := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:     "Eval Test Dataset",
		PromptId: promptID,
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}
	datasetID := datasetResp.Dataset.Id
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})
	t.Logf("Created dataset: %s", datasetID)

	// Add examples
	_, err = datasetsClient.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: datasetID,
		Examples: []*datasetsv1.ExampleInput{
			{
				Input:          toStruct(t, map[string]interface{}{"text": "hello"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"text": "hello"}),
			},
		},
	})
	if err != nil {
		t.Fatalf("AddExamples failed: %v", err)
	}

	// 1. CreateEvalRun
	createRunResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "Test Eval Run",
		PromptId:      promptID,
		PromptVersion: 1,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "exact_match", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}
	runID := createRunResp.EvalRun.Id
	t.Logf("1. Created eval run: %s", runID)

	// 2. GetEvalRun
	getRunResp, err := evalClient.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{Id: runID})
	if err != nil {
		t.Fatalf("GetEvalRun failed: %v", err)
	}
	t.Logf("2. Got eval run: status=%s", getRunResp.EvalRun.Status)

	// 3. ListEvalRuns
	listRunsResp, err := evalClient.ListEvalRuns(ctx, &evalv1.ListEvalRunsRequest{
		PromptId: promptID,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListEvalRuns failed: %v", err)
	}
	t.Logf("3. Listed %d eval runs", len(listRunsResp.EvalRuns))

	// 4. GetEvalResults
	resultsResp, err := evalClient.GetEvalResults(ctx, &evalv1.GetEvalResultsRequest{
		EvalRunId: runID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetEvalResults failed: %v", err)
	}
	t.Logf("4. Got %d results", len(resultsResp.Results))

	// 5. CancelEvalRun
	_, err = evalClient.CancelEvalRun(ctx, &evalv1.CancelEvalRunRequest{Id: runID})
	if err != nil {
		t.Logf("5. CancelEvalRun: %v (may already be complete)", err)
	} else {
		t.Logf("5. Cancelled eval run")
	}

	// 6. CompareRuns - create another run first
	createRun2Resp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "Test Eval Run 2",
		PromptId:      promptID,
		PromptVersion: 1,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "exact_match", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Logf("6. CreateEvalRun 2: %v", err)
	} else {
		compareResp, err := evalClient.CompareRuns(ctx, &evalv1.CompareRunsRequest{
			RunIdA: runID,
			RunIdB: createRun2Resp.EvalRun.Id,
		})
		if err != nil {
			t.Logf("6. CompareRuns: %v", err)
		} else {
			t.Logf("6. Compared runs: score_diff=%f, regressions=%d, improvements=%d", compareResp.ScoreDiff, compareResp.Regressions, compareResp.Improvements)
		}
	}
}

// ============================================================================
// DEPLOY SERVICE TESTS (quality gates only)
// ============================================================================
//
// Delos does not deploy anything. The deploy service holds quality gates and
// answers one question: does the latest completed eval run for a prompt satisfy
// this gate's conditions? CI systems act on the verdict.

func TestDeployService_Health(t *testing.T) {
	client, cleanup := getDeployClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &deployv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Deploy health: %s", resp.Status)
}

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
	t.Logf("Created prompt: %s", promptID)

	// 1. CreateQualityGate
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
	t.Logf("1. Created quality gate: %s (%s)", gate.GetName(), gate.GetId())

	// 2. ListQualityGates filtered by prompt - must contain exactly our gate.
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
	t.Logf("2. Listed %d gate(s) for prompt %s", len(filtered.QualityGates), promptID)

	// 3. ListQualityGates without a filter - must be a superset.
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
	t.Logf("3. Listed %d gate(s) overall", len(all.QualityGates))

	// 4. GetGateVerdict with no eval runs: not a pass, and the reason says why.
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
	t.Logf("4. Verdict for a never-evaluated prompt: pass=%v reasons=%v", verdict.Pass, verdict.Reasons)
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
	t.Logf("HTTP verdict: pass=%v reasons=%v", verdict.Pass, verdict.Reasons)

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
