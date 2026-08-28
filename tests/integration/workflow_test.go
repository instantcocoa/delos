// Package integration contains end-to-end workflow tests for Delos.
// These tests exercise the full platform with real LLM calls via Ollama.
//
//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	deployv1 "github.com/instantcocoa/delos/gen/go/deploy/v1"
	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	observev1 "github.com/instantcocoa/delos/gen/go/observe/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// These workflows span both binaries: prompts, datasets, evals and gate verdicts
// live in the control plane (gRPC on one address), while completions go to the
// gateway over HTTP. The eval service itself calls the gateway for completions,
// so eval runs exercise that hop server-side.

// ============================================================================
// SHARED HELPERS
// ============================================================================

// waitForEvalRun polls until the run reaches a terminal state and returns it.
// A run that fails, is cancelled, or never finishes fails the test: a workflow
// test whose evaluation did not run has verified nothing.
func waitForEvalRun(t *testing.T, ctx context.Context, client evalv1.EvalServiceClient,
	runID string, timeout time.Duration) *evalv1.EvalRun {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last *evalv1.EvalRun

	for time.Now().Before(deadline) {
		resp, err := client.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{Id: runID})
		if err != nil {
			t.Fatalf("GetEvalRun(%s) failed while waiting: %v", runID, err)
		}
		last = resp.EvalRun

		switch last.Status {
		case evalv1.EvalRunStatus_EVAL_RUN_STATUS_COMPLETED:
			return last
		case evalv1.EvalRunStatus_EVAL_RUN_STATUS_FAILED:
			t.Fatalf("eval run %s FAILED after %d/%d examples: %s",
				runID, last.CompletedExamples, last.TotalExamples, last.ErrorMessage)
		case evalv1.EvalRunStatus_EVAL_RUN_STATUS_CANCELLED:
			t.Fatalf("eval run %s was CANCELLED unexpectedly", runID)
		}

		time.Sleep(2 * time.Second)
	}

	t.Fatalf("eval run %s did not complete within %s (last status %s, %d/%d examples)",
		runID, timeout, last.GetStatus(), last.GetCompletedExamples(), last.GetTotalExamples())
	return nil
}

// ============================================================================
// PROMPT VERSIONING WORKFLOW TESTS
// ============================================================================

