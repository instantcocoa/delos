package deploy

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	deployv1 "github.com/instantcocoa/delos/gen/go/deploy/v1"
)

// Handler implements the DeployService gRPC interface.
type Handler struct {
	deployv1.UnimplementedDeployServiceServer
	logger  *slog.Logger
	service *DeployService
}

// NewHandler creates a new deploy service handler.
func NewHandler(logger *slog.Logger, svc *DeployService) *Handler {
	return &Handler{
		logger:  logger.With("component", "handler"),
		service: svc,
	}
}

// Register registers the handler with a gRPC server.
func (h *Handler) Register(s *grpc.Server) {
	deployv1.RegisterDeployServiceServer(s, h)
}

// CreateDeployment creates a new deployment.
func (h *Handler) CreateDeployment(ctx context.Context, req *deployv1.CreateDeploymentRequest) (*deployv1.CreateDeploymentResponse, error) {
	h.logger.InfoContext(ctx, "creating deployment", "prompt_id", req.PromptId, "version", req.ToVersion)

	input := CreateDeploymentInput{
		PromptID:     req.PromptId,
		ToVersion:    int(req.ToVersion),
		Environment:  req.Environment,
		Strategy:     strategyFromProto(req.Strategy),
		SkipApproval: req.SkipApproval,
		Metadata:     req.Metadata,
	}

	deployment, err := h.service.CreateDeployment(ctx, input)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create deployment: %v", err)
	}

	return &deployv1.CreateDeploymentResponse{
		Deployment: deploymentToProto(deployment),
	}, nil
}

// GetDeployment retrieves a deployment by ID.
func (h *Handler) GetDeployment(ctx context.Context, req *deployv1.GetDeploymentRequest) (*deployv1.GetDeploymentResponse, error) {
	h.logger.InfoContext(ctx, "getting deployment", "id", req.Id)

	deployment, err := h.service.GetDeployment(ctx, req.Id)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get deployment", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to get deployment: %v", err)
	}
	if deployment == nil {
		return nil, status.Errorf(codes.NotFound, "deployment not found: %s", req.Id)
	}

	return &deployv1.GetDeploymentResponse{
		Deployment: deploymentToProto(deployment),
	}, nil
}

// ListDeployments returns deployments.
func (h *Handler) ListDeployments(ctx context.Context, req *deployv1.ListDeploymentsRequest) (*deployv1.ListDeploymentsResponse, error) {
	h.logger.InfoContext(ctx, "listing deployments")

	query := ListDeploymentsQuery{
		PromptID:    req.PromptId,
		Environment: req.Environment,
		Status:      deploymentStatusFromProto(req.Status),
		Limit:       int(req.Limit),
		Offset:      int(req.Offset),
	}

	deployments, total, err := h.service.ListDeployments(ctx, query)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list deployments: %v", err)
	}

	protoDeployments := make([]*deployv1.Deployment, len(deployments))
	for i, d := range deployments {
		protoDeployments[i] = deploymentToProto(d)
	}

	return &deployv1.ListDeploymentsResponse{
		Deployments: protoDeployments,
		TotalCount:  int32(total),
	}, nil
}

// ApproveDeployment approves a pending deployment.
func (h *Handler) ApproveDeployment(ctx context.Context, req *deployv1.ApproveDeploymentRequest) (*deployv1.ApproveDeploymentResponse, error) {
	h.logger.InfoContext(ctx, "approving deployment", "id", req.Id)

	deployment, err := h.service.ApproveDeployment(ctx, req.Id, "user", req.Comment)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to approve deployment", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to approve deployment: %v", err)
	}

	return &deployv1.ApproveDeploymentResponse{
		Deployment: deploymentToProto(deployment),
	}, nil
}

