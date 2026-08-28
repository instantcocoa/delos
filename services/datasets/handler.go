package datasets

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
)

// Handler implements the DatasetsService gRPC interface.
type Handler struct {
	datasetsv1.UnimplementedDatasetsServiceServer
	logger  *slog.Logger
	service *DatasetsService
}

// NewHandler creates a new datasets service handler.
func NewHandler(logger *slog.Logger, svc *DatasetsService) *Handler {
	return &Handler{
		logger:  logger.With("component", "handler"),
		service: svc,
	}
}

// Register registers the handler with a gRPC server.
func (h *Handler) Register(s *grpc.Server) {
	datasetsv1.RegisterDatasetsServiceServer(s, h)
}

// CreateDataset creates a new dataset.
func (h *Handler) CreateDataset(ctx context.Context, req *datasetsv1.CreateDatasetRequest) (*datasetsv1.CreateDatasetResponse, error) {
	h.logger.InfoContext(ctx, "creating dataset", "name", req.Name)

	input := CreateDatasetInput{
		Name:        req.Name,
		Description: req.Description,
		PromptID:    req.PromptId,
		Schema:      schemaFromProto(req.Schema),
		Tags:        req.Tags,
		Metadata:    req.Metadata,
	}

	dataset, err := h.service.CreateDataset(ctx, input)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to create dataset", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create dataset: %v", err)
	}

	return &datasetsv1.CreateDatasetResponse{
		Dataset: datasetToProto(dataset),
	}, nil
}

// GetDataset retrieves a dataset by ID.
func (h *Handler) GetDataset(ctx context.Context, req *datasetsv1.GetDatasetRequest) (*datasetsv1.GetDatasetResponse, error) {
	h.logger.InfoContext(ctx, "getting dataset", "id", req.Id)

	dataset, err := h.service.GetDataset(ctx, req.Id)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get dataset", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to get dataset: %v", err)
	}
	if dataset == nil {
		return nil, status.Errorf(codes.NotFound, "dataset not found: %s", req.Id)
	}

	return &datasetsv1.GetDatasetResponse{
		Dataset: datasetToProto(dataset),
	}, nil
}

// UpdateDataset updates dataset metadata.
func (h *Handler) UpdateDataset(ctx context.Context, req *datasetsv1.UpdateDatasetRequest) (*datasetsv1.UpdateDatasetResponse, error) {
	h.logger.InfoContext(ctx, "updating dataset", "id", req.Id)

	input := UpdateDatasetInput{
		ID:          req.Id,
		Name:        req.Name,
		Description: req.Description,
		Tags:        req.Tags,
		Metadata:    req.Metadata,
	}

	dataset, err := h.service.UpdateDataset(ctx, input)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to update dataset", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to update dataset: %v", err)
	}

	return &datasetsv1.UpdateDatasetResponse{
		Dataset: datasetToProto(dataset),
	}, nil
}

// ListDatasets returns all datasets.
func (h *Handler) ListDatasets(ctx context.Context, req *datasetsv1.ListDatasetsRequest) (*datasetsv1.ListDatasetsResponse, error) {
	h.logger.InfoContext(ctx, "listing datasets")

	query := ListDatasetsQuery{
		PromptID: req.PromptId,
		Tags:     req.Tags,
		Search:   req.Search,
		Limit:    int(req.Limit),
		Offset:   int(req.Offset),
	}

	datasets, total, err := h.service.ListDatasets(ctx, query)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to list datasets", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list datasets: %v", err)
	}

	protoDatasets := make([]*datasetsv1.Dataset, len(datasets))
	for i, d := range datasets {
		protoDatasets[i] = datasetToProto(d)
	}

	return &datasetsv1.ListDatasetsResponse{
		Datasets:   protoDatasets,
		TotalCount: int32(total),
	}, nil
}