// TestPromptVersioningWorkflow covers what is unique to the workflow: resolving
// a prompt through slug:version references and diffing two versions. The shape
// of the version history itself is asserted in TestPromptService_GetPromptHistory.
func TestPromptVersioningWorkflow(t *testing.T) {
	promptClient, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	slug := fmt.Sprintf("versioning-workflow-%d", time.Now().UnixNano())

	// v1
	createResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name:        "Versioning Workflow Test",
		Slug:        slug,
		Description: "Testing prompt versioning workflow",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Summarize: {{text}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "text", Description: "Text to summarize", Type: "string", Required: true},
		},
		DefaultConfig: &promptv1.GenerationConfig{
			Temperature: 0.7,
			MaxTokens:   100,
		},
		Tags: []string{"test", "versioning"},
	})
	if err != nil {
		t.Fatalf("CreatePrompt v1 failed: %v", err)
	}
	promptID := createResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})

	if createResp.Prompt.Version != 1 {
		t.Errorf("Expected version 1, got %d", createResp.Prompt.Version)
	}

	// v2: new messages.
	updateResp, err := promptClient.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id:          promptID,
		Description: "Improved summarization prompt",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are an expert summarizer. Be concise and capture key points."},
			{Role: "user", Content: "Please summarize the following text:\n\n{{text}}"},
		},
		ChangeDescription: "Improved system prompt for better summarization",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt to v2 failed: %v", err)
	}
	if updateResp.Prompt.Version != 2 {
		t.Errorf("Expected version 2, got %d", updateResp.Prompt.Version)
	}

	// v3: config only. A partial update must not drop the messages set in v2.
	updateResp2, err := promptClient.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id: promptID,
		DefaultConfig: &promptv1.GenerationConfig{
			Temperature: 0.3,
			MaxTokens:   150,
		},
		ChangeDescription: "Lowered temperature for more consistent output",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt to v3 failed: %v", err)
	}
	if updateResp2.Prompt.Version != 3 {
		t.Errorf("Expected version 3, got %d", updateResp2.Prompt.Version)
	}
	if got := len(updateResp2.Prompt.Messages); got != 2 {
		t.Errorf("a config-only update dropped the messages: v3 has %d, want 2", got)
	}

	// slug:version references must resolve to the exact version named.
	v1Resp, err := promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Reference: fmt.Sprintf("%s:v1", slug),
	})
	if err != nil {
		t.Fatalf("GetPrompt v1 by reference failed: %v", err)
	}
	if v1Resp.Prompt.Version != 1 {
		t.Errorf("Expected version 1, got %d", v1Resp.Prompt.Version)
	}
	if v1Resp.Prompt.Id != promptID {
		t.Errorf("%s:v1 resolved to prompt %s, want %s", slug, v1Resp.Prompt.Id, promptID)
	}
	if len(v1Resp.Prompt.Messages) > 0 &&
		!strings.Contains(v1Resp.Prompt.Messages[0].Content, "You are a helpful assistant.") {
		t.Errorf("%s:v1 returned v1's version number but not v1's content: %q",
			slug, v1Resp.Prompt.Messages[0].Content)
	}

	v2Resp, err := promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Reference: fmt.Sprintf("%s:v2", slug),
	})
	if err != nil {
		t.Fatalf("GetPrompt v2 by reference failed: %v", err)
	}
	if v2Resp.Prompt.Version != 2 {
		t.Errorf("Expected version 2, got %d", v2Resp.Prompt.Version)
	}

	latestResp, err := promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Reference: fmt.Sprintf("%s:latest", slug),
	})
	if err != nil {
		t.Fatalf("GetPrompt latest by reference failed: %v", err)
	}
	if latestResp.Prompt.Version != 3 {
		t.Errorf("Expected latest version 3, got %d", latestResp.Prompt.Version)
	}

	// An unknown version is an error, not a silent fallback to latest.
	if bad, err := promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Reference: fmt.Sprintf("%s:v99", slug),
	}); err == nil {
		t.Errorf("expected an error for %s:v99, got version %d", slug, bad.GetPrompt().GetVersion())
	}

	// Comparing v1 and v2 must report the messages that changed.
	compareResp, err := promptClient.CompareVersions(ctx, &promptv1.CompareVersionsRequest{
		PromptId: promptID,
		VersionA: 1,
		VersionB: 2,
	})
	if err != nil {
		t.Fatalf("CompareVersions failed: %v", err)
	}
	if compareResp.SemanticSimilarity < 0 || compareResp.SemanticSimilarity > 1 {
		t.Errorf("Semantic similarity should be 0-1, got %f", compareResp.SemanticSimilarity)
	}
	if len(compareResp.Diffs) == 0 {
		t.Error("expected CompareVersions to report the messages that changed between v1 and v2")
	}
}

// ============================================================================
// PROMPT + OLLAMA INTEGRATION TESTS
// ============================================================================

