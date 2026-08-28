// Package integration contains integration tests for Delos services.
// Run with: go test -tags=integration ./tests/integration/...
//
//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// getPromptClient lives in clients_test.go; all control-plane services share
// one gRPC address.

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

	// The history of a prompt edited twice is three versions: the original plus
	// both edits, each carrying the change description it was saved with.
	historyResp, err := client.GetPromptHistory(ctx, &promptv1.GetPromptHistoryRequest{
		Id: promptID,
	})
	if err != nil {
		t.Fatalf("GetPromptHistory failed: %v", err)
	}

	if len(historyResp.Versions) != 3 {
		t.Fatalf("expected 3 versions in the history of a prompt updated twice, got %d: %+v",
			len(historyResp.Versions), historyResp.Versions)
	}

	wantChange := map[int32]string{2: "Updated to v2", 3: "Updated to v3"}
	seen := map[int32]bool{}
	for _, v := range historyResp.Versions {
		if seen[v.Version] {
			t.Errorf("version %d appears more than once in the history", v.Version)
		}
		seen[v.Version] = true
		if want, ok := wantChange[v.Version]; ok && v.ChangeDescription != want {
			t.Errorf("version %d: expected change description %q, got %q",
				v.Version, want, v.ChangeDescription)
		}
	}
	for _, want := range []int32{1, 2, 3} {
		if !seen[want] {
			t.Errorf("version %d missing from the history", want)
		}
	}
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
	// The two versions differ only in their messages, so the diff must say so.
	if len(compareResp.Diffs) == 0 {
		t.Fatal("expected CompareVersions to report at least one diff between v1 and v2")
	}
	foundMessages := false
	for _, d := range compareResp.Diffs {
		if d.Field == "" {
			t.Error("diff entry with an empty field name")
		}
		if strings.Contains(strings.ToLower(d.Field), "message") {
			foundMessages = true
		}
	}
	if !foundMessages {
		t.Errorf("expected a diff on the messages field, got %+v", compareResp.Diffs)
	}
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

	// Delete is a soft delete: the record survives, marked archived, so history
	// and past eval runs keep resolving. It must not come back as active.
	getResp, err := client.GetPrompt(ctx, &promptv1.GetPromptRequest{Id: promptID})
	if err != nil {
		t.Fatalf("GetPrompt after a soft delete failed: %v", err)
	}
	if getResp.Prompt == nil {
		t.Fatal("expected the soft-deleted prompt to still be retrievable by id")
	}
	if got := getResp.Prompt.Status; got != promptv1.PromptStatus_PROMPT_STATUS_ARCHIVED {
		t.Errorf("expected a deleted prompt to be ARCHIVED, got %s", got)
	}
}
