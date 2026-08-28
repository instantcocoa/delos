package runtime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	brdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

var _ Provider = (*BedrockProvider)(nil)

func bedrockTestProvider() *BedrockProvider {
	return &BedrockProvider{
		region:  "us-east-1",
		models:  bedrockModels,
		pricing: bedrockPriceTable(),
	}
}

// docJSON renders a Bedrock document back to JSON for assertions.
func docJSON(t *testing.T, d brdoc.Interface) string {
	t.Helper()
	if d == nil {
		t.Fatal("document is nil")
	}
	raw, err := d.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	return string(raw)
}

func TestBedrockProviderIdentity(t *testing.T) {
	p := bedrockTestProvider()
	if p.Name() != "bedrock" {
		t.Errorf("Name() = %q, want bedrock", p.Name())
	}
	if got := p.Models(t.Context()); len(got) != len(bedrockModels) {
		t.Errorf("Models() returned %d models, want %d", len(got), len(bedrockModels))
	}
}

func TestBedrockPricingCoversEveryModel(t *testing.T) {
	table := bedrockPriceTable()
	for _, m := range bedrockModels {
		if _, ok := table.Rate(m); !ok {
			t.Errorf("model %q has no pricing entry", m)
		}
		// Cross-region inference profiles must price identically to the base
		// model; in most regions they are the only way to reach it.
		if _, ok := table.Rate("us." + m); !ok {
			t.Errorf("cross-region profile us.%s has no pricing entry", m)
		}
	}
}

