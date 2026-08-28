package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ProviderError carries a backend provider's error verbatim, along with the
// upstream HTTP status. Gateway surfaces map it to an honest error response;
// it must never be swallowed into a generic 500.
type ProviderError struct {
	Provider   string
	StatusCode int
	Message    string

	// Code is the provider's machine-readable error code or type, when the
	// error envelope carried one ("insufficient_quota",
	// "rate_limit_exceeded", "RESOURCE_EXHAUSTED", ...). It is what
	// separates a transient 429 from a permanent one, so providers should
	// populate it whenever the upstream envelope has such a field.
	Code string

	// Wrapped is the underlying cause, when there is one: a cancelled
	// context, a transport failure, a decode error. It is exposed through
	// Unwrap so errors.Is/As see through this type. Without it a
	// context.Canceled buried in a ProviderError looks like an ordinary
	// upstream failure and the gateway retries a request whose client has
	// already hung up.
	Wrapped error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s: %s (upstream status %d)", e.Provider, e.Message, e.StatusCode)
}

// Unwrap exposes the underlying cause to errors.Is and errors.As.
func (e *ProviderError) Unwrap() error { return e.Wrapped }

// Retryable reports whether the upstream failure is worth retrying on this or
// a fallback provider.
//
// Retryable failures are server-side errors (5xx) and *transient* rate
// limiting. A 429 is not automatically transient: OpenAI and friends return
// the same status for "you are going too fast" (retry helps) and for "your
// account is out of credit" (retry never helps, and burns the circuit breaker
// for every other tenant while erasing the one message the caller needed).
// Those are told apart by Code and, for providers that do not send one, by the
// upstream message.
func (e *ProviderError) Retryable() bool {
	// A cancelled or timed-out caller is final however it was wrapped.
	if errors.Is(e.Wrapped, context.Canceled) || errors.Is(e.Wrapped, context.DeadlineExceeded) {
		return false
	}
	if e.StatusCode == 429 {
		return !e.QuotaExhausted()
	}
	return e.StatusCode >= 500
}

// quotaCodes are provider error codes that mean "this account cannot pay for
// this request", as opposed to "slow down".
var quotaCodes = map[string]bool{
	"insufficient_quota":          true,
	"billing_hard_limit_reached":  true,
	"billing_not_active":          true,
	"account_deactivated":         true,
	"credit_balance_too_low":      true,
	"quota_exceeded":              true,
	"insufficient_credits":        true,
	"payment_required":            true,
	"billing_error":               true,
	"permission_denied_billing":   true,
	"insufficient_quota_for_plan": true,
}

// quotaPhrases match the same condition in providers that send no code.
//
// They are deliberately narrow, and only ever about billing or credit. The
// asymmetry matters: misreading a transient failure as permanent breaks
// traffic that would have succeeded on retry, while misreading a permanent
// one merely restores the old behaviour. The word "quota" alone is not
// enough - Google spells its per-minute *rate limit* "Quota exceeded for
// quota metric '... requests per minute'", which is exactly the transient
// case retries exist for.
var quotaPhrases = []string{
	"exceeded your current quota", // OpenAI insufficient_quota
	"insufficient_quota",
	"insufficient quota",
	"credit balance is too low", // Anthropic
	"no credits remaining",
	"plan and billing", // "check your plan and billing details"
	"billing details",
	"billing hard limit",
}

// QuotaExhausted reports whether the error means the account is out of quota
// or credit: a permanent condition that no amount of retrying resolves.
func (e *ProviderError) QuotaExhausted() bool {
	if e == nil {
		return false
	}
	if e.StatusCode == 402 {
		return true
	}
	if quotaCodes[strings.ToLower(strings.TrimSpace(e.Code))] {
		return true
	}
	msg := strings.ToLower(e.Message)
	for _, phrase := range quotaPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// errAs is a generic convenience wrapper around errors.As.
func errAs[T error](err error) (T, bool) {
	var target T
	ok := errors.As(err, &target)
	return target, ok
}