func TestPromptWithOllamaCompletion(t *testing.T) {
	skipIfOllamaUnavailable(t)

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	slug := fmt.Sprintf("ollama-prompt-%d", time.Now().UnixNano())
	createResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name:        "Ollama Integration Test",
		Slug:        slug,
		Description: "Tests prompt rendering with Ollama",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are a {{role}}. Be helpful and concise."},
			{Role: "user", Content: "{{question}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "role", Description: "Assistant role", Type: "string", Required: true, DefaultValue: "helpful assistant"},
			{Name: "question", Description: "User question", Type: "string", Required: true},
		},
		DefaultConfig: &promptv1.GenerationConfig{
			Temperature: 0.3,
			MaxTokens:   50,
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	promptID := createResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})

	// Read the prompt back and render it, the way the eval runner does.
	getResp, err := promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{Id: promptID})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if getResp.Prompt.DefaultConfig == nil {
		t.Fatal("expected the stored prompt to carry its default generation config")
	}

	renderedMessages := make([]gwChatMessage, len(getResp.Prompt.Messages))
	for i, msg := range getResp.Prompt.Messages {
		content := msg.Content
		content = strings.ReplaceAll(content, "{{role}}", "math tutor")
		content = strings.ReplaceAll(content, "{{question}}", "What is 15 * 7?")
		if strings.Contains(content, "{{") {
			t.Errorf("message %d still has an unrendered variable: %q", i, content)
		}
		renderedMessages[i] = gwChatMessage{Role: msg.Role, Content: content}
	}

	temperature := getResp.Prompt.DefaultConfig.Temperature
	completeResp := ollamaComplete(t, ctx, gwChatRequest{
		Messages:    renderedMessages,
		Temperature: &temperature,
		MaxTokens:   int(getResp.Prompt.DefaultConfig.MaxTokens),
	})

	// The rendered prompt asks a math tutor for 15 * 7. The answer is 105.
	if !strings.Contains(completeResp.content(), "105") {
		t.Errorf("expected the rendered prompt to yield 105 for 15 * 7, got: %s",
			completeResp.content())
	}
}

// ============================================================================
// DATASET + EVAL WITH OLLAMA TESTS
// ============================================================================

func TestEvalWithOllama(t *testing.T) {
	skipIfOllamaUnavailable(t)

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	slug := fmt.Sprintf("eval-ollama-%d", time.Now().UnixNano())
	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name:        "Math Evaluator",
		Slug:        slug,
		Description: "Simple math questions for eval testing",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are a calculator. Only respond with the numeric answer, nothing else."},
			{Role: "user", Content: "{{question}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "question", Type: "string", Required: true},
		},
		DefaultConfig: &promptv1.GenerationConfig{
			Temperature: 0.0,
			MaxTokens:   10,
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	promptID := promptResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})

	datasetResp, err := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:        "Math Test Dataset",
		Description: "Simple math questions for evaluation",
		PromptId:    promptID,
		Tags:        []string{"eval-test", "math"},
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}
	datasetID := datasetResp.Dataset.Id
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})

	examples := []*datasetsv1.ExampleInput{
		{
			Input:          toStruct(t, map[string]interface{}{"question": "What is 2 + 2?"}),
			ExpectedOutput: toStruct(t, map[string]interface{}{"answer": "4"}),
		},
		{
			Input:          toStruct(t, map[string]interface{}{"question": "What is 5 * 3?"}),
			ExpectedOutput: toStruct(t, map[string]interface{}{"answer": "15"}),
		},
		{
			Input:          toStruct(t, map[string]interface{}{"question": "What is 100 / 4?"}),
			ExpectedOutput: toStruct(t, map[string]interface{}{"answer": "25"}),
		},
	}

	addResp, err := datasetsClient.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: datasetID,
		Examples:  examples,
	})
	if err != nil {
		t.Fatalf("AddExamples failed: %v", err)
	}
	if addResp.AddedCount != int32(len(examples)) {
		t.Fatalf("AddExamples stored %d of %d examples", addResp.AddedCount, len(examples))
	}

	evalRunResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "Math Eval with Ollama",
		PromptId:      promptID,
		PromptVersion: 1,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Provider: ollamaProvider,
			Model:    ollamaModel,
			Evaluators: []*evalv1.EvaluatorConfig{
				{
					Type:   "contains",
					Weight: 1.0,
					Params: map[string]string{"case_sensitive": "false"},
				},
			},
		},
		Metadata: map[string]string{"test_type": "integration"},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}
	evalRunID := evalRunResp.EvalRun.Id

	// The run must actually finish. A timeout or a FAILED run fails the test.
	run := waitForEvalRun(t, ctx, evalClient, evalRunID, 120*time.Second)

	if run.TotalExamples != int32(len(examples)) {
		t.Errorf("run covers %d examples, dataset has %d", run.TotalExamples, len(examples))
	}
	if run.CompletedExamples != run.TotalExamples {
		t.Errorf("a COMPLETED run left %d of %d examples unfinished",
			run.TotalExamples-run.CompletedExamples, run.TotalExamples)
	}

	resultsResp, err := evalClient.GetEvalResults(ctx, &evalv1.GetEvalResultsRequest{
		EvalRunId: evalRunID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetEvalResults failed: %v", err)
	}
	if len(resultsResp.Results) != len(examples) {
		t.Fatalf("expected one result per example (%d), got %d", len(examples), len(resultsResp.Results))
	}

	passCount := 0
	for i, result := range resultsResp.Results {
		if result.OverallScore < 0 || result.OverallScore > 1 {
			t.Errorf("result %d: overall score %.2f is outside [0, 1]", i, result.OverallScore)
		}
		if result.Passed {
			passCount++
		}
	}

	// The summary is a rollup of the results above, so it must agree with them.
	summary := run.Summary
	if summary == nil {
		t.Fatal("expected a COMPLETED run to carry a summary")
	}
	wantPassRate := float64(passCount) / float64(len(resultsResp.Results))
	if diff := summary.PassRate - wantPassRate; diff > 0.01 || diff < -0.01 {
		t.Errorf("summary pass rate %.2f disagrees with the results (%d/%d = %.2f)",
			summary.PassRate, passCount, len(resultsResp.Results), wantPassRate)
	}
	if summary.OverallScore < 0 || summary.OverallScore > 1 {
		t.Errorf("summary overall score %.2f is outside [0, 1]", summary.OverallScore)
	}
	if summary.AvgLatencyMs <= 0 {
		t.Errorf("expected the summary to record latency for real LLM calls, got %.0fms",
			summary.AvgLatencyMs)
	}
}

