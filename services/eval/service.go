package eval

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EvalService handles evaluation business logic.
type EvalService struct {
	store Store
}

// NewEvalService creates a new eval service.
func NewEvalService(store Store) *EvalService {
	return &EvalService{
		store: store,
	}
}

// CreateEvalRun creates a new evaluation run.
func (s *EvalService) CreateEvalRun(ctx context.Context, input CreateEvalRunInput) (*EvalRun, error) {
	now := time.Now()
	run := &EvalRun{
		ID:            uuid.New().String(),
		Name:          input.Name,
		Description:   input.Description,
		PromptID:      input.PromptID,
		PromptVersion: input.PromptVersion,
		DatasetID:     input.DatasetID,
		Config:        input.Config,
		Status:        EvalRunStatusPending,
		Metadata:      input.Metadata,
		CreatedBy:     input.CreatedBy,
		CreatedAt:     now,
	}

	if err := s.store.CreateEvalRun(ctx, run); err != nil {
		return nil, fmt.Errorf("failed to create eval run: %w", err)
	}

	return run, nil
}

// GetEvalRun retrieves an evaluation run by ID.
func (s *EvalService) GetEvalRun(ctx context.Context, id string) (*EvalRun, error) {
	run, err := s.store.GetEvalRun(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get eval run: %w", err)
	}
	return run, nil
}

// ListEvalRuns returns evaluation runs matching the query.
func (s *EvalService) ListEvalRuns(ctx context.Context, query ListEvalRunsQuery) ([]*EvalRun, int, error) {
	runs, total, err := s.store.ListEvalRuns(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list eval runs: %w", err)
	}
	return runs, total, nil
}

// CancelEvalRun cancels a running evaluation.
func (s *EvalService) CancelEvalRun(ctx context.Context, id string) (*EvalRun, error) {
	run, err := s.store.GetEvalRun(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get eval run: %w", err)
	}
	if run == nil {
		return nil, fmt.Errorf("eval run not found: %s", id)
	}

	if run.Status != EvalRunStatusPending && run.Status != EvalRunStatusRunning {
		return nil, fmt.Errorf("cannot cancel eval run with status: %d", run.Status)
	}

	run.Status = EvalRunStatusCancelled
	now := time.Now()
	run.CompletedAt = &now

	if err := s.store.UpdateEvalRun(ctx, run); err != nil {
		return nil, fmt.Errorf("failed to update eval run: %w", err)
	}

	return run, nil
}

// GetEvalResults retrieves results for an evaluation run.
func (s *EvalService) GetEvalResults(ctx context.Context, query GetEvalResultsQuery) ([]*EvalResult, int, error) {
	results, total, err := s.store.GetEvalResults(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get eval results: %w", err)
	}
	return results, total, nil
}

