// Package integration contains integration tests for Delos services.
//
//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// getDatasetsClient lives in clients_test.go; all control-plane services share
// one gRPC address.

// Helper to create structpb.Struct from map
func toStruct(t *testing.T, m map[string]interface{}) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("failed to create struct: %v", err)
	}
	return s
}

func TestDatasetsService_CreateAndGet(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a dataset
	createResp, err := client.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:        "Test Dataset",
		Description: "A test dataset for integration testing",
		Tags:        []string{"test", "integration"},
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}

	if createResp.Dataset == nil {
		t.Fatal("CreateDataset returned nil dataset")
	}

	datasetID := createResp.Dataset.Id
	defer func() {
		client.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})
	}()

	// Get the dataset
	getResp, err := client.GetDataset(ctx, &datasetsv1.GetDatasetRequest{Id: datasetID})
	if err != nil {
		t.Fatalf("GetDataset failed: %v", err)
	}

	if getResp.Dataset.Id != datasetID {
		t.Errorf("Expected id %s, got %s", datasetID, getResp.Dataset.Id)
	}
	if getResp.Dataset.Name != "Test Dataset" {
		t.Errorf("Expected name 'Test Dataset', got '%s'", getResp.Dataset.Name)
	}
	if getResp.Dataset.Description != "A test dataset for integration testing" {
		t.Errorf("Expected the description to round-trip, got '%s'", getResp.Dataset.Description)
	}
	if len(getResp.Dataset.Tags) != 2 {
		t.Errorf("Expected the 2 tags to round-trip, got %v", getResp.Dataset.Tags)
	}

	// Update, then read back: the new description must stick and the name must
	// not be clobbered by a partial update.
	updateResp, err := client.UpdateDataset(ctx, &datasetsv1.UpdateDatasetRequest{
		Id:          datasetID,
		Description: "Updated description",
	})
	if err != nil {
		t.Fatalf("UpdateDataset failed: %v", err)
	}
	if updateResp.Dataset.Description != "Updated description" {
		t.Errorf("UpdateDataset returned description '%s', want 'Updated description'",
			updateResp.Dataset.Description)
	}
	reread, err := client.GetDataset(ctx, &datasetsv1.GetDatasetRequest{Id: datasetID})
	if err != nil {
		t.Fatalf("GetDataset after update failed: %v", err)
	}
	if reread.Dataset.Description != "Updated description" {
		t.Errorf("update did not persist: got description '%s'", reread.Dataset.Description)
	}
	if reread.Dataset.Name != "Test Dataset" {
		t.Errorf("a description-only update clobbered the name: got '%s'", reread.Dataset.Name)
	}
}

