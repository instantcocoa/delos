package prompt

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/instantcocoa/delos/pkg/config"
)

func TestMemoryStore_Create(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompt := &Prompt{
		ID:          "test-id",
		Name:        "Test Prompt",
		Slug:        "test-prompt",
		Version:     1,
		Description: "A test prompt",
		Messages: []PromptMessage{
			{Role: "system", Content: "You are a helpful assistant."},
		},
		Tags:     []string{"test", "example"},
		Metadata: map[string]string{"key": "value"},
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(ctx, "test-id")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if got.Name != "Test Prompt" {
		t.Errorf("Get().Name = %v, want %v", got.Name, "Test Prompt")
	}
	if got.Slug != "test-prompt" {
		t.Errorf("Get().Slug = %v, want %v", got.Slug, "test-prompt")
	}
}

func TestMemoryStore_GetBySlug(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompt := &Prompt{
		ID:      "test-id",
		Name:    "Test Prompt",
		Slug:    "test-prompt",
		Version: 1,
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.GetBySlug(ctx, "test-prompt", 0)
	if err != nil {
		t.Fatalf("GetBySlug() error = %v", err)
	}

	if got == nil {
		t.Fatal("GetBySlug() returned nil")
	}
	if got.ID != "test-id" {
		t.Errorf("GetBySlug().ID = %v, want %v", got.ID, "test-id")
	}
}

func TestMemoryStore_Update(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompt := &Prompt{
		ID:          "test-id",
		Name:        "Test Prompt",
		Slug:        "test-prompt",
		Version:     1,
		Description: "Original description",
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	prompt.Description = "Updated description"

	if err := store.Update(ctx, prompt); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := store.Get(ctx, "test-id")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.Version != 2 {
		t.Errorf("Get().Version = %v, want %v", got.Version, 2)
	}
	if got.Description != "Updated description" {
		t.Errorf("Get().Description = %v, want %v", got.Description, "Updated description")
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompt := &Prompt{
		ID:     "test-id",
		Name:   "Test Prompt",
		Slug:   "test-prompt",
		Status: PromptStatusActive,
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := store.Delete(ctx, "test-id"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	got, err := store.Get(ctx, "test-id")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got == nil {
		t.Fatal("Get() returned nil, expected archived prompt")
	}
	if got.Status != PromptStatusArchived {
		t.Errorf("Get().Status = %v, want %v (archived)", got.Status, PromptStatusArchived)
	}
}

func TestMemoryStore_List(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		prompt := &Prompt{
			ID:   "test-id-" + string(rune('0'+i)),
			Name: "Test Prompt " + string(rune('0'+i)),
			Slug: "test-prompt-" + string(rune('0'+i)),
		}
		if err := store.Create(ctx, prompt); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	prompts, total, err := store.List(ctx, ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if total != 5 {
		t.Errorf("List() total = %v, want %v", total, 5)
	}
	if len(prompts) != 5 {
		t.Errorf("List() returned %v prompts, want %v", len(prompts), 5)
	}
}

func TestMemoryStore_List_WithSearch(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompts := []*Prompt{
		{ID: "1", Name: "Summarizer", Slug: "summarizer", Description: "Summarizes text"},
		{ID: "2", Name: "Translator", Slug: "translator", Description: "Translates text"},
		{ID: "3", Name: "Writer", Slug: "writer", Description: "Writes content"},
	}

	for _, p := range prompts {
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	results, total, err := store.List(ctx, ListQuery{
		Search: "text",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if total != 2 {
		t.Errorf("List() total = %v, want %v", total, 2)
	}
	if len(results) != 2 {
		t.Errorf("List() returned %v prompts, want %v", len(results), 2)
	}
}

func TestMemoryStore_GetHistory(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompt := &Prompt{
		ID:      "test-id",
		Name:    "Test Prompt",
		Slug:    "test-prompt",
		Version: 1,
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.GetHistory(ctx, "test-id", 10)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}

	if len(history) < 1 {
		t.Errorf("GetHistory() returned %v versions, want at least 1", len(history))
	}
	if history[0].Version != 1 {
		t.Errorf("GetHistory()[0].Version = %v, want %v", history[0].Version, 1)
	}
}

func TestMemoryStore_GetVersion(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompt := &Prompt{
		ID:          "test-id",
		Name:        "Test Prompt",
		Slug:        "test-prompt",
		Version:     1,
		Description: "Version 1",
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	prompt.Version = 2
	prompt.Description = "Version 2"
	if err := store.Update(ctx, prompt); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	v1, err := store.GetVersion(ctx, "test-id", 1)
	if err != nil {
		t.Fatalf("GetVersion() error = %v", err)
	}

	if v1 == nil {
		t.Fatal("GetVersion() returned nil")
	}
	if v1.Version != 1 {
		t.Errorf("GetVersion().Version = %v, want %v", v1.Version, 1)
	}
}

func TestMemoryStore_Get_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	got, err := store.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != nil {
		t.Errorf("Get() = %v, want nil for nonexistent", got)
	}
}

func TestMemoryStore_Create_DuplicateSlug(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompt1 := &Prompt{ID: "id-1", Slug: "same-slug"}
	prompt2 := &Prompt{ID: "id-2", Slug: "same-slug"}

	if err := store.Create(ctx, prompt1); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err := store.Create(ctx, prompt2)
	if err == nil {
		t.Error("expected error for duplicate slug")
	}
}

func TestMemoryStore_Update_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompt := &Prompt{ID: "nonexistent", Name: "Test"}

	err := store.Update(ctx, prompt)
	if err == nil {
		t.Error("expected error for updating nonexistent prompt")
	}
}

func TestMemoryStore_Delete_NotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.Delete(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for deleting nonexistent prompt")
	}
}

func TestMemoryStore_List_WithPagination(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 1; i <= 10; i++ {
		prompt := &Prompt{
			ID:   fmt.Sprintf("id-%d", i),
			Name: fmt.Sprintf("Prompt %d", i),
			Slug: fmt.Sprintf("prompt-%d", i),
		}
		if err := store.Create(ctx, prompt); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	prompts, total, err := store.List(ctx, ListQuery{
		Limit:  5,
		Offset: 3,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 10 {
		t.Errorf("List() total = %d, want 10", total)
	}
	if len(prompts) != 5 {
		t.Errorf("List() returned %d prompts, want 5", len(prompts))
	}
}

func TestMemoryStore_List_WithStatus(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompts := []*Prompt{
		{ID: "1", Slug: "active-1", Status: PromptStatusActive},
		{ID: "2", Slug: "draft-1", Status: PromptStatusDraft},
		{ID: "3", Slug: "active-2", Status: PromptStatusActive},
	}
	for _, p := range prompts {
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	results, total, err := store.List(ctx, ListQuery{
		Status: PromptStatusActive,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 2 {
		t.Errorf("List() total = %d, want 2", total)
	}
	if len(results) != 2 {
		t.Errorf("List() returned %d prompts, want 2", len(results))
	}
}

func TestMemoryStore_List_WithTags(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	prompts := []*Prompt{
		{ID: "1", Slug: "p1", Tags: []string{"go", "backend"}},
		{ID: "2", Slug: "p2", Tags: []string{"python", "ml"}},
		{ID: "3", Slug: "p3", Tags: []string{"go", "ml"}},
	}
	for _, p := range prompts {
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	results, total, err := store.List(ctx, ListQuery{
		Tags:  []string{"go"},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 2 {
		t.Errorf("List() total = %d, want 2", total)
	}

	results, total, err = store.List(ctx, ListQuery{
		Tags:  []string{"go", "ml"},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 1 {
		t.Errorf("List() total = %d, want 1 (only p3 has both tags)", total)
	}
	if len(results) != 1 || results[0].ID != "3" {
		t.Error("List() should return only p3")
	}
}

func TestNewStore_MemoryBackend(t *testing.T) {
	store, err := NewStore(StoreOptions{
		Backend: config.StorageMemory,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store == nil {
		t.Fatal("NewStore() returned nil")
	}

	ctx := context.Background()
	prompt := &Prompt{ID: "test-id", Name: "Test", Slug: "test"}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(ctx, "test-id")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got == nil || got.Name != "Test" {
		t.Error("Memory store not working correctly")
	}
}

func TestNewStore_DefaultBackend(t *testing.T) {
	store, err := NewStore(StoreOptions{Backend: ""})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store == nil {
		t.Fatal("NewStore() returned nil for default backend")
	}
}

func TestNewStore_PostgresBackendWithoutDB(t *testing.T) {
	_, err := NewStore(StoreOptions{
		Backend: config.StoragePostgres,
		DB:      nil,
	})

	if err == nil {
		t.Error("NewStore() expected error when postgres backend has no DB connection")
	}
}

func TestCopyPrompt_Nil(t *testing.T) {
	result := CopyPrompt(nil)
	if result != nil {
		t.Errorf("CopyPrompt(nil) = %v, want nil", result)
	}
}

func TestCopyPrompt_DeepCopy(t *testing.T) {
	original := &Prompt{
		ID:       "test",
		Name:     "Test",
		Messages: []PromptMessage{{Role: "user", Content: "Hello"}},
		Variables: []PromptVariable{{Name: "name", Type: "string"}},
		Tags:     []string{"tag1", "tag2"},
		DefaultConfig: GenerationConfig{
			Stop: []string{"stop1"},
		},
		Metadata: map[string]string{"key": "value"},
	}

	copied := CopyPrompt(original)

	original.Name = "Modified"
	original.Messages[0].Content = "Modified"
	original.Tags[0] = "modified"
	original.Metadata["key"] = "modified"

	if copied.Name != "Test" {
		t.Errorf("copied.Name = %q, want Test", copied.Name)
	}
	if copied.Messages[0].Content != "Hello" {
		t.Errorf("copied.Messages[0].Content = %q, want Hello", copied.Messages[0].Content)
	}
	if copied.Tags[0] != "tag1" {
		t.Errorf("copied.Tags[0] = %q, want tag1", copied.Tags[0])
	}
	if copied.Metadata["key"] != "value" {
		t.Errorf("copied.Metadata[key] = %q, want value", copied.Metadata["key"])
	}
}

// PostgreSQL integration tests

func getTestDSN() string {
	host := os.Getenv("POSTGRES_HOST")
	if host == "" {
		host = "localhost"
	}
	return "host=" + host + " port=5432 user=delos password=delos dbname=delos_test sslmode=disable"
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("postgres", getTestDSN())
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Skipf("PostgreSQL not available: %v", err)
	}

	createTables(t, db)

	t.Cleanup(func() {
		cleanupTables(t, db)
		db.Close()
	})

	return db
}

func createTables(t *testing.T, db *sql.DB) {
	t.Helper()

	tables := []string{
		`CREATE TABLE IF NOT EXISTS prompts (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			slug VARCHAR(255) UNIQUE NOT NULL,
			description TEXT,
			status VARCHAR(50) DEFAULT 'draft',
			created_by VARCHAR(255),
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_by VARCHAR(255),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS prompt_versions (
			id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text,
			prompt_id VARCHAR(255) REFERENCES prompts(id),
			version INT NOT NULL,
			change_description TEXT,
			updated_by VARCHAR(255),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(prompt_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS prompt_messages (
			id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text,
			prompt_version_id VARCHAR(255) REFERENCES prompt_versions(id),
			role VARCHAR(50) NOT NULL,
			content TEXT NOT NULL,
			position INT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS prompt_variables (
			id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text,
			prompt_version_id VARCHAR(255) REFERENCES prompt_versions(id),
			name VARCHAR(255) NOT NULL,
			description TEXT,
			var_type VARCHAR(50),
			required BOOLEAN DEFAULT false,
			default_value TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS prompt_generation_configs (
			id VARCHAR(255) PRIMARY KEY DEFAULT gen_random_uuid()::text,
			prompt_version_id VARCHAR(255) REFERENCES prompt_versions(id) UNIQUE,
			temperature FLOAT,
			max_tokens INT,
			top_p FLOAT,
			stop_sequences TEXT,
			output_schema TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS prompt_tags (
			prompt_id VARCHAR(255) REFERENCES prompts(id),
			tag VARCHAR(255) NOT NULL,
			PRIMARY KEY(prompt_id, tag)
		)`,
		`CREATE TABLE IF NOT EXISTS prompt_metadata (
			prompt_id VARCHAR(255) REFERENCES prompts(id),
			key VARCHAR(255) NOT NULL,
			value TEXT,
			PRIMARY KEY(prompt_id, key)
		)`,
	}

	for _, query := range tables {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("failed to create table: %v", err)
		}
	}
}

func cleanupTables(t *testing.T, db *sql.DB) {
	t.Helper()

	tables := []string{
		"prompt_metadata",
		"prompt_tags",
		"prompt_generation_configs",
		"prompt_variables",
		"prompt_messages",
		"prompt_versions",
		"prompts",
	}

	for _, table := range tables {
		db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE")
	}
}

func TestPostgresStore_Create_Integration(t *testing.T) {
	db := setupTestDB(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	prompt := &Prompt{
		ID:          "test-id",
		Name:        "Test Prompt",
		Slug:        "test-prompt",
		Version:     1,
		Description: "A test prompt",
		Messages: []PromptMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello!"},
		},
		Variables: []PromptVariable{
			{Name: "name", Type: "string", Required: true},
		},
		Tags:     []string{"test", "example"},
		Metadata: map[string]string{"key": "value"},
		DefaultConfig: GenerationConfig{
			Temperature: 0.7,
			MaxTokens:   100,
			TopP:        0.9,
			Stop:        []string{"END"},
		},
		CreatedBy: "tester",
		CreatedAt: time.Now(),
		UpdatedBy: "tester",
		UpdatedAt: time.Now(),
	}

	err := store.Create(ctx, prompt)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.Get(ctx, "test-id")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if got.Name != "Test Prompt" {
		t.Errorf("Name = %q, want %q", got.Name, "Test Prompt")
	}
	if got.Slug != "test-prompt" {
		t.Errorf("Slug = %q, want %q", got.Slug, "test-prompt")
	}
	if len(got.Messages) != 2 {
		t.Errorf("Messages count = %d, want 2", len(got.Messages))
	}
	if len(got.Variables) != 1 {
		t.Errorf("Variables count = %d, want 1", len(got.Variables))
	}
	if len(got.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2", len(got.Tags))
	}
	if got.Metadata["key"] != "value" {
		t.Errorf("Metadata[key] = %q, want %q", got.Metadata["key"], "value")
	}
}

func TestPostgresStore_Get_NotFound_Integration(t *testing.T) {
	db := setupTestDB(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	got, err := store.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != nil {
		t.Errorf("Get() = %v, want nil", got)
	}
}

func TestPostgresStore_GetBySlug_Integration(t *testing.T) {
	db := setupTestDB(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	prompt := &Prompt{
		ID:      "test-id",
		Name:    "Test",
		Slug:    "test-slug",
		Version: 1,
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.GetBySlug(ctx, "test-slug", 0)
	if err != nil {
		t.Fatalf("GetBySlug() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetBySlug() returned nil")
	}
	if got.ID != "test-id" {
		t.Errorf("ID = %q, want %q", got.ID, "test-id")
	}
}

func TestPostgresStore_Update_Integration(t *testing.T) {
	db := setupTestDB(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	prompt := &Prompt{
		ID:          "test-id",
		Name:        "Test",
		Slug:        "test",
		Version:     1,
		Description: "Original",
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	prompt.Description = "Updated"
	prompt.Version = 2
	prompt.Tags = []string{"new-tag"}
	prompt.Metadata = map[string]string{"new": "value"}
	prompt.UpdatedAt = time.Now()

	if err := store.Update(ctx, prompt); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := store.Get(ctx, "test-id")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Description != "Updated" {
		t.Errorf("Description = %q, want %q", got.Description, "Updated")
	}
	if got.Version != 2 {
		t.Errorf("Version = %d, want 2", got.Version)
	}
}

func TestPostgresStore_Delete_Integration(t *testing.T) {
	db := setupTestDB(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	prompt := &Prompt{
		ID:   "test-id",
		Name: "Test",
		Slug: "test",
	}

	if err := store.Create(ctx, prompt); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := store.Delete(ctx, "test-id"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	got, err := store.Get(ctx, "test-id")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != nil {
		t.Errorf("Get() = %v, want nil (soft deleted)", got)
	}
}

func TestNewStore_PostgresBackend_Integration(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewStore(StoreOptions{
		Backend: config.StoragePostgres,
		DB:      db,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store == nil {
		t.Fatal("NewStore() returned nil")
	}
}
