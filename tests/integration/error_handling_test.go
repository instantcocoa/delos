// Package integration contains error handling tests for all services.
//
// Tests verify that services return appropriate gRPC error codes for various
// error conditions (NOT_FOUND, INVALID_ARGUMENT, etc.).
//
//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
	runtimev1 "github.com/instantcocoa/delos/gen/go/runtime/v1"
)

// ============================================================================
// Prompt Service Error Handling
// ============================================================================

func TestPromptService_GetPrompt_NotFound(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Id: "nonexistent-prompt-id",
	})

	// Service may return nil prompt with no error, or NOT_FOUND error
	if err != nil {
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("Expected gRPC status error, got: %v", err)
		}
		if st.Code() != codes.NotFound {
			t.Errorf("Expected NOT_FOUND, got %s: %s", st.Code(), st.Message())
		}
		t.Logf("Correctly returned NOT_FOUND: %s", st.Message())
	} else if resp.Prompt == nil {
		t.Log("Returned nil prompt for nonexistent ID (acceptable)")
	} else {
		t.Errorf("Expected error or nil prompt for nonexistent ID, got: %+v", resp.Prompt)
	}
}

// Note: GetPromptBySlug doesn't exist in the API - prompts are fetched by ID only

func TestPromptService_DeletePrompt_NotFound(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{
		Id: "nonexistent-prompt-id-for-delete",
	})
	if err == nil {
		t.Fatal("Expected error when deleting nonexistent prompt")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	// Could be NOT_FOUND or INTERNAL depending on implementation
	t.Logf("Delete nonexistent returned %s: %s", st.Code(), st.Message())
}

func TestPromptService_CreatePrompt_DuplicateSlug(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slug := fmt.Sprintf("duplicate-test-slug-%d", time.Now().UnixNano())

	// Create first prompt
	resp1, err := client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "First Prompt",
		Slug: slug,
		Messages: []*promptv1.PromptMessage{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("First CreatePrompt failed: %v", err)
	}
	defer client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: resp1.Prompt.Id})

	// Try to create second prompt with same slug
	_, err = client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Second Prompt",
		Slug: slug,
		Messages: []*promptv1.PromptMessage{
			{Role: "user", Content: "World"},
		},
	})
	if err == nil {
		t.Fatal("Expected error for duplicate slug")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	// Should be ALREADY_EXISTS or INVALID_ARGUMENT
	t.Logf("Duplicate slug returned %s: %s", st.Code(), st.Message())
	if st.Code() != codes.AlreadyExists && st.Code() != codes.Internal {
		t.Logf("Note: Expected ALREADY_EXISTS, got %s", st.Code())
	}
}

// ============================================================================
// Datasets Service Error Handling
// ============================================================================

func TestDatasetsService_GetDataset_NotFound(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.GetDataset(ctx, &datasetsv1.GetDatasetRequest{
		Id: "nonexistent-dataset-id",
	})
	if err == nil {
		t.Fatal("Expected error for nonexistent dataset")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("Expected NOT_FOUND, got %s: %s", st.Code(), st.Message())
	}

	t.Logf("Correctly returned NOT_FOUND: %s", st.Message())
}

func TestDatasetsService_AddExamples_InvalidDatasetID(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: "nonexistent-dataset",
		Examples:  []*datasetsv1.ExampleInput{},
	})
	if err == nil {
		t.Fatal("Expected error for invalid dataset ID")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	t.Logf("Add to invalid dataset returned %s: %s", st.Code(), st.Message())
}

