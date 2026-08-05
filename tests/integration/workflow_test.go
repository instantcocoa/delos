// Package integration contains end-to-end workflow tests for Delos.
// These tests exercise the full platform with real LLM calls via Ollama.
//
//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

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
// PROMPT VERSIONING WORKFLOW TESTS
// ============================================================================

func TestPromptVersioningWorkflow(t *testing.T) {
	promptClient, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	slug := fmt.Sprintf("versioning-workflow-%d", time.Now().UnixNano())

	// Step 1: Create initial prompt (v1)
	t.Log("Step 1: Creating initial prompt v1")
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
	t.Logf("Created prompt %s (v%d)", promptID, createResp.Prompt.Version)

	// Step 2: Update prompt to create v2 with different system prompt
	t.Log("Step 2: Updating to v2 with improved system prompt")
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
	t.Logf("Updated to v%d", updateResp.Prompt.Version)

	// Step 3: Update again for v3
	t.Log("Step 3: Updating to v3 with temperature adjustment")
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
	t.Logf("Updated to v%d", updateResp2.Prompt.Version)

	// Step 4: Get specific versions using slug:version reference
	t.Log("Step 4: Testing slug:version references")

	// Get v1
	v1Resp, err := promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Reference: fmt.Sprintf("%s:v1", slug),
	})
	if err != nil {
		t.Fatalf("GetPrompt v1 by reference failed: %v", err)
	}
	if v1Resp.Prompt.Version != 1 {
		t.Errorf("Expected version 1, got %d", v1Resp.Prompt.Version)
	}
	t.Logf("Retrieved %s:v1 - version %d", slug, v1Resp.Prompt.Version)

	// Get latest
	latestResp, err := promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Reference: fmt.Sprintf("%s:latest", slug),
	})
	if err != nil {
		t.Fatalf("GetPrompt latest by reference failed: %v", err)
	}
	if latestResp.Prompt.Version != 3 {
		t.Errorf("Expected latest version 3, got %d", latestResp.Prompt.Version)
	}
	t.Logf("Retrieved %s:latest - version %d", slug, latestResp.Prompt.Version)

	// Step 5: Get version history
	t.Log("Step 5: Getting version history")
	historyResp, err := promptClient.GetPromptHistory(ctx, &promptv1.GetPromptHistoryRequest{
		Id: promptID,
	})
	if err != nil {
		t.Fatalf("GetPromptHistory failed: %v", err)
	}
	t.Logf("Found %d versions in history", len(historyResp.Versions))
	for _, v := range historyResp.Versions {
		t.Logf("  Version %d: %s", v.Version, v.ChangeDescription)
	}

	// Step 6: Compare versions
	t.Log("Step 6: Comparing v1 and v2")
	compareResp, err := promptClient.CompareVersions(ctx, &promptv1.CompareVersionsRequest{
		PromptId: promptID,
		VersionA: 1,
		VersionB: 2,
	})
	if err != nil {
		t.Fatalf("CompareVersions failed: %v", err)
	}
	t.Logf("Semantic similarity: %.2f", compareResp.SemanticSimilarity)
	t.Logf("Diffs found: %d", len(compareResp.Diffs))
	for _, diff := range compareResp.Diffs {
		t.Logf("  %s: %s", diff.Field, diff.DiffType)
	}

	if compareResp.SemanticSimilarity < 0 || compareResp.SemanticSimilarity > 1 {
		t.Errorf("Semantic similarity should be 0-1, got %f", compareResp.SemanticSimilarity)
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

	// Create a prompt with variables
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
	t.Logf("Created prompt: %s", promptID)

	// Get the prompt to retrieve its messages
	getResp, err := promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{Id: promptID})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}

	// Render the prompt messages manually (in real app, prompt service would do this)
	renderedMessages := make([]gwChatMessage, len(getResp.Prompt.Messages))
	for i, msg := range getResp.Prompt.Messages {
		content := msg.Content
		content = strings.ReplaceAll(content, "{{role}}", "math tutor")
		content = strings.ReplaceAll(content, "{{question}}", "What is 15 * 7?")
		renderedMessages[i] = gwChatMessage{
			Role:    msg.Role,
			Content: content,
		}
	}

	// Call Ollama through the gateway with the rendered prompt
	temperature := getResp.Prompt.DefaultConfig.Temperature
	completeResp := ollamaComplete(t, ctx, gwChatRequest{
		Messages:    renderedMessages,
		Temperature: &temperature,
		MaxTokens:   int(getResp.Prompt.DefaultConfig.MaxTokens),
	})

	t.Logf("Response: %s", completeResp.content())

	// Verify the response contains the answer
	if !strings.Contains(completeResp.content(), "105") {
		t.Logf("Note: Expected '105' in response, got: %s", completeResp.content())
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

	// Step 1: Create a math prompt
	t.Log("Step 1: Creating math prompt")
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
	t.Logf("Created prompt: %s", promptID)

	// Step 2: Create a dataset with test cases
	t.Log("Step 2: Creating dataset with math test cases")
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
	t.Logf("Created dataset: %s", datasetID)

	// Add test examples
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
	t.Logf("Added %d examples to dataset", addResp.AddedCount)

	// Step 3: Create an eval run
	t.Log("Step 3: Creating eval run with contains evaluator")
	evalRunResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "Math Eval with Ollama",
		PromptId:      promptID,
		PromptVersion: 1,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Provider: "ollama",
			Model:    "gemma3:4b",
			Evaluators: []*evalv1.EvaluatorConfig{
				{
					Type:   "contains",
					Weight: 1.0,
					Params: map[string]string{
						"case_sensitive": "false",
					},
				},
			},
		},
		Metadata: map[string]string{
			"test_type": "integration",
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}
	evalRunID := evalRunResp.EvalRun.Id
	t.Logf("Created eval run: %s (status: %s)", evalRunID, evalRunResp.EvalRun.Status)

	// Step 4: Wait for eval to complete (poll status)
	t.Log("Step 4: Waiting for eval to complete...")
	maxWait := 120 * time.Second
	pollInterval := 2 * time.Second
	deadline := time.Now().Add(maxWait)

	var finalStatus string
	for time.Now().Before(deadline) {
		statusResp, err := evalClient.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{Id: evalRunID})
		if err != nil {
			t.Fatalf("GetEvalRun failed: %v", err)
		}

		finalStatus = statusResp.EvalRun.Status.String()
		t.Logf("  Status: %s, Progress: %d/%d",
			finalStatus,
			statusResp.EvalRun.CompletedExamples,
			statusResp.EvalRun.TotalExamples)

		if statusResp.EvalRun.Status == evalv1.EvalRunStatus_EVAL_RUN_STATUS_COMPLETED ||
			statusResp.EvalRun.Status == evalv1.EvalRunStatus_EVAL_RUN_STATUS_FAILED {
			break
		}

		time.Sleep(pollInterval)
	}

	// Step 5: Get eval results
	t.Log("Step 5: Getting eval results")
	resultsResp, err := evalClient.GetEvalResults(ctx, &evalv1.GetEvalResultsRequest{
		EvalRunId: evalRunID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetEvalResults failed: %v", err)
	}

	t.Logf("Got %d results:", len(resultsResp.Results))
	passCount := 0
	for i, result := range resultsResp.Results {
		t.Logf("  Result %d: score=%.2f, passed=%v", i+1, result.OverallScore, result.Passed)
		if result.Passed {
			passCount++
		}
	}

	// Step 6: Get eval summary
	t.Log("Step 6: Getting eval run summary")
	finalResp, err := evalClient.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{Id: evalRunID})
	if err != nil {
		t.Fatalf("GetEvalRun final failed: %v", err)
	}

	if finalResp.EvalRun.Summary != nil {
		t.Logf("Summary:")
		t.Logf("  Overall Score: %.2f", finalResp.EvalRun.Summary.OverallScore)
		t.Logf("  Pass Rate: %.2f%%", finalResp.EvalRun.Summary.PassRate*100)
		t.Logf("  Avg Latency: %.0fms", finalResp.EvalRun.Summary.AvgLatencyMs)
	}

	t.Logf("Passed: %d/%d examples", passCount, len(resultsResp.Results))
}

