package controlplane

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/instantcocoa/delos/pkg/config"
	"github.com/instantcocoa/delos/services/observe"
)

func TestReflectionEnabledOnlyInDevelopment(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"development", true},
		{"staging", false},
		{"production", false},
		{"", false},
		{"Development", false}, // exact match only; no fuzzy environments
	}
	for _, tt := range tests {
		if got := reflectionEnabled(&config.Base{Environment: tt.env}); got != tt.want {
			t.Errorf("reflectionEnabled(env=%q) = %v, want %v", tt.env, got, tt.want)
		}
	}
}

func TestAuthModeLabel(t *testing.T) {
	if got := authMode(true); got != "token" {
		t.Errorf("authMode(true) = %q", got)
	}
	if got := authMode(false); got == "token" {
		t.Errorf("authMode(false) must not claim a token is configured, got %q", got)
	}
}

func testSpan(traceID string, at time.Time) observe.Span {
	return observe.Span{
		TraceID:     traceID,
		SpanID:      traceID + "-span",
		Name:        "chat",
		ServiceName: "delos-gateway",
		StartTime:   at,
		Duration:    time.Millisecond,
	}
}

func TestBoundedMemorySpanStore_EvictsOldestGeneration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	const cap = 10
	store := newBoundedMemorySpanStore(cap, logger)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)

	// Ingest well past the cap: memory must not grow with the input.
	for i := 0; i < cap*5; i++ {
		id := fmt.Sprintf("trace-%03d", i)
		if _, err := store.IngestSpans(ctx, []observe.Span{testSpan(id, base.Add(time.Duration(i)*time.Second))}); err != nil {
			t.Fatal(err)
		}
	}

	_, total, err := store.QueryTraces(ctx, observe.TraceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if total > 2*cap {
		t.Fatalf("retained %d traces, want at most %d (2 generations of %d)", total, 2*cap, cap)
	}
	if total == 0 {
		t.Fatal("eviction dropped everything; recent traces must survive")
	}

	// The most recent trace is still there, an ancient one is gone.
	newest := fmt.Sprintf("trace-%03d", cap*5-1)
	got, err := store.GetTrace(ctx, newest)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatalf("newest trace %s should still be readable", newest)
	}
	oldest, err := store.GetTrace(ctx, "trace-000")
	if err != nil {
		t.Fatal(err)
	}
	if oldest != nil {
		t.Fatal("trace-000 should have been evicted")
	}
}

func TestBoundedMemorySpanStore_ReadsAcrossGenerations(t *testing.T) {
	store := newBoundedMemorySpanStore(4, nil)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)

	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("t%d", i)
		if _, err := store.IngestSpans(ctx, []observe.Span{testSpan(id, base.Add(time.Duration(i)*time.Second))}); err != nil {
			t.Fatal(err)
		}
	}

	// t0..t3 are in the previous generation, t4/t5 in the current one; both
	// must be visible, newest first, with no duplicates.
	traces, total, err := store.QueryTraces(ctx, observe.TraceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 6 {
		t.Fatalf("total = %d, want 6 across both generations", total)
	}
	seen := map[string]bool{}
	for i, tr := range traces {
		if seen[tr.TraceID] {
			t.Fatalf("duplicate trace %s across generations", tr.TraceID)
		}
		seen[tr.TraceID] = true
		if i > 0 && traces[i-1].StartTime.Before(tr.StartTime) {
			t.Fatal("traces are not sorted newest first")
		}
	}

	// Pagination applies to the merged view, not per generation.
	page, total, err := store.QueryTraces(ctx, observe.TraceQuery{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if total != 6 || len(page) != 2 {
		t.Fatalf("page = %d traces (total %d), want 2 of 6", len(page), total)
	}
	if page[0].TraceID != "t4" || page[1].TraceID != "t3" {
		t.Fatalf("page = %s,%s; want t4,t3", page[0].TraceID, page[1].TraceID)
	}
}

func TestBoundedMemorySpanStore_UncappedWhenZero(t *testing.T) {
	store := newBoundedMemorySpanStore(0, nil)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		if _, err := store.IngestSpans(ctx, []observe.Span{testSpan(fmt.Sprintf("t%d", i), time.Now())}); err != nil {
			t.Fatal(err)
		}
	}
	_, total, err := store.QueryTraces(ctx, observe.TraceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 50 {
		t.Fatalf("total = %d, want 50 (cap disabled)", total)
	}
}
