package datasets

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DatasetsService handles dataset business logic.
type DatasetsService struct {
	store Store
}

// NewDatasetsService creates a new datasets service.
func NewDatasetsService(store Store) *DatasetsService {
	return &DatasetsService{
		store: store,
	}
}

// CreateDataset creates a new dataset.
func (s *DatasetsService) CreateDataset(ctx context.Context, input CreateDatasetInput) (*Dataset, error) {
	now := time.Now()
	dataset := &Dataset{
		ID:          uuid.New().String(),
		Name:        input.Name,
		Description: input.Description,
		PromptID:    input.PromptID,
		Schema:      input.Schema,
		Tags:        input.Tags,
		Metadata:    input.Metadata,
		Version:     1,
		CreatedBy:   input.CreatedBy,
		CreatedAt:   now,
		LastUpdated: now,
	}

	if err := s.store.CreateDataset(ctx, dataset); err != nil {
		return nil, fmt.Errorf("failed to create dataset: %w", err)
	}

	return dataset, nil
}

// GetDataset retrieves a dataset by ID.
func (s *DatasetsService) GetDataset(ctx context.Context, id string) (*Dataset, error) {
	dataset, err := s.store.GetDataset(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get dataset: %w", err)
	}
	return dataset, nil
}

// UpdateDataset updates a dataset.
func (s *DatasetsService) UpdateDataset(ctx context.Context, input UpdateDatasetInput) (*Dataset, error) {
	existing, err := s.store.GetDataset(ctx, input.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get dataset: %w", err)
	}
	if existing == nil {
		return nil, fmt.Errorf("dataset not found: %s", input.ID)
	}

	existing.Name = input.Name
	existing.Description = input.Description
	existing.Tags = input.Tags
	existing.Metadata = input.Metadata
	existing.LastUpdated = time.Now()
	existing.Version++

	if err := s.store.UpdateDataset(ctx, existing); err != nil {
		return nil, fmt.Errorf("failed to update dataset: %w", err)
	}

	return existing, nil
}

// DeleteDataset deletes a dataset.
func (s *DatasetsService) DeleteDataset(ctx context.Context, id string) error {
	if err := s.store.DeleteDataset(ctx, id); err != nil {
		return fmt.Errorf("failed to delete dataset: %w", err)
	}
	return nil
}

// ListDatasets returns datasets matching the query.
func (s *DatasetsService) ListDatasets(ctx context.Context, query ListDatasetsQuery) ([]*Dataset, int, error) {
	datasets, total, err := s.store.ListDatasets(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list datasets: %w", err)
	}
	return datasets, total, nil
}

// AddExamples adds examples to a dataset.
func (s *DatasetsService) AddExamples(ctx context.Context, input AddExamplesInput) ([]*Example, error) {
	// Verify dataset exists
	dataset, err := s.store.GetDataset(ctx, input.DatasetID)
	if err != nil {
		return nil, fmt.Errorf("failed to get dataset: %w", err)
	}
	if dataset == nil {
		return nil, fmt.Errorf("dataset not found: %s", input.DatasetID)
	}

	now := time.Now()
	examples := make([]*Example, len(input.Examples))
	for i, ex := range input.Examples {
		examples[i] = &Example{
			ID:             uuid.New().String(),
			DatasetID:      input.DatasetID,
			Input:          ex.Input,
			ExpectedOutput: ex.ExpectedOutput,
			Metadata:       ex.Metadata,
			Source:         ex.Source,
			CreatedAt:      now,
		}
	}

	if err := s.store.AddExamples(ctx, input.DatasetID, examples); err != nil {
		return nil, fmt.Errorf("failed to add examples: %w", err)
	}

	return examples, nil
}

// GetExamples retrieves examples from a dataset.
func (s *DatasetsService) GetExamples(ctx context.Context, query GetExamplesQuery) ([]*Example, int, error) {
	examples, total, err := s.store.GetExamples(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get examples: %w", err)
	}
	return examples, total, nil
}

// RemoveExamples removes examples from a dataset.
func (s *DatasetsService) RemoveExamples(ctx context.Context, datasetID string, exampleIDs []string) (int, error) {
	removed, err := s.store.RemoveExamples(ctx, datasetID, exampleIDs)
	if err != nil {
		return 0, fmt.Errorf("failed to remove examples: %w", err)
	}
	return removed, nil
}

// GetExample retrieves a single example by ID.
func (s *DatasetsService) GetExample(ctx context.Context, id string) (*Example, error) {
	example, err := s.store.GetExample(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get example: %w", err)
	}
	return example, nil
}

