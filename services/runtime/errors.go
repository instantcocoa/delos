package runtime

import (
	"errors"
	"fmt"
)

// ProviderError carries a backend provider's error verbatim, along with the
// upstream HTTP status. Gateway surfaces map it to an honest error response;
// it must never be swallowed into a generic 500.
type ProviderError struct {
	Provider   string
	StatusCode int
	Message    string
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s: %s (upstream status %d)", e.Provider, e.Message, e.StatusCode)
}

// Retryable reports whether the upstream failure is worth retrying on a
// fallback provider (rate limits and server-side errors).
func (e *ProviderError) Retryable() bool {
	return e.StatusCode == 429 || e.StatusCode >= 500
}

// errAs is a generic convenience wrapper around errors.As.
func errAs[T error](err error) (T, bool) {
	var target T
	ok := errors.As(err, &target)
	return target, ok
}
