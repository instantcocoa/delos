package eval

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStore_CreateAndGetEvalRun(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	run := &EvalRun{
		ID:        "run-1",
		Name:      "Test Run",
		PromptID:  "prompt-1",
		DatasetID: "dataset-1",
		Status:    EvalRunStatusPending,
		CreatedAt: time.Now(),
	}

	if err := store.CreateEvalRun(ctx, run); err != nil {
		t.Fatalf("failed to create eval run: %v", err)
	}

	retrieved, err := store.GetEvalRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("failed to get eval run: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected eval run, got nil")
	}

	if retrieved.Name != "Test Run" {
		t.Errorf("expected name 'Test Run', got '%s'", retrieved.Name)
	}
}

func TestMemoryStore_CreateDuplicateEvalRun(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	run := &EvalRun{ID: "run-1", Name: "Test"}
	if err := store.CreateEvalRun(ctx, run); err != nil {
		t.Fatalf("failed to create eval run: %v", err)
	}

	err := store.CreateEvalRun(ctx, run)
	if err == nil {
		t.Fatal("expected error for duplicate eval run")
	}
}

func TestMemoryStore_GetNonexistentEvalRun(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	retrieved, err := store.GetEvalRun(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Fatal("expected nil for nonexistent eval run")
	}
}

func TestMemoryStore_UpdateEvalRun(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	run := &EvalRun{ID: "run-1", Name: "Original", Status: EvalRunStatusPending}
	if err := store.CreateEvalRun(ctx, run); err != nil {
		t.Fatalf("failed to create eval run: %v", err)
	}

	run.Name = "Updated"
	run.Status = EvalRunStatusRunning
	if err := store.UpdateEvalRun(ctx, run); err != nil {
		t.Fatalf("failed to update eval run: %v", err)
	}

	retrieved, _ := store.GetEvalRun(ctx, "run-1")
	if retrieved.Name != "Updated" {
		t.Errorf("expected name 'Updated', got '%s'", retrieved.Name)
	}
	if retrieved.Status != EvalRunStatusRunning {
		t.Errorf("expected status Running, got %d", retrieved.Status)
	}
}

func TestMemoryStore_UpdateNonexistentEvalRun(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	run := &EvalRun{ID: "nonexistent", Name: "Test"}
	err := store.UpdateEvalRun(ctx, run)
	if err == nil {
		t.Fatal("expected error for nonexistent eval run")
	}
}

func TestMemoryStore_ListEvalRuns(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		run := &EvalRun{
			ID:        string(rune('a' + i)),
			Name:      "Run " + string(rune('A'+i)),
			PromptID:  "prompt-1",
			Status:    EvalRunStatusPending,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Hour),
		}
		if err := store.CreateEvalRun(ctx, run); err != nil {
			t.Fatalf("failed to create eval run: %v", err)
		}
	}

	// List all
	runs, total, err := store.ListEvalRuns(ctx, ListEvalRunsQuery{})
	if err != nil {
		t.Fatalf("failed to list eval runs: %v", err)
	}
	if total != 5 {
		t.Errorf("expected 5 runs, got %d", total)
	}

	// Verify sorted by created_at descending
	if runs[0].ID != "e" {
		t.Errorf("expected first run to be 'e' (most recent), got '%s'", runs[0].ID)
	}

	// List with limit
	runs, total, _ = store.ListEvalRuns(ctx, ListEvalRunsQuery{Limit: 2})
	if len(runs) != 2 {
		t.Errorf("expected 2 runs with limit, got %d", len(runs))
	}
	if total != 5 {
		t.Errorf("expected total 5, got %d", total)
	}

	// List with offset
	runs, _, _ = store.ListEvalRuns(ctx, ListEvalRunsQuery{Offset: 3})
	if len(runs) != 2 {
		t.Errorf("expected 2 runs with offset 3, got %d", len(runs))
	}
}

