// Package integration contains integration tests for the eval service.
//
// The eval service supports both CRUD operations for eval runs and actual
// execution of evaluations. The execution engine:
// - Polls for pending runs and executes them
// - Calls the delos-gateway over HTTP (DELOS_GATEWAY_URL) for LLM completions
// - Runs evaluators (exact_match, contains, regex, json_schema, llm_judge, semantic_similarity)
// - Tracks progress and generates results
//
// See workflow_test.go for full end-to-end tests with Ollama.
//
//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// ============================================================================
// TEST: List available evaluators
// ============================================================================

func TestEvaluator_ListAvailable(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := evalClient.ListEvaluators(ctx, &evalv1.ListEvaluatorsRequest{})
	if err != nil {
		t.Fatalf("ListEvaluators failed: %v", err)
	}

	t.Logf("Available evaluators (%d):", len(resp.Evaluators))

	expectedTypes := map[string]bool{
		"exact_match":         false,
		"contains":            false,
		"semantic_similarity": false,
		"llm_judge":           false,
		"regex":               false,
		"json_schema":         false,
	}

	for _, e := range resp.Evaluators {
		t.Logf("  - %s (%s): %s", e.Type, e.Name, e.Description)
		if len(e.Params) > 0 {
			for _, p := range e.Params {
				t.Logf("      param: %s (%s) required=%v default=%s", p.Name, p.Type, p.Required, p.DefaultValue)
			}
		}
		expectedTypes[e.Type] = true
	}

	// Check all expected evaluators are present
	for evalType, found := range expectedTypes {
		if !found {
			t.Errorf("Expected evaluator type '%s' not found", evalType)
		}
	}
}

// ============================================================================
// TEST: Create eval run (CRUD operation - does NOT execute)
// ============================================================================

func TestEvalRun_Create(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a prompt
	slug := fmt.Sprintf("eval-crud-test-%d", time.Now().UnixNano())
	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Eval CRUD Test Prompt",
		Slug: slug,
		Messages: []*promptv1.PromptMessage{
			{Role: "user", Content: "{{question}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "question", Type: "string", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptResp.Prompt.Id})

	// Create a dataset
	datasetResp, err := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:     "Eval CRUD Test Dataset",
		PromptId: promptResp.Prompt.Id,
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetResp.Dataset.Id})

	// Create eval run
	runResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "Test Eval Run",
		Description:   "Testing CRUD operations",
		PromptId:      promptResp.Prompt.Id,
		PromptVersion: 1,
		DatasetId:     datasetResp.Dataset.Id,
		Config: &evalv1.EvalConfig{
			Provider: "ollama",
			Model:    "gemma3:4b",
			Evaluators: []*evalv1.EvaluatorConfig{
				{
					Type:   "exact_match",
					Weight: 1.0,
				},
			},
		},
		Metadata: map[string]string{
			"test": "true",
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}

	t.Logf("Created eval run: %s", runResp.EvalRun.Id)

	// Verify the response
	if runResp.EvalRun.Name != "Test Eval Run" {
		t.Errorf("Expected name 'Test Eval Run', got '%s'", runResp.EvalRun.Name)
	}
	if runResp.EvalRun.PromptId != promptResp.Prompt.Id {
		t.Errorf("Expected prompt_id '%s', got '%s'", promptResp.Prompt.Id, runResp.EvalRun.PromptId)
	}
	if runResp.EvalRun.DatasetId != datasetResp.Dataset.Id {
		t.Errorf("Expected dataset_id '%s', got '%s'", datasetResp.Dataset.Id, runResp.EvalRun.DatasetId)
	}

	// Status will be PENDING initially (execution engine picks it up asynchronously)
	if runResp.EvalRun.Status != evalv1.EvalRunStatus_EVAL_RUN_STATUS_PENDING {
		t.Errorf("Expected initial status PENDING, got %s", runResp.EvalRun.Status)
	}

	// Verify config was stored
	if runResp.EvalRun.Config.Provider != "ollama" {
		t.Errorf("Expected provider 'ollama', got '%s'", runResp.EvalRun.Config.Provider)
	}
	if len(runResp.EvalRun.Config.Evaluators) != 1 {
		t.Errorf("Expected 1 evaluator, got %d", len(runResp.EvalRun.Config.Evaluators))
	}
}

// ============================================================================
// TEST: Get eval run
// ============================================================================

