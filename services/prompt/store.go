package prompt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/instantcocoa/delos/pkg/config"
)

// Store defines the interface for prompt storage operations.
type Store interface {
	Create(ctx context.Context, prompt *Prompt) error
	Get(ctx context.Context, id string) (*Prompt, error)
	GetBySlug(ctx context.Context, slug string, version int) (*Prompt, error)
	Update(ctx context.Context, prompt *Prompt) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, query ListQuery) ([]*Prompt, int, error)
	GetHistory(ctx context.Context, id string, limit int) ([]PromptVersion, error)
	GetVersion(ctx context.Context, id string, version int) (*Prompt, error)
}

// StoreOptions contains configuration for creating a store.
type StoreOptions struct {
	Backend config.StorageBackend
	DB      *sql.DB
}

// NewStore creates a new Store based on the provided options.
func NewStore(opts StoreOptions) (Store, error) {
	switch opts.Backend {
	case config.StoragePostgres:
		if opts.DB == nil {
			return nil, fmt.Errorf("database connection required for postgres backend")
		}
		return NewPostgresStore(opts.DB), nil
	case config.StorageMemory:
		return NewMemoryStore(), nil
	default:
		return NewMemoryStore(), nil
	}
}

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu       sync.RWMutex
	prompts  map[string]*Prompt         // id -> current prompt
	versions map[string][]*Prompt       // id -> all versions
	slugs    map[string]string          // slug -> id
	history  map[string][]PromptVersion // id -> version history
}

// NewMemoryStore creates a new in-memory prompt store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		prompts:  make(map[string]*Prompt),
		versions: make(map[string][]*Prompt),
		slugs:    make(map[string]string),
		history:  make(map[string][]PromptVersion),
	}
}

func (s *MemoryStore) Create(ctx context.Context, prompt *Prompt) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.slugs[prompt.Slug]; exists {
		return fmt.Errorf("slug already exists: %s", prompt.Slug)
	}

	s.prompts[prompt.ID] = prompt
	s.versions[prompt.ID] = []*Prompt{CopyPrompt(prompt)}
	s.slugs[prompt.Slug] = prompt.ID
	s.history[prompt.ID] = []PromptVersion{
		{
			Version:           prompt.Version,
			ChangeDescription: "Initial version",
			UpdatedBy:         prompt.CreatedBy,
			UpdatedAt:         prompt.CreatedAt,
		},
	}

	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id string) (*Prompt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prompt, ok := s.prompts[id]
	if !ok {
		return nil, nil
	}

	return CopyPrompt(prompt), nil
}

func (s *MemoryStore) GetBySlug(ctx context.Context, slug string, version int) (*Prompt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.slugs[slug]
	if !ok {
		return nil, nil
	}

	if version <= 0 {
		prompt, ok := s.prompts[id]
		if !ok {
			return nil, nil
		}
		return CopyPrompt(prompt), nil
	}

	versions := s.versions[id]
	for _, p := range versions {
		if p.Version == version {
			return CopyPrompt(p), nil
		}
	}

	return nil, nil
}

func (s *MemoryStore) Update(ctx context.Context, prompt *Prompt) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.prompts[prompt.ID]
	if !ok {
		return fmt.Errorf("prompt not found: %s", prompt.ID)
	}

	prompt.Version = existing.Version + 1
	prompt.UpdatedAt = time.Now()

	s.prompts[prompt.ID] = prompt
	s.versions[prompt.ID] = append(s.versions[prompt.ID], CopyPrompt(prompt))

	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prompt, ok := s.prompts[id]
	if !ok {
		return fmt.Errorf("prompt not found: %s", id)
	}

	prompt.Status = PromptStatusArchived
	prompt.UpdatedAt = time.Now()

	return nil
}

