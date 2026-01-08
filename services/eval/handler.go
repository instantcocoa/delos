package eval

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
)

// Handler implements the EvalService gRPC interface.
type Handler struct {
	evalv1.UnimplementedEvalServiceServer
	logger  *slog.Logger
	service *EvalService
}

// NewHandler creates a new eval service handler.
func NewHandler(logger *slog.Logger, svc *EvalService) *Handler {
	return &Handler{
		logger:  logger.With("component", "handler"),
		service: svc,
	}
}

// Register registers the handler with a gRPC server.
func (h *Handler) Register(s *grpc.Server) {
	evalv1.RegisterEvalServiceServer(s, h)
}

// CreateEvalRun creates and starts an evaluation run.
func (h *Handler) CreateEvalRun(ctx context.Context, req *evalv1.CreateEvalRunRequest) (*evalv1.CreateEvalRunResponse, error) {
	h.logger.InfoContext(ctx, "creating eval run", "name", req.Name, "prompt_id", req.PromptId)

	input := CreateEvalRunInput{
		Name:          req.Name,
		Description:   req.Description,
		PromptID:      req.PromptId,
		PromptVersion: int(req.PromptVersion),
		DatasetID:     req.DatasetId,
		Config:        evalConfigFromProto(req.Config),
		Metadata:      req.Metadata,
	}

	run, err := h.service.CreateEvalRun(ctx, input)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to create eval run", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create eval run: %v", err)
	}

	return &evalv1.CreateEvalRunResponse{
		EvalRun: evalRunToProto(run),
	}, nil
}

// GetEvalRun retrieves an evaluation run by ID.
func (h *Handler) GetEvalRun(ctx context.Context, req *evalv1.GetEvalRunRequest) (*evalv1.GetEvalRunResponse, error) {
	h.logger.InfoContext(ctx, "getting eval run", "id", req.Id)

	run, err := h.service.GetEvalRun(ctx, req.Id)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get eval run", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to get eval run: %v", err)
	}
	if run == nil {
		return nil, status.Errorf(codes.NotFound, "eval run not found: %s", req.Id)
	}

	return &evalv1.GetEvalRunResponse{
		EvalRun: evalRunToProto(run),
	}, nil
}

// ListEvalRuns returns evaluation runs.
func (h *Handler) ListEvalRuns(ctx context.Context, req *evalv1.ListEvalRunsRequest) (*evalv1.ListEvalRunsResponse, error) {
	h.logger.InfoContext(ctx, "listing eval runs")

	query := ListEvalRunsQuery{
		PromptID:  req.PromptId,
		DatasetID: req.DatasetId,
		Status:    evalRunStatusFromProto(req.Status),
		Limit:     int(req.Limit),
		Offset:    int(req.Offset),
	}

	runs, total, err := h.service.ListEvalRuns(ctx, query)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to list eval runs", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list eval runs: %v", err)
	}

	protoRuns := make([]*evalv1.EvalRun, len(runs))
	for i, r := range runs {
		protoRuns[i] = evalRunToProto(r)
	}

	return &evalv1.ListEvalRunsResponse{
		EvalRuns:   protoRuns,
		TotalCount: int32(total),
	}, nil
}

// CancelEvalRun cancels a running evaluation.
func (h *Handler) CancelEvalRun(ctx context.Context, req *evalv1.CancelEvalRunRequest) (*evalv1.CancelEvalRunResponse, error) {
	h.logger.InfoContext(ctx, "cancelling eval run", "id", req.Id)

	run, err := h.service.CancelEvalRun(ctx, req.Id)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to cancel eval run", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to cancel eval run: %v", err)
	}

	return &evalv1.CancelEvalRunResponse{
		EvalRun: evalRunToProto(run),
	}, nil
}

// GetEvalResults retrieves detailed results for an eval run.
func (h *Handler) GetEvalResults(ctx context.Context, req *evalv1.GetEvalResultsRequest) (*evalv1.GetEvalResultsResponse, error) {
	h.logger.InfoContext(ctx, "getting eval results", "eval_run_id", req.EvalRunId)

	query := GetEvalResultsQuery{
		EvalRunID:  req.EvalRunId,
		FailedOnly: req.FailedOnly,
		Limit:      int(req.Limit),
		Offset:     int(req.Offset),
	}

	results, total, err := h.service.GetEvalResults(ctx, query)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get eval results", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to get eval results: %v", err)
	}

	protoResults := make([]*evalv1.EvalResult, len(results))
	for i, r := range results {
		protoResults[i] = evalResultToProto(r)
	}

	return &evalv1.GetEvalResultsResponse{
		Results:    protoResults,
		TotalCount: int32(total),
	}, nil
}