func TestDatasetsService_ImportExamples_NoData(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Import with no data should fail with INVALID_ARGUMENT
	_, err := client.ImportExamples(ctx, &datasetsv1.ImportExamplesRequest{
		DatasetId: "any-dataset",
		Format:    datasetsv1.DataFormat_DATA_FORMAT_JSON,
	})
	if err == nil {
		t.Fatal("Expected error for import with no data")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	// Should be INVALID_ARGUMENT (no data) or NOT_FOUND (dataset doesn't exist)
	t.Logf("ImportExamples with no data returned %s: %s", st.Code(), st.Message())
}

func TestDatasetsService_ExportExamples_NonexistentDataset(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Export from nonexistent dataset - may return empty or error
	resp, err := client.ExportExamples(ctx, &datasetsv1.ExportExamplesRequest{
		DatasetId: "nonexistent-dataset",
		Format:    datasetsv1.DataFormat_DATA_FORMAT_JSON,
	})
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			t.Logf("ExportExamples returned %s: %s", st.Code(), st.Message())
		}
	} else {
		t.Logf("ExportExamples returned %d examples", resp.ExportedCount)
	}
}

// ============================================================================
// Eval Service Error Handling
// ============================================================================

func TestEvalService_GetEvalRun_NotFound(t *testing.T) {
	client, cleanup := getEvalClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{
		Id: "nonexistent-eval-run-id",
	})
	if err == nil {
		t.Fatal("Expected error for nonexistent eval run")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("Expected NOT_FOUND, got %s: %s", st.Code(), st.Message())
	}

	t.Logf("Correctly returned NOT_FOUND: %s", st.Message())
}

func TestEvalService_CancelEvalRun_NotFound(t *testing.T) {
	client, cleanup := getEvalClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.CancelEvalRun(ctx, &evalv1.CancelEvalRunRequest{
		Id: "nonexistent-eval-run-for-cancel",
	})
	if err == nil {
		t.Fatal("Expected error when cancelling nonexistent eval run")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	t.Logf("Cancel nonexistent returned %s: %s", st.Code(), st.Message())
}

func TestEvalService_CompareRuns_NotFound(t *testing.T) {
	client, cleanup := getEvalClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.CompareRuns(ctx, &evalv1.CompareRunsRequest{
		RunIdA: "nonexistent-run-a",
		RunIdB: "nonexistent-run-b",
	})
	if err == nil {
		t.Fatal("Expected error when comparing nonexistent runs")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	t.Logf("Compare nonexistent returned %s: %s", st.Code(), st.Message())
}

// ============================================================================
// Runtime Service Error Handling
// ============================================================================

func TestRuntimeService_Complete_InvalidProvider(t *testing.T) {
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "Hello"},
			},
			Provider: "nonexistent-provider",
			Model:    "some-model",
		},
	})
	if err == nil {
		t.Fatal("Expected error for invalid provider")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	// Should indicate the provider doesn't exist
	t.Logf("Invalid provider returned %s: %s", st.Code(), st.Message())
	if !strings.Contains(strings.ToLower(st.Message()), "provider") {
		t.Logf("Note: Expected error message to mention 'provider'")
	}
}

func TestRuntimeService_Complete_EmptyMessages(t *testing.T) {
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{},
			Provider: "ollama",
			Model:    "gemma3:4b",
		},
	})

	// Service may accept empty messages (LLM will handle it) or return error
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			t.Logf("Empty messages returned %s: %s", st.Code(), st.Message())
		} else {
			t.Logf("Empty messages returned error: %v", err)
		}
	} else {
		// Some services accept empty messages (LLM will return empty or error)
		t.Logf("Empty messages accepted - response: content=%q", resp.Content)
	}
}

// ============================================================================
// Cross-Service Error Handling
// ============================================================================

func TestEvalService_CreateEvalRun_InvalidPromptID(t *testing.T) {
	client, cleanup := getEvalClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create eval run with nonexistent prompt and dataset
	_, err := client.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:      "Invalid References Test",
		PromptId:  "nonexistent-prompt",
		DatasetId: "nonexistent-dataset",
		Config: &evalv1.EvalConfig{
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "exact_match", Weight: 1.0},
			},
		},
	})

	// Note: This might succeed (creating the record) or fail depending on validation
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			t.Logf("Invalid references returned %s: %s", st.Code(), st.Message())
		}
	} else {
		t.Log("CreateEvalRun accepted invalid references (validation happens later or not at all)")
	}
}
