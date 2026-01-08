package eval

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Store defines the interface for eval storage operations.
type Store interface {
	// CreateEvalRun creates a new evaluation run.
	CreateEvalRun(ctx context.Context, run *EvalRun) error

	// GetEvalRun retrieves an evaluation run by ID.
	GetEvalRun(ctx context.Context, id string) (*EvalRun, error)

	// UpdateEvalRun updates an evaluation run.
	UpdateEvalRun(ctx context.Context, run *EvalRun) error

	// ListEvalRuns returns evaluation runs matching the query.
	ListEvalRuns(ctx context.Context, query ListEvalRunsQuery) ([]*EvalRun, int, error)

	// AddEvalResult adds a result for an evaluation run.
	AddEvalResult(ctx context.Context, result *EvalResult) error

	// GetEvalResults retrieves results for an evaluation run.
	GetEvalResults(ctx context.Context, query GetEvalResultsQuery) ([]*EvalResult, int, error)

	// GetEvalResultsByRunID retrieves all results for a run (for comparison).
	GetEvalResultsByRunID(ctx context.Context, runID string) ([]*EvalResult, error)
}

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu      sync.RWMutex
	runs    map[string]*EvalRun
	results map[string][]*EvalResult // runID -> results
}

// NewMemoryStore creates a new in-memory eval store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runs:    make(map[string]*EvalRun),
		results: make(map[string][]*EvalResult),
	}
}

// CreateEvalRun creates a new evaluation run.
func (s *MemoryStore) CreateEvalRun(ctx context.Context, run *EvalRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.runs[run.ID]; exists {
		return fmt.Errorf("eval run already exists: %s", run.ID)
	}

	s.runs[run.ID] = run
	s.results[run.ID] = []*EvalResult{}
	return nil
}

// GetEvalRun retrieves an evaluation run by ID.
func (s *MemoryStore) GetEvalRun(ctx context.Context, id string) (*EvalRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.runs[id]
	if !ok {
		return nil, nil
	}

	copy := *run
	return &copy, nil
}

// UpdateEvalRun updates an evaluation run.
func (s *MemoryStore) UpdateEvalRun(ctx context.Context, run *EvalRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.runs[run.ID]; !exists {
		return fmt.Errorf("eval run not found: %s", run.ID)
	}

	s.runs[run.ID] = run
	return nil
}

// ListEvalRuns returns evaluation runs matching the query.
func (s *MemoryStore) ListEvalRuns(ctx context.Context, query ListEvalRunsQuery) ([]*EvalRun, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*EvalRun

	for _, run := range s.runs {
		if s.matchesQuery(run, query) {
			copy := *run
			results = append(results, &copy)
		}
	}

	// Sort by created_at descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	totalCount := len(results)

	// Apply pagination
	if query.Offset > 0 {
		if query.Offset >= len(results) {
			results = nil
		} else {
			results = results[query.Offset:]
		}
	}

	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return results, totalCount, nil
}

func (s *MemoryStore) matchesQuery(run *EvalRun, query ListEvalRunsQuery) bool {
	if query.PromptID != "" && run.PromptID != query.PromptID {
		return false
	}
	if query.DatasetID != "" && run.DatasetID != query.DatasetID {
		return false
	}
	if query.Status != EvalRunStatusUnspecified && run.Status != query.Status {
		return false
	}
	return true
}

// AddEvalResult adds a result for an evaluation run.
func (s *MemoryStore) AddEvalResult(ctx context.Context, result *EvalResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.runs[result.EvalRunID]; !exists {
		return fmt.Errorf("eval run not found: %s", result.EvalRunID)
	}

	s.results[result.EvalRunID] = append(s.results[result.EvalRunID], result)
	return nil
}

// GetEvalResults retrieves results for an evaluation run.
func (s *MemoryStore) GetEvalResults(ctx context.Context, query GetEvalResultsQuery) ([]*EvalResult, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results, exists := s.results[query.EvalRunID]
	if !exists {
		return nil, 0, nil
	}

	// Filter if needed
	var filtered []*EvalResult
	for _, result := range results {
		if query.FailedOnly && result.Passed {
			continue
		}
		copy := *result
		filtered = append(filtered, &copy)
	}

	totalCount := len(filtered)

	// Apply pagination
	if query.Offset > 0 {
		if query.Offset >= len(filtered) {
			filtered = nil
		} else {
			filtered = filtered[query.Offset:]
		}
	}

	if query.Limit > 0 && len(filtered) > query.Limit {
		filtered = filtered[:query.Limit]
	}

	return filtered, totalCount, nil
}

// GetEvalResultsByRunID retrieves all results for a run.
func (s *MemoryStore) GetEvalResultsByRunID(ctx context.Context, runID string) ([]*EvalResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results, exists := s.results[runID]
	if !exists {
		return nil, nil
	}

	// Copy results
	copied := make([]*EvalResult, len(results))
	for i, result := range results {
		copy := *result
		copied[i] = &copy
	}

	return copied, nil
}