func TestEvalRun_Get(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create prompt and dataset
	slug := fmt.Sprintf("eval-get-test-%d", time.Now().UnixNano())
	promptResp, _ := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Eval Get Test",
		Slug: slug,
		Messages: []*promptv1.PromptMessage{
			{Role: "user", Content: "{{q}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "q", Type: "string", Required: true},
		},
	})
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptResp.Prompt.Id})

	datasetResp, _ := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:     "Eval Get Test Dataset",
		PromptId: promptResp.Prompt.Id,
	})
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetResp.Dataset.Id})

	// Create eval run
	createResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:      "Get Test Run",
		PromptId:  promptResp.Prompt.Id,
		DatasetId: datasetResp.Dataset.Id,
		Config: &evalv1.EvalConfig{
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "contains", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}

	// Get the eval run
	getResp, err := evalClient.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{
		Id: createResp.EvalRun.Id,
	})
	if err != nil {
		t.Fatalf("GetEvalRun failed: %v", err)
	}

	if getResp.EvalRun.Id != createResp.EvalRun.Id {
		t.Errorf("Expected ID '%s', got '%s'", createResp.EvalRun.Id, getResp.EvalRun.Id)
	}
	if getResp.EvalRun.Name != "Get Test Run" {
		t.Errorf("Expected name 'Get Test Run', got '%s'", getResp.EvalRun.Name)
	}

	t.Logf("Successfully retrieved eval run: %s", getResp.EvalRun.Id)
}

// ============================================================================
// TEST: List eval runs
// ============================================================================

func TestEvalRun_List(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create prompt and dataset
	slug := fmt.Sprintf("eval-list-test-%d", time.Now().UnixNano())
	promptResp, _ := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Eval List Test",
		Slug: slug,
		Messages: []*promptv1.PromptMessage{
			{Role: "user", Content: "{{q}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "q", Type: "string", Required: true},
		},
	})
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptResp.Prompt.Id})

	datasetResp, _ := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:     "Eval List Test Dataset",
		PromptId: promptResp.Prompt.Id,
	})
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetResp.Dataset.Id})

	// Create multiple eval runs
	for i := 0; i < 3; i++ {
		_, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
			Name:      fmt.Sprintf("List Test Run %d", i+1),
			PromptId:  promptResp.Prompt.Id,
			DatasetId: datasetResp.Dataset.Id,
			Config: &evalv1.EvalConfig{
				Evaluators: []*evalv1.EvaluatorConfig{
					{Type: "exact_match", Weight: 1.0},
				},
			},
		})
		if err != nil {
			t.Fatalf("CreateEvalRun %d failed: %v", i+1, err)
		}
	}

	// List all runs
	listResp, err := evalClient.ListEvalRuns(ctx, &evalv1.ListEvalRunsRequest{
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListEvalRuns failed: %v", err)
	}

	t.Logf("Found %d eval runs (total_count=%d)", len(listResp.EvalRuns), listResp.TotalCount)

	// Should have at least 3 runs
	if len(listResp.EvalRuns) < 3 {
		t.Errorf("Expected at least 3 eval runs, got %d", len(listResp.EvalRuns))
	}

	// List with filter by prompt_id
	filteredResp, err := evalClient.ListEvalRuns(ctx, &evalv1.ListEvalRunsRequest{
		PromptId: promptResp.Prompt.Id,
		Limit:    100,
	})
	if err != nil {
		t.Fatalf("ListEvalRuns with filter failed: %v", err)
	}

	t.Logf("Found %d eval runs for prompt %s", len(filteredResp.EvalRuns), promptResp.Prompt.Id)

	// All returned runs should match the prompt_id
	for _, run := range filteredResp.EvalRuns {
		if run.PromptId != promptResp.Prompt.Id {
			t.Errorf("Expected prompt_id '%s', got '%s'", promptResp.Prompt.Id, run.PromptId)
		}
	}
}

// ============================================================================
// TEST: Cancel eval run
// ============================================================================

