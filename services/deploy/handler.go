package deploy

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	deployv1 "github.com/instantcocoa/delos/gen/go/deploy/v1"
	"github.com/instantcocoa/delos/pkg/grpcutil"
)

// Handler implements the DeployService gRPC interface (quality gates only).
type Handler struct {
	deployv1.UnimplementedDeployServiceServer
	logger *slog.Logger
	svc    *DeployService
}

// NewHandler creates a new deploy handler.
func NewHandler(logger *slog.Logger, svc *DeployService) *Handler {
	return &Handler{
		logger: logger.With("component", "handler"),
		svc:    svc,
	}
}

// Register registers the handler with a gRPC server.
func (h *Handler) Register(s *grpc.Server) {
	deployv1.RegisterDeployServiceServer(s, h)
}

// CreateQualityGate creates a quality gate.
func (h *Handler) CreateQualityGate(ctx context.Context, req *deployv1.CreateQualityGateRequest) (*deployv1.CreateQualityGateResponse, error) {
	input := CreateQualityGateInput{
		Name:        req.Name,
		Description: req.Description,
		PromptID:    req.PromptId,
	}
	for _, c := range req.Conditions {
		input.Conditions = append(input.Conditions, GateCondition{
			Metric:    c.Metric,
			Operator:  operatorFromProto(c.Operator),
			Threshold: c.Threshold,
		})
	}

	gate, err := h.svc.CreateQualityGate(ctx, input)
	if err != nil {
		return nil, grpcutil.InvalidArgumentError("gate", err.Error())
	}
	h.logger.InfoContext(ctx, "quality gate created", "name", gate.Name, "prompt", gate.PromptID)
	return &deployv1.CreateQualityGateResponse{QualityGate: gateToProto(gate)}, nil
}

// ListQualityGates lists quality gates.
func (h *Handler) ListQualityGates(ctx context.Context, req *deployv1.ListQualityGatesRequest) (*deployv1.ListQualityGatesResponse, error) {
	gates, err := h.svc.ListQualityGates(ctx, req.PromptId)
	if err != nil {
		return nil, grpcutil.InternalError(err)
	}
	resp := &deployv1.ListQualityGatesResponse{}
	for _, g := range gates {
		resp.QualityGates = append(resp.QualityGates, gateToProto(g))
	}
	return resp, nil
}

// GetGateVerdict evaluates a gate.
func (h *Handler) GetGateVerdict(ctx context.Context, req *deployv1.GetGateVerdictRequest) (*deployv1.GetGateVerdictResponse, error) {
	verdict, err := h.svc.Verdict(ctx, req.Name)
	if err != nil {
		if errorsIs(err, ErrGateNotFound) {
			return nil, grpcutil.NotFoundError("quality gate", req.Name)
		}
		return nil, grpcutil.InternalError(err)
	}
	return &deployv1.GetGateVerdictResponse{
		Pass:        verdict.Pass,
		Reasons:     verdict.Reasons,
		EvaluatedAt: timestamppb.New(verdict.EvaluatedAt),
		Gate:        gateToProto(verdict.Gate),
		EvalRunId:   verdict.EvalRunID,
	}, nil
}

// Health returns service health.
func (h *Handler) Health(ctx context.Context, req *deployv1.HealthRequest) (*deployv1.HealthResponse, error) {
	return &deployv1.HealthResponse{Status: "healthy", Version: "0.2.0"}, nil
}

// ---- conversions ----

func operatorFromProto(op deployv1.GateOperator) Operator {
	switch op {
	case deployv1.GateOperator_GATE_OPERATOR_GTE:
		return OperatorGTE
	case deployv1.GateOperator_GATE_OPERATOR_LTE:
		return OperatorLTE
	default:
		return OperatorUnspecified
	}
}

func operatorToProto(op Operator) deployv1.GateOperator {
	switch op {
	case OperatorGTE:
		return deployv1.GateOperator_GATE_OPERATOR_GTE
	case OperatorLTE:
		return deployv1.GateOperator_GATE_OPERATOR_LTE
	default:
		return deployv1.GateOperator_GATE_OPERATOR_UNSPECIFIED
	}
}

func gateToProto(g *QualityGate) *deployv1.QualityGate {
	if g == nil {
		return nil
	}
	pb := &deployv1.QualityGate{
		Id:          g.ID,
		Name:        g.Name,
		Description: g.Description,
		PromptId:    g.PromptID,
		CreatedAt:   timestamppb.New(g.CreatedAt),
		CreatedBy:   g.CreatedBy,
	}
	for _, c := range g.Conditions {
		pb.Conditions = append(pb.Conditions, &deployv1.GateCondition{
			Metric:    c.Metric,
			Operator:  operatorToProto(c.Operator),
			Threshold: c.Threshold,
		})
	}
	return pb
}
