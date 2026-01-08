package datasets

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStore_CreateAndGetDataset(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	dataset := &Dataset{
		ID:          "test-id",
		Name:        "Test Dataset",
		Description: "A test dataset",
		CreatedAt:   time.Now(),
		LastUpdated: time.Now(),
	}

	if err := store.CreateDataset(ctx, dataset); err != nil {
		t.Fatalf("failed to create dataset: %v", err)
	}

	retrieved, err := store.GetDataset(ctx, "test-id")
	if err != nil {
		t.Fatalf("failed to get dataset: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected dataset, got nil")
	}

	if retrieved.Name != "Test Dataset" {
		t.Errorf("expected name 'Test Dataset', got '%s'", retrieved.Name)
	}
}

func TestMemoryStore_CreateDuplicateDataset(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	dataset := &Dataset{ID: "test-id", Name: "Test"}
	if err := store.CreateDataset(ctx, dataset); err != nil {
		t.Fatalf("failed to create dataset: %v", err)
	}

	err := store.CreateDataset(ctx, dataset)
	if err == nil {
		t.Fatal("expected error for duplicate dataset")
	}
}

func TestMemoryStore_GetNonexistentDataset(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	retrieved, err := store.GetDataset(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Fatal("expected nil for nonexistent dataset")
	}
}

func TestMemoryStore_UpdateDataset(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	dataset := &Dataset{ID: "test-id", Name: "Original"}
	if err := store.CreateDataset(ctx, dataset); err != nil {
		t.Fatalf("failed to create dataset: %v", err)
	}

	dataset.Name = "Updated"
	if err := store.UpdateDataset(ctx, dataset); err != nil {
		t.Fatalf("failed to update dataset: %v", err)
	}

	retrieved, _ := store.GetDataset(ctx, "test-id")
	if retrieved.Name != "Updated" {
		t.Errorf("expected name 'Updated', got '%s'", retrieved.Name)
	}
}

func TestMemoryStore_DeleteDataset(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	dataset := &Dataset{ID: "test-id", Name: "Test"}
	if err := store.CreateDataset(ctx, dataset); err != nil {
		t.Fatalf("failed to create dataset: %v", err)
	}

	if err := store.DeleteDataset(ctx, "test-id"); err != nil {
		t.Fatalf("failed to delete dataset: %v", err)
	}

	retrieved, _ := store.GetDataset(ctx, "test-id")
	if retrieved != nil {
		t.Fatal("expected nil after deletion")
	}
}

func TestMemoryStore_ListDatasets(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create test datasets
	for i := 0; i < 5; i++ {
		dataset := &Dataset{
			ID:        string(rune('a' + i)),
			Name:      "Dataset " + string(rune('A'+i)),
			Tags:      []string{"test"},
			CreatedAt: time.Now().Add(time.Duration(i) * time.Hour),
		}
		if err := store.CreateDataset(ctx, dataset); err != nil {
			t.Fatalf("failed to create dataset: %v", err)
		}
	}

	// List all
	datasets, total, err := store.ListDatasets(ctx, ListDatasetsQuery{})
	if err != nil {
		t.Fatalf("failed to list datasets: %v", err)
	}
	if total != 5 {
		t.Errorf("expected 5 datasets, got %d", total)
	}

	// List with limit
	datasets, total, _ = store.ListDatasets(ctx, ListDatasetsQuery{Limit: 2})
	if len(datasets) != 2 {
		t.Errorf("expected 2 datasets with limit, got %d", len(datasets))
	}
	if total != 5 {
		t.Errorf("expected total 5, got %d", total)
	}

	// List with offset
	datasets, _, _ = store.ListDatasets(ctx, ListDatasetsQuery{Offset: 3})
	if len(datasets) != 2 {
		t.Errorf("expected 2 datasets with offset 3, got %d", len(datasets))
	}
}

func TestMemoryStore_ListDatasetsWithFilters(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	datasets := []*Dataset{
		{ID: "1", Name: "Alpha", PromptID: "prompt-1", Tags: []string{"a", "b"}, CreatedAt: time.Now()},
		{ID: "2", Name: "Beta", PromptID: "prompt-1", Tags: []string{"b", "c"}, CreatedAt: time.Now()},
		{ID: "3", Name: "Gamma", PromptID: "prompt-2", Tags: []string{"a"}, CreatedAt: time.Now()},
	}

	for _, d := range datasets {
		if err := store.CreateDataset(ctx, d); err != nil {
			t.Fatalf("failed to create dataset: %v", err)
		}
	}

	// Filter by prompt ID
	results, _, _ := store.ListDatasets(ctx, ListDatasetsQuery{PromptID: "prompt-1"})
	if len(results) != 2 {
		t.Errorf("expected 2 datasets for prompt-1, got %d", len(results))
	}

	// Filter by tag
	results, _, _ = store.ListDatasets(ctx, ListDatasetsQuery{Tags: []string{"a"}})
	if len(results) != 2 {
		t.Errorf("expected 2 datasets with tag 'a', got %d", len(results))
	}

	// Search by name
	results, _, _ = store.ListDatasets(ctx, ListDatasetsQuery{Search: "alpha"})
	if len(results) != 1 {
		t.Errorf("expected 1 dataset matching 'alpha', got %d", len(results))
	}
}

