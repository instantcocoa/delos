// Package deploy provides deployment management for prompt versions.
package deploy

import (
	"time"
)

// DeploymentStatus represents the status of a deployment.
type DeploymentStatus int

const (
	DeploymentStatusUnspecified DeploymentStatus = iota
	DeploymentStatusPendingApproval
	DeploymentStatusPendingGates
	DeploymentStatusGatesFailed
	DeploymentStatusInProgress
	DeploymentStatusCompleted
	DeploymentStatusRolledBack
	DeploymentStatusCancelled
	DeploymentStatusFailed
)

// DeploymentType represents the type of deployment strategy.
type DeploymentType int

const (
	DeploymentTypeUnspecified DeploymentType = iota
	DeploymentTypeImmediate
	DeploymentTypeGradual
	DeploymentTypeCanary
	DeploymentTypeBlueGreen
)

// Deployment represents a prompt version deployment.
type Deployment struct {
	ID            string
	PromptID      string
	FromVersion   int
	ToVersion     int
	Environment   string
	Strategy      DeploymentStrategy
	Status        DeploymentStatus
	StatusMessage string
	GateResults   []QualityGateResult
	GatesPassed   bool
	Rollout       *RolloutProgress
	CreatedAt     time.Time
	StartedAt     *time.Time
	CompletedAt   *time.Time
	CreatedBy     string
	ApprovedBy    string
	Metadata      map[string]string
}

// DeploymentStrategy contains configuration for how to deploy.
type DeploymentStrategy struct {
	Type              DeploymentType
	InitialPercentage int
	Increment         int
	IntervalSeconds   int
	AutoRollback      bool
	RollbackThreshold float64
}

// RolloutProgress tracks gradual rollout state.
type RolloutProgress struct {
	CurrentPercentage int
	TargetPercentage  int
	LastIncrementAt   *time.Time
	NextIncrementAt   *time.Time
}

// QualityGate defines conditions that must pass before deployment.
type QualityGate struct {
	ID         string
	Name       string
	PromptID   string
	Conditions []GateCondition
	Required   bool
	CreatedAt  time.Time
	CreatedBy  string
}

// GateCondition represents a single condition in a quality gate.
type GateCondition struct {
	Type      string // eval_score, latency, cost, custom
	Operator  string // gte, lte, eq
	Threshold float64
	EvalRunID string
	DatasetID string
}

// QualityGateResult represents the outcome of evaluating a quality gate.
type QualityGateResult struct {
	GateID           string
	GateName         string
	Passed           bool
	Message          string
	ConditionResults []ConditionResult
}

// ConditionResult represents the outcome of a single condition.
type ConditionResult struct {
	Type     string
	Expected float64
	Actual   float64
	Passed   bool
}

// DeploymentMetrics contains real-time metrics for a deployment.
type DeploymentMetrics struct {
	AvgLatencyMs float64
	ErrorRate    float64
	QualityScore float64
	RequestCount int
}

// CreateDeploymentInput contains input for creating a deployment.
type CreateDeploymentInput struct {
	PromptID     string
	ToVersion    int
	Environment  string
	Strategy     DeploymentStrategy
	SkipApproval bool
	Metadata     map[string]string
	CreatedBy    string
}

// ListDeploymentsQuery contains filters for listing deployments.
type ListDeploymentsQuery struct {
	PromptID    string
	Environment string
	Status      DeploymentStatus
	Limit       int
	Offset      int
}

// CreateQualityGateInput contains input for creating a quality gate.
type CreateQualityGateInput struct {
	Name       string
	PromptID   string
	Conditions []GateCondition
	Required   bool
	CreatedBy  string
}
