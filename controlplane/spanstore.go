package controlplane

import (
	"context"
	"log/slog"
	"sort"
	"sync"

	"github.com/instantcocoa/delos/services/observe"
)

// boundedMemorySpanStore caps the memory the in-memory span store can consume.
//
// OTLP ingest accepts spans from anything that can reach POST /v1/traces, so
// an uncapped map is an OOM waiting to happen. Rather than teach the observe
// store to evict (it has no delete path), this keeps two generations: spans
// land in `current` until it reaches maxSpans, then `current` becomes
// `previous` and a fresh store takes over. The older generation is dropped
// wholesale on the next rotation, so live memory is bounded by roughly
// 2 x maxSpans and old traces age out in insertion order.
//
// This is the development/ephemeral path only. DELOS_STORAGE_BACKEND=postgres
// uses the Postgres store, which has no such limit.
type boundedMemorySpanStore struct {
	maxSpans int
	logger   *slog.Logger

	mu           sync.RWMutex
	current      *observe.MemorySpanStore
	previous     *observe.MemorySpanStore
	currentSpans int
	rotations    int
}

// newBoundedMemorySpanStore returns a span store that keeps at most roughly
// 2*maxSpans spans. A maxSpans <= 0 disables the cap.
func newBoundedMemorySpanStore(maxSpans int, logger *slog.Logger) *boundedMemorySpanStore {
	return &boundedMemorySpanStore{
		maxSpans: maxSpans,
		logger:   logger,
		current:  observe.NewMemorySpanStore(),
	}
}

func (s *boundedMemorySpanStore) IngestSpans(ctx context.Context, spans []observe.Span) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, err := s.current.IngestSpans(ctx, spans)
	s.currentSpans += n
	if s.maxSpans > 0 && s.currentSpans >= s.maxSpans {
		s.previous = s.current
		s.current = observe.NewMemorySpanStore()
		s.currentSpans = 0
		s.rotations++
		if s.logger != nil {
			s.logger.WarnContext(ctx, "in-memory span store full, dropped the oldest generation",
				"max_spans", s.maxSpans, "rotations", s.rotations,
				"hint", "set DELOS_STORAGE_BACKEND=postgres to retain traces, or raise DELOS_OBSERVE_MEMORY_MAX_SPANS")
		}
	}
	return n, err
}

func (s *boundedMemorySpanStore) GetTrace(ctx context.Context, traceID string) (*observe.Trace, error) {
	s.mu.RLock()
	current, previous := s.current, s.previous
	s.mu.RUnlock()

	trace, err := current.GetTrace(ctx, traceID)
	if err != nil || trace != nil || previous == nil {
		return trace, err
	}
	return previous.GetTrace(ctx, traceID)
}

func (s *boundedMemorySpanStore) QueryTraces(ctx context.Context, query observe.TraceQuery) ([]observe.Trace, int, error) {
	s.mu.RLock()
	current, previous := s.current, s.previous
	s.mu.RUnlock()

	// Paginate over the merged view, so ask each generation for everything
	// that matches and apply offset/limit here.
	unpaged := query
	unpaged.Limit = 0
	unpaged.Offset = 0

	merged, _, err := current.QueryTraces(ctx, unpaged)
	if err != nil {
		return nil, 0, err
	}
	if previous != nil {
		older, _, err := previous.QueryTraces(ctx, unpaged)
		if err != nil {
			return nil, 0, err
		}
		seen := make(map[string]struct{}, len(merged))
		for _, t := range merged {
			seen[t.TraceID] = struct{}{}
		}
		for _, t := range older {
			// A trace whose spans straddle a rotation is already represented
			// by the newer generation; do not list it twice.
			if _, dup := seen[t.TraceID]; !dup {
				merged = append(merged, t)
			}
		}
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].StartTime.After(merged[j].StartTime)
	})

	total := len(merged)
	if query.Offset > 0 {
		if query.Offset >= len(merged) {
			return nil, total, nil
		}
		merged = merged[query.Offset:]
	}
	if query.Limit > 0 && len(merged) > query.Limit {
		merged = merged[:query.Limit]
	}
	return merged, total, nil
}

var _ observe.SpanStore = (*boundedMemorySpanStore)(nil)