// DeleteDataset deletes a dataset.
func (h *Handler) DeleteDataset(ctx context.Context, req *datasetsv1.DeleteDatasetRequest) (*datasetsv1.DeleteDatasetResponse, error) {
	h.logger.InfoContext(ctx, "deleting dataset", "id", req.Id)

	if err := h.service.DeleteDataset(ctx, req.Id); err != nil {
		h.logger.ErrorContext(ctx, "failed to delete dataset", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to delete dataset: %v", err)
	}

	return &datasetsv1.DeleteDatasetResponse{Success: true}, nil
}

// AddExamples adds examples to a dataset.
func (h *Handler) AddExamples(ctx context.Context, req *datasetsv1.AddExamplesRequest) (*datasetsv1.AddExamplesResponse, error) {
	h.logger.InfoContext(ctx, "adding examples", "dataset_id", req.DatasetId, "count", len(req.Examples))

	exampleInputs := make([]ExampleInput, len(req.Examples))
	for i, ex := range req.Examples {
		exampleInputs[i] = ExampleInput{
			Input:          structToMap(ex.Input),
			ExpectedOutput: structToMap(ex.ExpectedOutput),
			Metadata:       ex.Metadata,
			Source:         exampleSourceFromProto(ex.Source),
		}
	}

	input := AddExamplesInput{
		DatasetID: req.DatasetId,
		Examples:  exampleInputs,
	}

	examples, err := h.service.AddExamples(ctx, input)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to add examples", "error", err)
		if strings.Contains(err.Error(), "dataset not found") {
			return nil, status.Errorf(codes.NotFound, "dataset not found: %s", req.DatasetId)
		}
		return nil, status.Errorf(codes.Internal, "failed to add examples: %v", err)
	}

	protoExamples := make([]*datasetsv1.Example, len(examples))
	for i, e := range examples {
		protoExamples[i] = exampleToProto(e)
	}

	return &datasetsv1.AddExamplesResponse{
		AddedCount: int32(len(examples)),
		Examples:   protoExamples,
	}, nil
}

// GetExamples retrieves examples from a dataset.
func (h *Handler) GetExamples(ctx context.Context, req *datasetsv1.GetExamplesRequest) (*datasetsv1.GetExamplesResponse, error) {
	h.logger.InfoContext(ctx, "getting examples", "dataset_id", req.DatasetId)

	// Verify the dataset exists so an unknown ID 404s instead of silently
	// reporting zero examples.
	dataset, err := h.service.GetDataset(ctx, req.DatasetId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get dataset: %v", err)
	}
	if dataset == nil {
		return nil, status.Errorf(codes.NotFound, "dataset not found: %s", req.DatasetId)
	}

	query := GetExamplesQuery{
		DatasetID: req.DatasetId,
		Limit:     int(req.Limit),
		Offset:    int(req.Offset),
		Shuffle:   req.Shuffle,
	}

	examples, total, err := h.service.GetExamples(ctx, query)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get examples", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to get examples: %v", err)
	}

	protoExamples := make([]*datasetsv1.Example, len(examples))
	for i, e := range examples {
		protoExamples[i] = exampleToProto(e)
	}

	return &datasetsv1.GetExamplesResponse{
		Examples:   protoExamples,
		TotalCount: int32(total),
	}, nil
}

// RemoveExamples removes examples from a dataset.
func (h *Handler) RemoveExamples(ctx context.Context, req *datasetsv1.RemoveExamplesRequest) (*datasetsv1.RemoveExamplesResponse, error) {
	h.logger.InfoContext(ctx, "removing examples", "dataset_id", req.DatasetId)

	removed, err := h.service.RemoveExamples(ctx, req.DatasetId, req.ExampleIds)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to remove examples", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to remove examples: %v", err)
	}

	return &datasetsv1.RemoveExamplesResponse{RemovedCount: int32(removed)}, nil
}

// GenerateExamples auto-generates examples using LLM.
func (h *Handler) GenerateExamples(ctx context.Context, req *datasetsv1.GenerateExamplesRequest) (*datasetsv1.GenerateExamplesResponse, error) {
	h.logger.InfoContext(ctx, "generating examples", "dataset_id", req.DatasetId, "count", req.Count)
	// TODO: Implement with LLM integration
	return &datasetsv1.GenerateExamplesResponse{
		Examples:       []*datasetsv1.Example{},
		GeneratedCount: 0,
	}, nil
}

