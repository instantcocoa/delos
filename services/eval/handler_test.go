package eval

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

func newTestHandler(t *testing.T, opts ...HandlerOption) (*Handler, Store) {
	t.Helper()
	store := NewMemoryStore()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewHandler(logger, NewEvalService(store), opts...), store
}

// stubSources answers the reference lookups CreateEvalRun makes. Anything not
// in the known sets is NOT_FOUND, exactly as the real prompt and datasets
// handlers report it.
type stubSources struct {
	prompts  map[string]bool
	datasets map[string]bool
}

func (s stubSources) GetPrompt(ctx context.Context, id string) (*promptv1.Prompt, error) {
	if !s.prompts[id] {
		return nil, status.Errorf(codes.NotFound, "prompt not found: %s", id)
	}
	return &promptv1.Prompt{Id: id}, nil
}

func (s stubSources) GetExamples(ctx context.Context, datasetID string, limit int, shuffle bool) ([]*datasetsv1.Example, error) {
	if !s.datasets[datasetID] {
		return nil, status.Errorf(codes.NotFound, "dataset not found: %s", datasetID)
	}
	return nil, nil
}

// ---- error mapping ----

// TestCancelEvalRun_NotFound: the store said "eval run not found" and the
// handler answered INTERNAL, blaming the server for the caller's bad id.
func TestCancelEvalRun_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)

	_, err := h.CancelEvalRun(context.Background(), &evalv1.CancelEvalRunRequest{Id: "no-such-run"})
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("CancelEvalRun() code = %s, want NotFound", got)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "no-such-run") {
		t.Errorf("CancelEvalRun() message = %q, want it to name the id", msg)
	}
}

// TestCancelEvalRun_AlreadyFinished: a run that exists but cannot be cancelled
// is a precondition failure, distinct from a missing run.
func TestCancelEvalRun_AlreadyFinished(t *testing.T) {
	h, store := newTestHandler(t)
	ctx := context.Background()

	run := &EvalRun{ID: "run-done", Name: "Done", Status: EvalRunStatusCompleted, CreatedAt: time.Now()}
	if err := store.CreateEvalRun(ctx, run); err != nil {
		t.Fatalf("CreateEvalRun() error = %v", err)
	}

	_, err := h.CancelEvalRun(ctx, &evalv1.CancelEvalRunRequest{Id: "run-done"})
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("CancelEvalRun() code = %s, want FailedPrecondition", got)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "completed") {
		t.Errorf("CancelEvalRun() message = %q, want it to name the status", msg)
	}
}

func TestCompareRuns_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)

	_, err := h.CompareRuns(context.Background(), &evalv1.CompareRunsRequest{
		RunIdA: "missing-a", RunIdB: "missing-b",
	})
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("CompareRuns() code = %s, want NotFound", got)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "missing-a") {
		t.Errorf("CompareRuns() message = %q, want it to name the missing run", msg)
	}
}

// TestGetEvalResults_NotFound: an unknown run id returned an empty result set,
// which reads as "this run produced nothing" rather than "no such run".
func TestGetEvalResults_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)

	_, err := h.GetEvalResults(context.Background(), &evalv1.GetEvalResultsRequest{EvalRunId: "no-such-run"})
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("GetEvalResults() code = %s, want NotFound", got)
	}
}

// ---- fail-fast reference validation ----

// TestCreateEvalRun_RejectsUnknownReferences: a run naming a prompt or dataset
// that does not exist can never execute, so it is rejected at creation rather
// than queued and failed asynchronously by the runner.
func TestCreateEvalRun_RejectsUnknownReferences(t *testing.T) {
	sources := stubSources{
		prompts:  map[string]bool{"real-prompt": true},
		datasets: map[string]bool{"real-dataset": true},
	}
	h, _ := newTestHandler(t, WithReferenceValidation(sources, sources))
	ctx := context.Background()

	tests := []struct {
		name      string
		promptID  string
		datasetID string
		wantIn    string
	}{
		{"unknown prompt", "ghost-prompt", "real-dataset", "ghost-prompt"},
		{"unknown dataset", "real-prompt", "ghost-dataset", "ghost-dataset"},
		{"both unknown", "ghost-prompt", "ghost-dataset", "ghost-prompt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := h.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
				Name:      "Run",
				PromptId:  tc.promptID,
				DatasetId: tc.datasetID,
			})
			if err == nil {
				t.Fatalf("CreateEvalRun() accepted unresolvable references, got run %s", resp.GetEvalRun().GetId())
			}
			if got := status.Code(err); got != codes.NotFound {
				t.Errorf("CreateEvalRun() code = %s, want NotFound", got)
			}
			if msg := status.Convert(err).Message(); !strings.Contains(msg, tc.wantIn) {
				t.Errorf("CreateEvalRun() message = %q, want it to name %q", msg, tc.wantIn)
			}
		})
	}
}

func TestCreateEvalRun_AcceptsResolvableReferences(t *testing.T) {
	sources := stubSources{
		prompts:  map[string]bool{"real-prompt": true},
		datasets: map[string]bool{"real-dataset": true},
	}
	h, _ := newTestHandler(t, WithReferenceValidation(sources, sources))

	resp, err := h.CreateEvalRun(context.Background(), &evalv1.CreateEvalRunRequest{
		Name: "Run", PromptId: "real-prompt", DatasetId: "real-dataset",
	})
	if err != nil {
		t.Fatalf("CreateEvalRun() error = %v", err)
	}
	if resp.EvalRun.Status != evalv1.EvalRunStatus_EVAL_RUN_STATUS_PENDING {
		t.Errorf("status = %s, want PENDING", resp.EvalRun.Status)
	}
}

// Without the option the handler takes references on trust, so embedders with
// no prompt or dataset module still work.
func TestCreateEvalRun_WithoutValidation(t *testing.T) {
	h, _ := newTestHandler(t)

	if _, err := h.CreateEvalRun(context.Background(), &evalv1.CreateEvalRunRequest{
		Name: "Run", PromptId: "ghost", DatasetId: "ghost",
	}); err != nil {
		t.Fatalf("CreateEvalRun() error = %v", err)
	}
}
