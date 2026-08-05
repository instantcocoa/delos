package deploy

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testGate(name, prompt string, conditions ...GateCondition) *QualityGate {
	return &QualityGate{
		ID:         "id-" + name,
		Name:       name,
		PromptID:   prompt,
		Conditions: conditions,
		CreatedAt:  time.Now(),
	}
}

func TestMemoryStoreGateCRUD(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	gate := testGate("release", "prompt-1", GateCondition{Metric: MetricOverallScore, Operator: OperatorGTE, Threshold: 0.8})
	if err := store.CreateQualityGate(ctx, gate); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// duplicate name rejected (case-insensitive)
	dup := testGate("RELEASE", "prompt-2")
	dup.ID = "other-id"
	if err := store.CreateQualityGate(ctx, dup); err == nil {
		t.Error("duplicate gate name must be rejected")
	}

	byName, err := store.GetQualityGateByName(ctx, "release")
	if err != nil || byName == nil || byName.ID != gate.ID {
		t.Fatalf("GetQualityGateByName = %v, %v", byName, err)
	}
	if missing, err := store.GetQualityGateByName(ctx, "nope"); err != nil || missing != nil {
		t.Errorf("missing gate should be (nil, nil), got %v, %v", missing, err)
	}

	all, err := store.ListQualityGates(ctx, "")
	if err != nil || len(all) != 1 {
		t.Errorf("list all = %v, %v", all, err)
	}
	filtered, err := store.ListQualityGates(ctx, "prompt-1")
	if err != nil || len(filtered) != 1 {
		t.Errorf("list filtered = %v, %v", filtered, err)
	}
	none, err := store.ListQualityGates(ctx, "prompt-x")
	if err != nil || len(none) != 0 {
		t.Errorf("list none = %v, %v", none, err)
	}
}

func TestParseConditionTable(t *testing.T) {
	cases := []struct {
		in      string
		want    GateCondition
		wantErr bool
	}{
		{"overall_score>=0.8", GateCondition{MetricOverallScore, OperatorGTE, 0.8}, false},
		{"pass_rate >= 0.9", GateCondition{MetricPassRate, OperatorGTE, 0.9}, false},
		{"avg_latency_ms<=2000", GateCondition{MetricAvgLatencyMs, OperatorLTE, 2000}, false},
		{"total_cost_usd <= 1.5", GateCondition{MetricTotalCostUSD, OperatorLTE, 1.5}, false},
		{"overall_score>0.8", GateCondition{}, true},  // unsupported operator
		{"bogus_metric>=1", GateCondition{}, true},    // unknown metric
		{"overall_score>=abc", GateCondition{}, true}, // bad threshold
		{">=0.8", GateCondition{}, true},              // missing metric
	}
	for _, tc := range cases {
		got, err := ParseCondition(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseCondition(%q) should fail", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseCondition(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseCondition(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// fakeEvals returns a fixed run summary.
type fakeEvals struct {
	run *RunSummary
	err error
}

func (f fakeEvals) LatestCompletedRun(ctx context.Context, promptID string) (*RunSummary, error) {
	return f.run, f.err
}

func TestVerdict(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	run := &RunSummary{
		RunID:        "run-1",
		PromptID:     "prompt-1",
		OverallScore: 0.85,
		PassRate:     0.9,
		AvgLatencyMs: 1200,
		TotalCostUSD: 0.42,
		CompletedAt:  time.Now(),
	}
	svc := NewDeployService(store, fakeEvals{run: run})

	_, err := svc.CreateQualityGate(ctx, CreateQualityGateInput{
		Name:     "release",
		PromptID: "prompt-1",
		Conditions: []GateCondition{
			{Metric: MetricOverallScore, Operator: OperatorGTE, Threshold: 0.8},
			{Metric: MetricAvgLatencyMs, Operator: OperatorLTE, Threshold: 2000},
		},
	})
	if err != nil {
		t.Fatalf("create gate: %v", err)
	}

	verdict, err := svc.Verdict(ctx, "release")
	if err != nil {
		t.Fatalf("verdict: %v", err)
	}
	if !verdict.Pass {
		t.Errorf("expected pass, reasons: %v", verdict.Reasons)
	}
	if verdict.EvalRunID != "run-1" {
		t.Errorf("run id = %s", verdict.EvalRunID)
	}

	// A failing condition flips the verdict with an explanatory reason.
	_, err = svc.CreateQualityGate(ctx, CreateQualityGateInput{
		Name:     "strict",
		PromptID: "prompt-1",
		Conditions: []GateCondition{
			{Metric: MetricOverallScore, Operator: OperatorGTE, Threshold: 0.95},
		},
	})
	if err != nil {
		t.Fatalf("create strict gate: %v", err)
	}
	verdict, err = svc.Verdict(ctx, "strict")
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Pass {
		t.Error("expected fail")
	}
	found := false
	for _, r := range verdict.Reasons {
		if r == "overall_score 0.85 >= 0.95: fail" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected explanatory reason, got %v", verdict.Reasons)
	}
}

func TestVerdictNoRuns(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewDeployService(store, fakeEvals{run: nil})

	_, err := svc.CreateQualityGate(ctx, CreateQualityGateInput{
		Name:       "empty",
		PromptID:   "prompt-x",
		Conditions: []GateCondition{{Metric: MetricPassRate, Operator: OperatorGTE, Threshold: 0.5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := svc.Verdict(ctx, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Pass {
		t.Error("a gate with no eval runs must fail closed")
	}

	// unknown gate -> ErrGateNotFound
	if _, err := svc.Verdict(ctx, "missing"); err == nil || !errorsIs(err, ErrGateNotFound) {
		t.Errorf("expected ErrGateNotFound, got %v", err)
	}
}