// RollbackDeployment rolls back to a previous version.
func (h *Handler) RollbackDeployment(ctx context.Context, req *deployv1.RollbackDeploymentRequest) (*deployv1.RollbackDeploymentResponse, error) {
	h.logger.InfoContext(ctx, "rolling back deployment", "id", req.Id)

	deployment, rollback, err := h.service.RollbackDeployment(ctx, req.Id, req.Reason)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to rollback deployment", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to rollback deployment: %v", err)
	}

	return &deployv1.RollbackDeploymentResponse{
		Deployment:         deploymentToProto(deployment),
		RollbackDeployment: deploymentToProto(rollback),
	}, nil
}

// CancelDeployment cancels a pending/in-progress deployment.
func (h *Handler) CancelDeployment(ctx context.Context, req *deployv1.CancelDeploymentRequest) (*deployv1.CancelDeploymentResponse, error) {
	h.logger.InfoContext(ctx, "cancelling deployment", "id", req.Id)

	deployment, err := h.service.CancelDeployment(ctx, req.Id, req.Reason)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to cancel deployment", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to cancel deployment: %v", err)
	}

	return &deployv1.CancelDeploymentResponse{
		Deployment: deploymentToProto(deployment),
	}, nil
}

// GetDeploymentStatus gets real-time deployment status.
func (h *Handler) GetDeploymentStatus(ctx context.Context, req *deployv1.GetDeploymentStatusRequest) (*deployv1.GetDeploymentStatusResponse, error) {
	h.logger.InfoContext(ctx, "getting deployment status", "id", req.Id)

	deployment, currentMetrics, baselineMetrics, err := h.service.GetDeploymentStatus(ctx, req.Id)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get deployment status", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to get deployment status: %v", err)
	}

	resp := &deployv1.GetDeploymentStatusResponse{
		Status:      deploymentStatusToProto(deployment.Status),
		GateResults: gateResultsToProto(deployment.GateResults),
	}

	if deployment.Rollout != nil {
		resp.Rollout = rolloutToProto(deployment.Rollout)
	}

	if currentMetrics != nil {
		resp.CurrentMetrics = metricsToProto(currentMetrics)
	}
	if baselineMetrics != nil {
		resp.BaselineMetrics = metricsToProto(baselineMetrics)
	}

	return resp, nil
}

// CreateQualityGate creates a quality gate configuration.
func (h *Handler) CreateQualityGate(ctx context.Context, req *deployv1.CreateQualityGateRequest) (*deployv1.CreateQualityGateResponse, error) {
	h.logger.InfoContext(ctx, "creating quality gate", "name", req.Name)

	input := CreateQualityGateInput{
		Name:       req.Name,
		PromptID:   req.PromptId,
		Conditions: conditionsFromProto(req.Conditions),
		Required:   req.Required,
	}

	gate, err := h.service.CreateQualityGate(ctx, input)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to create quality gate", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create quality gate: %v", err)
	}

	return &deployv1.CreateQualityGateResponse{
		QualityGate: qualityGateToProto(gate),
	}, nil
}

// ListQualityGates lists quality gates for a prompt.
func (h *Handler) ListQualityGates(ctx context.Context, req *deployv1.ListQualityGatesRequest) (*deployv1.ListQualityGatesResponse, error) {
	h.logger.InfoContext(ctx, "listing quality gates", "prompt_id", req.PromptId)

	gates, err := h.service.ListQualityGates(ctx, req.PromptId)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to list quality gates", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list quality gates: %v", err)
	}

	protoGates := make([]*deployv1.QualityGate, len(gates))
	for i, g := range gates {
		protoGates[i] = qualityGateToProto(g)
	}

	return &deployv1.ListQualityGatesResponse{
		QualityGates: protoGates,
	}, nil
}

// Health returns the service health status.
func (h *Handler) Health(ctx context.Context, req *deployv1.HealthRequest) (*deployv1.HealthResponse, error) {
	return &deployv1.HealthResponse{
		Status:  "healthy",
		Version: "0.1.0",
	}, nil
}

// Conversion helpers

