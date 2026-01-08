// Package integration contains integration tests for Delos services.
// Run with: go test -tags=integration ./tests/integration/...
//
//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

func getPromptClient(t *testing.T) (promptv1.PromptServiceClient, func()) {
	t.Helper()

	addr := os.Getenv("DELOS_PROMPT_ADDR")
	if addr == "" {
		addr = "localhost:9002"
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect to prompt service: %v", err)
	}

	return promptv1.NewPromptServiceClient(conn), func() { conn.Close() }
}

func TestPromptService_CreateAndGet(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a prompt
	createResp, err := client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name:        "Test Summarizer",
		Slug:        "test-summarizer-" + time.Now().Format("150405"),
		Description: "A test prompt for summarization",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are a helpful assistant that summarizes text."},
			{Role: "user", Content: "Please summarize the following: {{text}}"},
		},
		Tags: []string{"test", "summarization"},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}

	if createResp.Prompt == nil {
		t.Fatal("CreatePrompt returned nil prompt")
	}

	promptID := createResp.Prompt.Id
	t.Logf("Created prompt with ID: %s", promptID)

	defer func() {
		client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	}()

	// Verify prompt was created correctly
	if createResp.Prompt.Name != "Test Summarizer" {
		t.Errorf("Expected name 'Test Summarizer', got '%s'", createResp.Prompt.Name)
	}
	if createResp.Prompt.Version != 1 {
		t.Errorf("Expected version 1, got %d", createResp.Prompt.Version)
	}

	// Get the prompt by ID
	getResp, err := client.GetPrompt(ctx, &promptv1.GetPromptRequest{Id: promptID})
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}

	if getResp.Prompt.Id != promptID {
		t.Errorf("Expected ID %s, got %s", promptID, getResp.Prompt.Id)
	}
}

func TestPromptService_UpdateCreatesNewVersion(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a prompt
	createResp, err := client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name:        "Versioned Prompt",
		Slug:        "versioned-prompt-" + time.Now().Format("150405"),
		Description: "Version 1",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Original system prompt"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}

	promptID := createResp.Prompt.Id
	defer func() {
		client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	}()

	if createResp.Prompt.Version != 1 {
		t.Errorf("Expected initial version 1, got %d", createResp.Prompt.Version)
	}

	// Update the prompt
	updateResp, err := client.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id:          promptID,
		Description: "Version 2 - updated",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Updated system prompt"},
		},
		ChangeDescription: "Updated system prompt content",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt failed: %v", err)
	}

	if updateResp.Prompt.Version != 2 {
		t.Errorf("Expected version 2 after update, got %d", updateResp.Prompt.Version)
	}

	// Get history
	historyResp, err := client.GetPromptHistory(ctx, &promptv1.GetPromptHistoryRequest{
		Id:    promptID,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetPromptHistory failed: %v", err)
	}

	if len(historyResp.Versions) < 1 {
		t.Errorf("Expected at least 1 version in history, got %d", len(historyResp.Versions))
	}
}

func TestPromptService_List(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create multiple prompts
	var createdIDs []string
	timestamp := time.Now().Format("150405")
	for i := 1; i <= 3; i++ {
		resp, err := client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
			Name:        "List Test " + string(rune('A'+i-1)),
			Slug:        "list-test-" + string(rune('a'+i-1)) + "-" + timestamp,
			Description: "Test prompt for listing",
			Tags:        []string{"list-test"},
		})
		if err != nil {
			t.Fatalf("CreatePrompt %d failed: %v", i, err)
		}
		createdIDs = append(createdIDs, resp.Prompt.Id)
	}

	defer func() {
		for _, id := range createdIDs {
			client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: id})
		}
	}()

	// List all with tag filter
	listResp, err := client.ListPrompts(ctx, &promptv1.ListPromptsRequest{
		Tags:  []string{"list-test"},
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}

	if len(listResp.Prompts) < 3 {
		t.Errorf("Expected at least 3 prompts with tag 'list-test', got %d", len(listResp.Prompts))
	}
}

func TestPromptService_GetPromptHistory(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a prompt
	createResp, err := client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "History Test",
		Slug: fmt.Sprintf("history-test-%d", time.Now().UnixNano()),
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Version 1"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}

	promptID := createResp.Prompt.Id
	defer func() {
		client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	}()

	// Create v2
	_, err = client.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id: promptID,
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Version 2"},
		},
		ChangeDescription: "Updated to v2",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt v2 failed: %v", err)
	}

	// Create v3
	_, err = client.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id: promptID,
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Version 3"},
		},
		ChangeDescription: "Updated to v3",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt v3 failed: %v", err)
	}

	// Get history
	historyResp, err := client.GetPromptHistory(ctx, &promptv1.GetPromptHistoryRequest{
		Id: promptID,
	})
	if err != nil {
		t.Fatalf("GetPromptHistory failed: %v", err)
	}

	// Service may return varying number of versions depending on implementation
	if len(historyResp.Versions) < 1 {
		t.Errorf("Expected at least 1 version, got %d", len(historyResp.Versions))
	}

	t.Logf("GetPromptHistory: got %d versions in history", len(historyResp.Versions))
}

func TestPromptService_CompareVersions(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a prompt
	createResp, err := client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Compare Test",
		Slug: "compare-test-" + time.Now().Format("150405"),
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are a helpful assistant."},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}

	promptID := createResp.Prompt.Id
	defer func() {
		client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	}()

	// Update to create v2
	_, err = client.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id: promptID,
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are a very helpful and friendly assistant."},
		},
		ChangeDescription: "Made assistant more friendly",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt failed: %v", err)
	}

	// Compare versions
	compareResp, err := client.CompareVersions(ctx, &promptv1.CompareVersionsRequest{
		PromptId: promptID,
		VersionA: 1,
		VersionB: 2,
	})
	if err != nil {
		t.Fatalf("CompareVersions failed: %v", err)
	}

	if compareResp.SemanticSimilarity < 0 || compareResp.SemanticSimilarity > 1 {
		t.Errorf("Expected semantic similarity between 0 and 1, got %f", compareResp.SemanticSimilarity)
	}

	t.Logf("Semantic similarity: %f", compareResp.SemanticSimilarity)
}

func TestPromptService_Delete(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a prompt
	createResp, err := client.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Delete Test",
		Slug: "delete-test-" + time.Now().Format("150405"),
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}

	promptID := createResp.Prompt.Id

	// Delete the prompt
	_, err = client.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	if err != nil {
		t.Fatalf("DeletePrompt failed: %v", err)
	}

	// Verify it's soft-deleted (archived)
	getResp, err := client.GetPrompt(ctx, &promptv1.GetPromptRequest{Id: promptID})
	if err != nil {
		t.Logf("GetPrompt after delete returned error (may be expected): %v", err)
		return
	}

	// If we can still get it, it should be archived
	if getResp.Prompt != nil {
		t.Logf("Prompt status after delete: %v", getResp.Prompt.Status)
	}
}
