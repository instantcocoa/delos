package deploy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Store defines the interface for quality gate storage.
type Store interface {
	// CreateQualityGate creates a quality gate. Names are unique.
	CreateQualityGate(ctx context.Context, gate *QualityGate) error

	// GetQualityGate retrieves a quality gate by ID. Returns (nil, nil) when
	// no such gate exists.
	GetQualityGate(ctx context.Context, id string) (*QualityGate, error)

	// GetQualityGateByName retrieves a quality gate by name. Returns
	// (nil, nil) when no such gate exists.
	GetQualityGateByName(ctx context.Context, name string) (*QualityGate, error)

	// ListQualityGates returns quality gates, filtered by prompt when
	// promptID is non-empty.
	ListQualityGates(ctx context.Context, promptID string) ([]*QualityGate, error)
}

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu    sync.RWMutex
	gates map[string]*QualityGate // id -> gate
}

// NewMemoryStore creates a new in-memory gate store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{gates: make(map[string]*QualityGate)}
}

// CreateQualityGate creates a quality gate.
func (s *MemoryStore) CreateQualityGate(ctx context.Context, gate *QualityGate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.gates[gate.ID]; exists {
		return fmt.Errorf("quality gate already exists: %s", gate.ID)
	}
	for _, existing := range s.gates {
		if strings.EqualFold(existing.Name, gate.Name) {
			return fmt.Errorf("quality gate name already in use: %s", gate.Name)
		}
	}

	stored := *gate
	s.gates[gate.ID] = &stored
	return nil
}

// GetQualityGate retrieves a quality gate by ID.
func (s *MemoryStore) GetQualityGate(ctx context.Context, id string) (*QualityGate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	gate, ok := s.gates[id]
	if !ok {
		return nil, nil
	}
	found := *gate
	return &found, nil
}

// GetQualityGateByName retrieves a quality gate by name.
func (s *MemoryStore) GetQualityGateByName(ctx context.Context, name string) (*QualityGate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, gate := range s.gates {
		if strings.EqualFold(gate.Name, name) {
			found := *gate
			return &found, nil
		}
	}
	return nil, nil
}

// ListQualityGates returns quality gates, optionally filtered by prompt.
func (s *MemoryStore) ListQualityGates(ctx context.Context, promptID string) ([]*QualityGate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*QualityGate
	for _, gate := range s.gates {
		if promptID != "" && gate.PromptID != promptID {
			continue
		}
		found := *gate
		results = append(results, &found)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].CreatedAt.Equal(results[j].CreatedAt) {
			return results[i].Name < results[j].Name
		}
		return results[i].CreatedAt.Before(results[j].CreatedAt)
	})

	return results, nil
}
