package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiProvider speaks Google's Generative Language API (the "Gemini API",
// as distinct from Vertex AI). Auth is an API key sent in the x-goog-api-key
// header rather than the ?key= query parameter, so keys never land in logs or
// proxy access records.
type GeminiProvider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	models     []string
	pricing    *PriceTable
}

// geminiPricing is USD per 1M tokens from Google's published price list for
// the Gemini API. Gemini 2.5 Pro is tiered by prompt length ($1.25/$10.00 up
// to 200k tokens, $2.50/$15.00 above); the gateway bills the base tier, so
// very long Pro prompts are under-billed and that is the one remaining known
// gap in USD accounting.
var geminiPricing = map[string]ModelRate{
	"gemini-2.5-pro":        {Input: 1.25, Output: 10.00},
	"gemini-2.5-flash":      {Input: 0.30, Output: 2.50},
	"gemini-2.5-flash-lite": {Input: 0.10, Output: 0.40},
	"gemini-2.0-flash":      {Input: 0.10, Output: 0.40},
	"gemini-2.0-flash-lite": {Input: 0.075, Output: 0.30},
	"gemini-embedding-001":  {Input: 0.15},
	"text-embedding-004":    {Input: 0}, // free tier, priced explicitly so it is not "unpriced"
}

// GeminiOption configures the provider.
type GeminiOption func(*GeminiProvider)

// WithGeminiBaseURL overrides the API base URL (testing and proxies).
func WithGeminiBaseURL(url string) GeminiOption {
	return func(p *GeminiProvider) { p.baseURL = url }
}

