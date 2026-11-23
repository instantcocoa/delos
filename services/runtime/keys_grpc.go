package runtime

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	keysv1 "github.com/instantcocoa/delos/gen/go/keys/v1"
)

// KeyHandler implements the control-plane KeyService gRPC API (proto/keys/v1)
// over a KeyServiceStore. It is deliberately thin: validation and conversion
// only, with all persistence in the store so the memory and Bifrost backends
// share one contract.
type KeyHandler struct {
	keysv1.UnimplementedKeyServiceServer
	store  KeyServiceStore
	logger *slog.Logger
}

// NewKeyHandler creates a KeyService handler.
func NewKeyHandler(store KeyServiceStore, logger *slog.Logger) *KeyHandler {
	return &KeyHandler{store: store, logger: logger.With("component", "keys")}
}

// Register registers the handler with a gRPC server.
func (h *KeyHandler) Register(s *grpc.Server) {
	keysv1.RegisterKeyServiceServer(s, h)
}

func (h *KeyHandler) storeError(ctx context.Context, op string, err error) error {
	if errors.Is(err, ErrKeyNotFound) {
		return status.Error(codes.NotFound, "key not found")
	}
	h.logger.ErrorContext(ctx, op, "error", err)
	return status.Error(codes.Internal, op)
}

