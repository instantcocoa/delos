package deploy

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStore_CreateAndGetDeployment(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	deployment := &Deployment{
		ID:          "deploy-1",
		PromptID:    "prompt-1",
		ToVersion:   1,
		Environment: "production",
		Status:      DeploymentStatusPendingApproval,
		CreatedAt:   time.Now(),
	}

	if err := store.CreateDeployment(ctx, deployment); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	retrieved, err := store.GetDeployment(ctx, "deploy-1")
	if err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected deployment, got nil")
	}

	if retrieved.PromptID != "prompt-1" {
		t.Errorf("expected prompt ID 'prompt-1', got '%s'", retrieved.PromptID)
	}
}

func TestMemoryStore_CreateDuplicateDeployment(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	deployment := &Deployment{ID: "deploy-1", PromptID: "prompt-1"}
	if err := store.CreateDeployment(ctx, deployment); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	err := store.CreateDeployment(ctx, deployment)
	if err == nil {
		t.Fatal("expected error for duplicate deployment")
	}
}

func TestMemoryStore_GetNonexistentDeployment(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	retrieved, err := store.GetDeployment(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Fatal("expected nil for nonexistent deployment")
	}
}

func TestMemoryStore_UpdateDeployment(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	deployment := &Deployment{ID: "deploy-1", Status: DeploymentStatusPendingApproval}
	if err := store.CreateDeployment(ctx, deployment); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	deployment.Status = DeploymentStatusCompleted
	if err := store.UpdateDeployment(ctx, deployment); err != nil {
		t.Fatalf("failed to update deployment: %v", err)
	}

	retrieved, _ := store.GetDeployment(ctx, "deploy-1")
	if retrieved.Status != DeploymentStatusCompleted {
		t.Errorf("expected status Completed, got %d", retrieved.Status)
	}
}

func TestMemoryStore_UpdateNonexistentDeployment(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	deployment := &Deployment{ID: "nonexistent"}
	err := store.UpdateDeployment(ctx, deployment)
	if err == nil {
		t.Fatal("expected error for nonexistent deployment")
	}
}

func TestMemoryStore_ListDeployments(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		deployment := &Deployment{
			ID:          string(rune('a' + i)),
			PromptID:    "prompt-1",
			Environment: "production",
			Status:      DeploymentStatusPendingApproval,
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Hour),
		}
		if err := store.CreateDeployment(ctx, deployment); err != nil {
			t.Fatalf("failed to create deployment: %v", err)
		}
	}

	// List all
	deployments, total, err := store.ListDeployments(ctx, ListDeploymentsQuery{})
	if err != nil {
		t.Fatalf("failed to list deployments: %v", err)
	}
	if total != 5 {
		t.Errorf("expected 5 deployments, got %d", total)
	}

	// Verify sorted by created_at descending
	if deployments[0].ID != "e" {
		t.Errorf("expected first deployment to be 'e', got '%s'", deployments[0].ID)
	}

	// List with limit
	deployments, total, _ = store.ListDeployments(ctx, ListDeploymentsQuery{Limit: 2})
	if len(deployments) != 2 {
		t.Errorf("expected 2 deployments with limit, got %d", len(deployments))
	}
	if total != 5 {
		t.Errorf("expected total 5, got %d", total)
	}

	// List with offset
	deployments, _, _ = store.ListDeployments(ctx, ListDeploymentsQuery{Offset: 3})
	if len(deployments) != 2 {
		t.Errorf("expected 2 deployments with offset 3, got %d", len(deployments))
	}
}

func TestMemoryStore_ListDeploymentsWithFilters(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	deployments := []*Deployment{
		{ID: "1", PromptID: "prompt-1", Environment: "production", Status: DeploymentStatusCompleted, CreatedAt: time.Now()},
		{ID: "2", PromptID: "prompt-1", Environment: "staging", Status: DeploymentStatusPendingApproval, CreatedAt: time.Now()},
		{ID: "3", PromptID: "prompt-2", Environment: "production", Status: DeploymentStatusPendingApproval, CreatedAt: time.Now()},
	}

	for _, d := range deployments {
		if err := store.CreateDeployment(ctx, d); err != nil {
			t.Fatalf("failed to create deployment: %v", err)
		}
	}

	// Filter by prompt ID
	results, _, _ := store.ListDeployments(ctx, ListDeploymentsQuery{PromptID: "prompt-1"})
	if len(results) != 2 {
		t.Errorf("expected 2 deployments for prompt-1, got %d", len(results))
	}

	// Filter by environment
	results, _, _ = store.ListDeployments(ctx, ListDeploymentsQuery{Environment: "production"})
	if len(results) != 2 {
		t.Errorf("expected 2 production deployments, got %d", len(results))
	}

	// Filter by status
	results, _, _ = store.ListDeployments(ctx, ListDeploymentsQuery{Status: DeploymentStatusPendingApproval})
	if len(results) != 2 {
		t.Errorf("expected 2 pending deployments, got %d", len(results))
	}
}