// NewGeminiProvider creates the provider for generativelanguage.googleapis.com.
func NewGeminiProvider(apiKey string, opts ...GeminiOption) *GeminiProvider {
	p := &GeminiProvider{
		apiKey:     apiKey,
		baseURL:    geminiBaseURL,
		httpClient: &http.Client{},
		models: []string{
			"gemini-2.5-pro",
			"gemini-2.5-flash",
			"gemini-2.5-flash-lite",
			"gemini-2.0-flash",
			"text-embedding-004",
		},
		pricing: NewPriceTable("gemini", geminiPricing).
			withNormalizer(func(model string) string { return strings.TrimPrefix(model, "models/") }),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *GeminiProvider) Name() string                        { return "gemini" }
func (p *GeminiProvider) Models(ctx context.Context) []string { return p.models }

// ---- wire types (Generative Language API) ----

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inline_data,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

// MarshalJSON emits an empty text part as {"text":""} rather than {}.
//
// `{"role":"user","content":""}` is a legal message on both OpenAI and
// Anthropic, and clients send it (a stub turn, a cleared textarea). With
// `omitempty` alone the part serialized as `{"parts":[{}]}`, which Gemini
// rejects with 400 INVALID_ARGUMENT. Parts that carry inline data or a
// function call keep the omission, since "text" is not theirs to send.
func (p geminiPart) MarshalJSON() ([]byte, error) {
	type wire geminiPart // sheds this method
	if p.InlineData != nil || p.FunctionCall != nil || p.FunctionResponse != nil {
		return json.Marshal(wire(p))
	}
	return json.Marshal(struct {
		Text string `json:"text"`
	}{Text: p.Text})
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"` // "user" | "model"
	Parts []geminiPart `json:"parts"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"` // AUTO | NONE | ANY
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig geminiFunctionCallingConfig `json:"functionCallingConfig"`
}

type geminiGenerationConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	MaxOutputTokens  int             `json:"maxOutputTokens,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
}

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiResponse struct {
	ResponseID    string               `json:"responseId"`
	ModelVersion  string               `json:"modelVersion"`
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
}

// ---- schema scrubbing ----

// Gemini's Schema type is a strict subset of OpenAPI 3.0, not JSON Schema.
// These are the only keys it accepts; everything else is a 400.
var geminiSchemaKeys = map[string]bool{
	"type": true, "format": true, "title": true, "description": true,
	"nullable": true, "enum": true, "maxItems": true, "minItems": true,
	"properties": true, "required": true, "minProperties": true,
	"maxProperties": true, "minLength": true, "maxLength": true,
	"pattern": true, "example": true, "anyOf": true, "propertyOrdering": true,
	"default": true, "items": true, "minimum": true, "maximum": true,
}

// geminiFormats lists the format values Gemini recognises per type. A format
// it does not know (Pydantic emits "uuid", "email", "date") is rejected, so
// unknown formats are dropped rather than forwarded.
var geminiFormats = map[string]map[string]bool{
	"string":  {"date-time": true, "enum": true},
	"number":  {"float": true, "double": true},
	"integer": {"int32": true, "int64": true},
}

// geminiMaxSchemaDepth bounds both nesting and $ref chasing, so a recursive
// schema ($defs entry that references itself) terminates instead of hanging.
const geminiMaxSchemaDepth = 32

// geminiScrubSchema rewrites a JSON Schema into Gemini's dialect.
//
// The previous implementation deleted two keywords ($schema,
// additionalProperties) and passed everything else through, so any
// Pydantic-generated tool schema — which is $ref plus $defs, and typically
// carries oneOf, const and exclusiveMinimum — was rejected with 400. It was
// also position-blind: it matched map keys anywhere, so a schema with a
// property genuinely named "additionalProperties" silently lost that field.
//
// This version works the other way round: resolve $ref against the document's
// $defs, translate what has an equivalent (oneOf to anyOf, const to a
// single-value enum, nullable unions to nullable), then keep only keys Gemini
// documents. Keyword filtering happens at schema positions only; property
// names are data and are never inspected. Anything that fails to parse as JSON
// is returned untouched so the backend, not the gateway, reports the problem.
func geminiScrubSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return raw
	}
	out, err := json.Marshal(geminiScrubNode(root, geminiCollectDefs(root), 0))
	if err != nil {
		return raw
	}
	return out
}

// geminiCollectDefs indexes the document's $defs/definitions by JSON-pointer
// path ("$defs/Address"), which is how $ref names them.
func geminiCollectDefs(root any) map[string]any {
	obj, ok := root.(map[string]any)
	if !ok {
		return nil
	}
	defs := map[string]any{}
	for _, container := range []string{"$defs", "definitions"} {
		members, ok := obj[container].(map[string]any)
		if !ok {
			continue
		}
		for name, schema := range members {
			defs[container+"/"+name] = schema
		}
	}
	return defs
}

func geminiScrubNode(v any, defs map[string]any, depth int) any {
	node, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if depth > geminiMaxSchemaDepth {
		return map[string]any{"type": "object"}
	}
	node = geminiNormalizeNode(node, defs, depth)

	out := make(map[string]any, len(node))
	for key, val := range node {
		switch key {
		case "properties":
			props, ok := val.(map[string]any)
			if !ok {
				continue
			}
			scrubbed := make(map[string]any, len(props))
			for name, sub := range props {
				// Property names are data. A property called
				// "additionalProperties" is a field, not a keyword.
				scrubbed[name] = geminiScrubNode(sub, defs, depth+1)
			}
			out["properties"] = scrubbed
		case "items":
			out["items"] = geminiScrubNode(val, defs, depth+1)
		case "anyOf":
			members, ok := val.([]any)
			if !ok {
				continue
			}
			scrubbed := make([]any, 0, len(members))
			for _, m := range members {
				scrubbed = append(scrubbed, geminiScrubNode(m, defs, depth+1))
			}
			out["anyOf"] = scrubbed
		case "format":
			format, _ := val.(string)
			typ, _ := node["type"].(string)
			if geminiFormats[typ][format] {
				out["format"] = val
			}
		default:
			if geminiSchemaKeys[key] {
				out[key] = val
			}
		}
	}
	return out
}

// geminiNormalizeNode returns a copy of node with $ref inlined and the
// constructs Gemini lacks rewritten into ones it has. It never mutates node.
func geminiNormalizeNode(node map[string]any, defs map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(node))
	for k, v := range node {
		out[k] = v
	}

	// $ref: inline the target, with sibling keys (description, default)
	// overriding it. Chains are followed until they stop or hit the depth cap.
	for hops := 0; hops <= geminiMaxSchemaDepth; hops++ {
		ref, ok := out["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/") {
			break
		}
		target, ok := defs[strings.TrimPrefix(ref, "#/")].(map[string]any)
		if !ok {
			break
		}
		merged := make(map[string]any, len(target)+len(out))
		for k, v := range target {
			merged[k] = v
		}
		for k, v := range out {
			if k != "$ref" {
				merged[k] = v
			}
		}
		out = merged
	}
	delete(out, "$ref")

	// allOf: Gemini has no intersection type. Shallow-merge the members,
	// letting the node's own keys win.
	if members, ok := out["allOf"].([]any); ok {
		merged := make(map[string]any)
		for _, m := range members {
			for k, v := range geminiNormalizeNode(asObject(m), defs, depth+1) {
				merged[k] = v
			}
		}
		for k, v := range out {
			if k != "allOf" {
				merged[k] = v
			}
		}
		out = merged
		delete(out, "allOf")
	}

	// oneOf is anyOf for schema-validation purposes here.
	if _, has := out["anyOf"]; !has {
		if members, ok := out["oneOf"].([]any); ok {
			out["anyOf"] = members
		}
	}
	delete(out, "oneOf")

	// A nullable union — Pydantic's Optional[T] is anyOf:[T, {"type":"null"}]
	// — becomes Gemini's nullable flag. {"type":"null"} on its own is not a
	// type Gemini accepts.
	if members, ok := out["anyOf"].([]any); ok {
		kept := make([]any, 0, len(members))
		nullable := false
		for _, m := range members {
			if typ, _ := asObject(m)["type"].(string); typ == "null" {
				nullable = true
				continue
			}
			kept = append(kept, m)
		}
		if nullable {
			out["nullable"] = true
		}
		switch len(kept) {
		case 0:
			delete(out, "anyOf")
		case 1:
			// A single remaining branch is just that schema.
			delete(out, "anyOf")
			for k, v := range asObject(kept[0]) {
				if _, taken := out[k]; !taken {
					out[k] = v
				}
			}
		default:
			out["anyOf"] = kept
		}
	}

	// type: ["string","null"] is the other spelling of the same thing.
	if types, ok := out["type"].([]any); ok {
		var concrete []string
		for _, t := range types {
			if name, _ := t.(string); name == "null" {
				out["nullable"] = true
			} else if name != "" {
				concrete = append(concrete, name)
			}
		}
		if len(concrete) == 1 {
			out["type"] = concrete[0]
		} else {
			delete(out, "type")
		}
	}

	// const X is an enum of one. Gemini enums are string-only, so a
	// non-string const keeps its value as a default instead.
	if v, ok := out["const"]; ok {
		if str, isStr := v.(string); isStr {
			if _, taken := out["enum"]; !taken {
				out["enum"] = []any{str}
			}
			if _, taken := out["type"]; !taken {
				out["type"] = "string"
			}
		} else if _, taken := out["default"]; !taken {
			out["default"] = v
		}
		delete(out, "const")
	}

	// An object with properties but no declared type is an object.
	if _, hasType := out["type"]; !hasType {
		if _, hasProps := out["properties"]; hasProps {
			out["type"] = "object"
		}
	}
	return out
}

// asObject returns v as a JSON object, or an empty one.
func asObject(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// ---- request translation ----

// geminiFromMessages converts internal messages into (systemInstruction,
// contents). Gemini keys function results by tool *name*, not by call ID, so
// the ID->name mapping is recovered from the assistant tool calls earlier in
// the conversation.
func geminiFromMessages(messages []Message) (*geminiContent, []geminiContent, error) {
	var system *geminiContent
	var contents []geminiContent
	toolNameByID := map[string]string{}

	appendParts := func(role string, parts []geminiPart) {
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, parts...)
			return
		}
		contents = append(contents, geminiContent{Role: role, Parts: parts})
	}

	for _, m := range messages {
		switch m.Role {
		case "system":
			parts, err := geminiPartsFromContent(m.Content)
			if err != nil {
				return nil, nil, err
			}
			if system == nil {
				system = &geminiContent{}
			}
			system.Parts = append(system.Parts, parts...)

		case "tool":
			name := toolNameByID[m.ToolCallID]
			if name == "" {
				name = m.Name
			}
			if name == "" {
				return nil, nil, fmt.Errorf("gemini: cannot map tool result for call %q to a function name; gemini keys function responses by name and no prior assistant tool call declared it", m.ToolCallID)
			}
			appendParts("user", []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{
					Name:     name,
					Response: geminiToolResponsePayload(m.Text()),
				},
			}})

		case "assistant":
			parts, err := geminiPartsFromContent(m.Content)
			if err != nil {
				return nil, nil, err
			}
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					toolNameByID[tc.ID] = tc.Name
				}
				args := json.RawMessage(strings.TrimSpace(tc.Arguments))
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				if !json.Valid(args) {
					return nil, nil, fmt.Errorf("gemini: tool call %q has invalid JSON arguments", tc.Name)
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{Name: tc.Name, Args: args},
				})
			}
			if len(parts) == 0 {
				parts = []geminiPart{{Text: ""}}
			}
			appendParts("model", parts)

		default: // user
			parts, err := geminiPartsFromContent(m.Content)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) == 0 {
				parts = []geminiPart{{Text: ""}}
			}
			appendParts("user", parts)
		}
	}
	return system, contents, nil
}

func geminiPartsFromContent(content []ContentPart) ([]geminiPart, error) {
	parts := make([]geminiPart, 0, len(content))
	for _, part := range content {
		switch part.Type {
		case "text":
			parts = append(parts, geminiPart{Text: part.Text})
		case "image":
			if part.ImageData == "" {
				return nil, fmt.Errorf("gemini: remote image URLs are not supported (%q); fetch the image client-side and pass base64 image data", part.ImageURL)
			}
			mime := part.MediaType
			if mime == "" {
				mime = "image/png"
			}
			parts = append(parts, geminiPart{
				InlineData: &geminiInlineData{MimeType: mime, Data: part.ImageData},
			})
		default:
			return nil, fmt.Errorf("unsupported content part type %q", part.Type)
		}
	}
	return parts, nil
}

// geminiToolResponsePayload wraps a tool result for functionResponse.response,
// which must be a JSON object. Results that already are one pass through.
func geminiToolResponsePayload(text string) json.RawMessage {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	payload, err := json.Marshal(map[string]string{"result": text})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return payload
}

func (p *GeminiProvider) buildRequest(params CompletionParams) (*geminiRequest, error) {
	system, contents, err := geminiFromMessages(params.Messages)
	if err != nil {
		return nil, err
	}
	// `"contents": null` is rejected outright, and a conversation that is
	// nothing but system messages produces exactly that. Send an empty user
	// turn instead, which is the closest legal request.
	if len(contents) == 0 {
		contents = []geminiContent{{Role: "user", Parts: []geminiPart{{Text: ""}}}}
	}
	req := &geminiRequest{Contents: contents, SystemInstruction: system}

	if len(params.Tools) > 0 {
		decls := make([]geminiFunctionDeclaration, 0, len(params.Tools))
		for _, t := range params.Tools {
			decls = append(decls, geminiFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  geminiScrubSchema(t.Parameters),
			})
		}
		req.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}

	if tc := params.ToolChoice; tc != nil {
		cfg := geminiFunctionCallingConfig{}
		switch tc.Mode {
		case "auto":
			cfg.Mode = "AUTO"
		case "none":
			cfg.Mode = "NONE"
		case "required":
			cfg.Mode = "ANY"
		case "tool":
			cfg.Mode = "ANY"
			cfg.AllowedFunctionNames = []string{tc.Name}
		}
		if cfg.Mode != "" {
			req.ToolConfig = &geminiToolConfig{FunctionCallingConfig: cfg}
		}
	}

	gc := geminiGenerationConfig{
		Temperature:     params.Temperature,
		TopP:            params.TopP,
		MaxOutputTokens: params.MaxTokens,
		StopSequences:   params.Stop,
	}
	if rf := params.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_object":
			gc.ResponseMimeType = "application/json"
		case "json_schema":
			gc.ResponseMimeType = "application/json"
			gc.ResponseSchema = geminiScrubSchema(rf.Schema)
		}
	}
	if gc.Temperature != nil || gc.TopP != nil || gc.MaxOutputTokens > 0 ||
		len(gc.StopSequences) > 0 || gc.ResponseMimeType != "" {
		req.GenerationConfig = &gc
	}
	return req, nil
}

// ---- transport ----

// geminiModelPath renders a bare model name ("gemini-2.5-flash") as the
// resource path segment the API expects ("models/gemini-2.5-flash").
func geminiModelPath(model string) string {
	return "models/" + strings.TrimPrefix(model, "models/")
}

func (p *GeminiProvider) post(ctx context.Context, path string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("x-goog-api-key", p.apiKey)
	}
	return p.httpClient.Do(req)
}

func (p *GeminiProvider) apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		return &ProviderError{Provider: "gemini", StatusCode: resp.StatusCode, Message: envelope.Error.Message}
	}
	return &ProviderError{Provider: "gemini", StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}

// usageFrom converts usage metadata, deriving the total when the backend
// omits it and pricing input and output separately.
func (p *GeminiProvider) usageFrom(model string, md geminiUsageMetadata) Usage {
	u := Usage{
		PromptTokens:     md.PromptTokenCount,
		CompletionTokens: md.CandidatesTokenCount,
		TotalTokens:      md.TotalTokenCount,
	}.withDerivedTotals()
	u.CostUSD = p.pricing.Cost(model, u)
	return u
}

func geminiFinishReason(reason string) string {
	switch reason {
	case "MAX_TOKENS":
		return FinishLength
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION":
		return FinishContentFilter
	default:
		return FinishStop
	}
}

// geminiToolCallID synthesizes an ID for a tool call: the API returns function
// calls without one, but every internal consumer keys results by ID.
func geminiToolCallID(index int) string {
	return fmt.Sprintf("call_%d", index+1)
}

// ---- Provider implementation ----

func (p *GeminiProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	req, err := p.buildRequest(params)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, "/"+geminiModelPath(params.Model)+":generateContent", req)
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.apiError(resp)
	}

	var out geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gemini: invalid response: %w", err)
	}
	if len(out.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: response contained no candidates")
	}

	candidate := out.Candidates[0]
	msg := Message{Role: "assistant"}
	for _, part := range candidate.Content.Parts {
		switch {
		case part.FunctionCall != nil:
			args := string(part.FunctionCall.Args)
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:        geminiToolCallID(len(msg.ToolCalls)),
				Name:      part.FunctionCall.Name,
				Arguments: args,
			})
		case part.Text != "":
			msg.Content = append(msg.Content, TextPart(part.Text))
		}
	}

	finish := geminiFinishReason(candidate.FinishReason)
	if len(msg.ToolCalls) > 0 {
		finish = FinishToolCalls
	}

	model := out.ModelVersion
	if model == "" {
		model = params.Model
	}
	result := &CompletionResult{
		ID:           out.ResponseID,
		Message:      msg,
		FinishReason: finish,
		Provider:     "gemini",
		Model:        model,
	}
	if out.UsageMetadata != nil {
		result.Usage = p.usageFrom(params.Model, *out.UsageMetadata)
	}
	return result, nil
}

func (p *GeminiProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	req, err := p.buildRequest(params)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, "/"+geminiModelPath(params.Model)+":streamGenerateContent?alt=sse", req)
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, p.apiError(resp)
	}

	chunks := make(chan StreamChunk)
	go func() {
		defer close(chunks)
		defer resp.Body.Close()

		events := make(chan SSEEvent)
		go ParseSSE(resp.Body, events)

		id, model := "", params.Model
		finish := FinishStop
		sawToolCall := false
		nextToolIndex := 0
		var usage *Usage

		for event := range events {
			if event.Err != nil {
				chunks <- StreamChunk{Err: fmt.Errorf("gemini stream error: %w", event.Err), Provider: "gemini", Model: model}
				return
			}
			data := strings.TrimSpace(event.Data)
			if data == "" {
				continue
			}
			var ev geminiResponse
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				chunks <- StreamChunk{Err: fmt.Errorf("gemini stream: invalid chunk: %w", err), Provider: "gemini", Model: model}
				return
			}
			if ev.ResponseID != "" {
				id = ev.ResponseID
			}
			if ev.ModelVersion != "" {
				model = ev.ModelVersion
			}
			if ev.UsageMetadata != nil {
				u := p.usageFrom(params.Model, *ev.UsageMetadata)
				usage = &u
			}
			if len(ev.Candidates) == 0 {
				continue
			}
			candidate := ev.Candidates[0]
			if candidate.FinishReason != "" {
				finish = geminiFinishReason(candidate.FinishReason)
			}
			for _, part := range candidate.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					// Gemini delivers each function call whole in a single
					// event, so one delta carries the complete arguments.
					args := string(part.FunctionCall.Args)
					if args == "" {
						args = "{}"
					}
					chunks <- StreamChunk{
						ID: id,
						ToolCall: &ToolCallDelta{
							Index:          nextToolIndex,
							ID:             geminiToolCallID(nextToolIndex),
							Name:           part.FunctionCall.Name,
							ArgumentsDelta: args,
						},
						Provider: "gemini",
						Model:    model,
					}
					nextToolIndex++
					sawToolCall = true
				case part.Text != "":
					chunks <- StreamChunk{ID: id, Delta: part.Text, Provider: "gemini", Model: model}
				}
			}
		}

		if sawToolCall {
			finish = FinishToolCalls
		}
		chunks <- StreamChunk{ID: id, Done: true, FinishReason: finish, Usage: usage, Provider: "gemini", Model: model}
	}()
	return chunks, nil
}

func (p *GeminiProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := geminiModelPath(params.Model)
	requests := make([]map[string]any, 0, len(params.Texts))
	for _, text := range params.Texts {
		requests = append(requests, map[string]any{
			"model": model,
			"content": map[string]any{
				"parts": []map[string]string{{"text": text}},
			},
		})
	}

	resp, err := p.post(ctx, "/"+model+":batchEmbedContents", map[string]any{"requests": requests})
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.apiError(resp)
	}

	var out struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gemini: invalid response: %w", err)
	}

	result := &EmbedResult{Model: params.Model, Provider: "gemini"}
	for _, e := range out.Embeddings {
		result.Embeddings = append(result.Embeddings, Embedding{Values: e.Values, Dimensions: len(e.Values)})
	}
	return result, nil
}
