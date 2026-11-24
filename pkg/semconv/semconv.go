// Package semconv holds every OpenTelemetry attribute name Delos emits for
// LLM requests.
//
// The OTel GenAI semantic conventions are pre-stable: attribute names still
// move between releases. Every gen_ai.* attribute used anywhere in this repo
// must come from a constant in this file, so that adopting a new spec revision
// is a one-file change and the semconv golden trace tests
// (services/runtime/telemetry_test.go) fail loudly when a name moves.
//
// The version we target is pinned in Version. Do not hand-write "gen_ai.*"
// string literals elsewhere.
package semconv

// Version is the OpenTelemetry semantic conventions release these attribute
// names are taken from. Bumping it means re-checking every constant below and
// consciously regenerating the golden attribute sets in the tests.
const Version = "1.29.0"

// GenAI attributes, per the OTel GenAI semantic conventions at Version.
const (
	// GenAISystem identifies the backend serving the request ("openai",
	// "anthropic", "gemini", "bedrock", ...).
	GenAISystem = "gen_ai.system"
	// GenAIOperationName is the operation performed ("chat", "embeddings").
	GenAIOperationName = "gen_ai.operation.name"

	// Request attributes.
	GenAIRequestModel       = "gen_ai.request.model"
	GenAIRequestMaxTokens   = "gen_ai.request.max_tokens"
	GenAIRequestTemperature = "gen_ai.request.temperature"
	GenAIRequestTopP        = "gen_ai.request.top_p"

	// Response attributes.
	GenAIResponseModel         = "gen_ai.response.model"
	GenAIResponseID            = "gen_ai.response.id"
	GenAIResponseFinishReasons = "gen_ai.response.finish_reasons"

	// Usage attributes.
	GenAIUsageInputTokens  = "gen_ai.usage.input_tokens"
	GenAIUsageOutputTokens = "gen_ai.usage.output_tokens"

	// Cache-token usage attributes. These follow the current (pre-stable)
	// GenAI semantic conventions' flat namespace and match the attribute names
	// Bifrost's otel connector emits, so spans from the Bifrost path join the
	// same queries as the Delos translator spans.
	GenAIUsageCacheReadInputTokens     = "gen_ai.usage.cache_read.input_tokens"
	GenAIUsageCacheCreationInputTokens = "gen_ai.usage.cache_creation.input_tokens"

	// GenAIConversationID is the session/conversation the request belongs to
	// (gen_ai.conversation.id in the current GenAI semantic conventions).
	GenAIConversationID = "gen_ai.conversation.id"

	// Content attributes. These carry prompt and completion text and are
	// only emitted when content capture is explicitly enabled.
	GenAIPrompt     = "gen_ai.prompt"
	GenAICompletion = "gen_ai.completion"
)

// Delos extensions. These are not part of the GenAI conventions; they are
// namespaced under "delos." so they never collide with a future spec name.
const (
	// DelosCostUSD is the computed cost of the request in US dollars.
	DelosCostUSD = "delos.cost_usd"
	// DelosCacheHit reports whether the response came from the gateway's
	// exact-match response cache.
	DelosCacheHit = "delos.cache_hit"
	// DelosRequestID is the gateway-assigned request ID, also returned to
	// the client in the X-Request-Id header.
	DelosRequestID = "delos.request_id"
	// DelosVirtualKey is the *name* of the virtual key that authorized the
	// request. Never the secret, never the hash.
	DelosVirtualKey = "delos.virtual_key"
	// DelosProviderAttempts counts provider attempts made before the
	// request succeeded or finally failed.
	DelosProviderAttempts = "delos.provider_attempts"
	// DelosOrgID is the organisation the request's key belongs to
	// (stamped from the key's metadata in D2).
	DelosOrgID = "delos.org_id"
	// DelosSubject is the subject (user/principal) the request's key acts for
	// (stamped from the key's metadata in D2).
	DelosSubject = "delos.subject"
	// DelosLabels carries the key's labels as a JSON object (stamped from the
	// key's metadata in D2).
	DelosLabels = "delos.labels"
)