func (s *MemoryStore) List(ctx context.Context, query ListQuery) ([]*Prompt, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Prompt

	for _, prompt := range s.prompts {
		if !s.matchesQuery(prompt, query) {
			continue
		}
		results = append(results, CopyPrompt(prompt))
	}

	s.sortPrompts(results, query.OrderBy, query.Descending)

	totalCount := len(results)

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

func (s *MemoryStore) GetHistory(ctx context.Context, id string, limit int) ([]PromptVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history, ok := s.history[id]
	if !ok {
		return nil, nil
	}

	result := make([]PromptVersion, len(history))
	for i, v := range history {
		result[len(history)-1-i] = v
	}

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

func (s *MemoryStore) GetVersion(ctx context.Context, id string, version int) (*Prompt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	versions, ok := s.versions[id]
	if !ok {
		return nil, nil
	}

	for _, p := range versions {
		if p.Version == version {
			return CopyPrompt(p), nil
		}
	}

	return nil, nil
}

func (s *MemoryStore) matchesQuery(prompt *Prompt, query ListQuery) bool {
	if query.Status != PromptStatusUnspecified && prompt.Status != query.Status {
		return false
	}

	if query.Status == PromptStatusUnspecified && prompt.Status == PromptStatusArchived {
		return false
	}

	if query.Search != "" {
		search := strings.ToLower(query.Search)
		if !strings.Contains(strings.ToLower(prompt.Name), search) &&
			!strings.Contains(strings.ToLower(prompt.Description), search) &&
			!strings.Contains(strings.ToLower(prompt.Slug), search) {
			return false
		}
	}

	if len(query.Tags) > 0 {
		tagSet := make(map[string]bool)
		for _, t := range prompt.Tags {
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

func (s *MemoryStore) sortPrompts(prompts []*Prompt, orderBy string, descending bool) {
	sort.Slice(prompts, func(i, j int) bool {
		var less bool
		switch orderBy {
		case "name":
			less = prompts[i].Name < prompts[j].Name
		case "updated_at":
			less = prompts[i].UpdatedAt.Before(prompts[j].UpdatedAt)
		default:
			less = prompts[i].CreatedAt.Before(prompts[j].CreatedAt)
		}
		if descending {
			return !less
		}
		return less
	})
}

// PostgresStore implements Store using PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Create(ctx context.Context, prompt *Prompt) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO prompts (id, name, slug, description, status, created_by, created_at, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, prompt.ID, prompt.Name, prompt.Slug, prompt.Description,
		StatusToString(prompt.Status), prompt.CreatedBy, prompt.CreatedAt,
		prompt.UpdatedBy, prompt.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert prompt: %w", err)
	}

	versionID, err := s.insertVersion(ctx, tx, prompt.ID, prompt.Version, "", prompt.UpdatedBy)
	if err != nil {
		return err
	}

	if err := s.insertVersionDetails(ctx, tx, versionID, prompt); err != nil {
		return err
	}

	if err := s.insertTags(ctx, tx, prompt.ID, prompt.Tags); err != nil {
		return err
	}

	if err := s.insertMetadata(ctx, tx, prompt.ID, prompt.Metadata); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) Get(ctx context.Context, id string) (*Prompt, error) {
	return s.getPrompt(ctx, id, 0)
}

func (s *PostgresStore) GetBySlug(ctx context.Context, slug string, version int) (*Prompt, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM prompts WHERE slug = $1 AND deleted_at IS NULL
	`, slug).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find prompt by slug: %w", err)
	}

	return s.getPrompt(ctx, id, version)
}

func (s *PostgresStore) GetVersion(ctx context.Context, id string, version int) (*Prompt, error) {
	return s.getPrompt(ctx, id, version)
}

func (s *PostgresStore) getPrompt(ctx context.Context, id string, version int) (*Prompt, error) {
	var prompt Prompt
	var statusStr string
	var deletedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, description, status, created_by, created_at, updated_by, updated_at, deleted_at
		FROM prompts WHERE id = $1
	`, id).Scan(&prompt.ID, &prompt.Name, &prompt.Slug, &prompt.Description,
		&statusStr, &prompt.CreatedBy, &prompt.CreatedAt, &prompt.UpdatedBy, &prompt.UpdatedAt, &deletedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt: %w", err)
	}

	if deletedAt.Valid {
		return nil, nil
	}

	prompt.Status = StringToStatus(statusStr)

	var versionID string
	var versionQuery string
	var args []interface{}
	if version > 0 {
		versionQuery = `
			SELECT id, version FROM prompt_versions
			WHERE prompt_id = $1 AND version = $2
		`
		args = []interface{}{id, version}
	} else {
		versionQuery = `
			SELECT id, version FROM prompt_versions
			WHERE prompt_id = $1
			ORDER BY version DESC LIMIT 1
		`
		args = []interface{}{id}
	}

	err = s.db.QueryRowContext(ctx, versionQuery, args...).Scan(&versionID, &prompt.Version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	prompt.Messages, err = s.getMessages(ctx, versionID)
	if err != nil {
		return nil, err
	}

	prompt.Variables, err = s.getVariables(ctx, versionID)
	if err != nil {
		return nil, err
	}

	prompt.DefaultConfig, err = s.getGenerationConfig(ctx, versionID)
	if err != nil {
		return nil, err
	}

	prompt.Tags, err = s.getTags(ctx, id)
	if err != nil {
		return nil, err
	}

	prompt.Metadata, err = s.getMetadata(ctx, id)
	if err != nil {
		return nil, err
	}

	return &prompt, nil
}

func (s *PostgresStore) Update(ctx context.Context, prompt *Prompt) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE prompts SET description = $2, updated_by = $3, updated_at = $4
		WHERE id = $1
	`, prompt.ID, prompt.Description, prompt.UpdatedBy, prompt.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update prompt: %w", err)
	}

	versionID, err := s.insertVersion(ctx, tx, prompt.ID, prompt.Version, "", prompt.UpdatedBy)
	if err != nil {
		return err
	}

	if err := s.insertVersionDetails(ctx, tx, versionID, prompt); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM prompt_tags WHERE prompt_id = $1", prompt.ID)
	if err != nil {
		return fmt.Errorf("failed to delete tags: %w", err)
	}
	if err := s.insertTags(ctx, tx, prompt.ID, prompt.Tags); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM prompt_metadata WHERE prompt_id = $1", prompt.ID)
	if err != nil {
		return fmt.Errorf("failed to delete metadata: %w", err)
	}
	if err := s.insertMetadata(ctx, tx, prompt.ID, prompt.Metadata); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE prompts SET deleted_at = NOW() WHERE id = $1
	`, id)
	return err
}

func (s *PostgresStore) List(ctx context.Context, query ListQuery) ([]*Prompt, int, error) {
	baseQuery := `FROM prompts p WHERE p.deleted_at IS NULL`
	args := make([]interface{}, 0)
	argNum := 1

	if query.Search != "" {
		baseQuery += fmt.Sprintf(" AND (p.name ILIKE $%d OR p.description ILIKE $%d)", argNum, argNum)
		args = append(args, "%"+query.Search+"%")
		argNum++
	}

	if query.Status != PromptStatusUnspecified {
		baseQuery += fmt.Sprintf(" AND p.status = $%d", argNum)
		args = append(args, StatusToString(query.Status))
		argNum++
	}

	if len(query.Tags) > 0 {
		baseQuery += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM prompt_tags pt WHERE pt.prompt_id = p.id AND pt.tag = ANY($%d))", argNum)
		args = append(args, query.Tags)
		argNum++
	}

	var total int
	countQuery := "SELECT COUNT(*) " + baseQuery
	err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count prompts: %w", err)
	}

	orderBy := "p.created_at"
	if query.OrderBy != "" {
		orderBy = "p." + query.OrderBy
	}
	orderDir := "ASC"
	if query.Descending {
		orderDir = "DESC"
	}

	limit := 100
	if query.Limit > 0 && query.Limit < 100 {
		limit = query.Limit
	}

	selectQuery := fmt.Sprintf(`
		SELECT p.id, p.name, p.slug, p.description, p.status, p.created_by, p.created_at, p.updated_by, p.updated_at
		%s ORDER BY %s %s LIMIT %d OFFSET %d
	`, baseQuery, orderBy, orderDir, limit, query.Offset)

	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query prompts: %w", err)
	}
	defer rows.Close()

	prompts := make([]*Prompt, 0)
	for rows.Next() {
		var p Prompt
		var statusStr string
		err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Description, &statusStr,
			&p.CreatedBy, &p.CreatedAt, &p.UpdatedBy, &p.UpdatedAt)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan prompt: %w", err)
		}
		p.Status = StringToStatus(statusStr)

		err = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(version), 1) FROM prompt_versions WHERE prompt_id = $1
		`, p.ID).Scan(&p.Version)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get version: %w", err)
		}

		prompts = append(prompts, &p)
	}

	return prompts, total, rows.Err()
}

func (s *PostgresStore) GetHistory(ctx context.Context, id string, limit int) ([]PromptVersion, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT version, change_description, updated_by, updated_at
		FROM prompt_versions
		WHERE prompt_id = $1
		ORDER BY version DESC
		LIMIT $2
	`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query history: %w", err)
	}
	defer rows.Close()

	versions := make([]PromptVersion, 0)
	for rows.Next() {
		var v PromptVersion
		var changeDesc sql.NullString
		err := rows.Scan(&v.Version, &changeDesc, &v.UpdatedBy, &v.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan version: %w", err)
		}
		v.ChangeDescription = changeDesc.String
		versions = append(versions, v)
	}

	return versions, rows.Err()
}

