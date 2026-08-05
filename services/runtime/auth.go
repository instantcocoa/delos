package runtime

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Auth is the gateway's virtual-key layer. With a nil authenticator the
// gateway runs in dev mode: any (or no) key is accepted and nothing is
// metered. With an authenticator, every /v1 request must present a valid,
// in-budget key scoped to the requested model.

type authedKeyCtx struct{}

// AuthedKey returns the virtual key attached to the request, if any.
func AuthedKey(ctx context.Context) *VirtualKey {
	k, _ := ctx.Value(authedKeyCtx{}).(*VirtualKey)
	return k
}

// presentedKey extracts the key secret from Authorization: Bearer or
// x-api-key (both surfaces' conventions).
func presentedKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if secret, ok := strings.CutPrefix(h, "Bearer "); ok {
			return secret
		}
	}
	return r.Header.Get("X-Api-Key")
}

// authenticate resolves the request's key. In dev mode it returns (nil, true).
func (s *HTTPServer) authenticate(w http.ResponseWriter, r *http.Request, anthSurface bool) (*VirtualKey, bool) {
	if s.auth == nil {
		return nil, true
	}
	key, err := s.auth.Authenticate(r.Context(), presentedKey(r))
	if err != nil {
		msg := "invalid or missing API key; pass a Delos virtual key as a Bearer token or x-api-key header"
		if anthSurface {
			s.writeAnthropicError(w, r, http.StatusUnauthorized, "authentication_error", msg)
		} else {
			s.writeOpenAIError(w, r, http.StatusUnauthorized, "invalid_api_key", msg)
		}
		return nil, false
	}
	return key, true
}

// budgetError describes why a request was refused before reaching a provider.
type budgetError struct {
	code string
	msg  string
}

// checkKeyLimits enforces model scope and monthly budgets. It returns nil
// when the request may proceed.
func (s *HTTPServer) checkKeyLimits(ctx context.Context, key *VirtualKey, model string) *budgetError {
	if key == nil {
		return nil
	}
	if !key.AllowsModel(model) {
		return &budgetError{
			code: "model_not_allowed",
			msg:  fmt.Sprintf("key %q is not permitted to use model %q", key.Name, model),
		}
	}
	if key.TokenBudget <= 0 && key.USDBudget <= 0 {
		return nil
	}
	usage, err := s.keyStore.GetUsage(ctx, key.ID, CurrentMonth(nowFunc()))
	if err != nil {
		s.logger.ErrorContext(ctx, "budget lookup failed", "key", key.ID, "error", err)
		return &budgetError{code: "budget_unavailable", msg: "could not verify budget; request refused"}
	}
	if key.TokenBudget > 0 && usage.Tokens >= key.TokenBudget {
		return &budgetError{
			code: "budget_exceeded",
			msg: fmt.Sprintf("monthly token budget exhausted for key %q (%d/%d tokens used)",
				key.Name, usage.Tokens, key.TokenBudget),
		}
	}
	if key.USDBudget > 0 && usage.USD >= key.USDBudget {
		return &budgetError{
			code: "budget_exceeded",
			msg: fmt.Sprintf("monthly dollar budget exhausted for key %q ($%.4f/$%.2f used)",
				key.Name, usage.USD, key.USDBudget),
		}
	}
	return nil
}

// budgetStatus maps a refusal to its HTTP status.
func budgetStatus(be *budgetError) int {
	switch be.code {
	case "model_not_allowed":
		return http.StatusForbidden
	case "budget_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusTooManyRequests
	}
}

// recordUsage adds a completed request's consumption to the key's month.
func (s *HTTPServer) recordUsage(ctx context.Context, key *VirtualKey, usage Usage) {
	if key == nil || s.keyStore == nil {
		return
	}
	if err := s.keyStore.AddUsage(ctx, key.ID, CurrentMonth(nowFunc()), int64(usage.TotalTokens), usage.CostUSD); err != nil {
		s.logger.ErrorContext(ctx, "usage recording failed", "key", key.ID, "error", err)
	}
}
