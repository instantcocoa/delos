package deploy

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DeployService handles deployment business logic.
type DeployService struct {
	store Store
}

// NewDeployService creates a new deploy service.
func NewDeployService(store Store) *DeployService {
	return &DeployService{
		store: store,
	}
}

// CreateDeployment creates a new deployment.
func (s *DeployService) CreateDeployment(ctx context.Context, input CreateDeploymentInput) (*Deployment, error) {
	now := time.Now()

	// Get current deployment to determine from_version
	fromVersion := 0
	current, err := s.store.GetCurrentDeployment(ctx, input.PromptID, input.Environment)
	if err != nil {
		return nil, fmt.Errorf("failed to get current deployment: %w", err)
	}
	if current != nil {
		fromVersion = current.ToVersion
	}

	// Determine initial status
	initialStatus := DeploymentStatusPendingApproval
	if input.SkipApproval {
		initialStatus = DeploymentStatusPendingGates
	}

	deployment := &Deployment{
		ID:          uuid.New().String(),
		PromptID:    input.PromptID,
		FromVersion: fromVersion,
		ToVersion:   input.ToVersion,
		Environment: input.Environment,
		Strategy:    input.Strategy,
		Status:      initialStatus,
		GatesPassed: false,
		CreatedAt:   now,
		CreatedBy:   input.CreatedBy,
		Metadata:    input.Metadata,
	}

	if err := s.store.CreateDeployment(ctx, deployment); err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	// If skipping approval, trigger gate evaluation
	if input.SkipApproval {
		if err := s.evaluateGates(ctx, deployment); err != nil {
			deployment.Status = DeploymentStatusGatesFailed
			deployment.StatusMessage = err.Error()
			s.store.UpdateDeployment(ctx, deployment)
		}
	}

	return deployment, nil
}

// GetDeployment retrieves a deployment by ID.
func (s *DeployService) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	deployment, err := s.store.GetDeployment(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	return deployment, nil
}

// ListDeployments returns deployments matching the query.
func (s *DeployService) ListDeployments(ctx context.Context, query ListDeploymentsQuery) ([]*Deployment, int, error) {
	deployments, total, err := s.store.ListDeployments(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list deployments: %w", err)
	}
	return deployments, total, nil
}

// ApproveDeployment approves a pending deployment.
func (s *DeployService) ApproveDeployment(ctx context.Context, id, approver, comment string) (*Deployment, error) {
	deployment, err := s.store.GetDeployment(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, fmt.Errorf("deployment not found: %s", id)
	}

	if deployment.Status != DeploymentStatusPendingApproval {
		return nil, fmt.Errorf("deployment is not pending approval: status=%d", deployment.Status)
	}

	deployment.ApprovedBy = approver
	deployment.Status = DeploymentStatusPendingGates
	deployment.StatusMessage = comment

	if err := s.store.UpdateDeployment(ctx, deployment); err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	// Trigger gate evaluation
	if err := s.evaluateGates(ctx, deployment); err != nil {
		deployment.Status = DeploymentStatusGatesFailed
		deployment.StatusMessage = err.Error()
		s.store.UpdateDeployment(ctx, deployment)
	}

	return deployment, nil
}