func TestEvalRun_Cancel(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create prompt and dataset
	slug := fmt.Sprintf("eval-cancel-test-%d", time.Now().UnixNano())
	promptResp, _ := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Eval Cancel Test",
		Slug: slug,
		Messages: []*promptv1.PromptMessage{
			{Role: "user", Content: "{{q}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "q", Type: "string", Required: true},
		},
	})
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptResp.Prompt.Id})

	datasetResp, _ := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:     "Eval Cancel Test Dataset",
		PromptId: promptResp.Prompt.Id,
	})
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetResp.Dataset.Id})

	// Create eval run
	createResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:      "Cancel Test Run",
		PromptId:  promptResp.Prompt.Id,
		DatasetId: datasetResp.Dataset.Id,
		Config: &evalv1.EvalConfig{
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "exact_match", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}

	// Verify it's pending
	if createResp.EvalRun.Status != evalv1.EvalRunStatus_EVAL_RUN_STATUS_PENDING {
		t.Fatalf("Expected status PENDING, got %s", createResp.EvalRun.Status)
	}

	// Cancel the run
	cancelResp, err := evalClient.CancelEvalRun(ctx, &evalv1.CancelEvalRunRequest{
		Id: createResp.EvalRun.Id,
	})
	if err != nil {
		t.Fatalf("CancelEvalRun failed: %v", err)
	}

	if cancelResp.EvalRun.Status != evalv1.EvalRunStatus_EVAL_RUN_STATUS_CANCELLED {
		t.Errorf("Expected status CANCELLED, got %s", cancelResp.EvalRun.Status)
	}

	t.Logf("Successfully cancelled eval run: %s (status=%s)", cancelResp.EvalRun.Id, cancelResp.EvalRun.Status)

	// Verify we can't cancel again
	_, err = evalClient.CancelEvalRun(ctx, &evalv1.CancelEvalRunRequest{
		Id: createResp.EvalRun.Id,
	})
	if err == nil {
		t.Error("Expected error when cancelling already cancelled run")
	} else {
		t.Logf("Correctly rejected re-cancel: %v", err)
	}
}

// ============================================================================
// TEST: Eval run with multiple evaluator configs
// ============================================================================

func TestEvalRun_MultipleEvaluatorConfigs(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create prompt and dataset
	slug := fmt.Sprintf("eval-multi-test-%d", time.Now().UnixNano())
	promptResp, _ := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Multi Evaluator Test",
		Slug: slug,
		Messages: []*promptv1.PromptMessage{
			{Role: "user", Content: "{{q}}"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "q", Type: "string", Required: true},
		},
	})
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptResp.Prompt.Id})

	datasetResp, _ := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:     "Multi Evaluator Test Dataset",
		PromptId: promptResp.Prompt.Id,
	})
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetResp.Dataset.Id})

	// Create eval run with multiple evaluators
	createResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:      "Multi Evaluator Run",
		PromptId:  promptResp.Prompt.Id,
		DatasetId: datasetResp.Dataset.Id,
		Config: &evalv1.EvalConfig{
			Provider:    "ollama",
			Model:       "gemma3:4b",
			Concurrency: 2,
			Evaluators: []*evalv1.EvaluatorConfig{
				{
					Type:   "exact_match",
					Name:   "Exact",
					Weight: 0.3,
				},
				{
					Type:   "contains",
					Name:   "Contains Check",
					Weight: 0.3,
					Params: map[string]string{
						"case_sensitive": "false",
					},
				},
				{
					Type:   "regex",
					Name:   "Pattern Match",
					Weight: 0.4,
					Params: map[string]string{
						"pattern": `\d+`,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}

	// Verify all evaluators were stored
	if len(createResp.EvalRun.Config.Evaluators) != 3 {
		t.Errorf("Expected 3 evaluators, got %d", len(createResp.EvalRun.Config.Evaluators))
	}

	// Verify weights sum (checking they were stored correctly)
	var totalWeight float64
	for _, e := range createResp.EvalRun.Config.Evaluators {
		totalWeight += e.Weight
		t.Logf("  Evaluator: %s (%s) weight=%.1f", e.Type, e.Name, e.Weight)
	}

	if totalWeight < 0.99 || totalWeight > 1.01 {
		t.Errorf("Expected weights to sum to 1.0, got %.2f", totalWeight)
	}

	t.Logf("Successfully stored eval run with %d evaluators", len(createResp.EvalRun.Config.Evaluators))
}

// ============================================================================
// TEST: Eval service health check (from evaluators test)
// ============================================================================

func TestEvalService_HealthFromEvaluatorsTest(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := evalClient.Health(ctx, &evalv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("Expected status 'healthy', got '%s'", resp.Status)
	}

	t.Logf("Eval service health: status=%s version=%s", resp.Status, resp.Version)
}