// ImportExamples imports examples from an external source.
func (s *DatasetsService) ImportExamples(ctx context.Context, input ImportExamplesInput) (*ImportResult, error) {
	// Verify dataset exists
	dataset, err := s.store.GetDataset(ctx, input.DatasetID)
	if err != nil {
		return nil, fmt.Errorf("failed to get dataset: %w", err)
	}
	if dataset == nil {
		return nil, fmt.Errorf("dataset not found: %s", input.DatasetID)
	}

	// Create data source
	source, err := NewSource(input.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to create data source: %w", err)
	}

	// Read data
	reader, err := source.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read data source: %w", err)
	}
	defer reader.Close()

	// Create parser
	parser, err := NewParser(input.Format)
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}

	// Parse data
	rows, errs := parser.Parse(reader, input.CSVOptions)

	result := &ImportResult{}
	now := time.Now()
	var examples []*Example

	// Build column mapping lookup
	inputCols := make(map[string]string)
	outputCols := make(map[string]string)
	for _, m := range input.ColumnMappings {
		if m.IsInput {
			inputCols[m.SourceColumn] = m.TargetField
		} else {
			outputCols[m.SourceColumn] = m.TargetField
		}
	}

	rowNum := 0
	for row := range rows {
		rowNum++
		if input.MaxRows > 0 && rowNum > input.MaxRows {
			break
		}

		// Map row to example
		example := &Example{
			ID:             uuid.New().String(),
			DatasetID:      input.DatasetID,
			Input:          make(map[string]interface{}),
			ExpectedOutput: make(map[string]interface{}),
			Source:         ExampleSourceImported,
			CreatedAt:      now,
		}

		// Apply column mappings
		for sourceCol, value := range row {
			if targetField, ok := inputCols[sourceCol]; ok {
				example.Input[targetField] = value
			}
			if targetField, ok := outputCols[sourceCol]; ok {
				example.ExpectedOutput[targetField] = value
			}
		}

		// If no mappings provided, use columns directly
		if len(input.ColumnMappings) == 0 {
			example.Input = row
		}

		examples = append(examples, example)
	}

	// Check for parsing errors
	select {
	case err := <-errs:
		if err != nil {
			if input.SkipInvalid {
				result.Errors = append(result.Errors, ImportError{
					RowNumber:    rowNum,
					ErrorMessage: err.Error(),
				})
				result.ErrorCount++
			} else {
				return nil, fmt.Errorf("parse error: %w", err)
			}
		}
	default:
	}

	// Add examples to store
	if len(examples) > 0 {
		if err := s.store.AddExamples(ctx, input.DatasetID, examples); err != nil {
			return nil, fmt.Errorf("failed to add examples: %w", err)
		}
		result.ImportedCount = len(examples)
	}

	return result, nil
}

// ExportExamples exports examples to a specified format.
func (s *DatasetsService) ExportExamples(ctx context.Context, input ExportExamplesInput) (*ExportResult, error) {
	// Verify dataset exists
	dataset, err := s.store.GetDataset(ctx, input.DatasetID)
	if err != nil {
		return nil, fmt.Errorf("failed to get dataset: %w", err)
	}
	if dataset == nil {
		return nil, fmt.Errorf("dataset not found: %s", input.DatasetID)
	}

	// Get examples
	query := GetExamplesQuery{
		DatasetID: input.DatasetID,
		Limit:     input.Limit,
		Offset:    input.Offset,
	}
	examples, _, err := s.store.GetExamples(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get examples: %w", err)
	}

	// Convert examples to rows
	rows := make([]Row, len(examples))
	for i, ex := range examples {
		row := make(Row)
		// Flatten input fields with "input_" prefix
		for k, v := range ex.Input {
			row["input_"+k] = v
		}
		// Flatten expected output fields with "expected_" prefix
		for k, v := range ex.ExpectedOutput {
			row["expected_"+k] = v
		}
		// Add metadata
		row["id"] = ex.ID
		row["source"] = ex.Source
		rows[i] = row
	}

	// Create writer
	writer, err := NewWriter(input.Format)
	if err != nil {
		return nil, fmt.Errorf("failed to create writer: %w", err)
	}

	// Write to buffer
	var buf bytes.Buffer
	if err := writer.Write(&buf, rows, input.CSVOptions); err != nil {
		return nil, fmt.Errorf("failed to write data: %w", err)
	}

	result := &ExportResult{
		Data:          buf.Bytes(),
		Format:        input.Format,
		ExportedCount: len(examples),
	}

	// If destination provided, write to it
	if input.Destination != nil && input.Destination.S3 != nil {
		s3Dest := NewS3Source(input.Destination.S3)
		if err := s3Dest.Write(ctx, bytes.NewReader(buf.Bytes())); err != nil {
			return nil, fmt.Errorf("failed to write to S3: %w", err)
		}
		result.DestinationURI = fmt.Sprintf("s3://%s/%s", input.Destination.S3.Bucket, input.Destination.S3.Key)
		result.Data = nil // Clear data since it's been written to destination
	}

	return result, nil
}