// CompareRuns compares two evaluation runs.
func (h *Handler) CompareRuns(ctx context.Context, req *evalv1.CompareRunsRequest) (*evalv1.CompareRunsResponse, error) {
	h.logger.InfoContext(ctx, "comparing runs", "run_a", req.RunIdA, "run_b", req.RunIdB)

	result, err := h.service.CompareRuns(ctx, req.RunIdA, req.RunIdB)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to compare runs", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to compare runs: %v", err)
	}

	examples := make([]*evalv1.ExampleComparison, len(result.Examples))
	for i, e := range result.Examples {
		examples[i] = &evalv1.ExampleComparison{
			ExampleId:  e.ExampleID,
			ScoreA:     e.ScoreA,
			ScoreB:     e.ScoreB,
			ScoreDiff:  e.ScoreDiff,
			Regression: e.Regression,
		}
	}

	return &evalv1.CompareRunsResponse{
		RunA: &evalv1.RunComparison{
			RunId:         result.RunA.RunID,
			PromptVersion: result.RunA.PromptVersion,
			OverallScore:  result.RunA.OverallScore,
			PassRate:      result.RunA.PassRate,
			AvgLatencyMs:  result.RunA.AvgLatencyMs,
			TotalCostUsd:  result.RunA.TotalCostUSD,
		},
		RunB: &evalv1.RunComparison{
			RunId:         result.RunB.RunID,
			PromptVersion: result.RunB.PromptVersion,
			OverallScore:  result.RunB.OverallScore,
			PassRate:      result.RunB.PassRate,
			AvgLatencyMs:  result.RunB.AvgLatencyMs,
			TotalCostUsd:  result.RunB.TotalCostUSD,
		},
		ScoreDiff:    result.ScoreDiff,
		Regressions:  int32(result.Regressions),
		Improvements: int32(result.Improvements),
		Examples:     examples,
	}, nil
}

// ListEvaluators returns available evaluator types.
func (h *Handler) ListEvaluators(ctx context.Context, req *evalv1.ListEvaluatorsRequest) (*evalv1.ListEvaluatorsResponse, error) {
	h.logger.InfoContext(ctx, "listing evaluators")

	evaluators, err := h.service.ListEvaluators(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to list evaluators", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list evaluators: %v", err)
	}

	protoEvaluators := make([]*evalv1.Evaluator, len(evaluators))
	for i, e := range evaluators {
		params := make([]*evalv1.EvaluatorParam, len(e.Params))
		for j, p := range e.Params {
			params[j] = &evalv1.EvaluatorParam{
				Name:         p.Name,
				Type:         p.Type,
				Description:  p.Description,
				Required:     p.Required,
				DefaultValue: p.DefaultValue,
			}
		}
		protoEvaluators[i] = &evalv1.Evaluator{
			Type:        e.Type,
			Name:        e.Name,
			Description: e.Description,
			Params:      params,
		}
	}

	return &evalv1.ListEvaluatorsResponse{
		Evaluators: protoEvaluators,
	}, nil
}

// Health returns the service health status.
func (h *Handler) Health(ctx context.Context, req *evalv1.HealthRequest) (*evalv1.HealthResponse, error) {
	return &evalv1.HealthResponse{
		Status:  "healthy",
		Version: "0.1.0",
	}, nil
}

// Conversion helpers

func evalRunToProto(r *EvalRun) *evalv1.EvalRun {
	run := &evalv1.EvalRun{
		Id:                r.ID,
		Name:              r.Name,
		Description:       r.Description,
		PromptId:          r.PromptID,
		PromptVersion:     int32(r.PromptVersion),
		DatasetId:         r.DatasetID,
		Config:            evalConfigToProto(r.Config),
		Status:            evalRunStatusToProto(r.Status),
		ErrorMessage:      r.ErrorMessage,
		TotalExamples:     int32(r.TotalExamples),
		CompletedExamples: int32(r.CompletedExamples),
		CreatedAt:         timestamppb.New(r.CreatedAt),
		CreatedBy:         r.CreatedBy,
		Metadata:          r.Metadata,
	}

	if r.Summary != nil {
		run.Summary = &evalv1.EvalSummary{
			OverallScore:      r.Summary.OverallScore,
			ScoresByEvaluator: r.Summary.ScoresByEvaluator,
			PassedCount:       int32(r.Summary.PassedCount),
			FailedCount:       int32(r.Summary.FailedCount),
			PassRate:          r.Summary.PassRate,
			TotalCostUsd:      r.Summary.TotalCostUSD,
			TotalTokens:       int32(r.Summary.TotalTokens),
			AvgLatencyMs:      r.Summary.AvgLatencyMs,
		}
	}

	if r.StartedAt != nil {
		run.StartedAt = timestamppb.New(*r.StartedAt)
	}
	if r.CompletedAt != nil {
		run.CompletedAt = timestamppb.New(*r.CompletedAt)
	}

	return run
}

