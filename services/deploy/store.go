package deploy

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Store defines the interface for deployment storage operations.
type Store interface {
	// CreateDeployment creates a new deployment.
	CreateDeployment(ctx context.Context, deployment *Deployment) error

	// GetDeployment retrieves a deployment by ID.
	GetDeployment(ctx context.Context, id string) (*Deployment, error)

	// UpdateDeployment updates a deployment.
	UpdateDeployment(ctx context.Context, deployment *Deployment) error

	// ListDeployments returns deployments matching the query.
	ListDeployments(ctx context.Context, query ListDeploymentsQuery) ([]*Deployment, int, error)

	// GetCurrentDeployment gets the current active deployment for a prompt/environment.
	GetCurrentDeployment(ctx context.Context, promptID, environment string) (*Deployment, error)

	// CreateQualityGate creates a quality gate.
	CreateQualityGate(ctx context.Context, gate *QualityGate) error

	// GetQualityGate retrieves a quality gate by ID.
	GetQualityGate(ctx context.Context, id string) (*QualityGate, error)

	// ListQualityGates returns quality gates for a prompt.
	ListQualityGates(ctx context.Context, promptID string) ([]*QualityGate, error)
}

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu           sync.RWMutex
	deployments  map[string]*Deployment
	qualityGates map[string]*QualityGate
}

// NewMemoryStore creates a new in-memory deploy store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		deployments:  make(map[string]*Deployment),
		qualityGates: make(map[string]*QualityGate),
	}
}

// CreateDeployment creates a new deployment.
func (s *MemoryStore) CreateDeployment(ctx context.Context, deployment *Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deployments[deployment.ID]; exists {
		return fmt.Errorf("deployment already exists: %s", deployment.ID)
	}

	s.deployments[deployment.ID] = deployment
	return nil
}

// GetDeployment retrieves a deployment by ID.
func (s *MemoryStore) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	deployment, ok := s.deployments[id]
	if !ok {
		return nil, nil
	}

	copy := *deployment
	return &copy, nil
}

// UpdateDeployment updates a deployment.
func (s *MemoryStore) UpdateDeployment(ctx context.Context, deployment *Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deployments[deployment.ID]; !exists {
		return fmt.Errorf("deployment not found: %s", deployment.ID)
	}

	s.deployments[deployment.ID] = deployment
	return nil
}

// ListDeployments returns deployments matching the query.
func (s *MemoryStore) ListDeployments(ctx context.Context, query ListDeploymentsQuery) ([]*Deployment, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Deployment

	for _, deployment := range s.deployments {
		if s.matchesQuery(deployment, query) {
			copy := *deployment
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

func (s *MemoryStore) matchesQuery(deployment *Deployment, query ListDeploymentsQuery) bool {
	if query.PromptID != "" && deployment.PromptID != query.PromptID {
		return false
	}
	if query.Environment != "" && deployment.Environment != query.Environment {
		return false
	}
	if query.Status != DeploymentStatusUnspecified && deployment.Status != query.Status {
		return false
	}
	return true
}

// GetCurrentDeployment gets the current active deployment for a prompt/environment.
func (s *MemoryStore) GetCurrentDeployment(ctx context.Context, promptID, environment string) (*Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var current *Deployment
	for _, deployment := range s.deployments {
		if deployment.PromptID == promptID &&
			deployment.Environment == environment &&
			deployment.Status == DeploymentStatusCompleted {
			if current == nil || deployment.CompletedAt.After(*current.CompletedAt) {
				copy := *deployment
				current = &copy
			}
		}
	}

	return current, nil
}

// CreateQualityGate creates a quality gate.
func (s *MemoryStore) CreateQualityGate(ctx context.Context, gate *QualityGate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.qualityGates[gate.ID]; exists {
		return fmt.Errorf("quality gate already exists: %s", gate.ID)
	}

	s.qualityGates[gate.ID] = gate
	return nil
}

// GetQualityGate retrieves a quality gate by ID.
func (s *MemoryStore) GetQualityGate(ctx context.Context, id string) (*QualityGate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	gate, ok := s.qualityGates[id]
	if !ok {
		return nil, nil
	}

	copy := *gate
	return &copy, nil
}

// ListQualityGates returns quality gates for a prompt.
func (s *MemoryStore) ListQualityGates(ctx context.Context, promptID string) ([]*QualityGate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*QualityGate
	for _, gate := range s.qualityGates {
		if gate.PromptID == promptID {
			copy := *gate
			results = append(results, &copy)
		}
	}

	// Sort by created_at
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.Before(results[j].CreatedAt)
	})

	return results, nil
}
