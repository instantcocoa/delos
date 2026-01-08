package prompt

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
)

// Handler implements the PromptService gRPC interface.
type Handler struct {
	promptv1.UnimplementedPromptServiceServer
	store  Store
	logger *slog.Logger
}

// NewHandler creates a new prompt service handler.
func NewHandler(store Store, logger *slog.Logger) *Handler {
	return &Handler{
		store:  store,
		logger: logger.With("component", "prompt"),
	}
}

// Register registers the handler with a gRPC server.
func (h *Handler) Register(s *grpc.Server) {
	promptv1.RegisterPromptServiceServer(s, h)
}

// CreatePrompt creates a new prompt.
func (h *Handler) CreatePrompt(ctx context.Context, req *promptv1.CreatePromptRequest) (*promptv1.CreatePromptResponse, error) {
	// Validate input
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	slug := req.Slug
	if slug == "" {
		slug = Slugify(req.Name)
	}
	if !IsValidSlug(slug) {
		return nil, fmt.Errorf("invalid slug: %s", slug)
	}

	now := time.Now()
	prompt := &Prompt{
		ID:            GenerateID(),
		Name:          req.Name,
		Slug:          slug,
		Version:       1,
		Description:   req.Description,
		Messages:      protoToMessages(req.Messages),
		Variables:     protoToVariables(req.Variables),
		DefaultConfig: protoToConfig(req.DefaultConfig),
		Tags:          req.Tags,
		Metadata:      req.Metadata,
		Status:        PromptStatusActive,
		CreatedBy:     "system", // TODO: Get from auth context
		CreatedAt:     now,
		UpdatedBy:     "system",
		UpdatedAt:     now,
	}

	if err := h.store.Create(ctx, prompt); err != nil {
		h.logger.ErrorContext(ctx, "failed to create prompt", "error", err)
		return nil, err
	}

	h.logger.InfoContext(ctx, "prompt created", "id", prompt.ID, "slug", prompt.Slug)

	return &promptv1.CreatePromptResponse{
		Prompt: toProto(prompt),
	}, nil
}

// GetPrompt retrieves a prompt by ID or reference.
func (h *Handler) GetPrompt(ctx context.Context, req *promptv1.GetPromptRequest) (*promptv1.GetPromptResponse, error) {
	var prompt *Prompt
	var err error

	if req.Id != "" {
		prompt, err = h.store.Get(ctx, req.Id)
	} else if req.Reference != "" {
		slug, version := ParseReference(req.Reference)
		prompt, err = h.store.GetBySlug(ctx, slug, version)
	} else {
		return nil, fmt.Errorf("id or reference required")
	}

	if err != nil {
		return nil, err
	}

	return &promptv1.GetPromptResponse{
		Prompt: toProto(prompt),
	}, nil
}

// UpdatePrompt creates a new version of an existing prompt.
func (h *Handler) UpdatePrompt(ctx context.Context, req *promptv1.UpdatePromptRequest) (*promptv1.UpdatePromptResponse, error) {
	existing, err := h.store.Get(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("prompt not found: %s", req.Id)
	}

	previousVersion := existing.Version

	// Update fields
	if req.Description != "" {
		existing.Description = req.Description
	}
	if len(req.Messages) > 0 {
		existing.Messages = protoToMessages(req.Messages)
	}
	if len(req.Variables) > 0 {
		existing.Variables = protoToVariables(req.Variables)
	}
	if req.DefaultConfig != nil && (req.DefaultConfig.MaxTokens > 0 || req.DefaultConfig.Temperature > 0) {
		existing.DefaultConfig = protoToConfig(req.DefaultConfig)
	}
	if len(req.Tags) > 0 {
		existing.Tags = req.Tags
	}
	if len(req.Metadata) > 0 {
		existing.Metadata = req.Metadata
	}
	existing.UpdatedBy = "system" // TODO: Get from auth context
	existing.UpdatedAt = time.Now()

	if err := h.store.Update(ctx, existing); err != nil {
		h.logger.ErrorContext(ctx, "failed to update prompt", "error", err)
		return nil, err
	}

	h.logger.InfoContext(ctx, "prompt updated",
		"id", existing.ID,
		"version", existing.Version,
		"previous_version", previousVersion,
	)

	return &promptv1.UpdatePromptResponse{
		Prompt:          toProto(existing),
		PreviousVersion: int32(previousVersion),
	}, nil
}

