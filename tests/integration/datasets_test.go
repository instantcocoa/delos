// Package integration contains integration tests for Delos services.
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
	"google.golang.org/protobuf/types/known/structpb"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

func getDatasetsClient(t *testing.T) (datasetsv1.DatasetsServiceClient, func()) {
	t.Helper()

	addr := os.Getenv("DELOS_DATASETS_ADDR")
	if addr == "" {
		addr = "localhost:9003"
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect to datasets service: %v", err)
	}

	return datasetsv1.NewDatasetsServiceClient(conn), func() { conn.Close() }
}

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
	t.Logf("Created dataset with ID: %s", datasetID)

	defer func() {
		client.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})
	}()

	// Get the dataset
	getResp, err := client.GetDataset(ctx, &datasetsv1.GetDatasetRequest{Id: datasetID})
	if err != nil {
		t.Fatalf("GetDataset failed: %v", err)
	}

	if getResp.Dataset.Name != "Test Dataset" {
		t.Errorf("Expected name 'Test Dataset', got '%s'", getResp.Dataset.Name)
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
		t.Errorf("Expected 2 examples, got %d", len(getExamplesResp.Examples))
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
}
