// Package integration contains error handling tests for both binaries.
//
// Control-plane tests verify gRPC error codes (NOT_FOUND, INVALID_ARGUMENT,
// ...). Gateway tests verify the OpenAI-compatible HTTP error envelope
// {"error":{"message","type","code"}} and its status codes.
//
//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// ============================================================================
// Prompt Service Error Handling
// ============================================================================

func TestPromptService_GetPrompt_NotFound(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A missing prompt is NOT_FOUND. Returning a nil prompt with no error is a
	// contract violation: callers cannot distinguish it from an empty result.
	resp, err := client.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Id: "nonexistent-prompt-id",
	})
	if err == nil {
		t.Fatalf("expected NOT_FOUND for a nonexistent prompt id, got prompt=%+v", resp.GetPrompt())
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("expected NOT_FOUND, got %s: %s", st.Code(), st.Message())
	}
}

// TestPromptService_GetPrompt_NotFound_BySlug covers the reference form the CLI
// uses: an unknown slug is NOT_FOUND, not a null prompt.
func TestPromptService_GetPrompt_NotFound_BySlug(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	slug := fmt.Sprintf("no-such-slug-%d", time.Now().UnixNano())
	resp, err := client.GetPrompt(ctx, &promptv1.GetPromptRequest{Reference: slug})
	if err == nil {
		t.Fatalf("expected NOT_FOUND for an unknown slug %q, got prompt=%+v", slug, resp.GetPrompt())
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("expected NOT_FOUND, got %s: %s", st.Code(), st.Message())
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

	if st.Code() != codes.NotFound {
		t.Errorf("expected NOT_FOUND when deleting a nonexistent prompt, got %s: %s",
			st.Code(), st.Message())
	}
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

	if st.Code() != codes.AlreadyExists {
		t.Errorf("expected ALREADY_EXISTS for a duplicate slug, got %s: %s", st.Code(), st.Message())
	}
	if !strings.Contains(st.Message(), slug) {
		t.Errorf("expected the error to name the conflicting slug %q, got %q", slug, st.Message())
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

	if st.Code() != codes.InvalidArgument && st.Code() != codes.NotFound {
		t.Errorf("expected INVALID_ARGUMENT or NOT_FOUND adding to a nonexistent dataset, got %s: %s",
			st.Code(), st.Message())
	}
}

func TestDatasetsService_ImportExamples_NoData(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use a dataset that really exists, so the only thing wrong with the request
	// is the missing payload.
	created, err := client.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name: fmt.Sprintf("import-no-data-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}
	defer client.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: created.Dataset.Id})

	_, err = client.ImportExamples(ctx, &datasetsv1.ImportExamplesRequest{
		DatasetId: created.Dataset.Id,
		Format:    datasetsv1.DataFormat_DATA_FORMAT_JSON,
	})
	if err == nil {
		t.Fatal("Expected error for import with no data")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected INVALID_ARGUMENT importing with no data, got %s: %s",
			st.Code(), st.Message())
	}
}

func TestDatasetsService_ExportExamples_NonexistentDataset(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Exporting a dataset that does not exist is NOT_FOUND. Returning an empty
	// export would tell a caller their dataset is empty, not missing.
	resp, err := client.ExportExamples(ctx, &datasetsv1.ExportExamplesRequest{
		DatasetId: "nonexistent-dataset",
		Format:    datasetsv1.DataFormat_DATA_FORMAT_JSON,
	})
	if err == nil {
		t.Fatalf("expected NOT_FOUND exporting a nonexistent dataset, got %d examples",
			resp.ExportedCount)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("expected NOT_FOUND, got %s: %s", st.Code(), st.Message())
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

	if st.Code() != codes.NotFound {
		t.Errorf("expected NOT_FOUND cancelling a nonexistent eval run, got %s: %s",
			st.Code(), st.Message())
	}
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

	if st.Code() != codes.NotFound {
		t.Errorf("expected NOT_FOUND comparing nonexistent runs, got %s: %s",
			st.Code(), st.Message())
	}
}

// ============================================================================
// Gateway Error Handling (OpenAI-compatible surface)
// ============================================================================

func TestGateway_Complete_UnknownModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	g := newGatewayClient(15 * time.Second)
	statusCode, body, err := g.postJSON(ctx, "/v1/chat/completions", gwChatRequest{
		Model:    "nonexistent-provider/some-model",
		Messages: []gwChatMessage{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("gateway request failed: %v", err)
	}
	if statusCode != http.StatusNotFound {
		t.Fatalf("Expected 404 for unknown model, got %d: %s", statusCode, body)
	}
	env := decodeGatewayError(t, body)
	if env.Error.Code != "model_not_found" {
		t.Errorf("Expected code model_not_found, got %q", env.Error.Code)
	}
	if !strings.Contains(env.Error.Message, "request id:") {
		t.Errorf("Expected error message to carry the request id, got %q", env.Error.Message)
	}
}

func TestGateway_Complete_MalformedJSON(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	g := newGatewayClient(15 * time.Second)
	statusCode, body, err := g.postRaw(ctx, "/v1/chat/completions", []byte(`{"model":`))
	if err != nil {
		t.Fatalf("gateway request failed: %v", err)
	}
	if statusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 for malformed JSON, got %d: %s", statusCode, body)
	}
	env := decodeGatewayError(t, body)
	if env.Error.Code != "invalid_json" {
		t.Errorf("Expected code invalid_json, got %q", env.Error.Code)
	}
}

func TestGateway_Complete_MissingFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	g := newGatewayClient(15 * time.Second)

	statusCode, body, err := g.postJSON(ctx, "/v1/chat/completions", map[string]any{
		"messages": []gwChatMessage{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("gateway request failed: %v", err)
	}
	if statusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 for missing model, got %d: %s", statusCode, body)
	}
	if env := decodeGatewayError(t, body); env.Error.Code != "missing_model" {
		t.Errorf("Expected code missing_model, got %q", env.Error.Code)
	}

	statusCode, body, err = g.postJSON(ctx, "/v1/chat/completions", map[string]any{
		"model": "gpt-4o",
	})
	if err != nil {
		t.Fatalf("gateway request failed: %v", err)
	}
	if statusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400 for missing messages, got %d: %s", statusCode, body)
	}
	if env := decodeGatewayError(t, body); env.Error.Code != "missing_messages" {
		t.Errorf("Expected code missing_messages, got %q", env.Error.Code)
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

	// An eval run that names a prompt and dataset which do not exist can never
	// execute, so it must be rejected at creation rather than accepted and left
	// to fail asynchronously.
	resp, err := client.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:      "Invalid References Test",
		PromptId:  "nonexistent-prompt",
		DatasetId: "nonexistent-dataset",
		Config: &evalv1.EvalConfig{
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "exact_match", Weight: 1.0},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected CreateEvalRun to reject a nonexistent prompt and dataset, got run %s",
			resp.GetEvalRun().GetId())
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got: %v", err)
	}
	if st.Code() != codes.NotFound && st.Code() != codes.InvalidArgument {
		t.Errorf("expected NOT_FOUND or INVALID_ARGUMENT for unresolvable references, got %s: %s",
			st.Code(), st.Message())
	}
}