// ListPrompts returns all prompts with optional filtering.
func (h *Handler) ListPrompts(ctx context.Context, req *promptv1.ListPromptsRequest) (*promptv1.ListPromptsResponse, error) {
	query := ListQuery{
		Search:     req.Search,
		Tags:       req.Tags,
		Status:     protoToStatus(req.Status),
		Limit:      int(req.Limit),
		Offset:     int(req.Offset),
		OrderBy:    req.OrderBy,
		Descending: req.Descending,
	}

	prompts, total, err := h.store.List(ctx, query)
	if err != nil {
		return nil, err
	}

	protoPrompts := make([]*promptv1.Prompt, len(prompts))
	for i, p := range prompts {
		protoPrompts[i] = toProto(p)
	}

	return &promptv1.ListPromptsResponse{
		Prompts:    protoPrompts,
		TotalCount: int32(total),
	}, nil
}

// DeletePrompt deletes a prompt (soft delete).
func (h *Handler) DeletePrompt(ctx context.Context, req *promptv1.DeletePromptRequest) (*promptv1.DeletePromptResponse, error) {
	if err := h.store.Delete(ctx, req.Id); err != nil {
		h.logger.ErrorContext(ctx, "failed to delete prompt", "error", err)
		return nil, err
	}

	h.logger.InfoContext(ctx, "prompt deleted", "id", req.Id)

	return &promptv1.DeletePromptResponse{
		Success: true,
	}, nil
}

// GetPromptHistory returns version history for a prompt.
func (h *Handler) GetPromptHistory(ctx context.Context, req *promptv1.GetPromptHistoryRequest) (*promptv1.GetPromptHistoryResponse, error) {
	versions, err := h.store.GetHistory(ctx, req.Id, int(req.Limit))
	if err != nil {
		return nil, err
	}

	protoVersions := make([]*promptv1.PromptVersion, len(versions))
	for i, v := range versions {
		protoVersions[i] = &promptv1.PromptVersion{
			Version:           int32(v.Version),
			ChangeDescription: v.ChangeDescription,
			UpdatedBy:         v.UpdatedBy,
			UpdatedAt:         timestamppb.New(v.UpdatedAt),
		}
	}

	return &promptv1.GetPromptHistoryResponse{
		PromptId: req.Id,
		Versions: protoVersions,
	}, nil
}

// CompareVersions performs semantic diff between versions.
func (h *Handler) CompareVersions(ctx context.Context, req *promptv1.CompareVersionsRequest) (*promptv1.CompareVersionsResponse, error) {
	promptA, err := h.store.GetVersion(ctx, req.PromptId, int(req.VersionA))
	if err != nil {
		return nil, err
	}
	if promptA == nil {
		return nil, fmt.Errorf("version %d not found", req.VersionA)
	}

	promptB, err := h.store.GetVersion(ctx, req.PromptId, int(req.VersionB))
	if err != nil {
		return nil, err
	}
	if promptB == nil {
		return nil, fmt.Errorf("version %d not found", req.VersionB)
	}

	var diffs []VersionDiff

	// Compare description
	if promptA.Description != promptB.Description {
		diffs = append(diffs, VersionDiff{
			Field:    "description",
			OldValue: promptA.Description,
			NewValue: promptB.Description,
			DiffType: "modified",
		})
	}

	// Compare messages
	if len(promptA.Messages) != len(promptB.Messages) {
		diffs = append(diffs, VersionDiff{
			Field:    "messages",
			OldValue: fmt.Sprintf("%d messages", len(promptA.Messages)),
			NewValue: fmt.Sprintf("%d messages", len(promptB.Messages)),
			DiffType: "modified",
		})
	} else {
		for i := range promptA.Messages {
			if promptA.Messages[i].Content != promptB.Messages[i].Content {
				diffs = append(diffs, VersionDiff{
					Field:    fmt.Sprintf("messages[%d].content", i),
					OldValue: Truncate(promptA.Messages[i].Content, 100),
					NewValue: Truncate(promptB.Messages[i].Content, 100),
					DiffType: "modified",
				})
			}
		}
	}

	// Semantic similarity (placeholder - would use embeddings in real implementation)
	similarity := 1.0
	if len(diffs) > 0 {
		similarity = 1.0 - float64(len(diffs))*0.1
		if similarity < 0 {
			similarity = 0
		}
	}

	protoDiffs := make([]*promptv1.VersionDiff, len(diffs))
	for i, d := range diffs {
		protoDiffs[i] = &promptv1.VersionDiff{
			Field:    d.Field,
			OldValue: d.OldValue,
			NewValue: d.NewValue,
			DiffType: d.DiffType,
		}
	}

	return &promptv1.CompareVersionsResponse{
		Diffs:              protoDiffs,
		SemanticSimilarity: similarity,
	}, nil
}