// RollbackDeployment rolls back to a previous version.
func (s *DeployService) RollbackDeployment(ctx context.Context, id, reason string) (*Deployment, *Deployment, error) {
	deployment, err := s.store.GetDeployment(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, nil, fmt.Errorf("deployment not found: %s", id)
	}

	// Mark original deployment as rolled back
	now := time.Now()
	deployment.Status = DeploymentStatusRolledBack
	deployment.StatusMessage = reason
	deployment.CompletedAt = &now

	if err := s.store.UpdateDeployment(ctx, deployment); err != nil {
		return nil, nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	// Create a new deployment to restore the previous version
	rollbackDeployment := &Deployment{
		ID:            uuid.New().String(),
		PromptID:      deployment.PromptID,
		FromVersion:   deployment.ToVersion,
		ToVersion:     deployment.FromVersion,
		Environment:   deployment.Environment,
		Strategy:      DeploymentStrategy{Type: DeploymentTypeImmediate},
		Status:        DeploymentStatusCompleted,
		StatusMessage: fmt.Sprintf("Rollback from deployment %s: %s", id, reason),
		GatesPassed:   true,
		CreatedAt:     now,
		StartedAt:     &now,
		CompletedAt:   &now,
		CreatedBy:     "system",
		Metadata:      map[string]string{"rollback_from": id},
	}

	if err := s.store.CreateDeployment(ctx, rollbackDeployment); err != nil {
		return nil, nil, fmt.Errorf("failed to create rollback deployment: %w", err)
	}

	return deployment, rollbackDeployment, nil
}

// CancelDeployment cancels a pending/in-progress deployment.
func (s *DeployService) CancelDeployment(ctx context.Context, id, reason string) (*Deployment, error) {
	deployment, err := s.store.GetDeployment(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, fmt.Errorf("deployment not found: %s", id)
	}

	// Can only cancel pending or in-progress deployments
	if deployment.Status != DeploymentStatusPendingApproval &&
		deployment.Status != DeploymentStatusPendingGates &&
		deployment.Status != DeploymentStatusInProgress {
		return nil, fmt.Errorf("cannot cancel deployment with status: %d", deployment.Status)
	}

	now := time.Now()
	deployment.Status = DeploymentStatusCancelled
	deployment.StatusMessage = reason
	deployment.CompletedAt = &now

	if err := s.store.UpdateDeployment(ctx, deployment); err != nil {
		return nil, fmt.Errorf("failed to update deployment: %w", err)
	}

	return deployment, nil
}

// GetDeploymentStatus gets the current status of a deployment.
func (s *DeployService) GetDeploymentStatus(ctx context.Context, id string) (*Deployment, *DeploymentMetrics, *DeploymentMetrics, error) {
	deployment, err := s.store.GetDeployment(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get deployment: %w", err)
	}
	if deployment == nil {
		return nil, nil, nil, fmt.Errorf("deployment not found: %s", id)
	}

	// In a real implementation, this would fetch real-time metrics
	currentMetrics := &DeploymentMetrics{}
	baselineMetrics := &DeploymentMetrics{}

	return deployment, currentMetrics, baselineMetrics, nil
}

// CreateQualityGate creates a quality gate configuration.
func (s *DeployService) CreateQualityGate(ctx context.Context, input CreateQualityGateInput) (*QualityGate, error) {
	gate := &QualityGate{
		ID:         uuid.New().String(),
		Name:       input.Name,
		PromptID:   input.PromptID,
		Conditions: input.Conditions,
		Required:   input.Required,
		CreatedAt:  time.Now(),
		CreatedBy:  input.CreatedBy,
	}

	if err := s.store.CreateQualityGate(ctx, gate); err != nil {
		return nil, fmt.Errorf("failed to create quality gate: %w", err)
	}

	return gate, nil
}

// ListQualityGates returns quality gates for a prompt.
func (s *DeployService) ListQualityGates(ctx context.Context, promptID string) ([]*QualityGate, error) {
	gates, err := s.store.ListQualityGates(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("failed to list quality gates: %w", err)
	}
	return gates, nil
}

// evaluateGates evaluates quality gates for a deployment.
func (s *DeployService) evaluateGates(ctx context.Context, deployment *Deployment) error {
	gates, err := s.store.ListQualityGates(ctx, deployment.PromptID)
	if err != nil {
		return fmt.Errorf("failed to list quality gates: %w", err)
	}

	if len(gates) == 0 {
		// No gates to evaluate, mark as passed and start deployment
		deployment.GatesPassed = true
		deployment.Status = DeploymentStatusInProgress
		now := time.Now()
		deployment.StartedAt = &now

		// For immediate deployments, complete right away
		if deployment.Strategy.Type == DeploymentTypeImmediate {
			deployment.Status = DeploymentStatusCompleted
			deployment.CompletedAt = &now
		}

		return s.store.UpdateDeployment(ctx, deployment)
	}

	// Evaluate each gate
	var results []QualityGateResult
	allPassed := true

	for _, gate := range gates {
		result := s.evaluateGate(ctx, gate, deployment)
		results = append(results, result)

		if !result.Passed && gate.Required {
			allPassed = false
		}
	}

	deployment.GateResults = results
	deployment.GatesPassed = allPassed

	if allPassed {
		deployment.Status = DeploymentStatusInProgress
		now := time.Now()
		deployment.StartedAt = &now

		// For immediate deployments, complete right away
		if deployment.Strategy.Type == DeploymentTypeImmediate {
			deployment.Status = DeploymentStatusCompleted
			deployment.CompletedAt = &now
		}
	} else {
		deployment.Status = DeploymentStatusGatesFailed
		deployment.StatusMessage = "One or more required quality gates failed"
	}

	return s.store.UpdateDeployment(ctx, deployment)
}

// evaluateGate evaluates a single quality gate.
func (s *DeployService) evaluateGate(ctx context.Context, gate *QualityGate, deployment *Deployment) QualityGateResult {
	result := QualityGateResult{
		GateID:   gate.ID,
		GateName: gate.Name,
		Passed:   true,
	}

	// In a real implementation, this would evaluate each condition
	// by fetching eval results, latency metrics, etc.
	var conditionResults []ConditionResult
	for _, condition := range gate.Conditions {
		conditionResult := ConditionResult{
			Type:     condition.Type,
			Expected: condition.Threshold,
			Actual:   condition.Threshold, // Simulated: actual equals expected
			Passed:   true,
		}
		conditionResults = append(conditionResults, conditionResult)
	}

	result.ConditionResults = conditionResults
	result.Message = "All conditions passed"

	return result
}