func TestMemoryStore_AddAndGetExamples(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	dataset := &Dataset{ID: "ds-1", Name: "Test"}
	if err := store.CreateDataset(ctx, dataset); err != nil {
		t.Fatalf("failed to create dataset: %v", err)
	}

	examples := []*Example{
		{ID: "ex-1", DatasetID: "ds-1", Input: map[string]interface{}{"q": "test1"}},
		{ID: "ex-2", DatasetID: "ds-1", Input: map[string]interface{}{"q": "test2"}},
	}

	if err := store.AddExamples(ctx, "ds-1", examples); err != nil {
		t.Fatalf("failed to add examples: %v", err)
	}

	// Verify dataset example count updated
	ds, _ := store.GetDataset(ctx, "ds-1")
	if ds.ExampleCount != 2 {
		t.Errorf("expected example count 2, got %d", ds.ExampleCount)
	}

	// Get examples
	retrieved, total, _ := store.GetExamples(ctx, GetExamplesQuery{DatasetID: "ds-1"})
	if total != 2 {
		t.Errorf("expected 2 examples, got %d", total)
	}
	if len(retrieved) != 2 {
		t.Errorf("expected 2 examples returned, got %d", len(retrieved))
	}
}

func TestMemoryStore_RemoveExamples(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	dataset := &Dataset{ID: "ds-1", Name: "Test"}
	store.CreateDataset(ctx, dataset)

	examples := []*Example{
		{ID: "ex-1", DatasetID: "ds-1"},
		{ID: "ex-2", DatasetID: "ds-1"},
		{ID: "ex-3", DatasetID: "ds-1"},
	}
	store.AddExamples(ctx, "ds-1", examples)

	removed, err := store.RemoveExamples(ctx, "ds-1", []string{"ex-1", "ex-3"})
	if err != nil {
		t.Fatalf("failed to remove examples: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}

	remaining, total, _ := store.GetExamples(ctx, GetExamplesQuery{DatasetID: "ds-1"})
	if total != 1 {
		t.Errorf("expected 1 remaining, got %d", total)
	}
	if remaining[0].ID != "ex-2" {
		t.Errorf("expected ex-2 remaining, got %s", remaining[0].ID)
	}
}

func TestMemoryStore_GetExample(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	dataset := &Dataset{ID: "ds-1", Name: "Test"}
	store.CreateDataset(ctx, dataset)

	examples := []*Example{
		{ID: "ex-1", DatasetID: "ds-1", Input: map[string]interface{}{"q": "hello"}},
	}
	store.AddExamples(ctx, "ds-1", examples)

	example, err := store.GetExample(ctx, "ex-1")
	if err != nil {
		t.Fatalf("failed to get example: %v", err)
	}
	if example == nil {
		t.Fatal("expected example, got nil")
	}
	if example.Input["q"] != "hello" {
		t.Errorf("unexpected input: %v", example.Input)
	}

	// Nonexistent example
	example, _ = store.GetExample(ctx, "nonexistent")
	if example != nil {
		t.Fatal("expected nil for nonexistent example")
	}
}

func TestMemoryStore_ExamplesWithPagination(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	dataset := &Dataset{ID: "ds-1", Name: "Test"}
	store.CreateDataset(ctx, dataset)

	var examples []*Example
	for i := 0; i < 10; i++ {
		examples = append(examples, &Example{
			ID:        string(rune('a' + i)),
			DatasetID: "ds-1",
		})
	}
	store.AddExamples(ctx, "ds-1", examples)

	// Get with limit
	results, total, _ := store.GetExamples(ctx, GetExamplesQuery{DatasetID: "ds-1", Limit: 3})
	if len(results) != 3 {
		t.Errorf("expected 3 examples with limit, got %d", len(results))
	}
	if total != 10 {
		t.Errorf("expected total 10, got %d", total)
	}

	// Get with offset
	results, _, _ = store.GetExamples(ctx, GetExamplesQuery{DatasetID: "ds-1", Offset: 8})
	if len(results) != 2 {
		t.Errorf("expected 2 examples with offset 8, got %d", len(results))
	}
}