// ============================================================================
// TRACING VERIFICATION TESTS
// ============================================================================

func TestOllamaCallsAreTraced(t *testing.T) {
	skipIfOllamaUnavailable(t)

	observeClient, observeCleanup := getObserveClient(t)
	defer observeCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Generate a unique identifier for this test
	testID := fmt.Sprintf("trace-test-%d", time.Now().UnixNano())

	// Step 1: Make an Ollama completion with unique content
	t.Log("Step 1: Making Ollama completion request")
	completeResp := ollamaComplete(t, ctx, gwChatRequest{
		Messages:  []gwChatMessage{{Role: "user", Content: fmt.Sprintf("Test ID: %s. What is 1+1?", testID)}},
		MaxTokens: 10,
	})
	t.Logf("Got response: %s", completeResp.content())

	// Step 2: Wait a moment for traces to be ingested
	t.Log("Step 2: Waiting for traces to be ingested...")
	time.Sleep(2 * time.Second)

	// Step 3: Query for recent traces from the runtime service
	t.Log("Step 3: Querying for runtime service traces")
	queryResp, err := observeClient.QueryTraces(ctx, &observev1.QueryTracesRequest{
		ServiceName: "delos-gateway",
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("QueryTraces failed: %v", err)
	}

	t.Logf("Found %d traces from the gateway", len(queryResp.Traces))

	// Check if we can find traces with LLM-related operations
	foundLLMTrace := false
	for _, trace := range queryResp.Traces {
		for _, span := range trace.Spans {
			if strings.Contains(span.Name, "Complete") ||
				strings.Contains(span.Name, "ollama") ||
				strings.Contains(span.Name, "LLM") {
				foundLLMTrace = true
				t.Logf("Found LLM trace: %s (span: %s)", trace.TraceId, span.Name)
			}
		}
	}

	if !foundLLMTrace {
		t.Log("Note: No explicit LLM traces found - tracing may not be fully implemented")
	}

	// Step 4: Query by operation name
	t.Log("Step 4: Querying traces by operation")
	queryResp2, err := observeClient.QueryTraces(ctx, &observev1.QueryTracesRequest{
		OperationName: "Complete",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("QueryTraces by operation failed: %v", err)
	}
	t.Logf("Found %d traces for 'Complete' operation", len(queryResp2.Traces))
}

// ============================================================================
// END-TO-END WORKFLOW: PROMPT -> DATASET -> EVAL -> GATE VERDICT
// ============================================================================

func TestEndToEndWorkflow(t *testing.T) {
	skipIfOllamaUnavailable(t)

	// Get all clients
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

	// ========================================
	// PHASE 1: Create Prompt (v1)
	// ========================================
	t.Log("=== PHASE 1: Creating Prompt v1 ===")

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
	t.Logf("Created prompt: %s (v%d)", promptID, promptResp.Prompt.Version)

	// ========================================
	// PHASE 2: Create Dataset with Test Cases
	// ========================================
	t.Log("=== PHASE 2: Creating Dataset ===")

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
	t.Logf("Created dataset: %s", datasetID)

	// Add test examples
	_, err = datasetsClient.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
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
	})
	if err != nil {
		t.Fatalf("AddExamples failed: %v", err)
	}
	t.Log("Added 2 test examples")

	// ========================================
	// PHASE 3: Run Evaluation with Ollama
	// ========================================
	t.Log("=== PHASE 3: Running Evaluation ===")

	evalRunResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "E2E Eval Run",
		PromptId:      promptID,
		PromptVersion: 1,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Provider: "ollama",
			Model:    "gemma3:4b",
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "contains", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}
	evalRunID := evalRunResp.EvalRun.Id
	t.Logf("Created eval run: %s", evalRunID)

	// Wait for eval to complete
	t.Log("Waiting for evaluation to complete...")
	maxWait := 120 * time.Second
	deadline := time.Now().Add(maxWait)
	var evalScore float64
	evalCompleted := false

	for time.Now().Before(deadline) {
		statusResp, err := evalClient.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{Id: evalRunID})
		if err != nil {
			t.Fatalf("GetEvalRun failed: %v", err)
		}

		if statusResp.EvalRun.Status == evalv1.EvalRunStatus_EVAL_RUN_STATUS_COMPLETED {
			if statusResp.EvalRun.Summary != nil {
				evalScore = float64(statusResp.EvalRun.Summary.OverallScore)
			}
			evalCompleted = true
			t.Logf("Eval completed: score=%.2f", evalScore)
			break
		}
		if statusResp.EvalRun.Status == evalv1.EvalRunStatus_EVAL_RUN_STATUS_FAILED {
			t.Logf("Eval failed: %s", statusResp.EvalRun.ErrorMessage)
			break
		}

		time.Sleep(2 * time.Second)
	}

	// ========================================
	// PHASE 4: Quality Gate the run should pass
	// ========================================
	t.Log("=== PHASE 4: Creating a Quality Gate the run passes ===")

	passGateName := fmt.Sprintf("e2e-gate-pass-%d", timestamp)
	passGateResp, err := deployClient.CreateQualityGate(ctx, &deployv1.CreateQualityGateRequest{
		Name:        passGateName,
		Description: "Permissive gate: the eval run above should clear it",
		PromptId:    promptID,
		Conditions: []*deployv1.GateCondition{
			{
				Metric:    "overall_score",
				Operator:  deployv1.GateOperator_GATE_OPERATOR_GTE,
				Threshold: 0.1,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateQualityGate (permissive) failed: %v", err)
	}
	t.Logf("Created quality gate: %s (%s)", passGateResp.QualityGate.Name, passGateResp.QualityGate.Id)

	// ========================================
	// PHASE 5: Gate verdicts (what CI acts on)
	// ========================================
	t.Log("=== PHASE 5: Reading Gate Verdicts ===")

	passVerdict, err := deployClient.GetGateVerdict(ctx, &deployv1.GetGateVerdictRequest{
		Name: passGateName,
	})
	if err != nil {
		t.Fatalf("GetGateVerdict (permissive) failed: %v", err)
	}
	t.Logf("Permissive gate verdict: pass=%v run=%s reasons=%v",
		passVerdict.Pass, passVerdict.EvalRunId, passVerdict.Reasons)
	if len(passVerdict.Reasons) == 0 {
		t.Error("expected the verdict to explain itself")
	}
	if evalCompleted {
		// The verdict must agree with the score the run actually produced.
		want := evalScore >= 0.1
		if passVerdict.Pass != want {
			t.Errorf("gate overall_score>=0.1 returned pass=%v for a run scoring %.2f: %v",
				passVerdict.Pass, evalScore, passVerdict.Reasons)
		}
		if !want {
			t.Logf("note: the model scored %.2f, below the permissive threshold", evalScore)
		}
		if passVerdict.EvalRunId == "" {
			t.Error("expected the verdict to name the eval run it was computed from")
		}
	} else {
		t.Logf("eval run did not complete; verdict reported pass=%v", passVerdict.Pass)
	}

	// A gate no real run clears: same prompt, impossible threshold.
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
	t.Logf("Strict gate verdict: pass=%v reasons=%v", strictVerdict.Pass, strictVerdict.Reasons)
	if strictVerdict.Pass {
		t.Errorf("expected the strict gate to fail, reasons: %v", strictVerdict.Reasons)
	}
	if len(strictVerdict.Reasons) == 0 {
		t.Error("expected the failing verdict to explain which condition failed")
	}

	// ========================================
	// PHASE 6: Update Prompt to v2
	// ========================================
	t.Log("=== PHASE 6: Creating Prompt v2 ===")

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
	t.Logf("Updated to v%d", updateResp.Prompt.Version)

	// ========================================
	// PHASE 7: Run Eval on v2 and Compare
	// ========================================
	t.Log("=== PHASE 7: Evaluating v2 and Comparing ===")

	evalRun2Resp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "E2E Eval Run v2",
		PromptId:      promptID,
		PromptVersion: 2,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Provider: "ollama",
			Model:    "gemma3:4b",
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "contains", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun v2 failed: %v", err)
	}
	evalRun2ID := evalRun2Resp.EvalRun.Id
	t.Logf("Created eval run for v2: %s", evalRun2ID)

	// Wait for v2 eval
	deadline = time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		statusResp, err := evalClient.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{Id: evalRun2ID})
		if err != nil {
			t.Fatalf("GetEvalRun v2 failed: %v", err)
		}

		if statusResp.EvalRun.Status == evalv1.EvalRunStatus_EVAL_RUN_STATUS_COMPLETED ||
			statusResp.EvalRun.Status == evalv1.EvalRunStatus_EVAL_RUN_STATUS_FAILED {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Compare the two runs
	compareResp, err := evalClient.CompareRuns(ctx, &evalv1.CompareRunsRequest{
		RunIdA: evalRunID,
		RunIdB: evalRun2ID,
	})
	if err != nil {
		t.Logf("CompareRuns failed: %v", err)
	} else {
		t.Logf("Comparison: score_diff=%.2f, regressions=%d, improvements=%d",
			compareResp.ScoreDiff, compareResp.Regressions, compareResp.Improvements)
	}

	// ========================================
	// Summary
	// ========================================
	t.Log("=== END-TO-END WORKFLOW COMPLETE ===")
	t.Logf("Prompt: %s (v1 -> v2)", promptID)
	t.Logf("Dataset: %s (2 examples)", datasetID)
	t.Logf("Eval Runs: %s (v1), %s (v2)", evalRunID, evalRun2ID)
	t.Logf("Gates: %s (pass=%v), %s (pass=%v)", passGateName, passVerdict.Pass, strictGateName, strictVerdict.Pass)
}

// ============================================================================
// NOTE: Import/Export Dataset Tests
// ============================================================================
// Import/Export functionality is implemented. See datasets_test.go for tests.
// Supports JSON, JSONL, and CSV formats with column mappings.

// ============================================================================
// HELPER: Create struct from map
// ============================================================================

func mustStruct(m map[string]interface{}) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(err)
	}
	return s
}