func TestBedrockCost(t *testing.T) {
	p := bedrockTestProvider()
	tests := []struct {
		name  string
		model string
		usage Usage
		want  float64
	}{
		{
			name:  "sonnet input and output priced separately",
			model: "anthropic.claude-sonnet-4-5-20250929-v1:0",
			usage: Usage{PromptTokens: 1000, CompletionTokens: 1000},
			want:  (1000*3.00 + 1000*15.00) / 1e6,
		},
		{
			name:  "cross-region inference profile prices as the base model",
			model: "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
			usage: Usage{PromptTokens: 1000, CompletionTokens: 1000},
			want:  (1000*3.00 + 1000*15.00) / 1e6,
		},
		{
			name:  "nova lite",
			model: "amazon.nova-lite-v1:0",
			usage: Usage{PromptTokens: 1000},
			want:  0.06 / 1e3,
		},
		{
			name:  "llama has a flat rate both directions",
			model: "meta.llama3-3-70b-instruct-v1:0",
			usage: Usage{PromptTokens: 250, CompletionTokens: 250},
			want:  500 * 0.72 / 1e6,
		},
		{
			name:  "titan embeddings",
			model: "amazon.titan-embed-text-v2:0",
			usage: Usage{PromptTokens: 1000},
			want:  0.02 / 1e3,
		},
		{
			name:  "unknown model bills zero",
			model: "totally-unknown-model",
			usage: Usage{PromptTokens: 1000, CompletionTokens: 1000},
			want:  0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := p.pricing.Cost(tc.model, tc.usage)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Cost(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

// tool_choice "none" is legal on both gateway surfaces and the Converse API
// has no equivalent, so it must suppress the tool configuration rather than
// fail the request — previously it errored, and errored before the "no tools
// declared" check, so even a request with no tools at all failed the whole
// failover chain and surfaced as a 502.
func TestBedrockToolChoiceNone(t *testing.T) {
	cfg, err := bedrockToolConfig(nil, &ToolChoice{Mode: "none"})
	if err != nil {
		t.Fatalf("no tools + none: %v", err)
	}
	if cfg != nil {
		t.Errorf("no tools + none produced %+v, want nil", cfg)
	}

	cfg, err = bedrockToolConfig([]Tool{{Name: "t"}}, &ToolChoice{Mode: "none"})
	if err != nil {
		t.Fatalf("tools + none: %v", err)
	}
	if cfg != nil {
		t.Errorf("tools + none produced %+v, want nil (tools suppressed)", cfg)
	}
}

func TestBedrockUsage(t *testing.T) {
	p := bedrockTestProvider()

	if got := p.usage("amazon.nova-lite-v1:0", nil); got != (Usage{}) {
		t.Errorf("usage(nil) = %+v, want zero", got)
	}

	got := p.usage("amazon.nova-lite-v1:0", &brtypes.TokenUsage{
		InputTokens:  aws.Int32(400),
		OutputTokens: aws.Int32(600),
		TotalTokens:  aws.Int32(1000),
	})
	if got.PromptTokens != 400 || got.CompletionTokens != 600 || got.TotalTokens != 1000 {
		t.Errorf("usage tokens = %+v", got)
	}
	// 400 input at $0.06/1M plus 600 output at $0.24/1M.
	if want := (400*0.06 + 600*0.24) / 1e6; math.Abs(got.CostUSD-want) > 1e-12 {
		t.Errorf("usage cost = %v, want %v", got.CostUSD, want)
	}

	// TotalTokens absent: derived from input + output.
	derived := p.usage("amazon.nova-lite-v1:0", &brtypes.TokenUsage{
		InputTokens:  aws.Int32(3),
		OutputTokens: aws.Int32(4),
	})
	if derived.TotalTokens != 7 {
		t.Errorf("derived total = %d, want 7", derived.TotalTokens)
	}
}

func TestBedrockFinishReason(t *testing.T) {
	tests := map[brtypes.StopReason]string{
		brtypes.StopReasonEndTurn:             FinishStop,
		brtypes.StopReasonStopSequence:        FinishStop,
		brtypes.StopReasonMaxTokens:           FinishLength,
		brtypes.StopReasonToolUse:             FinishToolCalls,
		brtypes.StopReasonContentFiltered:     FinishContentFilter,
		brtypes.StopReasonGuardrailIntervened: FinishContentFilter,
		brtypes.StopReason("something_new"):   FinishStop,
	}
	for in, want := range tests {
		if got := bedrockFinishReason(in); got != want {
			t.Errorf("bedrockFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBedrockImageFormat(t *testing.T) {
	ok := map[string]brtypes.ImageFormat{
		"image/png":  brtypes.ImageFormatPng,
		"image/jpeg": brtypes.ImageFormatJpeg,
		"image/jpg":  brtypes.ImageFormatJpeg,
		"IMAGE/GIF":  brtypes.ImageFormatGif,
		"image/webp": brtypes.ImageFormatWebp,
	}
	for in, want := range ok {
		got, err := bedrockImageFormat(in)
		if err != nil {
			t.Fatalf("bedrockImageFormat(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("bedrockImageFormat(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := bedrockImageFormat("image/tiff"); err == nil {
		t.Error("expected error for unsupported media type")
	}
}

func TestBedrockContentBlockImage(t *testing.T) {
	raw := []byte{0x89, 'P', 'N', 'G'}
	part := ContentPart{
		Type:      "image",
		ImageData: base64.StdEncoding.EncodeToString(raw),
		MediaType: "image/png",
	}
	block, err := bedrockContentBlock(part)
	if err != nil {
		t.Fatalf("bedrockContentBlock: %v", err)
	}
	img, ok := block.(*brtypes.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("got %T, want *ContentBlockMemberImage", block)
	}
	if img.Value.Format != brtypes.ImageFormatPng {
		t.Errorf("format = %q", img.Value.Format)
	}
	src, ok := img.Value.Source.(*brtypes.ImageSourceMemberBytes)
	if !ok {
		t.Fatalf("source is %T, want *ImageSourceMemberBytes", img.Value.Source)
	}
	if string(src.Value) != string(raw) {
		t.Errorf("decoded bytes = %v, want %v", src.Value, raw)
	}
}

func TestBedrockContentBlockErrors(t *testing.T) {
	if _, err := bedrockContentBlock(ContentPart{Type: "image", ImageURL: "https://example.com/a.png"}); err == nil {
		t.Error("expected unsupported error for remote image URL")
	}
	if _, err := bedrockContentBlock(ContentPart{Type: "image", ImageData: "!!not base64!!", MediaType: "image/png"}); err == nil {
		t.Error("expected error for invalid base64")
	}
	if _, err := bedrockContentBlock(ContentPart{Type: "audio"}); err == nil {
		t.Error("expected error for unknown part type")
	}
}

func TestBedrockFromMessagesTextAndSystem(t *testing.T) {
	system, messages, err := bedrockFromMessages([]Message{
		TextMessage("system", "be terse"),
		TextMessage("system", "and polite"),
		TextMessage("user", "hello"),
		TextMessage("assistant", "hi"),
	})
	if err != nil {
		t.Fatalf("bedrockFromMessages: %v", err)
	}
	if len(system) != 2 {
		t.Fatalf("got %d system blocks, want 2", len(system))
	}
	first, ok := system[0].(*brtypes.SystemContentBlockMemberText)
	if !ok || first.Value != "be terse" {
		t.Errorf("system[0] = %#v", system[0])
	}
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	if messages[0].Role != brtypes.ConversationRoleUser || messages[1].Role != brtypes.ConversationRoleAssistant {
		t.Errorf("roles = %q, %q", messages[0].Role, messages[1].Role)
	}
	text, ok := messages[0].Content[0].(*brtypes.ContentBlockMemberText)
	if !ok || text.Value != "hello" {
		t.Errorf("user content = %#v", messages[0].Content[0])
	}
}

func TestBedrockFromMessagesMergesConsecutiveRoles(t *testing.T) {
	_, messages, err := bedrockFromMessages([]Message{
		TextMessage("user", "one"),
		TextMessage("user", "two"),
		TextMessage("assistant", "ack"),
	})
	if err != nil {
		t.Fatalf("bedrockFromMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2 (merged)", len(messages))
	}
	if len(messages[0].Content) != 2 {
		t.Fatalf("merged user message has %d blocks, want 2", len(messages[0].Content))
	}
}

func TestBedrockFromMessagesEmptyContentGetsPlaceholder(t *testing.T) {
	_, messages, err := bedrockFromMessages([]Message{{Role: "user"}})
	if err != nil {
		t.Fatalf("bedrockFromMessages: %v", err)
	}
	if len(messages) != 1 || len(messages[0].Content) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	if _, ok := messages[0].Content[0].(*brtypes.ContentBlockMemberText); !ok {
		t.Errorf("placeholder is %T, want text block", messages[0].Content[0])
	}
}

func TestBedrockFromMessagesToolRoundTrip(t *testing.T) {
	_, messages, err := bedrockFromMessages([]Message{
		TextMessage("user", "weather in SF?"),
		{
			Role:      "assistant",
			ToolCalls: []ToolCall{{ID: "call_1", Name: "get_weather", Arguments: `{"city":"SF"}`}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: []ContentPart{TextPart("62F and foggy")}},
	})
	if err != nil {
		t.Fatalf("bedrockFromMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(messages))
	}

	use, ok := messages[1].Content[0].(*brtypes.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("assistant block is %T, want tool use", messages[1].Content[0])
	}
	if aws.ToString(use.Value.ToolUseId) != "call_1" || aws.ToString(use.Value.Name) != "get_weather" {
		t.Errorf("tool use = %+v", use.Value)
	}
	if got := docJSON(t, use.Value.Input); got != `{"city":"SF"}` {
		t.Errorf("tool input JSON = %s", got)
	}

	if messages[2].Role != brtypes.ConversationRoleUser {
		t.Errorf("tool result role = %q, want user", messages[2].Role)
	}
	result, ok := messages[2].Content[0].(*brtypes.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("tool result block is %T", messages[2].Content[0])
	}
	if aws.ToString(result.Value.ToolUseId) != "call_1" {
		t.Errorf("tool result id = %q", aws.ToString(result.Value.ToolUseId))
	}
	text, ok := result.Value.Content[0].(*brtypes.ToolResultContentBlockMemberText)
	if !ok || text.Value != "62F and foggy" {
		t.Errorf("tool result content = %#v", result.Value.Content[0])
	}
}

func TestBedrockFromMessagesToolCallWithoutArguments(t *testing.T) {
	_, messages, err := bedrockFromMessages([]Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Name: "ping"}}},
	})
	if err != nil {
		t.Fatalf("bedrockFromMessages: %v", err)
	}
	use := messages[0].Content[0].(*brtypes.ContentBlockMemberToolUse)
	if got := docJSON(t, use.Value.Input); got != "{}" {
		t.Errorf("empty arguments became %s, want {}", got)
	}
}

func TestBedrockFromMessagesRejectsInvalidToolArguments(t *testing.T) {
	_, _, err := bedrockFromMessages([]Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Name: "ping", Arguments: "{oops"}}},
	})
	if err == nil {
		t.Error("expected error for invalid tool arguments JSON")
	}
}

func TestBedrockToolConfig(t *testing.T) {
	tools := []Tool{{
		Name:        "get_weather",
		Description: "Look up the weather",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
	}}

	cfg, err := bedrockToolConfig(tools, &ToolChoice{Mode: "auto"})
	if err != nil {
		t.Fatalf("bedrockToolConfig: %v", err)
	}
	if len(cfg.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(cfg.Tools))
	}
	spec, ok := cfg.Tools[0].(*brtypes.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("tool is %T, want *ToolMemberToolSpec", cfg.Tools[0])
	}
	if aws.ToString(spec.Value.Name) != "get_weather" {
		t.Errorf("tool name = %q", aws.ToString(spec.Value.Name))
	}
	if aws.ToString(spec.Value.Description) != "Look up the weather" {
		t.Errorf("tool description = %q", aws.ToString(spec.Value.Description))
	}
	schema, ok := spec.Value.InputSchema.(*brtypes.ToolInputSchemaMemberJson)
	if !ok {
		t.Fatalf("schema is %T", spec.Value.InputSchema)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(docJSON(t, schema.Value)), &decoded); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if decoded["type"] != "object" {
		t.Errorf("schema type = %v", decoded["type"])
	}
	if _, ok := cfg.ToolChoice.(*brtypes.ToolChoiceMemberAuto); !ok {
		t.Errorf("tool choice = %T, want auto", cfg.ToolChoice)
	}
}

func TestBedrockToolConfigChoiceModes(t *testing.T) {
	tools := []Tool{{Name: "t"}}

	cfg, err := bedrockToolConfig(tools, &ToolChoice{Mode: "required"})
	if err != nil {
		t.Fatalf("required: %v", err)
	}
	if _, ok := cfg.ToolChoice.(*brtypes.ToolChoiceMemberAny); !ok {
		t.Errorf("required → %T, want any", cfg.ToolChoice)
	}

	cfg, err = bedrockToolConfig(tools, &ToolChoice{Mode: "tool", Name: "t"})
	if err != nil {
		t.Fatalf("tool: %v", err)
	}
	specific, ok := cfg.ToolChoice.(*brtypes.ToolChoiceMemberTool)
	if !ok {
		t.Fatalf("tool → %T, want specific tool", cfg.ToolChoice)
	}
	if aws.ToString(specific.Value.Name) != "t" {
		t.Errorf("specific tool name = %q", aws.ToString(specific.Value.Name))
	}

	// Missing tools means no tool config at all.
	cfg, err = bedrockToolConfig(nil, nil)
	if err != nil || cfg != nil {
		t.Errorf("bedrockToolConfig(nil, nil) = %v, %v; want nil, nil", cfg, err)
	}

	// A tool declared with no schema still produces a valid object schema.
	cfg, err = bedrockToolConfig(tools, nil)
	if err != nil {
		t.Fatalf("default schema: %v", err)
	}
	spec := cfg.Tools[0].(*brtypes.ToolMemberToolSpec)
	schema := spec.Value.InputSchema.(*brtypes.ToolInputSchemaMemberJson)
	if got := docJSON(t, schema.Value); got != `{"type":"object"}` {
		t.Errorf("default schema = %s", got)
	}
	if cfg.ToolChoice != nil {
		t.Errorf("nil choice produced %T", cfg.ToolChoice)
	}
}

func TestBedrockToolConfigUnsupported(t *testing.T) {
	if _, err := bedrockToolConfig([]Tool{{Name: "t"}}, &ToolChoice{Mode: "tool"}); err == nil {
		t.Error("expected error for tool choice without a name")
	}
	if _, err := bedrockToolConfig([]Tool{{Name: "t"}}, &ToolChoice{Mode: "wat"}); err == nil {
		t.Error("expected error for unknown tool choice mode")
	}
	if _, err := bedrockToolConfig([]Tool{{Name: "t", Parameters: json.RawMessage("{nope")}}, nil); err == nil {
		t.Error("expected error for invalid tool schema")
	}
}

func TestBedrockInferenceConfig(t *testing.T) {
	if cfg := bedrockInferenceConfig(CompletionParams{}); cfg != nil {
		t.Errorf("empty params produced %+v, want nil", cfg)
	}

	temp, topP := 0.25, 0.9
	cfg := bedrockInferenceConfig(CompletionParams{
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   512,
		Stop:        []string{"END"},
	})
	if cfg == nil {
		t.Fatal("cfg is nil")
	}
	if math.Abs(float64(aws.ToFloat32(cfg.Temperature))-0.25) > 1e-6 {
		t.Errorf("temperature = %v", aws.ToFloat32(cfg.Temperature))
	}
	if math.Abs(float64(aws.ToFloat32(cfg.TopP))-0.9) > 1e-6 {
		t.Errorf("topP = %v", aws.ToFloat32(cfg.TopP))
	}
	if aws.ToInt32(cfg.MaxTokens) != 512 {
		t.Errorf("maxTokens = %d", aws.ToInt32(cfg.MaxTokens))
	}
	if len(cfg.StopSequences) != 1 || cfg.StopSequences[0] != "END" {
		t.Errorf("stopSequences = %v", cfg.StopSequences)
	}

	// Temperature 0 is meaningful and must survive as an explicit value.
	zero := 0.0
	cfg = bedrockInferenceConfig(CompletionParams{Temperature: &zero})
	if cfg == nil || cfg.Temperature == nil || aws.ToFloat32(cfg.Temperature) != 0 {
		t.Errorf("zero temperature dropped: %+v", cfg)
	}
}

func TestBedrockConverseInput(t *testing.T) {
	in, err := bedrockConverseInput(CompletionParams{
		Model:    "amazon.nova-pro-v1:0",
		Messages: []Message{TextMessage("system", "sys"), TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("bedrockConverseInput: %v", err)
	}
	if aws.ToString(in.ModelId) != "amazon.nova-pro-v1:0" {
		t.Errorf("modelId = %q", aws.ToString(in.ModelId))
	}
	if len(in.System) != 1 || len(in.Messages) != 1 {
		t.Errorf("system=%d messages=%d", len(in.System), len(in.Messages))
	}
	if in.InferenceConfig != nil || in.ToolConfig != nil {
		t.Errorf("unset params produced configs: %+v %+v", in.InferenceConfig, in.ToolConfig)
	}

	// Empty model falls back to the default.
	in, err = bedrockConverseInput(CompletionParams{Messages: []Message{TextMessage("user", "hi")}})
	if err != nil {
		t.Fatalf("default model: %v", err)
	}
	if aws.ToString(in.ModelId) != bedrockDefaultModel {
		t.Errorf("default modelId = %q, want %q", aws.ToString(in.ModelId), bedrockDefaultModel)
	}
}

func TestBedrockConverseInputRejectsResponseFormat(t *testing.T) {
	_, err := bedrockConverseInput(CompletionParams{
		Messages:       []Message{TextMessage("user", "hi")},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	})
	if err == nil {
		t.Error("expected unsupported error for response_format")
	}
}

func TestBedrockMessageFromBlocks(t *testing.T) {
	msg, err := bedrockMessageFromBlocks([]brtypes.ContentBlock{
		&brtypes.ContentBlockMemberText{Value: "let me check"},
		&brtypes.ContentBlockMemberToolUse{Value: brtypes.ToolUseBlock{
			ToolUseId: aws.String("call_9"),
			Name:      aws.String("get_weather"),
			Input:     brdoc.NewLazyDocument(map[string]any{"city": "SF"}),
		}},
		// Unknown block kinds are dropped, not fatal.
		&brtypes.ContentBlockMemberCachePoint{},
	})
	if err != nil {
		t.Fatalf("bedrockMessageFromBlocks: %v", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("role = %q", msg.Role)
	}
	if msg.Text() != "let me check" {
		t.Errorf("text = %q", msg.Text())
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_9" || tc.Name != "get_weather" {
		t.Errorf("tool call = %+v", tc)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		t.Fatalf("arguments are not valid JSON (%q): %v", tc.Arguments, err)
	}
	if args["city"] != "SF" {
		t.Errorf("arguments = %q", tc.Arguments)
	}
}

func TestBedrockMessageFromBlocksNilToolInput(t *testing.T) {
	msg, err := bedrockMessageFromBlocks([]brtypes.ContentBlock{
		&brtypes.ContentBlockMemberToolUse{Value: brtypes.ToolUseBlock{
			ToolUseId: aws.String("c"),
			Name:      aws.String("ping"),
		}},
	})
	if err != nil {
		t.Fatalf("bedrockMessageFromBlocks: %v", err)
	}
	if msg.ToolCalls[0].Arguments != "{}" {
		t.Errorf("arguments = %q, want {}", msg.ToolCalls[0].Arguments)
	}
}

func TestBedrockError(t *testing.T) {
	if err := bedrockError(nil); err != nil {
		t.Errorf("bedrockError(nil) = %v", err)
	}

	// Plain error: no HTTP status available, so 502.
	var pe *ProviderError
	if !errors.As(bedrockError(errors.New("boom")), &pe) {
		t.Fatal("expected a *ProviderError")
	}
	if pe.Provider != "bedrock" || pe.StatusCode != 502 {
		t.Errorf("fallback error = %+v", pe)
	}

	// Smithy response error: status is carried through.
	respErr := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusTooManyRequests}},
		Err:      errors.New("throttled"),
	}
	if !errors.As(bedrockError(respErr), &pe) {
		t.Fatal("expected a *ProviderError")
	}
	if pe.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", pe.StatusCode)
	}
	if !pe.Retryable() {
		t.Error("429 should be retryable")
	}

	// An existing ProviderError passes through unchanged.
	original := &ProviderError{Provider: "bedrock", StatusCode: 400, Message: "bad"}
	if got := bedrockError(original); got != error(original) {
		t.Errorf("existing ProviderError was rewrapped: %v", got)
	}
}
