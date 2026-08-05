package eval

import (
	"context"
	"time"
)

// RunSummaryForGates is the narrow view of an eval run that quality gates
// consume. It mirrors deploy.RunSummary without importing the deploy package,
// keeping the dependency direction eval <- controlplane -> deploy.
type RunSummaryForGates struct {
	RunID        string
	PromptID     string
	OverallScore float64
	PassRate     float64
	AvgLatencyMs float64
	TotalCostUSD float64
	CompletedAt  time.Time
}

// LatestCompletedRunForPrompt returns the most recent completed eval run for
// a prompt, or nil when the prompt has none. Used by the deploy module's
// gate verdicts via an in-process adapter.
func LatestCompletedRunForPrompt(ctx context.Context, store Store, promptID string) (*RunSummaryForGates, error) {
	runs, _, err := store.ListEvalRuns(ctx, ListEvalRunsQuery{
		PromptID: promptID,
		Status:   EvalRunStatusCompleted,
		Limit:    50,
	})
	if err != nil {
		return nil, err
	}

	var latest *EvalRun
	for _, run := range runs {
		if run.Summary == nil || run.CompletedAt == nil {
			continue
		}
		if latest == nil || run.CompletedAt.After(*latest.CompletedAt) {
			latest = run
		}
	}
	if latest == nil {
		return nil, nil
	}
	return &RunSummaryForGates{
		RunID:        latest.ID,
		PromptID:     latest.PromptID,
		OverallScore: latest.Summary.OverallScore,
		PassRate:     latest.Summary.PassRate,
		AvgLatencyMs: latest.Summary.AvgLatencyMs,
		TotalCostUSD: latest.Summary.TotalCostUSD,
		CompletedAt:  *latest.CompletedAt,
	}, nil
}