func TestMemoryStore_ListEvalRunsWithFilters(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	runs := []*EvalRun{
		{ID: "1", PromptID: "prompt-1", DatasetID: "dataset-1", Status: EvalRunStatusPending, CreatedAt: time.Now()},
		{ID: "2", PromptID: "prompt-1", DatasetID: "dataset-2", Status: EvalRunStatusCompleted, CreatedAt: time.Now()},
		{ID: "3", PromptID: "prompt-2", DatasetID: "dataset-1", Status: EvalRunStatusPending, CreatedAt: time.Now()},
	}

	for _, r := range runs {
		if err := store.CreateEvalRun(ctx, r); err != nil {
			t.Fatalf("failed to create eval run: %v", err)
		}
	}

	// Filter by prompt ID
	results, _, _ := store.ListEvalRuns(ctx, ListEvalRunsQuery{PromptID: "prompt-1"})
	if len(results) != 2 {
		t.Errorf("expected 2 runs for prompt-1, got %d", len(results))
	}

	// Filter by dataset ID
	results, _, _ = store.ListEvalRuns(ctx, ListEvalRunsQuery{DatasetID: "dataset-1"})
	if len(results) != 2 {
		t.Errorf("expected 2 runs for dataset-1, got %d", len(results))
	}

	// Filter by status
	results, _, _ = store.ListEvalRuns(ctx, ListEvalRunsQuery{Status: EvalRunStatusPending})
	if len(results) != 2 {
		t.Errorf("expected 2 pending runs, got %d", len(results))
	}
}

func TestMemoryStore_AddAndGetEvalResults(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	run := &EvalRun{ID: "run-1", Name: "Test"}
	if err := store.CreateEvalRun(ctx, run); err != nil {
		t.Fatalf("failed to create eval run: %v", err)
	}

	results := []*EvalResult{
		{ID: "r-1", EvalRunID: "run-1", ExampleID: "ex-1", Passed: true, OverallScore: 0.9},
		{ID: "r-2", EvalRunID: "run-1", ExampleID: "ex-2", Passed: false, OverallScore: 0.3},
	}

	for _, r := range results {
		if err := store.AddEvalResult(ctx, r); err != nil {
			t.Fatalf("failed to add eval result: %v", err)
		}
	}

	// Get all results
	retrieved, total, err := store.GetEvalResults(ctx, GetEvalResultsQuery{EvalRunID: "run-1"})
	if err != nil {
		t.Fatalf("failed to get eval results: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 results, got %d", total)
	}

	// Get failed only
	retrieved, total, _ = store.GetEvalResults(ctx, GetEvalResultsQuery{EvalRunID: "run-1", FailedOnly: true})
	if total != 1 {
		t.Errorf("expected 1 failed result, got %d", total)
	}
	if len(retrieved) != 1 || retrieved[0].Passed {
		t.Error("expected only failed results")
	}
}

func TestMemoryStore_AddResultToNonexistentRun(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	result := &EvalResult{ID: "r-1", EvalRunID: "nonexistent"}
	err := store.AddEvalResult(ctx, result)
	if err == nil {
		t.Fatal("expected error when adding result to nonexistent run")
	}
}

func TestMemoryStore_GetEvalResultsByRunID(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	run := &EvalRun{ID: "run-1", Name: "Test"}
	store.CreateEvalRun(ctx, run)

	results := []*EvalResult{
		{ID: "r-1", EvalRunID: "run-1", ExampleID: "ex-1"},
		{ID: "r-2", EvalRunID: "run-1", ExampleID: "ex-2"},
		{ID: "r-3", EvalRunID: "run-1", ExampleID: "ex-3"},
	}

	for _, r := range results {
		store.AddEvalResult(ctx, r)
	}

	retrieved, err := store.GetEvalResultsByRunID(ctx, "run-1")
	if err != nil {
		t.Fatalf("failed to get results: %v", err)
	}
	if len(retrieved) != 3 {
		t.Errorf("expected 3 results, got %d", len(retrieved))
	}

	// Nonexistent run
	retrieved, _ = store.GetEvalResultsByRunID(ctx, "nonexistent")
	if retrieved != nil {
		t.Error("expected nil for nonexistent run")
	}
}

func TestMemoryStore_ResultsPagination(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	run := &EvalRun{ID: "run-1", Name: "Test"}
	store.CreateEvalRun(ctx, run)

	for i := 0; i < 10; i++ {
		result := &EvalResult{
			ID:        string(rune('a' + i)),
			EvalRunID: "run-1",
			ExampleID: string(rune('a' + i)),
			Passed:    true,
		}
		store.AddEvalResult(ctx, result)
	}

	// Get with limit
	results, total, _ := store.GetEvalResults(ctx, GetEvalResultsQuery{EvalRunID: "run-1", Limit: 3})
	if len(results) != 3 {
		t.Errorf("expected 3 results with limit, got %d", len(results))
	}
	if total != 10 {
		t.Errorf("expected total 10, got %d", total)
	}

	// Get with offset
	results, _, _ = store.GetEvalResults(ctx, GetEvalResultsQuery{EvalRunID: "run-1", Offset: 8})
	if len(results) != 2 {
		t.Errorf("expected 2 results with offset 8, got %d", len(results))
	}
}
