package prompt

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// newVersionedPrompt returns a v1 prompt suitable for versioning tests.
func newVersionedPrompt() *Prompt {
	now := time.Now()
	return &Prompt{
		ID:                "pmt_versioning",
		Name:              "Summarizer",
		Slug:              "summarizer",
		Version:           1,
		Description:       "v1 description",
		Messages:          []PromptMessage{{Role: "system", Content: "v1 content"}},
		Status:            PromptStatusActive,
		ChangeDescription: "Initial version",
		CreatedBy:         "tester",
		CreatedAt:         now,
		UpdatedBy:         "tester",
		UpdatedAt:         now,
	}
}

// TestMemoryStore_UpdateKeepsPreviousVersion proves that an update creates v2
// while v1 remains retrievable.
func TestMemoryStore_UpdateKeepsPreviousVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	p := newVersionedPrompt()
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated := CopyPrompt(p)
	updated.Description = "v2 description"
	updated.Messages = []PromptMessage{{Role: "system", Content: "v2 content"}}
	updated.ChangeDescription = "tightened the instructions"
	if err := store.Update(ctx, updated); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("Update() version = %d, want 2", updated.Version)
	}

	v1, err := store.GetVersion(ctx, p.ID, 1)
	if err != nil {
		t.Fatalf("GetVersion(1) error = %v", err)
	}
	if v1 == nil {
		t.Fatal("GetVersion(1) = nil, want the original version to still be retrievable")
	}
	if v1.Messages[0].Content != "v1 content" {
		t.Errorf("v1 content = %q, want %q", v1.Messages[0].Content, "v1 content")
	}

	v2, err := store.GetVersion(ctx, p.ID, 2)
	if err != nil {
		t.Fatalf("GetVersion(2) error = %v", err)
	}
	if v2 == nil {
		t.Fatal("GetVersion(2) = nil, want the new version")
	}
	if v2.Messages[0].Content != "v2 content" {
		t.Errorf("v2 content = %q, want %q", v2.Messages[0].Content, "v2 content")
	}

	current, err := store.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if current.Version != 2 {
		t.Errorf("current version = %d, want 2", current.Version)
	}
}

// TestMemoryStore_UpdateRecordsHistory proves that history lists every version.
func TestMemoryStore_UpdateRecordsHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	p := newVersionedPrompt()
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	updated := CopyPrompt(p)
	updated.ChangeDescription = "second pass"
	if err := store.Update(ctx, updated); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	history, err := store.GetHistory(ctx, p.ID, 10)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("GetHistory() returned %d versions, want 2 (v1 and v2)", len(history))
	}
	if history[0].Version != 2 {
		t.Errorf("history[0].Version = %d, want 2 (newest first)", history[0].Version)
	}
	if history[0].ChangeDescription != "second pass" {
		t.Errorf("history[0].ChangeDescription = %q, want %q", history[0].ChangeDescription, "second pass")
	}
	if history[1].Version != 1 {
		t.Errorf("history[1].Version = %d, want 1", history[1].Version)
	}
	if history[1].ChangeDescription != "Initial version" {
		t.Errorf("history[1].ChangeDescription = %q, want %q", history[1].ChangeDescription, "Initial version")
	}
}

// TestMemoryStore_GetBySlugVersion proves slug+version lookups resolve to the
// requested version rather than always the newest.
func TestMemoryStore_GetBySlugVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	p := newVersionedPrompt()
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	updated := CopyPrompt(p)
	updated.Messages = []PromptMessage{{Role: "system", Content: "v2 content"}}
	if err := store.Update(ctx, updated); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	tests := []struct {
		version int
		want    string
	}{
		{version: 0, want: "v2 content"},
		{version: 1, want: "v1 content"},
		{version: 2, want: "v2 content"},
	}
	for _, tc := range tests {
		got, err := store.GetBySlug(ctx, "summarizer", tc.version)
		if err != nil {
			t.Fatalf("GetBySlug(version=%d) error = %v", tc.version, err)
		}
		if got == nil {
			t.Fatalf("GetBySlug(version=%d) = nil", tc.version)
		}
		if got.Messages[0].Content != tc.want {
			t.Errorf("GetBySlug(version=%d) content = %q, want %q", tc.version, got.Messages[0].Content, tc.want)
		}
	}

	missing, err := store.GetBySlug(ctx, "summarizer", 9)
	if err != nil {
		t.Fatalf("GetBySlug(version=9) error = %v", err)
	}
	if missing != nil {
		t.Errorf("GetBySlug(version=9) = %+v, want nil", missing)
	}
}

