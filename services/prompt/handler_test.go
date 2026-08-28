package prompt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// mockStore implements Store for testing
type mockStore struct {
	createErr        error
	getResult        *Prompt
	getErr           error
	getBySlugResult  *Prompt
	getBySlugErr     error
	updateErr        error
	deleteErr        error
	listResult       []*Prompt
	listTotal        int
	listErr          error
	historyResult    []PromptVersion
	historyErr       error
	getVersionResult *Prompt
	getVersionErr    error
}

func (m *mockStore) Create(ctx context.Context, prompt *Prompt) error {
	return m.createErr
}

func (m *mockStore) Get(ctx context.Context, id string) (*Prompt, error) {
	return m.getResult, m.getErr
}

func (m *mockStore) GetBySlug(ctx context.Context, slug string, version int) (*Prompt, error) {
	return m.getBySlugResult, m.getBySlugErr
}

func (m *mockStore) Update(ctx context.Context, prompt *Prompt) error {
	return m.updateErr
}

func (m *mockStore) Delete(ctx context.Context, id string) error {
	return m.deleteErr
}

func (m *mockStore) List(ctx context.Context, query ListQuery) ([]*Prompt, int, error) {
	return m.listResult, m.listTotal, m.listErr
}

func (m *mockStore) GetHistory(ctx context.Context, id string, limit int) ([]PromptVersion, error) {
	return m.historyResult, m.historyErr
}

func (m *mockStore) GetVersion(ctx context.Context, id string, version int) (*Prompt, error) {
	return m.getVersionResult, m.getVersionErr
}

var _ Store = (*mockStore)(nil)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func samplePrompt() *Prompt {
	return &Prompt{
		ID:          "pmt_123",
		Name:        "Test Prompt",
		Slug:        "test-prompt",
		Version:     1,
		Description: "A test prompt",
		Messages: []PromptMessage{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hello {{name}}"},
		},
		Variables: []PromptVariable{
			{Name: "name", Description: "User name", Type: "string", Required: true, DefaultValue: "World"},
		},
		DefaultConfig: GenerationConfig{
			Temperature:  0.7,
			MaxTokens:    1000,
			TopP:         0.9,
			Stop:         []string{"END"},
			OutputSchema: `{"type":"object"}`,
		},
		Tags:      []string{"test", "example"},
		Metadata:  map[string]string{"key": "value"},
		Status:    PromptStatusActive,
		CreatedBy: "user1",
		CreatedAt: time.Now(),
		UpdatedBy: "user1",
		UpdatedAt: time.Now(),
	}
}

func TestNewHandler(t *testing.T) {
	store := &mockStore{}
	handler := NewHandler(store, newTestLogger())

	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
	if handler.store != store {
		t.Error("store not set correctly")
	}
}

func TestCreatePrompt_Success(t *testing.T) {
	store := &mockStore{}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.CreatePromptRequest{
		Name:        "Test Prompt",
		Slug:        "test-prompt",
		Description: "A test prompt",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are helpful"},
		},
		Variables: []*promptv1.PromptVariable{
			{Name: "name", Type: "string", Required: true},
		},
		DefaultConfig: &promptv1.GenerationConfig{
			Temperature: 0.7,
			MaxTokens:   1000,
		},
		Tags:     []string{"test"},
		Metadata: map[string]string{"key": "value"},
	}

	resp, err := handler.CreatePrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Prompt == nil {
		t.Fatal("expected prompt in response")
	}
	if resp.Prompt.Name != "Test Prompt" {
		t.Errorf("expected name 'Test Prompt', got '%s'", resp.Prompt.Name)
	}
	if resp.Prompt.Slug != "test-prompt" {
		t.Errorf("expected slug 'test-prompt', got '%s'", resp.Prompt.Slug)
	}
}

func TestCreatePrompt_MissingName(t *testing.T) {
	store := &mockStore{}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.CreatePromptRequest{Name: ""}

	_, err := handler.CreatePrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestCreatePrompt_InvalidSlug(t *testing.T) {
	store := &mockStore{}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.CreatePromptRequest{
		Name: "Test",
		Slug: "INVALID_SLUG!!!",
	}

	_, err := handler.CreatePrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for invalid slug")
	}
}

func TestCreatePrompt_RepositoryError(t *testing.T) {
	store := &mockStore{createErr: errors.New("database error")}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.CreatePromptRequest{
		Name: "Test Prompt",
		Slug: "test-prompt",
	}

	_, err := handler.CreatePrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error from store")
	}
}

func TestGetPrompt_ByID_Success(t *testing.T) {
	prompt := samplePrompt()
	store := &mockStore{getResult: prompt}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.GetPromptRequest{Id: "pmt_123"}

	resp, err := handler.GetPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Prompt == nil {
		t.Fatal("expected prompt in response")
	}
	if resp.Prompt.Id != "pmt_123" {
		t.Errorf("expected id 'pmt_123', got '%s'", resp.Prompt.Id)
	}
}