func evalConfigToProto(c EvalConfig) *evalv1.EvalConfig {
	evaluators := make([]*evalv1.EvaluatorConfig, len(c.Evaluators))
	for i, e := range c.Evaluators {
		evaluators[i] = &evalv1.EvaluatorConfig{
			Type:   e.Type,
			Name:   e.Name,
			Params: e.Params,
			Weight: e.Weight,
		}
	}

	return &evalv1.EvalConfig{
		Evaluators:  evaluators,
		Provider:    c.Provider,
		Model:       c.Model,
		Concurrency: int32(c.Concurrency),
		SampleSize:  int32(c.SampleSize),
		Shuffle:     c.Shuffle,
	}
}

func evalConfigFromProto(c *evalv1.EvalConfig) EvalConfig {
	if c == nil {
		return EvalConfig{}
	}

	evaluators := make([]EvaluatorConfig, len(c.Evaluators))
	for i, e := range c.Evaluators {
		evaluators[i] = EvaluatorConfig{
			Type:   e.Type,
			Name:   e.Name,
			Params: e.Params,
			Weight: e.Weight,
		}
	}

	return EvalConfig{
		Evaluators:  evaluators,
		Provider:    c.Provider,
		Model:       c.Model,
		Concurrency: int(c.Concurrency),
		SampleSize:  int(c.SampleSize),
		Shuffle:     c.Shuffle,
	}
}

func evalRunStatusToProto(s EvalRunStatus) evalv1.EvalRunStatus {
	switch s {
	case EvalRunStatusPending:
		return evalv1.EvalRunStatus_EVAL_RUN_STATUS_PENDING
	case EvalRunStatusRunning:
		return evalv1.EvalRunStatus_EVAL_RUN_STATUS_RUNNING
	case EvalRunStatusCompleted:
		return evalv1.EvalRunStatus_EVAL_RUN_STATUS_COMPLETED
	case EvalRunStatusFailed:
		return evalv1.EvalRunStatus_EVAL_RUN_STATUS_FAILED
	case EvalRunStatusCancelled:
		return evalv1.EvalRunStatus_EVAL_RUN_STATUS_CANCELLED
	default:
		return evalv1.EvalRunStatus_EVAL_RUN_STATUS_UNSPECIFIED
	}
}

func evalRunStatusFromProto(s evalv1.EvalRunStatus) EvalRunStatus {
	switch s {
	case evalv1.EvalRunStatus_EVAL_RUN_STATUS_PENDING:
		return EvalRunStatusPending
	case evalv1.EvalRunStatus_EVAL_RUN_STATUS_RUNNING:
		return EvalRunStatusRunning
	case evalv1.EvalRunStatus_EVAL_RUN_STATUS_COMPLETED:
		return EvalRunStatusCompleted
	case evalv1.EvalRunStatus_EVAL_RUN_STATUS_FAILED:
		return EvalRunStatusFailed
	case evalv1.EvalRunStatus_EVAL_RUN_STATUS_CANCELLED:
		return EvalRunStatusCancelled
	default:
		return EvalRunStatusUnspecified
	}
}

func evalResultToProto(r *EvalResult) *evalv1.EvalResult {
	evaluatorResults := make(map[string]*evalv1.EvaluatorResult)
	for k, v := range r.EvaluatorResults {
		evaluatorResults[k] = &evalv1.EvaluatorResult{
			EvaluatorType: v.EvaluatorType,
			Score:         v.Score,
			Passed:        v.Passed,
			Explanation:   v.Explanation,
			Details:       v.Details,
		}
	}

	return &evalv1.EvalResult{
		Id:               r.ID,
		EvalRunId:        r.EvalRunID,
		ExampleId:        r.ExampleID,
		Input:            mapToStruct(r.Input),
		ExpectedOutput:   mapToStruct(r.ExpectedOutput),
		ActualOutput:     mapToStruct(r.ActualOutput),
		EvaluatorResults: evaluatorResults,
		OverallScore:     r.OverallScore,
		Passed:           r.Passed,
		LatencyMs:        r.LatencyMs,
		TokensUsed:       int32(r.TokensUsed),
		CostUsd:          r.CostUSD,
		Error:            r.Error,
	}
}

func mapToStruct(m map[string]interface{}) *structpb.Struct {
	if m == nil {
		return nil
	}
	s, _ := structpb.NewStruct(m)
	return s
}
