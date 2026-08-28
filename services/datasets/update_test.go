package datasets

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
)

func newUpdateHandler(t *testing.T) *Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewHandler(logger, NewDatasetsService(NewMemoryStore()))
}

// TestUpdateDataset_PartialUpdate pins that an omitted field keeps its stored
// value. UpdateDataset used to copy the whole input over the record, so a
// description-only update wrote an empty name and empty tags.
func TestUpdateDataset_PartialUpdate(t *testing.T) {
	h := newUpdateHandler(t)
	ctx := context.Background()

	created, err := h.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:        "Original Name",
		Description: "original description",
		Tags:        []string{"a", "b"},
		Metadata:    map[string]string{"owner": "team"},
	})
	if err != nil {
		t.Fatalf("CreateDataset() error = %v", err)
	}
	id := created.Dataset.Id

	updated, err := h.UpdateDataset(ctx, &datasetsv1.UpdateDatasetRequest{
		Id:          id,
		Description: "updated description",
	})
	if err != nil {
		t.Fatalf("UpdateDataset() error = %v", err)
	}
	if updated.Dataset.Description != "updated description" {
		t.Errorf("description = %q, want %q", updated.Dataset.Description, "updated description")
	}
	if updated.Dataset.Name != "Original Name" {
		t.Errorf("a description-only update clobbered the name: got %q", updated.Dataset.Name)
	}
	if len(updated.Dataset.Tags) != 2 {
		t.Errorf("a description-only update clobbered the tags: got %v", updated.Dataset.Tags)
	}
	if updated.Dataset.Metadata["owner"] != "team" {
		t.Errorf("a description-only update clobbered the metadata: got %v", updated.Dataset.Metadata)
	}

	// The change must survive a re-read, not just be reflected in the response.
	reread, err := h.GetDataset(ctx, &datasetsv1.GetDatasetRequest{Id: id})
	if err != nil {
		t.Fatalf("GetDataset() error = %v", err)
	}
	if reread.Dataset.Name != "Original Name" || reread.Dataset.Description != "updated description" {
		t.Errorf("stored dataset = name %q / description %q, want %q / %q",
			reread.Dataset.Name, reread.Dataset.Description, "Original Name", "updated description")
	}

	// A name-only update leaves the description alone.
	renamed, err := h.UpdateDataset(ctx, &datasetsv1.UpdateDatasetRequest{Id: id, Name: "New Name"})
	if err != nil {
		t.Fatalf("UpdateDataset(name) error = %v", err)
	}
	if renamed.Dataset.Name != "New Name" {
		t.Errorf("name = %q, want %q", renamed.Dataset.Name, "New Name")
	}
	if renamed.Dataset.Description != "updated description" {
		t.Errorf("a name-only update clobbered the description: got %q", renamed.Dataset.Description)
	}
}

// TestUpdateDataset_NotFound: updating a dataset that does not exist is
// NOT_FOUND, not INTERNAL.
func TestUpdateDataset_NotFound(t *testing.T) {
	h := newUpdateHandler(t)

	_, err := h.UpdateDataset(context.Background(), &datasetsv1.UpdateDatasetRequest{
		Id: "does-not-exist", Description: "x",
	})
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("UpdateDataset() code = %s, want NotFound", got)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "does-not-exist") {
		t.Errorf("UpdateDataset() message = %q, want it to name the id", msg)
	}
}

// TestDeleteDataset_NotFound: deleting a dataset that does not exist is
// NOT_FOUND, not INTERNAL.
func TestDeleteDataset_NotFound(t *testing.T) {
	h := newUpdateHandler(t)

	_, err := h.DeleteDataset(context.Background(), &datasetsv1.DeleteDatasetRequest{Id: "does-not-exist"})
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("DeleteDataset() code = %s, want NotFound", got)
	}
}