func TestGetPrompt_ByReference_Success(t *testing.T) {
	prompt := samplePrompt()
	store := &mockStore{getBySlugResult: prompt}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.GetPromptRequest{Reference: "test-prompt:v1"}

	resp, err := handler.GetPrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Prompt == nil {
		t.Fatal("expected prompt in response")
	}
}

func TestGetPrompt_NoIdOrReference(t *testing.T) {
	store := &mockStore{}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.GetPromptRequest{Id: "", Reference: ""}

	_, err := handler.GetPrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing id and reference")
	}
}

func TestUpdatePrompt_Success(t *testing.T) {
	existing := samplePrompt()
	store := &mockStore{getResult: existing}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.UpdatePromptRequest{
		Id:          "pmt_123",
		Description: "Updated description",
		Messages: []*promptv1.PromptMessage{
			{Role: "user", Content: "Updated content"},
		},
		ChangeDescription: "Updated the prompt",
	}

	resp, err := handler.UpdatePrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Prompt == nil {
		t.Fatal("expected prompt in response")
	}
	if resp.PreviousVersion != 1 {
		t.Errorf("expected previous version 1, got %d", resp.PreviousVersion)
	}
}

func TestUpdatePrompt_NotFound(t *testing.T) {
	store := &mockStore{getResult: nil}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.UpdatePromptRequest{Id: "nonexistent"}

	_, err := handler.UpdatePrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for not found")
	}
}

func TestListPrompts_Success(t *testing.T) {
	prompts := []*Prompt{samplePrompt(), samplePrompt()}
	prompts[1].ID = "pmt_456"
	prompts[1].Name = "Second Prompt"

	store := &mockStore{listResult: prompts, listTotal: 2}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.ListPromptsRequest{
		Search: "test",
		Tags:   []string{"example"},
		Limit:  10,
		Offset: 0,
	}

	resp, err := handler.ListPrompts(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Prompts) != 2 {
		t.Errorf("expected 2 prompts, got %d", len(resp.Prompts))
	}
	if resp.TotalCount != 2 {
		t.Errorf("expected total count 2, got %d", resp.TotalCount)
	}
}

func TestDeletePrompt_Success(t *testing.T) {
	store := &mockStore{}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.DeletePromptRequest{Id: "pmt_123"}

	resp, err := handler.DeletePrompt(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestDeletePrompt_RepositoryError(t *testing.T) {
	store := &mockStore{deleteErr: errors.New("database error")}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.DeletePromptRequest{Id: "pmt_123"}

	_, err := handler.DeletePrompt(context.Background(), req)
	if err == nil {
		t.Fatal("expected error from store")
	}
}

func TestGetPromptHistory_Success(t *testing.T) {
	versions := []PromptVersion{
		{Version: 2, ChangeDescription: "Updated", UpdatedBy: "user1", UpdatedAt: time.Now()},
		{Version: 1, ChangeDescription: "Initial", UpdatedBy: "user1", UpdatedAt: time.Now().Add(-time.Hour)},
	}
	store := &mockStore{historyResult: versions}
	handler := NewHandler(store, newTestLogger())

	req := &promptv1.GetPromptHistoryRequest{Id: "pmt_123", Limit: 10}

	resp, err := handler.GetPromptHistory(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.PromptId != "pmt_123" {
		t.Errorf("expected prompt_id 'pmt_123', got '%s'", resp.PromptId)
	}
	if len(resp.Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(resp.Versions))
	}
	if resp.Versions[0].Version != 2 {
		t.Errorf("expected version 2, got %d", resp.Versions[0].Version)
	}
}

func TestHealth_Success(t *testing.T) {
	store := &mockStore{}
	handler := NewHandler(store, newTestLogger())

	resp, err := handler.Health(context.Background(), &promptv1.HealthRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("expected status 'healthy', got '%s'", resp.Status)
	}
	if resp.Version != "0.1.0" {
		t.Errorf("expected version '0.1.0', got '%s'", resp.Version)
	}
}

func TestProtoToMessages_Success(t *testing.T) {
	msgs := []*promptv1.PromptMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello {{name}}"},
	}

	result := protoToMessages(msgs)

	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("expected role 'system', got '%s'", result[0].Role)
	}
}

func TestProtoToVariables_Success(t *testing.T) {
	vars := []*promptv1.PromptVariable{
		{Name: "name", Description: "User name", Type: "string", Required: true, DefaultValue: "World"},
	}

	result := protoToVariables(vars)

	if len(result) != 1 {
		t.Errorf("expected 1 variable, got %d", len(result))
	}
	if result[0].Name != "name" {
		t.Errorf("expected name 'name', got '%s'", result[0].Name)
	}
}