// newVersionedHandler wires a handler over a real MemoryStore holding a
// prompt at v1 and v2.
func newVersionedHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	ctx := context.Background()
	store := NewMemoryStore()
	h := NewHandler(store, newTestLogger())

	created, err := h.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Summarizer",
		Slug: "summarizer",
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "v1 content"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt() error = %v", err)
	}

	if _, err := h.UpdatePrompt(ctx, &promptv1.UpdatePromptRequest{
		Id: created.Prompt.Id,
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "v2 content"},
		},
		ChangeDescription: "second pass",
	}); err != nil {
		t.Fatalf("UpdatePrompt() error = %v", err)
	}

	return h, created.Prompt.Id
}

func TestHandler_GetPromptBySlug(t *testing.T) {
	h, id := newVersionedHandler(t)

	resp, err := h.GetPrompt(context.Background(), &promptv1.GetPromptRequest{Id: "summarizer"})
	if err != nil {
		t.Fatalf("GetPrompt(slug) error = %v", err)
	}
	if resp.Prompt == nil {
		t.Fatal("GetPrompt(slug) returned a nil prompt (this is what printed `null` in the CLI)")
	}
	if resp.Prompt.Id != id {
		t.Errorf("GetPrompt(slug).Id = %q, want %q", resp.Prompt.Id, id)
	}
	if resp.Prompt.Version != 2 {
		t.Errorf("GetPrompt(slug).Version = %d, want 2 (latest)", resp.Prompt.Version)
	}
}

func TestHandler_GetPromptByReference(t *testing.T) {
	h, id := newVersionedHandler(t)
	ctx := context.Background()

	tests := []struct {
		name        string
		req         *promptv1.GetPromptRequest
		wantVersion int32
		wantContent string
	}{
		{
			name:        "slug and version",
			req:         &promptv1.GetPromptRequest{Reference: "summarizer:v1"},
			wantVersion: 1,
			wantContent: "v1 content",
		},
		{
			name:        "id with slug reference",
			req:         &promptv1.GetPromptRequest{Id: id, Reference: "summarizer:v1"},
			wantVersion: 1,
			wantContent: "v1 content",
		},
		{
			name:        "id with bare version reference",
			req:         &promptv1.GetPromptRequest{Id: id, Reference: "v1"},
			wantVersion: 1,
			wantContent: "v1 content",
		},
		{
			name:        "latest",
			req:         &promptv1.GetPromptRequest{Reference: "summarizer:latest"},
			wantVersion: 2,
			wantContent: "v2 content",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := h.GetPrompt(ctx, tc.req)
			if err != nil {
				t.Fatalf("GetPrompt() error = %v", err)
			}
			if resp.Prompt.Version != tc.wantVersion {
				t.Errorf("Version = %d, want %d", resp.Prompt.Version, tc.wantVersion)
			}
			if resp.Prompt.Messages[0].Content != tc.wantContent {
				t.Errorf("content = %q, want %q", resp.Prompt.Messages[0].Content, tc.wantContent)
			}
		})
	}
}

func TestHandler_GetPromptNotFoundIsNotFound(t *testing.T) {
	h, id := newVersionedHandler(t)
	ctx := context.Background()

	tests := []struct {
		name string
		req  *promptv1.GetPromptRequest
	}{
		{name: "unknown slug", req: &promptv1.GetPromptRequest{Id: "nope"}},
		{name: "unknown version", req: &promptv1.GetPromptRequest{Id: id, Reference: "v9"}},
		{name: "unknown reference", req: &promptv1.GetPromptRequest{Reference: "nope:v1"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := h.GetPrompt(ctx, tc.req)
			if err == nil {
				t.Fatalf("GetPrompt() = %+v, want a NotFound error instead of a nil prompt", resp)
			}
			if status.Code(err) != codes.NotFound {
				t.Errorf("status code = %s, want NotFound", status.Code(err))
			}
		})
	}
}

