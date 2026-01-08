package datasets

import (
	"context"
	"log/slog"

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