func deploymentToProto(d *Deployment) *deployv1.Deployment {
	deployment := &deployv1.Deployment{
		Id:            d.ID,
		PromptId:      d.PromptID,
		FromVersion:   int32(d.FromVersion),
		ToVersion:     int32(d.ToVersion),
		Environment:   d.Environment,
		Strategy:      strategyToProto(d.Strategy),
		Status:        deploymentStatusToProto(d.Status),
		StatusMessage: d.StatusMessage,
		GateResults:   gateResultsToProto(d.GateResults),
		GatesPassed:   d.GatesPassed,
		CreatedAt:     timestamppb.New(d.CreatedAt),
		CreatedBy:     d.CreatedBy,
		ApprovedBy:    d.ApprovedBy,
		Metadata:      d.Metadata,
	}

	if d.Rollout != nil {
		deployment.Rollout = rolloutToProto(d.Rollout)
	}
	if d.StartedAt != nil {
		deployment.StartedAt = timestamppb.New(*d.StartedAt)
	}
	if d.CompletedAt != nil {
		deployment.CompletedAt = timestamppb.New(*d.CompletedAt)
	}

	return deployment
}

func strategyToProto(s DeploymentStrategy) *deployv1.DeploymentStrategy {
	return &deployv1.DeploymentStrategy{
		Type:              deploymentTypeToProto(s.Type),
		InitialPercentage: int32(s.InitialPercentage),
		Increment:         int32(s.Increment),
		IntervalSeconds:   int32(s.IntervalSeconds),
		AutoRollback:      s.AutoRollback,
		RollbackThreshold: s.RollbackThreshold,
	}
}

func strategyFromProto(s *deployv1.DeploymentStrategy) DeploymentStrategy {
	if s == nil {
		return DeploymentStrategy{Type: DeploymentTypeImmediate}
	}
	return DeploymentStrategy{
		Type:              deploymentTypeFromProto(s.Type),
		InitialPercentage: int(s.InitialPercentage),
		Increment:         int(s.Increment),
		IntervalSeconds:   int(s.IntervalSeconds),
		AutoRollback:      s.AutoRollback,
		RollbackThreshold: s.RollbackThreshold,
	}
}

func deploymentTypeToProto(t DeploymentType) deployv1.DeploymentType {
	switch t {
	case DeploymentTypeImmediate:
		return deployv1.DeploymentType_DEPLOYMENT_TYPE_IMMEDIATE
	case DeploymentTypeGradual:
		return deployv1.DeploymentType_DEPLOYMENT_TYPE_GRADUAL
	case DeploymentTypeCanary:
		return deployv1.DeploymentType_DEPLOYMENT_TYPE_CANARY
	case DeploymentTypeBlueGreen:
		return deployv1.DeploymentType_DEPLOYMENT_TYPE_BLUE_GREEN
	default:
		return deployv1.DeploymentType_DEPLOYMENT_TYPE_UNSPECIFIED
	}
}

func deploymentTypeFromProto(t deployv1.DeploymentType) DeploymentType {
	switch t {
	case deployv1.DeploymentType_DEPLOYMENT_TYPE_IMMEDIATE:
		return DeploymentTypeImmediate
	case deployv1.DeploymentType_DEPLOYMENT_TYPE_GRADUAL:
		return DeploymentTypeGradual
	case deployv1.DeploymentType_DEPLOYMENT_TYPE_CANARY:
		return DeploymentTypeCanary
	case deployv1.DeploymentType_DEPLOYMENT_TYPE_BLUE_GREEN:
		return DeploymentTypeBlueGreen
	default:
		return DeploymentTypeUnspecified
	}
}

func deploymentStatusToProto(s DeploymentStatus) deployv1.DeploymentStatus {
	switch s {
	case DeploymentStatusPendingApproval:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_PENDING_APPROVAL
	case DeploymentStatusPendingGates:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_PENDING_GATES
	case DeploymentStatusGatesFailed:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_GATES_FAILED
	case DeploymentStatusInProgress:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_IN_PROGRESS
	case DeploymentStatusCompleted:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_COMPLETED
	case DeploymentStatusRolledBack:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_ROLLED_BACK
	case DeploymentStatusCancelled:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELLED
	case DeploymentStatusFailed:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED
	default:
		return deployv1.DeploymentStatus_DEPLOYMENT_STATUS_UNSPECIFIED
	}
}

