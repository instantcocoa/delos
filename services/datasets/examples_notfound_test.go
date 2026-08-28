package datasets

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
)

func newExamplesHandler(t *testing.T) (*Handler, string) {
	t.Helper()

	store := NewMemoryStore()
	svc := NewDatasetsService(store)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	h := NewHandler(logger, svc)

	created, err := h.CreateDataset(context.Background(), &datasetsv1.CreateDatasetRequest{Name: "smoke-tests"})
	if err != nil {
		t.Fatalf("CreateDataset() error = %v", err)
	}
	return h, created.Dataset.Id
}

// TestGetExamples_UnknownDatasetIsNotFound covers the case where the CLI used
// to report "Found 0 examples" (and print null) for a dataset that never
// existed.
func TestGetExamples_UnknownDatasetIsNotFound(t *testing.T) {
	h, _ := newExamplesHandler(t)

	_, err := h.GetExamples(context.Background(), &datasetsv1.GetExamplesRequest{DatasetId: "does-not-exist"})
	if err == nil {
		t.Fatal("GetExamples() on an unknown dataset returned no error, want NotFound")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("status code = %s, want NotFound", status.Code(err))
	}
}

// TestGetExamples_EmptyDatasetIsOK ensures an existing but empty dataset is
// still a successful, empty response rather than an error.
func TestGetExamples_EmptyDatasetIsOK(t *testing.T) {
	h, id := newExamplesHandler(t)

	resp, err := h.GetExamples(context.Background(), &datasetsv1.GetExamplesRequest{DatasetId: id})
	if err != nil {
		t.Fatalf("GetExamples() error = %v", err)
	}
	if len(resp.Examples) != 0 || resp.TotalCount != 0 {
		t.Errorf("got %d examples (total %d), want 0", len(resp.Examples), resp.TotalCount)
	}
}

func TestAddExamples_UnknownDatasetIsNotFound(t *testing.T) {
	h, _ := newExamplesHandler(t)

	_, err := h.AddExamples(context.Background(), &datasetsv1.AddExamplesRequest{
		DatasetId: "does-not-exist",
		Examples:  []*datasetsv1.ExampleInput{{}},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("status code = %s, want NotFound", status.Code(err))
	}
}

func TestExportExamples_UnknownDatasetIsNotFound(t *testing.T) {
	h, _ := newExamplesHandler(t)

	_, err := h.ExportExamples(context.Background(), &datasetsv1.ExportExamplesRequest{
		DatasetId: "does-not-exist",
		Format:    datasetsv1.DataFormat_DATA_FORMAT_JSON,
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("status code = %s, want NotFound", status.Code(err))
	}
}

// TestAddThenExportRoundTrip exercises the path the new `datasets add` and
// `datasets export` CLI commands drive.
func TestAddThenExportRoundTrip(t *testing.T) {
	h, id := newExamplesHandler(t)
	ctx := context.Background()

	input := mapToStruct(map[string]interface{}{"input": "hello"})
	expected := mapToStruct(map[string]interface{}{"output": "Hello!"})

	added, err := h.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: id,
		Examples: []*datasetsv1.ExampleInput{{
			Input:          input,
			ExpectedOutput: expected,
			Source:         datasetsv1.ExampleSource_EXAMPLE_SOURCE_MANUAL,
		}},
	})
	if err != nil {
		t.Fatalf("AddExamples() error = %v", err)
	}
	if added.AddedCount != 1 {
		t.Fatalf("AddedCount = %d, want 1", added.AddedCount)
	}

	got, err := h.GetExamples(ctx, &datasetsv1.GetExamplesRequest{DatasetId: id})
	if err != nil {
		t.Fatalf("GetExamples() error = %v", err)
	}
	if len(got.Examples) != 1 {
		t.Fatalf("got %d examples, want 1", len(got.Examples))
	}

	exported, err := h.ExportExamples(ctx, &datasetsv1.ExportExamplesRequest{
		DatasetId: id,
		Format:    datasetsv1.DataFormat_DATA_FORMAT_JSONL,
	})
	if err != nil {
		t.Fatalf("ExportExamples() error = %v", err)
	}
	if exported.ExportedCount != 1 {
		t.Errorf("ExportedCount = %d, want 1", exported.ExportedCount)
	}
	if len(exported.Data) == 0 {
		t.Error("ExportExamples() returned no data")
	}
}

// TestImportIgnoresExportEnvelopeFields imports rows in exactly the shape
// ExportExamples emits them. The auto-detect path used to replace (rather than
// merge) the input map, so sibling keys like "metadata" leaked into the input
// depending on random map iteration order.
func TestImportIgnoresExportEnvelopeFields(t *testing.T) {
	h, id := newExamplesHandler(t)
	ctx := context.Background()

	data := []byte(`{"expected_output":{"output":"Hello!"},"id":"exported-1","input":{"input":"hello"},"metadata":null}` + "\n" +
		`{"expected_output":{"output":"Goodbye!"},"id":"exported-2","input":{"input":"bye"},"metadata":null}` + "\n")

	imported, err := h.ImportExamples(ctx, &datasetsv1.ImportExamplesRequest{
		DatasetId: id,
		Source: &datasetsv1.DataSource{
			Source: &datasetsv1.DataSource_Inline{
				Inline: &datasetsv1.InlineSource{Data: data, Format: datasetsv1.DataFormat_DATA_FORMAT_JSONL},
			},
		},
		Format: datasetsv1.DataFormat_DATA_FORMAT_JSONL,
	})
	if err != nil {
		t.Fatalf("ImportExamples() error = %v", err)
	}
	if imported.ImportedCount != 2 {
		t.Fatalf("ImportedCount = %d, want 2", imported.ImportedCount)
	}

	got, err := h.GetExamples(ctx, &datasetsv1.GetExamplesRequest{DatasetId: id})
	if err != nil {
		t.Fatalf("GetExamples() error = %v", err)
	}
	for _, ex := range got.Examples {
		in := ex.Input.AsMap()
		if len(in) != 1 {
			t.Errorf("imported input = %v, want exactly the original input field", in)
		}
		if _, ok := in["metadata"]; ok {
			t.Errorf("imported input = %v, want no envelope \"metadata\" field", in)
		}
		if _, ok := in["id"]; ok {
			t.Errorf("imported input = %v, want no envelope \"id\" field", in)
		}
		if ex.Id == "exported-1" || ex.Id == "exported-2" {
			t.Errorf("imported example kept the exported id %q, want a freshly assigned id", ex.Id)
		}
	}
}