// ImportExamples imports examples from external sources.
func (h *Handler) ImportExamples(ctx context.Context, req *datasetsv1.ImportExamplesRequest) (*datasetsv1.ImportExamplesResponse, error) {
	h.logger.InfoContext(ctx, "importing examples", "dataset_id", req.DatasetId, "format", req.Format)

	// Verify dataset exists
	dataset, err := h.service.GetDataset(ctx, req.DatasetId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get dataset: %v", err)
	}
	if dataset == nil {
		return nil, status.Errorf(codes.NotFound, "dataset not found: %s", req.DatasetId)
	}

	// Get source data
	var data []byte
	if req.Source != nil {
		switch src := req.Source.Source.(type) {
		case *datasetsv1.DataSource_Inline:
			if src.Inline != nil {
				data = src.Inline.Data
			}
		case *datasetsv1.DataSource_LocalFile:
			return nil, status.Errorf(codes.Unimplemented, "local file import not yet implemented")
		case *datasetsv1.DataSource_Url:
			return nil, status.Errorf(codes.Unimplemented, "URL import not yet implemented")
		case *datasetsv1.DataSource_S3:
			return nil, status.Errorf(codes.Unimplemented, "S3 import not yet implemented")
		case *datasetsv1.DataSource_Gcs:
			return nil, status.Errorf(codes.Unimplemented, "GCS import not yet implemented")
		default:
			return nil, status.Errorf(codes.InvalidArgument, "unsupported data source type")
		}
	}

	if len(data) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "no data provided for import")
	}

	// Parse data based on format
	var examples []ExampleInput
	var importErrors []*datasetsv1.ImportError

	switch req.Format {
	case datasetsv1.DataFormat_DATA_FORMAT_JSON:
		examples, importErrors, err = h.parseJSONImport(data, req.ColumnMappings, req.SkipInvalid)
	case datasetsv1.DataFormat_DATA_FORMAT_JSONL:
		examples, importErrors, err = h.parseJSONLImport(data, req.ColumnMappings, req.SkipInvalid)
	case datasetsv1.DataFormat_DATA_FORMAT_CSV:
		examples, importErrors, err = h.parseCSVImport(data, req.ColumnMappings, req.CsvOptions, req.SkipInvalid)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported format: %v", req.Format)
	}

	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse import data: %v", err)
	}

	// Apply max rows limit
	if req.MaxRows > 0 && len(examples) > int(req.MaxRows) {
		examples = examples[:req.MaxRows]
	}

	// Add examples to dataset
	addInput := AddExamplesInput{
		DatasetID: req.DatasetId,
		Examples:  examples,
	}

	addedExamples, err := h.service.AddExamples(ctx, addInput)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to add examples: %v", err)
	}

	return &datasetsv1.ImportExamplesResponse{
		ImportedCount: int32(len(addedExamples)),
		SkippedCount:  int32(len(importErrors)),
		ErrorCount:    int32(len(importErrors)),
		Errors:        importErrors,
	}, nil
}

// ExportExamples exports examples to various formats.
func (h *Handler) ExportExamples(ctx context.Context, req *datasetsv1.ExportExamplesRequest) (*datasetsv1.ExportExamplesResponse, error) {
	h.logger.InfoContext(ctx, "exporting examples", "dataset_id", req.DatasetId, "format", req.Format)

	// Verify dataset exists
	dataset, err := h.service.GetDataset(ctx, req.DatasetId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get dataset: %v", err)
	}
	if dataset == nil {
		return nil, status.Errorf(codes.NotFound, "dataset not found: %s", req.DatasetId)
	}

	// Get examples from dataset
	query := GetExamplesQuery{
		DatasetID: req.DatasetId,
		Limit:     int(req.Limit),
		Offset:    int(req.Offset),
	}
	if query.Limit == 0 {
		query.Limit = 10000 // Default limit
	}

	examples, _, err := h.service.GetExamples(ctx, query)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get examples: %v", err)
	}

	if len(examples) == 0 {
		return &datasetsv1.ExportExamplesResponse{
			Data:          []byte{},
			Format:        req.Format,
			ExportedCount: 0,
		}, nil
	}

	// Convert to export format
	var data []byte
	switch req.Format {
	case datasetsv1.DataFormat_DATA_FORMAT_JSON:
		data, err = h.exportAsJSON(examples)
	case datasetsv1.DataFormat_DATA_FORMAT_JSONL:
		data, err = h.exportAsJSONL(examples)
	case datasetsv1.DataFormat_DATA_FORMAT_CSV:
		data, err = h.exportAsCSV(examples, req.CsvOptions)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported format: %v", req.Format)
	}

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to export data: %v", err)
	}

	// If destination provided, write to it (not implemented yet)
	if req.Destination != nil {
		return nil, status.Errorf(codes.Unimplemented, "export to external destination not yet implemented")
	}

	return &datasetsv1.ExportExamplesResponse{
		Data:          data,
		Format:        req.Format,
		ExportedCount: int32(len(examples)),
	}, nil
}