func deploymentStatusFromProto(s deployv1.DeploymentStatus) DeploymentStatus {
	switch s {
	case deployv1.DeploymentStatus_DEPLOYMENT_STATUS_PENDING_APPROVAL:
		return DeploymentStatusPendingApproval
	case deployv1.DeploymentStatus_DEPLOYMENT_STATUS_PENDING_GATES:
		return DeploymentStatusPendingGates
	case deployv1.DeploymentStatus_DEPLOYMENT_STATUS_GATES_FAILED:
		return DeploymentStatusGatesFailed
	case deployv1.DeploymentStatus_DEPLOYMENT_STATUS_IN_PROGRESS:
		return DeploymentStatusInProgress
	case deployv1.DeploymentStatus_DEPLOYMENT_STATUS_COMPLETED:
		return DeploymentStatusCompleted
	case deployv1.DeploymentStatus_DEPLOYMENT_STATUS_ROLLED_BACK:
		return DeploymentStatusRolledBack
	case deployv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELLED:
		return DeploymentStatusCancelled
	case deployv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED:
		return DeploymentStatusFailed
	default:
		return DeploymentStatusUnspecified
	}
}

func rolloutToProto(r *RolloutProgress) *deployv1.RolloutProgress {
	rp := &deployv1.RolloutProgress{
		CurrentPercentage: int32(r.CurrentPercentage),
		TargetPercentage:  int32(r.TargetPercentage),
	}
	if r.LastIncrementAt != nil {
		rp.LastIncrementAt = timestamppb.New(*r.LastIncrementAt)
	}
	if r.NextIncrementAt != nil {
		rp.NextIncrementAt = timestamppb.New(*r.NextIncrementAt)
	}
	return rp
}

func gateResultsToProto(results []QualityGateResult) []*deployv1.QualityGateResult {
	protoResults := make([]*deployv1.QualityGateResult, len(results))
	for i, r := range results {
		conditionResults := make([]*deployv1.ConditionResult, len(r.ConditionResults))
		for j, cr := range r.ConditionResults {
			conditionResults[j] = &deployv1.ConditionResult{
				Type:     cr.Type,
				Expected: cr.Expected,
				Actual:   cr.Actual,
				Passed:   cr.Passed,
			}
		}
		protoResults[i] = &deployv1.QualityGateResult{
			GateId:           r.GateID,
			GateName:         r.GateName,
			Passed:           r.Passed,
			Message:          r.Message,
			ConditionResults: conditionResults,
		}
	}
	return protoResults
}

func metricsToProto(m *DeploymentMetrics) *deployv1.DeploymentMetrics {
	return &deployv1.DeploymentMetrics{
		AvgLatencyMs: m.AvgLatencyMs,
		ErrorRate:    m.ErrorRate,
		QualityScore: m.QualityScore,
		RequestCount: int32(m.RequestCount),
	}
}

func qualityGateToProto(g *QualityGate) *deployv1.QualityGate {
	conditions := make([]*deployv1.GateCondition, len(g.Conditions))
	for i, c := range g.Conditions {
		conditions[i] = &deployv1.GateCondition{
			Type:      c.Type,
			Operator:  c.Operator,
			Threshold: c.Threshold,
			EvalRunId: c.EvalRunID,
			DatasetId: c.DatasetID,
		}
	}

	return &deployv1.QualityGate{
		Id:         g.ID,
		Name:       g.Name,
		PromptId:   g.PromptID,
		Conditions: conditions,
		Required:   g.Required,
		CreatedAt:  timestamppb.New(g.CreatedAt),
		CreatedBy:  g.CreatedBy,
	}
}

func conditionsFromProto(conditions []*deployv1.GateCondition) []GateCondition {
	result := make([]GateCondition, len(conditions))
	for i, c := range conditions {
		result[i] = GateCondition{
			Type:      c.Type,
			Operator:  c.Operator,
			Threshold: c.Threshold,
			EvalRunID: c.EvalRunId,
			DatasetID: c.DatasetId,
		}
	}
	return result
}