func TestHandler_GetPromptHistoryBySlug(t *testing.T) {
	h, id := newVersionedHandler(t)

	resp, err := h.GetPromptHistory(context.Background(), &promptv1.GetPromptHistoryRequest{Id: "summarizer", Limit: 10})
	if err != nil {
		t.Fatalf("GetPromptHistory(slug) error = %v", err)
	}
	if resp.PromptId != id {
		t.Errorf("PromptId = %q, want %q", resp.PromptId, id)
	}
	if len(resp.Versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(resp.Versions))
	}
	if resp.Versions[0].Version != 2 || resp.Versions[1].Version != 1 {
		t.Errorf("versions = [%d %d], want [2 1]", resp.Versions[0].Version, resp.Versions[1].Version)
	}
	if resp.Versions[0].ChangeDescription != "second pass" {
		t.Errorf("v2 change description = %q, want %q", resp.Versions[0].ChangeDescription, "second pass")
	}
}

func TestHandler_GetPromptHistoryUnknownIsNotFound(t *testing.T) {
	h, _ := newVersionedHandler(t)

	_, err := h.GetPromptHistory(context.Background(), &promptv1.GetPromptHistoryRequest{Id: "nope"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("status code = %s, want NotFound", status.Code(err))
	}
}

func TestHandler_CompareVersionsAcrossVersions(t *testing.T) {
	h, id := newVersionedHandler(t)
	ctx := context.Background()

	for _, target := range []string{id, "summarizer"} {
		resp, err := h.CompareVersions(ctx, &promptv1.CompareVersionsRequest{
			PromptId: target,
			VersionA: 1,
			VersionB: 2,
		})
		if err != nil {
			t.Fatalf("CompareVersions(%q) error = %v", target, err)
		}
		if len(resp.Diffs) == 0 {
			t.Fatalf("CompareVersions(%q) found no diffs between v1 and v2", target)
		}
		found := false
		for _, d := range resp.Diffs {
			if d.OldValue == "v1 content" && d.NewValue == "v2 content" {
				found = true
			}
		}
		if !found {
			t.Errorf("CompareVersions(%q) diffs = %+v, want the message content change", target, resp.Diffs)
		}
	}
}

func TestHandler_CompareVersionsMissingVersionIsNotFound(t *testing.T) {
	h, id := newVersionedHandler(t)

	_, err := h.CompareVersions(context.Background(), &promptv1.CompareVersionsRequest{
		PromptId: id,
		VersionA: 1,
		VersionB: 9,
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("status code = %s, want NotFound", status.Code(err))
	}
}

func TestHandler_UpdatePromptBySlug(t *testing.T) {
	h, id := newVersionedHandler(t)

	resp, err := h.UpdatePrompt(context.Background(), &promptv1.UpdatePromptRequest{
		Id:                "summarizer",
		Description:       "third",
		ChangeDescription: "third pass",
	})
	if err != nil {
		t.Fatalf("UpdatePrompt(slug) error = %v", err)
	}
	if resp.Prompt.Id != id {
		t.Errorf("Id = %q, want %q", resp.Prompt.Id, id)
	}
	if resp.Prompt.Version != 3 {
		t.Errorf("Version = %d, want 3", resp.Prompt.Version)
	}
	if resp.PreviousVersion != 2 {
		t.Errorf("PreviousVersion = %d, want 2", resp.PreviousVersion)
	}
}

func TestHandler_UpdatePromptUnknownIsNotFound(t *testing.T) {
	h, _ := newVersionedHandler(t)

	_, err := h.UpdatePrompt(context.Background(), &promptv1.UpdatePromptRequest{Id: "nope"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("status code = %s, want NotFound", status.Code(err))
	}
}

func TestVersionToken(t *testing.T) {
	tests := []struct {
		in          string
		wantVersion int
		wantOK      bool
	}{
		{in: "v2", wantVersion: 2, wantOK: true},
		{in: "3", wantVersion: 3, wantOK: true},
		{in: "latest", wantVersion: 0, wantOK: true},
		{in: "summarizer", wantOK: false},
		{in: "summarizer:v2", wantOK: false},
		{in: "v", wantOK: false},
	}
	for _, tc := range tests {
		got, ok := versionToken(tc.in)
		if ok != tc.wantOK || (ok && got != tc.wantVersion) {
			t.Errorf("versionToken(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.wantVersion, tc.wantOK)
		}
	}
}
