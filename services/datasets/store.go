package datasets

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store defines the interface for dataset storage operations.
type Store interface {
	// CreateDataset creates a new dataset.
	CreateDataset(ctx context.Context, dataset *Dataset) error

	// GetDataset retrieves a dataset by ID.
	GetDataset(ctx context.Context, id string) (*Dataset, error)

	// UpdateDataset updates a dataset.
	UpdateDataset(ctx context.Context, dataset *Dataset) error

	// DeleteDataset deletes a dataset.
	DeleteDataset(ctx context.Context, id string) error

	// ListDatasets returns datasets matching the query.
	ListDatasets(ctx context.Context, query ListDatasetsQuery) ([]*Dataset, int, error)

	// AddExamples adds examples to a dataset.
	AddExamples(ctx context.Context, datasetID string, examples []*Example) error

	// GetExamples retrieves examples from a dataset.
	GetExamples(ctx context.Context, query GetExamplesQuery) ([]*Example, int, error)

	// RemoveExamples removes examples from a dataset.
	RemoveExamples(ctx context.Context, datasetID string, exampleIDs []string) (int, error)

	// GetExample retrieves a single example by ID.
	GetExample(ctx context.Context, id string) (*Example, error)
}

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu       sync.RWMutex
	datasets map[string]*Dataset
	examples map[string][]*Example // datasetID -> examples
}

// NewMemoryStore creates a new in-memory dataset store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		datasets: make(map[string]*Dataset),
		examples: make(map[string][]*Example),
	}
}

// CreateDataset creates a new dataset.
func (s *MemoryStore) CreateDataset(ctx context.Context, dataset *Dataset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.datasets[dataset.ID]; exists {
		return fmt.Errorf("dataset already exists: %s", dataset.ID)
	}

	s.datasets[dataset.ID] = dataset
	s.examples[dataset.ID] = []*Example{}
	return nil
}

// GetDataset retrieves a dataset by ID.
func (s *MemoryStore) GetDataset(ctx context.Context, id string) (*Dataset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dataset, ok := s.datasets[id]
	if !ok {
		return nil, nil
	}

	copy := *dataset
	return &copy, nil
}

// UpdateDataset updates a dataset.
func (s *MemoryStore) UpdateDataset(ctx context.Context, dataset *Dataset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.datasets[dataset.ID]; !exists {
		return fmt.Errorf("dataset not found: %s", dataset.ID)
	}

	s.datasets[dataset.ID] = dataset
	return nil
}

// DeleteDataset deletes a dataset.
func (s *MemoryStore) DeleteDataset(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.datasets[id]; !exists {
		return fmt.Errorf("dataset not found: %s", id)
	}

	delete(s.datasets, id)
	delete(s.examples, id)
	return nil
}

// ListDatasets returns datasets matching the query.
func (s *MemoryStore) ListDatasets(ctx context.Context, query ListDatasetsQuery) ([]*Dataset, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Dataset

	for _, dataset := range s.datasets {
		if s.matchesQuery(dataset, query) {
			copy := *dataset
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

func (s *MemoryStore) matchesQuery(dataset *Dataset, query ListDatasetsQuery) bool {
	// Filter by prompt ID
	if query.PromptID != "" && dataset.PromptID != query.PromptID {
		return false
	}

	// Filter by search
	if query.Search != "" {
		search := strings.ToLower(query.Search)
		if !strings.Contains(strings.ToLower(dataset.Name), search) &&
			!strings.Contains(strings.ToLower(dataset.Description), search) {
			return false
		}
	}

	// Filter by tags
	if len(query.Tags) > 0 {
		tagSet := make(map[string]bool)
		for _, t := range dataset.Tags {
			tagSet[t] = true
		}
		for _, t := range query.Tags {
			if !tagSet[t] {
				return false
			}
		}
	}

	return true
}

// AddExamples adds examples to a dataset.
func (s *MemoryStore) AddExamples(ctx context.Context, datasetID string, examples []*Example) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dataset, exists := s.datasets[datasetID]
	if !exists {
		return fmt.Errorf("dataset not found: %s", datasetID)
	}

	s.examples[datasetID] = append(s.examples[datasetID], examples...)
	dataset.ExampleCount = len(s.examples[datasetID])
	dataset.LastUpdated = time.Now()

	return nil
}

// GetExamples retrieves examples from a dataset.
func (s *MemoryStore) GetExamples(ctx context.Context, query GetExamplesQuery) ([]*Example, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	examples, exists := s.examples[query.DatasetID]
	if !exists {
		return nil, 0, nil
	}

	// Copy examples
	results := make([]*Example, len(examples))
	for i, e := range examples {
		copy := *e
		results[i] = &copy
	}

	// Shuffle if requested
	if query.Shuffle {
		rand.Shuffle(len(results), func(i, j int) {
			results[i], results[j] = results[j], results[i]
		})
	}

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

// RemoveExamples removes examples from a dataset.
func (s *MemoryStore) RemoveExamples(ctx context.Context, datasetID string, exampleIDs []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dataset, exists := s.datasets[datasetID]
	if !exists {
		return 0, fmt.Errorf("dataset not found: %s", datasetID)
	}

	examples := s.examples[datasetID]
	idSet := make(map[string]bool)
	for _, id := range exampleIDs {
		idSet[id] = true
	}

	var remaining []*Example
	removed := 0
	for _, e := range examples {
		if idSet[e.ID] {
			removed++
		} else {
			remaining = append(remaining, e)
		}
	}

	s.examples[datasetID] = remaining
	dataset.ExampleCount = len(remaining)
	dataset.LastUpdated = time.Now()

	return removed, nil
}

// GetExample retrieves a single example by ID.
func (s *MemoryStore) GetExample(ctx context.Context, id string) (*Example, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, examples := range s.examples {
		for _, e := range examples {
			if e.ID == id {
				copy := *e
				return &copy, nil
			}
		}
	}

	return nil, nil
}