func TestDatasetsService_AddExamples(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a dataset
	createResp, err := client.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:        "Examples Test",
		Description: "Dataset for testing examples",
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}

	datasetID := createResp.Dataset.Id
	defer func() {
		client.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})
	}()

	// Add examples using ExampleInput
	addResp, err := client.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: datasetID,
		Examples: []*datasetsv1.ExampleInput{
			{
				Input:          toStruct(t, map[string]interface{}{"text": "Hello, how are you?"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"response": "I'm doing well!"}),
				Metadata:       map[string]string{"category": "greeting"},
			},
			{
				Input:          toStruct(t, map[string]interface{}{"text": "What is 2+2?"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"response": "4"}),
				Metadata:       map[string]string{"category": "math"},
			},
		},
	})
	if err != nil {
		t.Fatalf("AddExamples failed: %v", err)
	}

	if addResp.AddedCount != 2 {
		t.Errorf("Expected 2 examples added, got %d", addResp.AddedCount)
	}

	// Get examples
	getExamplesResp, err := client.GetExamples(ctx, &datasetsv1.GetExamplesRequest{
		DatasetId: datasetID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetExamples failed: %v", err)
	}

	if len(getExamplesResp.Examples) != 2 {
		t.Fatalf("Expected 2 examples, got %d", len(getExamplesResp.Examples))
	}
	if getExamplesResp.TotalCount != 2 {
		t.Errorf("Expected total_count 2, got %d", getExamplesResp.TotalCount)
	}

	// The metadata sent with each example must come back with it.
	categories := map[string]bool{}
	for _, ex := range getExamplesResp.Examples {
		if ex.Id == "" {
			t.Error("example stored without an id")
		}
		if ex.Input == nil {
			t.Errorf("example %s stored without its input", ex.Id)
		}
		categories[ex.Metadata["category"]] = true
	}
	for _, want := range []string{"greeting", "math"} {
		if !categories[want] {
			t.Errorf("example metadata category %q did not round-trip, got %v", want, categories)
		}
	}

	// Removing one example leaves exactly the other behind.
	removed := getExamplesResp.Examples[0].Id
	removeResp, err := client.RemoveExamples(ctx, &datasetsv1.RemoveExamplesRequest{
		DatasetId:  datasetID,
		ExampleIds: []string{removed},
	})
	if err != nil {
		t.Fatalf("RemoveExamples failed: %v", err)
	}
	if removeResp.RemovedCount != 1 {
		t.Errorf("Expected RemovedCount 1, got %d", removeResp.RemovedCount)
	}

	afterResp, err := client.GetExamples(ctx, &datasetsv1.GetExamplesRequest{
		DatasetId: datasetID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetExamples after remove failed: %v", err)
	}
	if len(afterResp.Examples) != 1 {
		t.Fatalf("Expected 1 example left after removing one, got %d", len(afterResp.Examples))
	}
	if afterResp.Examples[0].Id == removed {
		t.Errorf("RemoveExamples removed the wrong example: %s is still present", removed)
	}
}

func TestDatasetsService_LinkToPrompt(t *testing.T) {
	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a prompt with unique slug
	slug := fmt.Sprintf("dataset-link-test-%d", time.Now().UnixNano())
	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Dataset Link Test Prompt",
		Slug: slug,
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Answer questions."},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}

	promptID := promptResp.Prompt.Id
	defer func() {
		promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	}()

	// Create a dataset linked to the prompt
	datasetResp, err := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:     "Linked Dataset",
		PromptId: promptID,
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}

	datasetID := datasetResp.Dataset.Id
	defer func() {
		datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})
	}()

	if datasetResp.Dataset.PromptId != promptID {
		t.Errorf("Expected prompt ID %s, got %s", promptID, datasetResp.Dataset.PromptId)
	}
}

func TestDatasetsService_List(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create multiple datasets
	var createdIDs []string
	for i := 1; i <= 3; i++ {
		resp, err := client.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
			Name: "List Test Dataset " + string(rune('A'+i-1)),
			Tags: []string{"list-test"},
		})
		if err != nil {
			t.Fatalf("CreateDataset %d failed: %v", i, err)
		}
		createdIDs = append(createdIDs, resp.Dataset.Id)
	}

	defer func() {
		for _, id := range createdIDs {
			client.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: id})
		}
	}()

	// List with tag filter
	listResp, err := client.ListDatasets(ctx, &datasetsv1.ListDatasetsRequest{
		Tags:  []string{"list-test"},
		Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListDatasets failed: %v", err)
	}

	if len(listResp.Datasets) < 3 {
		t.Errorf("Expected at least 3 datasets, got %d", len(listResp.Datasets))
	}
	// A tag filter that returns untagged datasets is not filtering.
	want := map[string]bool{}
	for _, id := range createdIDs {
		want[id] = true
	}
	returned := map[string]bool{}
	for _, d := range listResp.Datasets {
		returned[d.Id] = true
		if !contains(d.Tags, "list-test") {
			t.Errorf("ListDatasets(tags=[list-test]) returned dataset %s with tags %v", d.Id, d.Tags)
		}
	}
	for id := range want {
		if !returned[id] {
			t.Errorf("ListDatasets(tags=[list-test]) omitted dataset %s, which carries the tag", id)
		}
	}
}

// contains reports whether haystack holds needle.
func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