func TestProtoToConfig_Success(t *testing.T) {
	cfg := &promptv1.GenerationConfig{
		Temperature:  0.7,
		MaxTokens:    1000,
		TopP:         0.9,
		Stop:         []string{"END", "STOP"},
		OutputSchema: `{"type":"object"}`,
	}

	result := protoToConfig(cfg)

	if result.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %f", result.Temperature)
	}
	if result.MaxTokens != 1000 {
		t.Errorf("expected max_tokens 1000, got %d", result.MaxTokens)
	}
}

func TestProtoToConfig_Nil(t *testing.T) {
	result := protoToConfig(nil)

	if result.Temperature != 0 {
		t.Errorf("expected temperature 0, got %f", result.Temperature)
	}
}

func TestProtoToStatus_AllStatuses(t *testing.T) {
	tests := []struct {
		input    promptv1.PromptStatus
		expected PromptStatus
	}{
		{promptv1.PromptStatus_PROMPT_STATUS_UNSPECIFIED, PromptStatusUnspecified},
		{promptv1.PromptStatus_PROMPT_STATUS_DRAFT, PromptStatusDraft},
		{promptv1.PromptStatus_PROMPT_STATUS_ACTIVE, PromptStatusActive},
		{promptv1.PromptStatus_PROMPT_STATUS_DEPRECATED, PromptStatusDeprecated},
		{promptv1.PromptStatus_PROMPT_STATUS_ARCHIVED, PromptStatusArchived},
	}

	for _, tc := range tests {
		t.Run(tc.input.String(), func(t *testing.T) {
			result := protoToStatus(tc.input)
			if result != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}

func TestToProto_Success(t *testing.T) {
	prompt := samplePrompt()

	result := toProto(prompt)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Id != prompt.ID {
		t.Errorf("expected id '%s', got '%s'", prompt.ID, result.Id)
	}
	if result.Name != prompt.Name {
		t.Errorf("expected name '%s', got '%s'", prompt.Name, result.Name)
	}
}

func TestToProto_Nil(t *testing.T) {
	result := toProto(nil)
	if result != nil {
		t.Error("expected nil result for nil input")
	}
}

// ---- error mapping ----

// TestDeletePrompt_NotFound: deleting a prompt that does not exist used to
// return the raw store error (code Unknown) or, on Postgres, succeed silently.
func TestDeletePrompt_NotFound(t *testing.T) {
	store := &mockStore{deleteErr: fmt.Errorf("%w: %s", ErrNotFound, "pmt_missing")}
	handler := NewHandler(store, newTestLogger())

	_, err := handler.DeletePrompt(context.Background(), &promptv1.DeletePromptRequest{Id: "pmt_missing"})
	if err == nil {
		t.Fatal("DeletePrompt() on a nonexistent prompt returned no error, want NOT_FOUND")
	}
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("DeletePrompt() code = %s, want NotFound", got)
	}
	if !strings.Contains(status.Convert(err).Message(), "pmt_missing") {
		t.Errorf("DeletePrompt() message = %q, want it to name the id", status.Convert(err).Message())
	}
}

func TestDeletePrompt_EmptyID(t *testing.T) {
	handler := NewHandler(&mockStore{}, newTestLogger())

	_, err := handler.DeletePrompt(context.Background(), &promptv1.DeletePromptRequest{})
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("DeletePrompt(\"\") code = %s, want InvalidArgument", got)
	}
}

// TestCreatePrompt_DuplicateSlug: a slug clash is ALREADY_EXISTS naming the
// slug. It used to be INTERNAL carrying the Postgres driver string and the
// constraint name.
func TestCreatePrompt_DuplicateSlug(t *testing.T) {
	store := &mockStore{createErr: &SlugConflictError{Slug: "taken-slug"}}
	handler := NewHandler(store, newTestLogger())

	_, err := handler.CreatePrompt(context.Background(), &promptv1.CreatePromptRequest{
		Name: "Second", Slug: "taken-slug",
	})
	if err == nil {
		t.Fatal("CreatePrompt() with a duplicate slug returned no error")
	}
	if got := status.Code(err); got != codes.AlreadyExists {
		t.Errorf("CreatePrompt() code = %s, want AlreadyExists", got)
	}
	msg := status.Convert(err).Message()
	if !strings.Contains(msg, "taken-slug") {
		t.Errorf("CreatePrompt() message = %q, want it to name the conflicting slug", msg)
	}
}

// TestCreatePrompt_StoreErrorDoesNotLeak: an unexpected store error may embed
// driver text and constraint names. Those belong in the log, not the response.
func TestCreatePrompt_StoreErrorDoesNotLeak(t *testing.T) {
	store := &mockStore{createErr: errors.New(
		`pq: duplicate key value violates unique constraint "prompts_pkey" (23505)`)}
	handler := NewHandler(store, newTestLogger())

	_, err := handler.CreatePrompt(context.Background(), &promptv1.CreatePromptRequest{Name: "X"})
	if got := status.Code(err); got != codes.Internal {
		t.Fatalf("CreatePrompt() code = %s, want Internal", got)
	}
	msg := status.Convert(err).Message()
	for _, leak := range []string{"pq:", "constraint", "prompts_pkey", "23505"} {
		if strings.Contains(msg, leak) {
			t.Errorf("CreatePrompt() message %q leaks %q", msg, leak)
		}
	}
}

func TestUpdatePrompt_StoreNotFound(t *testing.T) {
	store := &mockStore{
		getResult: samplePrompt(),
		updateErr: fmt.Errorf("%w: %s", ErrNotFound, "pmt_123"),
	}
	handler := NewHandler(store, newTestLogger())

	_, err := handler.UpdatePrompt(context.Background(), &promptv1.UpdatePromptRequest{
		Id: "pmt_123", Description: "new",
	})
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("UpdatePrompt() code = %s, want NotFound", got)
	}
}

