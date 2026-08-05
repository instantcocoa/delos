package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// DeployService evaluates quality gates. It holds gate definitions and reads
// eval run summaries through the narrow EvalResults interface; the control
// plane wires that to the eval module with a direct in-process adapter.
type DeployService struct {
	store Store
	evals EvalResults
}

// NewDeployService creates a gate service.
func NewDeployService(store Store, evals EvalResults) *DeployService {
	return &DeployService{store: store, evals: evals}
}

// CreateQualityGate validates and stores a gate.
func (s *DeployService) CreateQualityGate(ctx context.Context, input CreateQualityGateInput) (*QualityGate, error) {
	if input.Name == "" {
		return nil, fmt.Errorf("gate name is required")
	}
	if input.PromptID == "" {
		return nil, fmt.Errorf("prompt id is required")
	}
	if len(input.Conditions) == 0 {
		return nil, fmt.Errorf("at least one condition is required")
	}
	for _, c := range input.Conditions {
		if !isSupportedMetric(c.Metric) {
			return nil, fmt.Errorf("unknown metric %q", c.Metric)
		}
		if c.Operator != OperatorGTE && c.Operator != OperatorLTE {
			return nil, fmt.Errorf("condition on %q needs an operator (>= or <=)", c.Metric)
		}
	}

	gate := &QualityGate{
		ID:          uuid.NewString(),
		Name:        input.Name,
		Description: input.Description,
		PromptID:    input.PromptID,
		Conditions:  input.Conditions,
		CreatedAt:   time.Now().UTC(),
		CreatedBy:   input.CreatedBy,
	}
	if err := s.store.CreateQualityGate(ctx, gate); err != nil {
		return nil, err
	}
	return gate, nil
}

// ListQualityGates lists gates, optionally filtered by prompt.
func (s *DeployService) ListQualityGates(ctx context.Context, promptID string) ([]*QualityGate, error) {
	return s.store.ListQualityGates(ctx, promptID)
}

// Verdict evaluates the named gate against the latest completed eval run for
// its prompt.
func (s *DeployService) Verdict(ctx context.Context, name string) (*GateVerdict, error) {
	gate, err := s.store.GetQualityGateByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if gate == nil {
		return nil, fmt.Errorf("%w: %s", ErrGateNotFound, name)
	}

	verdict := &GateVerdict{Gate: gate, EvaluatedAt: time.Now().UTC()}

	if s.evals == nil {
		verdict.Reasons = []string{"no eval results source is configured"}
		return verdict, nil
	}
	run, err := s.evals.LatestCompletedRun(ctx, gate.PromptID)
	if err != nil {
		return nil, fmt.Errorf("failed to read eval results for prompt %s: %w", gate.PromptID, err)
	}
	if run == nil {
		verdict.Reasons = []string{
			fmt.Sprintf("no completed eval run found for prompt %q - run an eval before checking this gate", gate.PromptID),
		}
		return verdict, nil
	}

	verdict.EvalRunID = run.RunID
	verdict.Pass = true
	for _, c := range gate.Conditions {
		value, ok := run.Metric(c.Metric)
		if !ok {
			verdict.Pass = false
			verdict.Reasons = append(verdict.Reasons, fmt.Sprintf("%s: metric not present in run summary: fail", c.Metric))
			continue
		}
		holds := (c.Operator == OperatorGTE && value >= c.Threshold) ||
			(c.Operator == OperatorLTE && value <= c.Threshold)
		state := "pass"
		if !holds {
			state = "fail"
			verdict.Pass = false
		}
		verdict.Reasons = append(verdict.Reasons, fmt.Sprintf("%s %s %s %s: %s",
			c.Metric, formatNumber(value), c.Operator, formatNumber(c.Threshold), state))
	}
	verdict.Reasons = append(verdict.Reasons,
		fmt.Sprintf("evaluated against run %s (completed %s)", run.RunID, run.CompletedAt.UTC().Format(time.RFC3339)))
	return verdict, nil
}

// VerdictHandler serves GET /v1/gates/{gate}/verdict as JSON for CI systems
// and dashboards. Mounted by the control plane.
func (s *DeployService) VerdictHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("gate")
		if name == "" {
			http.Error(w, `{"error":"gate name required"}`, http.StatusBadRequest)
			return
		}
		verdict, err := s.Verdict(r.Context(), name)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			status := http.StatusInternalServerError
			if errorsIs(err, ErrGateNotFound) {
				status = http.StatusNotFound
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"gate":         verdict.Gate.Name,
			"pass":         verdict.Pass,
			"reasons":      verdict.Reasons,
			"eval_run_id":  verdict.EvalRunID,
			"evaluated_at": verdict.EvaluatedAt.Format(time.RFC3339),
		})
	})
}

// errorsIs is a local alias to keep the import list tidy.
func errorsIs(err, target error) bool { return errors.Is(err, target) }