// Helper methods for PostgresStore

func (s *PostgresStore) insertVersion(ctx context.Context, tx *sql.Tx, promptID string, version int, changeDesc, updatedBy string) (string, error) {
	var versionID string
	err := tx.QueryRowContext(ctx, `
		INSERT INTO prompt_versions (prompt_id, version, change_description, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id
	`, promptID, version, changeDesc, updatedBy).Scan(&versionID)
	if err != nil {
		return "", fmt.Errorf("failed to insert version: %w", err)
	}
	return versionID, nil
}

func (s *PostgresStore) insertVersionDetails(ctx context.Context, tx *sql.Tx, versionID string, prompt *Prompt) error {
	for i, msg := range prompt.Messages {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO prompt_messages (prompt_version_id, role, content, position)
			VALUES ($1, $2, $3, $4)
		`, versionID, msg.Role, msg.Content, i)
		if err != nil {
			return fmt.Errorf("failed to insert message: %w", err)
		}
	}

	for _, v := range prompt.Variables {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO prompt_variables (prompt_version_id, name, description, var_type, required, default_value)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, versionID, v.Name, v.Description, v.Type, v.Required, v.DefaultValue)
		if err != nil {
			return fmt.Errorf("failed to insert variable: %w", err)
		}
	}

	stopSeq, _ := json.Marshal(prompt.DefaultConfig.Stop)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO prompt_generation_configs (prompt_version_id, temperature, max_tokens, top_p, stop_sequences, output_schema)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, versionID, prompt.DefaultConfig.Temperature, prompt.DefaultConfig.MaxTokens,
		prompt.DefaultConfig.TopP, string(stopSeq), prompt.DefaultConfig.OutputSchema)
	if err != nil {
		return fmt.Errorf("failed to insert config: %w", err)
	}

	return nil
}

func (s *PostgresStore) insertTags(ctx context.Context, tx *sql.Tx, promptID string, tags []string) error {
	for _, tag := range tags {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO prompt_tags (prompt_id, tag) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, promptID, tag)
		if err != nil {
			return fmt.Errorf("failed to insert tag: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) insertMetadata(ctx context.Context, tx *sql.Tx, promptID string, metadata map[string]string) error {
	for k, v := range metadata {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO prompt_metadata (prompt_id, key, value) VALUES ($1, $2, $3)
			ON CONFLICT (prompt_id, key) DO UPDATE SET value = $3
		`, promptID, k, v)
		if err != nil {
			return fmt.Errorf("failed to insert metadata: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) getMessages(ctx context.Context, versionID string) ([]PromptMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT role, content FROM prompt_messages
		WHERE prompt_version_id = $1
		ORDER BY position
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	messages := make([]PromptMessage, 0)
	for rows.Next() {
		var m PromptMessage
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (s *PostgresStore) getVariables(ctx context.Context, versionID string) ([]PromptVariable, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, description, var_type, required, default_value
		FROM prompt_variables
		WHERE prompt_version_id = $1
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query variables: %w", err)
	}
	defer rows.Close()

	variables := make([]PromptVariable, 0)
	for rows.Next() {
		var v PromptVariable
		var desc, defVal sql.NullString
		if err := rows.Scan(&v.Name, &desc, &v.Type, &v.Required, &defVal); err != nil {
			return nil, fmt.Errorf("failed to scan variable: %w", err)
		}
		v.Description = desc.String
		v.DefaultValue = defVal.String
		variables = append(variables, v)
	}
	return variables, rows.Err()
}

func (s *PostgresStore) getGenerationConfig(ctx context.Context, versionID string) (GenerationConfig, error) {
	var config GenerationConfig
	var stopSeq sql.NullString
	var outputSchema sql.NullString
	var temp, topP sql.NullFloat64
	var maxTokens sql.NullInt32

	err := s.db.QueryRowContext(ctx, `
		SELECT temperature, max_tokens, top_p, stop_sequences, output_schema
		FROM prompt_generation_configs
		WHERE prompt_version_id = $1
	`, versionID).Scan(&temp, &maxTokens, &topP, &stopSeq, &outputSchema)
	if err == sql.ErrNoRows {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("failed to get config: %w", err)
	}

	config.Temperature = temp.Float64
	config.MaxTokens = int(maxTokens.Int32)
	config.TopP = topP.Float64
	config.OutputSchema = outputSchema.String

	if stopSeq.Valid {
		json.Unmarshal([]byte(stopSeq.String), &config.Stop)
	}

	return config, nil
}

func (s *PostgresStore) getTags(ctx context.Context, promptID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tag FROM prompt_tags WHERE prompt_id = $1
	`, promptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query tags: %w", err)
	}
	defer rows.Close()

	tags := make([]string, 0)
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("failed to scan tag: %w", err)
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (s *PostgresStore) getMetadata(ctx context.Context, promptID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, value FROM prompt_metadata WHERE prompt_id = $1
	`, promptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query metadata: %w", err)
	}
	defer rows.Close()

	metadata := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("failed to scan metadata: %w", err)
		}
		metadata[k] = v
	}
	return metadata, rows.Err()
}

// Ensure implementations satisfy the interface
var (
	_ Store = (*MemoryStore)(nil)
	_ Store = (*PostgresStore)(nil)
)
