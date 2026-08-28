package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
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
		// "We could not check your key" is not "your key is invalid". Telling
		// a caller with a perfectly good key that it is invalid, because
		// Postgres hiccuped, sends them rotating credentials to fix an outage
		// on our side. It is a 503, and it is retryable.
		status, code, anthCode := http.StatusUnauthorized, "invalid_api_key", "authentication_error"
		msg := "invalid or missing API key; pass a Delos virtual key as a Bearer token or x-api-key header"
		if errors.Is(err, ErrKeyStoreUnavailable) {
			status, code, anthCode = http.StatusServiceUnavailable, "key_store_unavailable", "api_error"
			msg = "could not verify the API key: the key store is unavailable; retry shortly"
			s.logger.ErrorContext(r.Context(), "key verification unavailable", "error", err)
		}
		if anthSurface {
			s.writeAnthropicError(w, r, status, anthCode, msg)
		} else {
			s.writeOpenAIError(w, r, status, code, msg)
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
//
// Budgets are enforced on recorded usage plus in-flight reservations (see
// budgetLedger). What that buys, precisely: a request is only admitted if the
// month's recorded spend plus one conservative estimate per concurrent
// in-flight request is still under the budget. It does not make the budget a
// hard cap — the estimate is not the true cost, which is unknowable before the
// model has answered — so the guaranteed bound is
//
//	final spend <= budget + (concurrency * largest actual request cost)
//
// where concurrency is the number of requests in flight for the key. Before
// reservations the bound was unbounded in the estimate's place: every
// concurrent request read the same pre-burst usage, so a key at 999/1000 could
// admit an unlimited number of 1M-token requests at once.
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
	// A key without a store behind it cannot be metered. recordUsage already
	// guarded this; the lookup below did not, so a configuration that set an
	// authenticator without a key store panicked on the first budgeted
	// request instead of refusing it.
	if s.keyStore == nil {
		s.logger.ErrorContext(ctx, "key carries a budget but no key store is configured", "key", key.ID)
		return &budgetError{code: "budget_unavailable", msg: "could not verify budget; request refused"}
	}
	usage, err := s.keyStore.GetUsage(ctx, key.ID, CurrentMonth(nowFunc()))
	if err != nil {
		s.logger.ErrorContext(ctx, "budget lookup failed", "key", key.ID, "error", err)
		return &budgetError{code: "budget_unavailable", msg: "could not verify budget; request refused"}
	}

	// Reserve before the comparison, and undo the reservation if the request
	// is refused, so that concurrent checks see each other. The gap between
	// reading usage and reserving is the remaining window, and it is one
	// store round trip wide.
	reservedTokens, reservedUSD := keyBudgets.reserve(key.ID)
	tokens := usage.Tokens + reservedTokens
	spend := usage.USD + reservedUSD

	if key.TokenBudget > 0 && tokens >= key.TokenBudget {
		keyBudgets.release(key.ID)
		return &budgetError{
			code: "budget_exceeded",
			msg: fmt.Sprintf("monthly token budget exhausted for key %q (%d/%d tokens used)",
				key.Name, usage.Tokens, key.TokenBudget),
		}
	}
	if key.USDBudget > 0 && spend >= key.USDBudget {
		keyBudgets.release(key.ID)
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

// recordUsage adds a completed request's consumption to the key's month and
// releases the reservation that admitted it. The reservation is released after
// the write, so the key is never momentarily unaccounted for.
func (s *HTTPServer) recordUsage(ctx context.Context, key *VirtualKey, usage Usage) {
	if key == nil || s.keyStore == nil {
		return
	}
	total := usage.withDerivedTotals()
	if err := s.keyStore.AddUsage(ctx, key.ID, CurrentMonth(nowFunc()), int64(total.TotalTokens), total.CostUSD); err != nil {
		s.logger.ErrorContext(ctx, "usage recording failed", "key", key.ID, "error", err)
	}
	keyBudgets.release(key.ID)
}

// ---- in-flight budget reservations ----

// Reservation sizing. The true cost of a request is unknown until the model
// answers, so admission uses a conservative placeholder: enough that a burst
// against a nearly-exhausted budget is refused, small enough that it does not
// refuse traffic on a budget with real headroom.
const (
	reservedTokensPerRequest = 4096
	reservedUSDPerRequest    = 0.05

	// reservationTTL bounds a leaked reservation. recordUsage releases
	// explicitly, but a request that fails before producing usage (provider
	// error, client disconnect mid-stream) never gets there. Expiry means such
	// a leak costs a key a slice of its budget for minutes, not for the month.
	reservationTTL = 5 * time.Minute
)

// budgetLedger holds the tokens and dollars committed by in-flight requests
// but not yet recorded.
//
// It is process-wide and keyed by virtual-key ID rather than hanging off one
// HTTPServer: a reservation is a statement about a key, and two surfaces
// mounted in the same process are spending the same key's budget. It is also
// only ever process-wide — several gateway replicas do not see each other's
// reservations, so the bound documented on checkKeyLimits multiplies by the
// number of replicas. Enforcing across replicas needs the reservation to live
// in Postgres, which is a larger change than this one.
type budgetLedger struct {
	mu       sync.Mutex
	inflight map[string][]reservationEntry
}

type reservationEntry struct {
	tokens  int64
	usd     float64
	expires time.Time
}

var keyBudgets = &budgetLedger{inflight: make(map[string][]reservationEntry)}

// reserve records one in-flight request for the key and returns the total
// reserved for it, this request included.
func (l *budgetLedger) reserve(keyID string) (int64, float64) {
	now := nowFunc()
	l.mu.Lock()
	defer l.mu.Unlock()
	live := l.prune(keyID, now)
	live = append(live, reservationEntry{
		tokens:  reservedTokensPerRequest,
		usd:     reservedUSDPerRequest,
		expires: now.Add(reservationTTL),
	})
	l.inflight[keyID] = live

	var tokens int64
	var usd float64
	for _, r := range live {
		tokens += r.tokens
		usd += r.usd
	}
	return tokens, usd
}

// release drops the key's oldest live reservation. Every reservation is the
// same size, so which one is released does not matter.
func (l *budgetLedger) release(keyID string) {
	now := nowFunc()
	l.mu.Lock()
	defer l.mu.Unlock()
	live := l.prune(keyID, now)
	if len(live) == 0 {
		delete(l.inflight, keyID)
		return
	}
	live = live[1:]
	if len(live) == 0 {
		delete(l.inflight, keyID)
		return
	}
	l.inflight[keyID] = live
}

// prune drops expired reservations for a key. Callers hold l.mu.
func (l *budgetLedger) prune(keyID string, now time.Time) []reservationEntry {
	entries := l.inflight[keyID]
	live := entries[:0]
	for _, r := range entries {
		if now.Before(r.expires) {
			live = append(live, r)
		}
	}
	return live
}