func TestMemoryStore_GetCurrentDeployment(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	earlier := now.Add(-time.Hour)

	// Create two completed deployments
	d1 := &Deployment{
		ID:          "1",
		PromptID:    "prompt-1",
		Environment: "production",
		ToVersion:   1,
		Status:      DeploymentStatusCompleted,
		CompletedAt: &earlier,
		CreatedAt:   earlier,
	}
	d2 := &Deployment{
		ID:          "2",
		PromptID:    "prompt-1",
		Environment: "production",
		ToVersion:   2,
		Status:      DeploymentStatusCompleted,
		CompletedAt: &now,
		CreatedAt:   now,
	}
	store.CreateDeployment(ctx, d1)
	store.CreateDeployment(ctx, d2)

	current, err := store.GetCurrentDeployment(ctx, "prompt-1", "production")
	if err != nil {
		t.Fatalf("failed to get current deployment: %v", err)
	}
	if current == nil {
		t.Fatal("expected current deployment, got nil")
	}
	if current.ToVersion != 2 {
		t.Errorf("expected version 2, got %d", current.ToVersion)
	}

	// Non-existent prompt
	current, _ = store.GetCurrentDeployment(ctx, "nonexistent", "production")
	if current != nil {
		t.Error("expected nil for nonexistent prompt")
	}
}

func TestMemoryStore_CreateAndGetQualityGate(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	gate := &QualityGate{
		ID:       "gate-1",
		Name:     "Test Gate",
		PromptID: "prompt-1",
		Required: true,
		Conditions: []GateCondition{
			{Type: "eval_score", Operator: "gte", Threshold: 0.9},
		},
		CreatedAt: time.Now(),
	}

	if err := store.CreateQualityGate(ctx, gate); err != nil {
		t.Fatalf("failed to create quality gate: %v", err)
	}

	retrieved, err := store.GetQualityGate(ctx, "gate-1")
	if err != nil {
		t.Fatalf("failed to get quality gate: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected quality gate, got nil")
	}

	if retrieved.Name != "Test Gate" {
		t.Errorf("expected name 'Test Gate', got '%s'", retrieved.Name)
	}
}

func TestMemoryStore_CreateDuplicateQualityGate(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	gate := &QualityGate{ID: "gate-1", Name: "Test"}
	if err := store.CreateQualityGate(ctx, gate); err != nil {
		t.Fatalf("failed to create quality gate: %v", err)
	}

	err := store.CreateQualityGate(ctx, gate)
	if err == nil {
		t.Fatal("expected error for duplicate quality gate")
	}
}

func TestMemoryStore_GetNonexistentQualityGate(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	retrieved, err := store.GetQualityGate(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Fatal("expected nil for nonexistent quality gate")
	}
}

func TestMemoryStore_ListQualityGates(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	gates := []*QualityGate{
		{ID: "1", Name: "Gate 1", PromptID: "prompt-1", CreatedAt: time.Now()},
		{ID: "2", Name: "Gate 2", PromptID: "prompt-1", CreatedAt: time.Now().Add(time.Hour)},
		{ID: "3", Name: "Gate 3", PromptID: "prompt-2", CreatedAt: time.Now()},
	}

	for _, g := range gates {
		if err := store.CreateQualityGate(ctx, g); err != nil {
			t.Fatalf("failed to create quality gate: %v", err)
		}
	}

	// List for prompt-1
	results, err := store.ListQualityGates(ctx, "prompt-1")
	if err != nil {
		t.Fatalf("failed to list quality gates: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 gates for prompt-1, got %d", len(results))
	}

	// Verify sorted by created_at ascending
	if results[0].ID != "1" {
		t.Errorf("expected first gate to be '1', got '%s'", results[0].ID)
	}

	// List for nonexistent prompt
	results, _ = store.ListQualityGates(ctx, "nonexistent")
	if len(results) != 0 {
		t.Errorf("expected 0 gates for nonexistent prompt, got %d", len(results))
	}
}