// ============================================================================
// TRACING VERIFICATION TESTS
// ============================================================================

// TestOllamaCallsAreTraced asserts a gateway completion shows up in the observe
// backend. It needs the gateway to be exporting OTLP at the control plane; when
// it is not, the test skips visibly rather than passing on an empty result.
func TestOllamaCallsAreTraced(t *testing.T) {
	skipIfOllamaUnavailable(t)

	if os.Getenv("DELOS_OTLP_ENDPOINT") == "" {
		t.Skip("DELOS_OTLP_ENDPOINT is unset, so the gateway under test exports no " +
			"spans - start it with DELOS_OTLP_ENDPOINT pointed at the control plane " +
			"to exercise tracing")
	}

	observeClient, observeCleanup := getObserveClient(t)
	defer observeCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	testID := fmt.Sprintf("trace-test-%d", time.Now().UnixNano())

	completeResp := ollamaComplete(t, ctx, gwChatRequest{
		Messages:  []gwChatMessage{{Role: "user", Content: fmt.Sprintf("Test ID: %s. What is 1+1?", testID)}},
		MaxTokens: 10,
	})
	if strings.TrimSpace(completeResp.content()) == "" {
		t.Fatal("expected a completion to trace")
	}

	// Spans are batched, so poll rather than sleeping once.
	var traces []*observev1.Trace
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		queryResp, err := observeClient.QueryTraces(ctx, &observev1.QueryTracesRequest{
			ServiceName: "delos-gateway",
			Limit:       50,
		})
		if err != nil {
			t.Fatalf("QueryTraces failed: %v", err)
		}
		traces = queryResp.Traces
		if len(traces) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if len(traces) == 0 {
		t.Fatal("the gateway is exporting OTLP but observe holds no delos-gateway traces " +
			"after a completion")
	}

	var llmSpans int
	for _, trace := range traces {
		for _, span := range trace.Spans {
			if span.GenAiSystem != "" || span.RequestModel != "" ||
				strings.Contains(strings.ToLower(span.Name), "chat") ||
				strings.Contains(strings.ToLower(span.Name), "complet") {
				llmSpans++
			}
		}
	}
	if llmSpans == 0 {
		t.Errorf("found %d gateway traces but none carries an LLM span (gen_ai attributes "+
			"or a chat/completion operation name)", len(traces))
	}
}

