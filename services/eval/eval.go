// Package eval provides evaluation and quality testing for LLM outputs.
package eval

import (
	"time"
)

// EvalRunStatus represents the status of an evaluation run.
type EvalRunStatus int

const (
	EvalRunStatusUnspecified EvalRunStatus = iota
	EvalRunStatusPending
	EvalRunStatusRunning
	EvalRunStatusCompleted
	EvalRunStatusFailed
	EvalRunStatusCancelled
)

// EvalRun represents an evaluation execution.
type EvalRun struct {
	ID                string
	Name              string
	Description       string
	PromptID          string
	PromptVersion     int
	DatasetID         string
	Config            EvalConfig
	Status            EvalRunStatus
	ErrorMessage      string
	TotalExamples     int
	CompletedExamples int
	Summary           *EvalSummary
	CreatedAt         time.Time
	StartedAt         *time.Time
	CompletedAt       *time.Time
	CreatedBy         string
	Metadata          map[string]string
}

// EvalConfig contains configuration for an evaluation run.
type EvalConfig struct {
	Evaluators  []EvaluatorConfig
	Provider    string
	Model       string
	Concurrency int
	SampleSize  int
	Shuffle     bool
}

// EvaluatorConfig contains configuration for a single evaluator.
type EvaluatorConfig struct {
	Type   string // exact_match, semantic_similarity, llm_judge, custom
	Name   string
	Params map[string]string
	Weight float64
}

// EvalSummary contains aggregated results from an evaluation.
type EvalSummary struct {
	OverallScore      float64
	ScoresByEvaluator map[string]float64
	PassedCount       int
	FailedCount       int
	PassRate          float64
	TotalCostUSD      float64
	TotalTokens       int
	AvgLatencyMs      float64
}

// EvalResult represents a single example's evaluation result.
type EvalResult struct {
	ID               string
	EvalRunID        string
	ExampleID        string
	Input            map[string]interface{}
	ExpectedOutput   map[string]interface{}
	ActualOutput     map[string]interface{}
	EvaluatorResults map[string]*EvaluatorResult
	OverallScore     float64
	Passed           bool
	LatencyMs        float64
	TokensUsed       int
	CostUSD          float64
	Error            string
}

// EvaluatorResult contains the result from a single evaluator.
type EvaluatorResult struct {
	EvaluatorType string
	Score         float64
	Passed        bool
	Explanation   string
	Details       map[string]string
}

// Evaluator describes an available evaluator type.
type Evaluator struct {
	Type        string
	Name        string
	Description string
	Params      []EvaluatorParam
}

// EvaluatorParam describes a parameter for an evaluator.
type EvaluatorParam struct {
	Name         string
	Type         string
	Description  string
	Required     bool
	DefaultValue string
}

// CreateEvalRunInput contains input for creating an evaluation run.
type CreateEvalRunInput struct {
	Name          string
	Description   string
	PromptID      string
	PromptVersion int
	DatasetID     string
	Config        EvalConfig
	Metadata      map[string]string
	CreatedBy     string
}

// ListEvalRunsQuery contains filters for listing evaluation runs.
type ListEvalRunsQuery struct {
	PromptID  string
	DatasetID string
	Status    EvalRunStatus
	Limit     int
	Offset    int
}

// GetEvalResultsQuery contains filters for getting evaluation results.
type GetEvalResultsQuery struct {
	EvalRunID  string
	FailedOnly bool
	Limit      int
	Offset     int
}

// RunComparison contains summary data for comparing runs.
type RunComparison struct {
	RunID         string
	PromptVersion string
	OverallScore  float64
	PassRate      float64
	AvgLatencyMs  float64
	TotalCostUSD  float64
}

// ExampleComparison compares a single example across two runs.
type ExampleComparison struct {
	ExampleID  string
	ScoreA     float64
	ScoreB     float64
	ScoreDiff  float64
	Regression bool
}

// CompareRunsResult contains the full comparison between two runs.
type CompareRunsResult struct {
	RunA         RunComparison
	RunB         RunComparison
	ScoreDiff    float64
	Regressions  int
	Improvements int
	Examples     []ExampleComparison
}