// CompareRuns compares two evaluation runs.
func (s *EvalService) CompareRuns(ctx context.Context, runIDA, runIDB string) (*CompareRunsResult, error) {
	runA, err := s.store.GetEvalRun(ctx, runIDA)
	if err != nil {
		return nil, fmt.Errorf("failed to get run A: %w", err)
	}
	if runA == nil {
		return nil, fmt.Errorf("run not found: %s", runIDA)
	}

	runB, err := s.store.GetEvalRun(ctx, runIDB)
	if err != nil {
		return nil, fmt.Errorf("failed to get run B: %w", err)
	}
	if runB == nil {
		return nil, fmt.Errorf("run not found: %s", runIDB)
	}

	resultsA, err := s.store.GetEvalResultsByRunID(ctx, runIDA)
	if err != nil {
		return nil, fmt.Errorf("failed to get results for run A: %w", err)
	}

	resultsB, err := s.store.GetEvalResultsByRunID(ctx, runIDB)
	if err != nil {
		return nil, fmt.Errorf("failed to get results for run B: %w", err)
	}

	// Build comparison
	compA := RunComparison{
		RunID:         runA.ID,
		PromptVersion: fmt.Sprintf("%d", runA.PromptVersion),
	}
	compB := RunComparison{
		RunID:         runB.ID,
		PromptVersion: fmt.Sprintf("%d", runB.PromptVersion),
	}

	if runA.Summary != nil {
		compA.OverallScore = runA.Summary.OverallScore
		compA.PassRate = runA.Summary.PassRate
		compA.AvgLatencyMs = runA.Summary.AvgLatencyMs
		compA.TotalCostUSD = runA.Summary.TotalCostUSD
	}

	if runB.Summary != nil {
		compB.OverallScore = runB.Summary.OverallScore
		compB.PassRate = runB.Summary.PassRate
		compB.AvgLatencyMs = runB.Summary.AvgLatencyMs
		compB.TotalCostUSD = runB.Summary.TotalCostUSD
	}

	// Compare individual examples
	resultMapA := make(map[string]*EvalResult)
	for _, r := range resultsA {
		resultMapA[r.ExampleID] = r
	}

	var examples []ExampleComparison
	regressions := 0
	improvements := 0

	for _, rB := range resultsB {
		if rA, ok := resultMapA[rB.ExampleID]; ok {
			diff := rB.OverallScore - rA.OverallScore
			isRegression := diff < -0.01

			examples = append(examples, ExampleComparison{
				ExampleID:  rB.ExampleID,
				ScoreA:     rA.OverallScore,
				ScoreB:     rB.OverallScore,
				ScoreDiff:  diff,
				Regression: isRegression,
			})

			if isRegression {
				regressions++
			} else if diff > 0.01 {
				improvements++
			}
		}
	}

	return &CompareRunsResult{
		RunA:         compA,
		RunB:         compB,
		ScoreDiff:    compB.OverallScore - compA.OverallScore,
		Regressions:  regressions,
		Improvements: improvements,
		Examples:     examples,
	}, nil
}

// ListEvaluators returns available evaluator types.
func (s *EvalService) ListEvaluators(ctx context.Context) ([]Evaluator, error) {
	return []Evaluator{
		{
			Type:        "exact_match",
			Name:        "Exact Match",
			Description: "Checks if actual output exactly matches expected output",
			Params:      []EvaluatorParam{},
		},
		{
			Type:        "contains",
			Name:        "Contains",
			Description: "Checks if actual output contains expected strings",
			Params: []EvaluatorParam{
				{Name: "case_sensitive", Type: "boolean", Description: "Whether to use case-sensitive matching", Required: false, DefaultValue: "false"},
			},
		},
		{
			Type:        "semantic_similarity",
			Name:        "Semantic Similarity",
			Description: "Compares outputs using embedding similarity",
			Params: []EvaluatorParam{
				{Name: "threshold", Type: "number", Description: "Minimum similarity score (0-1)", Required: false, DefaultValue: "0.8"},
				{Name: "model", Type: "string", Description: "Embedding model to use", Required: false, DefaultValue: "text-embedding-3-small"},
			},
		},
		{
			Type:        "llm_judge",
			Name:        "LLM Judge",
			Description: "Uses an LLM to evaluate output quality",
			Params: []EvaluatorParam{
				{Name: "criteria", Type: "string", Description: "Evaluation criteria", Required: true},
				{Name: "model", Type: "string", Description: "Judge model", Required: false, DefaultValue: "gpt-4o"},
			},
		},
		{
			Type:        "regex",
			Name:        "Regex Match",
			Description: "Matches output against a regular expression",
			Params: []EvaluatorParam{
				{Name: "pattern", Type: "string", Description: "Regular expression pattern", Required: true},
			},
		},
		{
			Type:        "json_schema",
			Name:        "JSON Schema",
			Description: "Validates output against a JSON schema",
			Params: []EvaluatorParam{
				{Name: "schema", Type: "json", Description: "JSON schema to validate against", Required: true},
			},
		},
	}, nil
}