// TestUpdatePrompt_PartialUpdate pins that an omitted field is left alone: a
// config-only update must not drop the messages, and a description-only update
// must not drop the tags.
func TestUpdatePrompt_PartialUpdate(t *testing.T) {
	store := NewMemoryStore()
	handler := NewHandler(store, newTestLogger())
	ctx := context.Background()

	created, err := handler.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Partial", Slug: "partial-update",
		Description: "original description",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hello"},
		},
		Tags: []string{"keep-me"},
	})
	if err != nil {
		t.Fatalf("CreatePrompt() error = %v", err)
	}
	id := created.Prompt.Id

	// Config-only update: messages and tags survive.
	updated, err := handler.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id:            id,
		DefaultConfig: &promptv1.GenerationConfig{Temperature: 0.5, MaxTokens: 256},
	})
	if err != nil {
		t.Fatalf("UpdatePrompt(config only) error = %v", err)
	}
	if len(updated.Prompt.Messages) != 2 {
		t.Errorf("config-only update dropped messages: got %d, want 2", len(updated.Prompt.Messages))
	}
	if len(updated.Prompt.Tags) != 1 {
		t.Errorf("config-only update dropped tags: got %v", updated.Prompt.Tags)
	}
	if updated.Prompt.Description != "original description" {
		t.Errorf("config-only update changed the description: got %q", updated.Prompt.Description)
	}

	// Description-only update: messages, tags and config survive.
	updated, err = handler.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id: id, Description: "new description",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt(description only) error = %v", err)
	}
	if updated.Prompt.Description != "new description" {
		t.Errorf("description not applied: got %q", updated.Prompt.Description)
	}
	if len(updated.Prompt.Messages) != 2 {
		t.Errorf("description-only update dropped messages: got %d, want 2", len(updated.Prompt.Messages))
	}
	if updated.Prompt.Name != "Partial" {
		t.Errorf("description-only update clobbered the name: got %q", updated.Prompt.Name)
	}
	if updated.Prompt.DefaultConfig.GetMaxTokens() != 256 {
		t.Errorf("description-only update dropped the config: got %+v", updated.Prompt.DefaultConfig)
	}
}

// TestGetPrompt_AfterDelete pins the soft-delete semantic at the API boundary:
// a deleted prompt is NOT_FOUND, matching what List and the Postgres backend
// already did.
func TestGetPrompt_AfterDelete(t *testing.T) {
	store := NewMemoryStore()
	handler := NewHandler(store, newTestLogger())
	ctx := context.Background()

	created, err := handler.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Doomed", Slug: "doomed-prompt",
	})
	if err != nil {
		t.Fatalf("CreatePrompt() error = %v", err)
	}
	id := created.Prompt.Id

	if _, err := handler.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: id}); err != nil {
		t.Fatalf("DeletePrompt() error = %v", err)
	}

	if _, err := handler.GetPrompt(ctx, &promptv1.GetPromptRequest{Id: id}); status.Code(err) != codes.NotFound {
		t.Errorf("GetPrompt() after delete code = %s, want NotFound", status.Code(err))
	}
	if _, err := handler.GetPrompt(ctx, &promptv1.GetPromptRequest{Id: "doomed-prompt"}); status.Code(err) != codes.NotFound {
		t.Errorf("GetPrompt(by slug) after delete code = %s, want NotFound", status.Code(err))
	}
	if _, err := handler.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: id}); status.Code(err) != codes.NotFound {
		t.Errorf("second DeletePrompt() code = %s, want NotFound", status.Code(err))
	}
}