func mintSpecFromProto(req *keysv1.MintKeyRequest) (MintSpec, error) {
	spec := MintSpec{
		SessionID: req.SessionId,
		OrgID:     req.OrgId,
		Subject:   req.Subject,
		Labels:    req.Labels,
		Name:      req.Name,
		Models:    req.Models,
	}
	if req.ExpiresAt != nil {
		if err := req.ExpiresAt.CheckValid(); err != nil {
			return spec, status.Errorf(codes.InvalidArgument, "invalid expires_at: %v", err)
		}
		spec.ExpiresAt = req.ExpiresAt.AsTime()
	}
	for _, b := range req.Budgets {
		if b.MaxLimitUsd <= 0 {
			return spec, status.Error(codes.InvalidArgument, "budget max_limit_usd must be positive")
		}
		reset, err := effectiveResetDuration(b.ResetDuration)
		if err != nil {
			return spec, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		spec.Budgets = append(spec.Budgets, BudgetSpec{MaxLimitUSD: b.MaxLimitUsd, ResetDuration: reset})
	}
	if req.RateLimit != nil {
		rl := &RateLimitSpec{
			TokenMaxLimit:        req.RateLimit.TokenMaxLimit,
			TokenResetDuration:   req.RateLimit.TokenResetDuration,
			RequestMaxLimit:      req.RateLimit.RequestMaxLimit,
			RequestResetDuration: req.RateLimit.RequestResetDuration,
		}
		if rl.TokenMaxLimit > 0 {
			if _, err := effectiveResetDuration(rl.TokenResetDuration); err != nil {
				return spec, status.Errorf(codes.InvalidArgument, "invalid token reset duration: %v", err)
			}
		}
		if rl.RequestMaxLimit > 0 {
			if _, err := effectiveResetDuration(rl.RequestResetDuration); err != nil {
				return spec, status.Errorf(codes.InvalidArgument, "invalid request reset duration: %v", err)
			}
		}
		spec.RateLimit = rl
	}
	return spec, nil
}

func keyToProto(k *KeyRecord) *keysv1.Key {
	if k == nil {
		return nil
	}
	out := &keysv1.Key{
		Id:        k.ID,
		Name:      k.Name,
		SessionId: k.SessionID,
		OrgId:     k.OrgID,
		Subject:   k.Subject,
		Labels:    k.Labels,
		Models:    k.Models,
		Active:    k.Active,
		CreatedAt: timestamppb.New(k.CreatedAt),
	}
	if !k.ExpiresAt.IsZero() {
		out.ExpiresAt = timestamppb.New(k.ExpiresAt)
	}
	for _, b := range k.Budgets {
		out.Budgets = append(out.Budgets, &keysv1.Budget{MaxLimitUsd: b.MaxLimitUSD, ResetDuration: b.ResetDuration})
	}
	if k.RateLimit != nil {
		out.RateLimit = &keysv1.RateLimit{
			TokenMaxLimit:        k.RateLimit.TokenMaxLimit,
			TokenResetDuration:   k.RateLimit.TokenResetDuration,
			RequestMaxLimit:      k.RateLimit.RequestMaxLimit,
			RequestResetDuration: k.RateLimit.RequestResetDuration,
		}
	}
	return out
}

// MintKey creates a virtual key and returns the plaintext secret exactly once.
func (h *KeyHandler) MintKey(ctx context.Context, req *keysv1.MintKeyRequest) (*keysv1.MintKeyResponse, error) {
	spec, err := mintSpecFromProto(req)
	if err != nil {
		return nil, err
	}
	secret, key, err := h.store.MintKey(ctx, spec)
	if err != nil {
		return nil, h.storeError(ctx, "failed to mint key", err)
	}
	h.logger.InfoContext(ctx, "key minted",
		"id", key.ID, "session_id", key.SessionID, "org_id", key.OrgID,
		"budgets", len(key.Budgets), "expires_at", key.ExpiresAt)
	return &keysv1.MintKeyResponse{Key: keyToProto(key), Secret: secret}, nil
}

// RevokeKey deactivates a key.
func (h *KeyHandler) RevokeKey(ctx context.Context, req *keysv1.RevokeKeyRequest) (*keysv1.RevokeKeyResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	key, err := h.store.RevokeKey(ctx, req.Id)
	if err != nil {
		return nil, h.storeError(ctx, "failed to revoke key", err)
	}
	h.logger.InfoContext(ctx, "key revoked", "id", req.Id)
	return &keysv1.RevokeKeyResponse{Key: keyToProto(key)}, nil
}

// GetKeyUsage reports a key's budget and rate-limit consumption.
func (h *KeyHandler) GetKeyUsage(ctx context.Context, req *keysv1.GetKeyUsageRequest) (*keysv1.GetKeyUsageResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	usage, err := h.store.GetKeyUsage(ctx, req.Id)
	if err != nil {
		return nil, h.storeError(ctx, "failed to get key usage", err)
	}
	out := &keysv1.GetKeyUsageResponse{KeyId: usage.KeyID}
	for _, b := range usage.Budgets {
		out.Budgets = append(out.Budgets, &keysv1.BudgetUsage{
			Id:              b.ID,
			MaxLimitUsd:     b.MaxLimitUSD,
			ResetDuration:   b.ResetDuration,
			CurrentUsageUsd: b.CurrentUsageUSD,
			LastReset:       timestamppb.New(b.LastReset),
		})
	}
	if usage.RateLimit != nil {
		out.RateLimit = &keysv1.RateLimitUsage{
			Id:                 usage.RateLimit.ID,
			TokenMaxLimit:      usage.RateLimit.TokenMaxLimit,
			TokenCurrentUsage:  usage.RateLimit.TokenCurrentUsage,
			RequestMaxLimit:    usage.RateLimit.RequestMaxLimit,
			RequestCurrentUsage: usage.RateLimit.RequestCurrentUsage,
		}
	}
	return out, nil
}

// ListKeys returns every key in creation order, without secrets.
func (h *KeyHandler) ListKeys(ctx context.Context, req *keysv1.ListKeysRequest) (*keysv1.ListKeysResponse, error) {
	keys, err := h.store.ListKeys(ctx)
	if err != nil {
		return nil, h.storeError(ctx, "failed to list keys", err)
	}
	out := &keysv1.ListKeysResponse{Keys: make([]*keysv1.Key, 0, len(keys))}
	for _, k := range keys {
		out.Keys = append(out.Keys, keyToProto(k))
	}
	return out, nil
}