// Import helpers

func (h *Handler) parseJSONImport(data []byte, mappings []*datasetsv1.ColumnMapping, skipInvalid bool) ([]ExampleInput, []*datasetsv1.ImportError, error) {
	var rawData []map[string]interface{}
	if err := json.Unmarshal(data, &rawData); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON array: %w", err)
	}

	return h.convertToExamples(rawData, mappings, skipInvalid)
}

func (h *Handler) parseJSONLImport(data []byte, mappings []*datasetsv1.ColumnMapping, skipInvalid bool) ([]ExampleInput, []*datasetsv1.ImportError, error) {
	var rawData []map[string]interface{}
	var importErrors []*datasetsv1.ImportError

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var row map[string]interface{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			if skipInvalid {
				importErrors = append(importErrors, &datasetsv1.ImportError{
					RowNumber:    int32(i + 1),
					ErrorMessage: fmt.Sprintf("invalid JSON: %v", err),
					RawData:      line,
				})
				continue
			}
			return nil, nil, fmt.Errorf("invalid JSON on line %d: %w", i+1, err)
		}
		rawData = append(rawData, row)
	}

	examples, moreErrors, err := h.convertToExamples(rawData, mappings, skipInvalid)
	importErrors = append(importErrors, moreErrors...)
	return examples, importErrors, err
}

func (h *Handler) parseCSVImport(data []byte, mappings []*datasetsv1.ColumnMapping, opts *datasetsv1.CSVOptions, skipInvalid bool) ([]ExampleInput, []*datasetsv1.ImportError, error) {
	reader := csv.NewReader(bytes.NewReader(data))

	// Apply CSV options
	if opts != nil {
		if opts.Delimiter != "" {
			runes := []rune(opts.Delimiter)
			if len(runes) > 0 {
				reader.Comma = runes[0]
			}
		}
	}

	// Read header
	header, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	// Read all records
	var rawData []map[string]interface{}
	var importErrors []*datasetsv1.ImportError
	rowNum := 1 // Header was row 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		rowNum++
		if err != nil {
			if skipInvalid {
				importErrors = append(importErrors, &datasetsv1.ImportError{
					RowNumber:    int32(rowNum),
					ErrorMessage: fmt.Sprintf("failed to read row: %v", err),
				})
				continue
			}
			return nil, nil, fmt.Errorf("failed to read CSV row %d: %w", rowNum, err)
		}

		row := make(map[string]interface{})
		for i, value := range record {
			if i < len(header) {
				row[header[i]] = value
			}
		}
		rawData = append(rawData, row)
	}

	examples, moreErrors, err := h.convertToExamples(rawData, mappings, skipInvalid)
	importErrors = append(importErrors, moreErrors...)
	return examples, importErrors, err
}

func (h *Handler) convertToExamples(rawData []map[string]interface{}, mappings []*datasetsv1.ColumnMapping, skipInvalid bool) ([]ExampleInput, []*datasetsv1.ImportError, error) {
	var examples []ExampleInput
	var importErrors []*datasetsv1.ImportError

	// Build mapping lookup
	inputMappings := make(map[string]string)
	outputMappings := make(map[string]string)
	for _, m := range mappings {
		if m.IsInput {
			inputMappings[m.SourceColumn] = m.TargetField
		} else {
			outputMappings[m.SourceColumn] = m.TargetField
		}
	}

	for i, row := range rawData {
		input := make(map[string]interface{})
		output := make(map[string]interface{})

		if len(mappings) > 0 {
			// Use explicit mappings
			for sourceCol, targetField := range inputMappings {
				if val, ok := row[sourceCol]; ok {
					input[targetField] = val
				}
			}
			for sourceCol, targetField := range outputMappings {
				if val, ok := row[sourceCol]; ok {
					output[targetField] = val
				}
			}
		} else {
			// Auto-detect: use "input_*" and "expected_*" prefixes, or "input"/"expected_output" fields
			for key, val := range row {
				// Skip the envelope fields ExportExamples emits, so an export
				// can be re-imported without them leaking into the input.
				if key == "id" || key == "dataset_id" || key == "metadata" || key == "created_at" {
					continue
				}
				if key == "input" {
					if m, ok := val.(map[string]interface{}); ok {
						// Merge rather than replace: map iteration order is
						// random, so replacing would drop sibling fields
						// non-deterministically.
						maps.Copy(input, m)
					} else {
						input["value"] = val
					}
				} else if key == "expected_output" || key == "expected" || key == "output" {
					if m, ok := val.(map[string]interface{}); ok {
						maps.Copy(output, m)
					} else {
						output["value"] = val
					}
				} else if strings.HasPrefix(key, "input_") {
					input[strings.TrimPrefix(key, "input_")] = val
				} else if strings.HasPrefix(key, "expected_") {
					output[strings.TrimPrefix(key, "expected_")] = val
				} else {
					// Default: put in input
					input[key] = val
				}
			}
		}

		if len(input) == 0 && len(output) == 0 {
			if skipInvalid {
				importErrors = append(importErrors, &datasetsv1.ImportError{
					RowNumber:    int32(i + 1),
					ErrorMessage: "no input or output fields found",
				})
				continue
			}
			return nil, nil, fmt.Errorf("row %d: no input or output fields found", i+1)
		}

		examples = append(examples, ExampleInput{
			Input:          input,
			ExpectedOutput: output,
			Source:         ExampleSourceImported,
		})
	}

	return examples, importErrors, nil
}