// Health returns the service health status.
func (h *Handler) Health(ctx context.Context, req *promptv1.HealthRequest) (*promptv1.HealthResponse, error) {
	return &promptv1.HealthResponse{
		Status:  "healthy",
		Version: "0.1.0",
	}, nil
}

// Proto conversion helpers

func protoToMessages(msgs []*promptv1.PromptMessage) []PromptMessage {
	result := make([]PromptMessage, len(msgs))
	for i, m := range msgs {
		result[i] = PromptMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}
	return result
}

func protoToVariables(vars []*promptv1.PromptVariable) []PromptVariable {
	result := make([]PromptVariable, len(vars))
	for i, v := range vars {
		result[i] = PromptVariable{
			Name:         v.Name,
			Description:  v.Description,
			Type:         v.Type,
			Required:     v.Required,
			DefaultValue: v.DefaultValue,
		}
	}
	return result
}

func protoToConfig(cfg *promptv1.GenerationConfig) GenerationConfig {
	if cfg == nil {
		return GenerationConfig{}
	}
	return GenerationConfig{
		Temperature:  cfg.Temperature,
		MaxTokens:    int(cfg.MaxTokens),
		TopP:         cfg.TopP,
		Stop:         cfg.Stop,
		OutputSchema: cfg.OutputSchema,
	}
}

func protoToStatus(status promptv1.PromptStatus) PromptStatus {
	switch status {
	case promptv1.PromptStatus_PROMPT_STATUS_DRAFT:
		return PromptStatusDraft
	case promptv1.PromptStatus_PROMPT_STATUS_ACTIVE:
		return PromptStatusActive
	case promptv1.PromptStatus_PROMPT_STATUS_DEPRECATED:
		return PromptStatusDeprecated
	case promptv1.PromptStatus_PROMPT_STATUS_ARCHIVED:
		return PromptStatusArchived
	default:
		return PromptStatusUnspecified
	}
}

func toProto(p *Prompt) *promptv1.Prompt {
	if p == nil {
		return nil
	}

	messages := make([]*promptv1.PromptMessage, len(p.Messages))
	for i, m := range p.Messages {
		messages[i] = &promptv1.PromptMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	variables := make([]*promptv1.PromptVariable, len(p.Variables))
	for i, v := range p.Variables {
		variables[i] = &promptv1.PromptVariable{
			Name:         v.Name,
			Description:  v.Description,
			Type:         v.Type,
			Required:     v.Required,
			DefaultValue: v.DefaultValue,
		}
	}

	var status promptv1.PromptStatus
	switch p.Status {
	case PromptStatusDraft:
		status = promptv1.PromptStatus_PROMPT_STATUS_DRAFT
	case PromptStatusActive:
		status = promptv1.PromptStatus_PROMPT_STATUS_ACTIVE
	case PromptStatusDeprecated:
		status = promptv1.PromptStatus_PROMPT_STATUS_DEPRECATED
	case PromptStatusArchived:
		status = promptv1.PromptStatus_PROMPT_STATUS_ARCHIVED
	default:
		status = promptv1.PromptStatus_PROMPT_STATUS_UNSPECIFIED
	}

	return &promptv1.Prompt{
		Id:          p.ID,
		Name:        p.Name,
		Slug:        p.Slug,
		Version:     int32(p.Version),
		Description: p.Description,
		Messages:    messages,
		Variables:   variables,
		DefaultConfig: &promptv1.GenerationConfig{
			Temperature:  p.DefaultConfig.Temperature,
			MaxTokens:    int32(p.DefaultConfig.MaxTokens),
			TopP:         p.DefaultConfig.TopP,
			Stop:         p.DefaultConfig.Stop,
			OutputSchema: p.DefaultConfig.OutputSchema,
		},
		Tags:      p.Tags,
		Metadata:  p.Metadata,
		Status:    status,
		CreatedBy: p.CreatedBy,
		CreatedAt: timestamppb.New(p.CreatedAt),
		UpdatedBy: p.UpdatedBy,
		UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
}
