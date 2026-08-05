package observe

// OpenTelemetry gen_ai.* semantic-convention attribute names that observe
// promotes out of the attribute bag into typed columns.
//
// TODO: switch to pkg/semconv once it lands. The gateway owns emission; these
// constants exist so observe can read spans from ANY gen_ai emitter (OTel SDK
// instrumentation, Langfuse, another gateway) without depending on gateway code.
// The conventions are pre-stable; keep this list in sync with the pinned
// semconv version when it is bumped.
const (
	AttrGenAISystem        = "gen_ai.system"
	AttrGenAIRequestModel  = "gen_ai.request.model"
	AttrGenAIResponseModel = "gen_ai.response.model"
	AttrGenAIInputTokens   = "gen_ai.usage.input_tokens"
	AttrGenAIOutputTokens  = "gen_ai.usage.output_tokens"

	// AttrDelosCostUSD is the computed request cost in US dollars. There is no
	// standardised gen_ai cost attribute yet, so this is a Delos extension --
	// spans without it simply report zero cost.
	AttrDelosCostUSD = "delos.cost_usd"

	// AttrServiceName is the resource attribute carrying the emitting service.
	AttrServiceName = "service.name"
)

// Deprecated gen_ai attribute spellings that older instrumentation still
// emits. Read-only fallbacks: never emit these.
const (
	attrGenAIPromptTokens     = "gen_ai.usage.prompt_tokens"
	attrGenAICompletionTokens = "gen_ai.usage.completion_tokens"
	attrLLMRequestModel       = "llm.request.model"
	attrLLMResponseModel      = "llm.response.model"
)

// genAIInputTokenKeys lists input-token attribute names in priority order.
var genAIInputTokenKeys = []string{AttrGenAIInputTokens, attrGenAIPromptTokens}

// genAIOutputTokenKeys lists output-token attribute names in priority order.
var genAIOutputTokenKeys = []string{AttrGenAIOutputTokens, attrGenAICompletionTokens}

// genAIRequestModelKeys lists request-model attribute names in priority order.
var genAIRequestModelKeys = []string{AttrGenAIRequestModel, attrLLMRequestModel}

// genAIResponseModelKeys lists response-model attribute names in priority order.
var genAIResponseModelKeys = []string{AttrGenAIResponseModel, attrLLMResponseModel}