// Export helpers

func (h *Handler) exportAsJSON(examples []*Example) ([]byte, error) {
	var exportData []map[string]interface{}
	for _, ex := range examples {
		exportData = append(exportData, map[string]interface{}{
			"id":              ex.ID,
			"input":           ex.Input,
			"expected_output": ex.ExpectedOutput,
			"metadata":        ex.Metadata,
		})
	}
	return json.MarshalIndent(exportData, "", "  ")
}

func (h *Handler) exportAsJSONL(examples []*Example) ([]byte, error) {
	var buf bytes.Buffer
	for _, ex := range examples {
		row := map[string]interface{}{
			"id":              ex.ID,
			"input":           ex.Input,
			"expected_output": ex.ExpectedOutput,
			"metadata":        ex.Metadata,
		}
		line, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func (h *Handler) exportAsCSV(examples []*Example, opts *datasetsv1.CSVOptions) ([]byte, error) {
	if len(examples) == 0 {
		return []byte{}, nil
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	// Apply CSV options
	if opts != nil && opts.Delimiter != "" {
		runes := []rune(opts.Delimiter)
		if len(runes) > 0 {
			writer.Comma = runes[0]
		}
	}

	// Collect all unique input and output keys
	inputKeys := make(map[string]bool)
	outputKeys := make(map[string]bool)
	for _, ex := range examples {
		for k := range ex.Input {
			inputKeys[k] = true
		}
		for k := range ex.ExpectedOutput {
			outputKeys[k] = true
		}
	}

	// Build header
	var header []string
	header = append(header, "id")
	var inputKeyList, outputKeyList []string
	for k := range inputKeys {
		inputKeyList = append(inputKeyList, k)
		header = append(header, "input_"+k)
	}
	for k := range outputKeys {
		outputKeyList = append(outputKeyList, k)
		header = append(header, "expected_"+k)
	}

	if err := writer.Write(header); err != nil {
		return nil, err
	}

	// Write data rows
	for _, ex := range examples {
		var row []string
		row = append(row, ex.ID)
		for _, k := range inputKeyList {
			if val, ok := ex.Input[k]; ok {
				row = append(row, fmt.Sprintf("%v", val))
			} else {
				row = append(row, "")
			}
		}
		for _, k := range outputKeyList {
			if val, ok := ex.ExpectedOutput[k]; ok {
				row = append(row, fmt.Sprintf("%v", val))
			} else {
				row = append(row, "")
			}
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}

	writer.Flush()
	return buf.Bytes(), writer.Error()
}

// Health returns the service health status.
func (h *Handler) Health(ctx context.Context, req *datasetsv1.HealthRequest) (*datasetsv1.HealthResponse, error) {
	return &datasetsv1.HealthResponse{
		Status:  "healthy",
		Version: "0.1.0",
	}, nil
}

// Conversion helpers

func datasetToProto(d *Dataset) *datasetsv1.Dataset {
	return &datasetsv1.Dataset{
		Id:           d.ID,
		Name:         d.Name,
		Description:  d.Description,
		PromptId:     d.PromptID,
		Schema:       schemaToProto(d.Schema),
		ExampleCount: int32(d.ExampleCount),
		LastUpdated:  timestamppb.New(d.LastUpdated),
		Tags:         d.Tags,
		Metadata:     d.Metadata,
		Version:      int32(d.Version),
		CreatedBy:    d.CreatedBy,
		CreatedAt:    timestamppb.New(d.CreatedAt),
	}
}

func schemaToProto(s DatasetSchema) *datasetsv1.DatasetSchema {
	inputFields := make([]*datasetsv1.SchemaField, len(s.InputFields))
	for i, f := range s.InputFields {
		inputFields[i] = &datasetsv1.SchemaField{
			Name:        f.Name,
			Type:        f.Type,
			Description: f.Description,
			Required:    f.Required,
		}
	}

	outputFields := make([]*datasetsv1.SchemaField, len(s.ExpectedOutputFields))
	for i, f := range s.ExpectedOutputFields {
		outputFields[i] = &datasetsv1.SchemaField{
			Name:        f.Name,
			Type:        f.Type,
			Description: f.Description,
			Required:    f.Required,
		}
	}

	return &datasetsv1.DatasetSchema{
		InputFields:          inputFields,
		ExpectedOutputFields: outputFields,
	}
}

func schemaFromProto(s *datasetsv1.DatasetSchema) DatasetSchema {
	if s == nil {
		return DatasetSchema{}
	}

	inputFields := make([]SchemaField, len(s.InputFields))
	for i, f := range s.InputFields {
		inputFields[i] = SchemaField{
			Name:        f.Name,
			Type:        f.Type,
			Description: f.Description,
			Required:    f.Required,
		}
	}

	outputFields := make([]SchemaField, len(s.ExpectedOutputFields))
	for i, f := range s.ExpectedOutputFields {
		outputFields[i] = SchemaField{
			Name:        f.Name,
			Type:        f.Type,
			Description: f.Description,
			Required:    f.Required,
		}
	}

	return DatasetSchema{
		InputFields:          inputFields,
		ExpectedOutputFields: outputFields,
	}
}

func exampleToProto(e *Example) *datasetsv1.Example {
	return &datasetsv1.Example{
		Id:             e.ID,
		DatasetId:      e.DatasetID,
		Input:          mapToStruct(e.Input),
		ExpectedOutput: mapToStruct(e.ExpectedOutput),
		Metadata:       e.Metadata,
		Source:         exampleSourceToProto(e.Source),
		CreatedAt:      timestamppb.New(e.CreatedAt),
	}
}

func exampleSourceToProto(s ExampleSource) datasetsv1.ExampleSource {
	switch s {
	case ExampleSourceManual:
		return datasetsv1.ExampleSource_EXAMPLE_SOURCE_MANUAL
	case ExampleSourceGenerated:
		return datasetsv1.ExampleSource_EXAMPLE_SOURCE_GENERATED
	case ExampleSourceProduction:
		return datasetsv1.ExampleSource_EXAMPLE_SOURCE_PRODUCTION
	case ExampleSourceImported:
		return datasetsv1.ExampleSource_EXAMPLE_SOURCE_IMPORTED
	default:
		return datasetsv1.ExampleSource_EXAMPLE_SOURCE_UNSPECIFIED
	}
}

func exampleSourceFromProto(s datasetsv1.ExampleSource) ExampleSource {
	switch s {
	case datasetsv1.ExampleSource_EXAMPLE_SOURCE_MANUAL:
		return ExampleSourceManual
	case datasetsv1.ExampleSource_EXAMPLE_SOURCE_GENERATED:
		return ExampleSourceGenerated
	case datasetsv1.ExampleSource_EXAMPLE_SOURCE_PRODUCTION:
		return ExampleSourceProduction
	case datasetsv1.ExampleSource_EXAMPLE_SOURCE_IMPORTED:
		return ExampleSourceImported
	default:
		return ExampleSourceUnspecified
	}
}

func mapToStruct(m map[string]interface{}) *structpb.Struct {
	if m == nil {
		return nil
	}
	s, _ := structpb.NewStruct(m)
	return s
}

func structToMap(s *structpb.Struct) map[string]interface{} {
	if s == nil {
		return nil
	}
	return s.AsMap()
}
