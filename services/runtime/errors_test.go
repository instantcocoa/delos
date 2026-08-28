package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestProviderErrorUnwrap(t *testing.T) {
	// A client that hangs up mid-request surfaces as a context.Canceled
	// wrapped by whatever provider code noticed it. Without Unwrap the
	// gateway cannot tell that from an upstream failure, and retries a
	// request nobody is waiting for - on a second, differently-priced
	// provider.
	pe := &ProviderError{
		Provider: "openai", StatusCode: 500,
		Message: "request failed", Wrapped: context.Canceled,
	}
	if !errors.Is(pe, context.Canceled) {
		t.Fatal("errors.Is must see through ProviderError to the wrapped cause")
	}
	if retryable(pe) {
		t.Error("a cancelled caller must never be retried or failed over")
	}
	if retryable(fmt.Errorf("stream aborted: %w", pe)) {
		t.Error("cancellation must be detected through additional wrapping too")
	}
}

func TestProviderErrorRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  *ProviderError
		want bool
	}{
		{"5xx is transient", &ProviderError{StatusCode: 502, Message: "bad gateway"}, true},
		{"400 is the caller's fault", &ProviderError{StatusCode: 400, Message: "bad request"}, false},
		{"404 is final", &ProviderError{StatusCode: 404, Message: "no such model"}, false},
		{
			"429 rate limit is transient",
			&ProviderError{
				StatusCode: 429, Code: "rate_limit_exceeded",
				Message: "Rate limit reached for gpt-4o on tokens per min (TPM): Limit 30000. Please try again in 84ms.",
			},
			true,
		},
		{
			"429 insufficient_quota is permanent",
			&ProviderError{StatusCode: 429, Code: "insufficient_quota", Message: "quota"},
			false,
		},
		{
			// The code is what OpenAI sends; the message is what everyone
			// else sends. Both must be recognized, because retrying an
			// out-of-credit key trips the breaker for every other tenant
			// and replaces the one message the caller needed with
			// "circuit breaker open".
			"429 quota detected from the message alone",
			&ProviderError{
				StatusCode: 429,
				Message:    "You exceeded your current quota, please check your plan and billing details.",
			},
			false,
		},
		{
			"anthropic low credit balance is permanent",
			&ProviderError{
				StatusCode: 429,
				Message:    "Your credit balance is too low to access the Claude API.",
			},
			false,
		},
		{
			// Google spells its per-minute rate limit with the word
			// "quota". It is the transient case retries exist for, and
			// classifying it as permanent would break working traffic.
			"gemini per-minute RESOURCE_EXHAUSTED stays transient",
			&ProviderError{
				StatusCode: 429,
				Message:    "Resource has been exhausted (e.g. check quota). Quota exceeded for quota metric 'Generate Content API requests per minute'.",
			},
			true,
		},
		{"402 is permanent", &ProviderError{StatusCode: 402, Message: "payment required"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Retryable(); got != tc.want {
				t.Errorf("Retryable() = %v, want %v", got, tc.want)
			}
			if got := retryable(tc.err); got != tc.want {
				t.Errorf("retryable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRetryableIsAnAllowlist(t *testing.T) {
	// Translation and validation failures are raised before any request
	// leaves the process. They fail identically on every attempt and every
	// fallback provider, so retrying them only spends time and money - and
	// each attempt counts against the circuit breaker, letting one malformed
	// client request degrade a provider for everyone.
	translation := fmt.Errorf("messages[0]: unsupported content part type %q", "audio")
	if retryable(translation) {
		t.Error("a translation failure must not be retried")
	}
	if retryable(fmt.Errorf("anthropic: response_format is not supported for this backend")) {
		t.Error("an unsupported-capability failure must not be retried")
	}
	if retryable(nil) {
		t.Error("a nil error is not retryable")
	}
}