// ============================================================================
// END-TO-END WORKFLOW: PROMPT -> DATASET -> EVAL -> GATE VERDICT
// ============================================================================

func TestEndToEndWorkflow(t *testing.T) {
	skipIfOllamaUnavailable(t)

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	deployClient, deployCleanup := getDeployClient(t)
	defer deployCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	timestamp := time.Now().UnixNano()

	// ---- Prompt v1 ----
	slug := fmt.Sprintf("e2e-workflow-%d", timestamp)
	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name:        "E2E Workflow Prompt",
		Slug:        slug,
		Description: "Prompt for end-to-end workflow testing",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are a helpful assistant. Answer questions concisely."},
			{Role: "user", Content: "{{question}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "question", Type: "string", Required: true},
		},
		DefaultConfig: &promptv1.GenerationConfig{
			Temperature: 0.3,
			MaxTokens:   50,
		},
		Tags: []string{"e2e-test"},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	promptID := promptResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})

	// ---- Dataset ----
	datasetResp, err := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:        "E2E Test Dataset",
		Description: "Test cases for e2e workflow",
		PromptId:    promptID,
		Tags:        []string{"e2e-test"},
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}
	datasetID := datasetResp.Dataset.Id
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})

	if _, err := datasetsClient.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: datasetID,
		Examples: []*datasetsv1.ExampleInput{
			{
				Input:          toStruct(t, map[string]interface{}{"question": "What is the capital of France?"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"answer": "Paris"}),
			},
			{
				Input:          toStruct(t, map[string]interface{}{"question": "What is 2+2?"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"answer": "4"}),
			},
		},
	}); err != nil {
		t.Fatalf("AddExamples failed: %v", err)
	}

	// ---- Evaluate v1 ----
	evalRunResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "E2E Eval Run",
		PromptId:      promptID,
		PromptVersion: 1,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Provider: ollamaProvider,
			Model:    ollamaModel,
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "contains", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}
	evalRunID := evalRunResp.EvalRun.Id

	// The gate assertions below are only meaningful if the run finished, so a
	// timed-out or failed run fails the test here.
	run := waitForEvalRun(t, ctx, evalClient, evalRunID, 120*time.Second)
	if run.Summary == nil {
		t.Fatal("expected a COMPLETED run to carry a summary the gate can read")
	}

	// ---- Gates ----
	//
	// Both gates have fixed outcomes that hold for any score the model produces,
	// so the test asserts the gate engine rather than re-deriving the verdict
	// from the score the server just reported.
	//
	//   permissive: overall_score >= 0  -> every completed run clears it
	//   strict:     overall_score >= 0.99 AND avg_latency_ms <= 0.001
	//               -> no real LLM run clears it

	passGateName := fmt.Sprintf("e2e-gate-pass-%d", timestamp)
	if _, err := deployClient.CreateQualityGate(ctx, &deployv1.CreateQualityGateRequest{
		Name:        passGateName,
		Description: "Permissive gate: any completed eval run clears it",
		PromptId:    promptID,
		Conditions: []*deployv1.GateCondition{
			{
				Metric:    "overall_score",
				Operator:  deployv1.GateOperator_GATE_OPERATOR_GTE,
				Threshold: 0,
			},
		},
	}); err != nil {
		t.Fatalf("CreateQualityGate (permissive) failed: %v", err)
	}

	passVerdict, err := deployClient.GetGateVerdict(ctx, &deployv1.GetGateVerdictRequest{
		Name: passGateName,
	})
	if err != nil {
		t.Fatalf("GetGateVerdict (permissive) failed: %v", err)
	}
	if !passVerdict.Pass {
		t.Errorf("gate overall_score>=0 must pass for a completed run scoring %.2f, got reasons: %v",
			run.Summary.OverallScore, passVerdict.Reasons)
	}
	if len(passVerdict.Reasons) == 0 {
		t.Error("expected the verdict to explain itself")
	}
	if passVerdict.EvalRunId != evalRunID {
		t.Errorf("verdict was computed from run %q, want the run just completed (%s)",
			passVerdict.EvalRunId, evalRunID)
	}

	strictGateName := fmt.Sprintf("e2e-gate-strict-%d", timestamp)
	if _, err := deployClient.CreateQualityGate(ctx, &deployv1.CreateQualityGateRequest{
		Name:        strictGateName,
		Description: "Strict gate: no realistic run clears it",
		PromptId:    promptID,
		Conditions: []*deployv1.GateCondition{
			{
				Metric:    "overall_score",
				Operator:  deployv1.GateOperator_GATE_OPERATOR_GTE,
				Threshold: 0.99,
			},
			{
				Metric:    "avg_latency_ms",
				Operator:  deployv1.GateOperator_GATE_OPERATOR_LTE,
				Threshold: 0.001,
			},
		},
	}); err != nil {
		t.Fatalf("CreateQualityGate (strict) failed: %v", err)
	}

	strictVerdict, err := deployClient.GetGateVerdict(ctx, &deployv1.GetGateVerdictRequest{
		Name: strictGateName,
	})
	if err != nil {
		t.Fatalf("GetGateVerdict (strict) failed: %v", err)
	}
	if strictVerdict.Pass {
		t.Errorf("expected the strict gate to fail an LLM run that took %.0fms, reasons: %v",
			run.Summary.AvgLatencyMs, strictVerdict.Reasons)
	}
	if len(strictVerdict.Reasons) == 0 {
		t.Error("expected the failing verdict to explain which condition failed")
	}
	// The latency condition is unsatisfiable for a real call, so it must be the
	// one named in the failure.
	if !strings.Contains(strings.ToLower(strings.Join(strictVerdict.Reasons, " ")), "latency") {
		t.Errorf("expected the failing verdict to name avg_latency_ms, got %v", strictVerdict.Reasons)
	}

	// ---- Prompt v2, evaluate, compare ----
	updateResp, err := promptClient.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id: promptID,
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are an expert assistant. Provide accurate, concise answers."},
			{Role: "user", Content: "Question: {{question}}\nAnswer:"},
		},
		ChangeDescription: "Improved prompt formatting for better responses",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt to v2 failed: %v", err)
	}
	if updateResp.Prompt.Version != 2 {
		t.Fatalf("expected version 2 after update, got %d", updateResp.Prompt.Version)
	}

	evalRun2Resp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "E2E Eval Run v2",
		PromptId:      promptID,
		PromptVersion: 2,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Provider: ollamaProvider,
			Model:    ollamaModel,
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "contains", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun v2 failed: %v", err)
	}
	evalRun2ID := evalRun2Resp.EvalRun.Id

	run2 := waitForEvalRun(t, ctx, evalClient, evalRun2ID, 120*time.Second)
	if run2.Summary == nil {
		t.Fatal("expected the v2 run to carry a summary")
	}

	compareResp, err := evalClient.CompareRuns(ctx, &evalv1.CompareRunsRequest{
		RunIdA: evalRunID,
		RunIdB: evalRun2ID,
	})
	if err != nil {
		t.Fatalf("CompareRuns failed for two completed runs: %v", err)
	}
	// The reported difference must be the difference between the two summaries.
	wantDiff := run2.Summary.OverallScore - run.Summary.OverallScore
	if diff := compareResp.ScoreDiff - wantDiff; diff > 0.01 || diff < -0.01 {
		t.Errorf("CompareRuns reported score_diff=%.3f, but the runs scored %.3f and %.3f (diff %.3f)",
			compareResp.ScoreDiff, run.Summary.OverallScore, run2.Summary.OverallScore, wantDiff)
	}

	// The newest completed run is what a gate on this prompt now reads.
	latestVerdict, err := deployClient.GetGateVerdict(ctx, &deployv1.GetGateVerdictRequest{
		Name: passGateName,
	})
	if err != nil {
		t.Fatalf("GetGateVerdict after the v2 run failed: %v", err)
	}
	if latestVerdict.EvalRunId != evalRun2ID {
		t.Errorf("gate verdict still points at run %q; after a newer completed run it should read %s",
			latestVerdict.EvalRunId, evalRun2ID)
	}
}
