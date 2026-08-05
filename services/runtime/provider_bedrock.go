package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	bedrockDefaultRegion = "us-east-1"
	bedrockDefaultModel  = "anthropic.claude-sonnet-4-5-20250929-v1:0"
	bedrockEmbedModel    = "amazon.titan-embed-text-v2:0"
)

// bedrockModels is the curated model list this provider serves, in listing order.
var bedrockModels = []string{
	"anthropic.claude-sonnet-4-5-20250929-v1:0",
	"anthropic.claude-haiku-4-5-20251001-v1:0",
	"amazon.nova-pro-v1:0",
	"amazon.nova-lite-v1:0",
	"meta.llama3-3-70b-instruct-v1:0",
	"amazon.titan-embed-text-v2:0",
}

// bedrockPricing is USD per 1K total tokens, blended.
var bedrockPricing = map[string]float64{
	"anthropic.claude-sonnet-4-5-20250929-v1:0": 0.003,
	"anthropic.claude-haiku-4-5-20251001-v1:0":  0.001,
	"amazon.nova-pro-v1:0":                      0.0008,
	"amazon.nova-lite-v1:0":                     0.00006,
	"meta.llama3-3-70b-instruct-v1:0":           0.00072,
	"amazon.titan-embed-text-v2:0":              0.00002,
}

// bedrockAPI is the subset of the Bedrock Runtime client this provider uses.
// Declaring it as an interface keeps the provider testable without AWS.
type bedrockAPI interface {
	Converse(ctx context.Context, in *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	ConverseStream(ctx context.Context, in *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
	InvokeModel(ctx context.Context, in *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error)
}

// BedrockProvider speaks the AWS Bedrock Runtime Converse API through the
// official AWS SDK, which handles SigV4 signing and event-stream decoding.
type BedrockProvider struct {
	client  bedrockAPI
	region  string
	models  []string
	pricing map[string]float64
}

// NewBedrockProvider builds a Bedrock provider from the default AWS credential
// chain (environment, shared config, IMDS, ...). AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY and AWS_SESSION_TOKEN are all picked up by that chain.
func NewBedrockProvider(ctx context.Context, region string) (*BedrockProvider, error) {
	if region == "" {
		region = bedrockDefaultRegion
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("bedrock: loading AWS config: %w", err)
	}
	return &BedrockProvider{
		client:  bedrockruntime.NewFromConfig(cfg),
		region:  region,
		models:  bedrockModels,
		pricing: bedrockPricing,
	}, nil
}

func (p *BedrockProvider) Name() string                        { return "bedrock" }
func (p *BedrockProvider) Models(ctx context.Context) []string { return p.models }

// ---- request translation (pure functions, unit-tested without AWS) ----

// bedrockImageFormat maps an IANA media type onto Bedrock's image format enum.
func bedrockImageFormat(mediaType string) (brtypes.ImageFormat, error) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png", "png":
		return brtypes.ImageFormatPng, nil
	case "image/jpeg", "image/jpg", "jpeg", "jpg":
		return brtypes.ImageFormatJpeg, nil
	case "image/gif", "gif":
		return brtypes.ImageFormatGif, nil
	case "image/webp", "webp":
		return brtypes.ImageFormatWebp, nil
	default:
		return "", fmt.Errorf("bedrock: unsupported image media type %q", mediaType)
	}
}

// bedrockContentBlock converts one internal content part to a Converse block.
func bedrockContentBlock(part ContentPart) (brtypes.ContentBlock, error) {
	switch part.Type {
	case "text":
		return &brtypes.ContentBlockMemberText{Value: part.Text}, nil
	case "image":
		if part.ImageData == "" {
			if part.ImageURL != "" {
				return nil, fmt.Errorf("bedrock: remote image URLs are not supported; supply base64 image data instead")
			}
			return nil, fmt.Errorf("bedrock: image content part has no data")
		}
		format, err := bedrockImageFormat(part.MediaType)
		if err != nil {
			return nil, err
		}
		raw, err := base64.StdEncoding.DecodeString(part.ImageData)
		if err != nil {
			return nil, fmt.Errorf("bedrock: invalid base64 image data: %w", err)
		}
		return &brtypes.ContentBlockMemberImage{Value: brtypes.ImageBlock{
			Format: format,
			Source: &brtypes.ImageSourceMemberBytes{Value: raw},
		}}, nil
	default:
		return nil, fmt.Errorf("bedrock: unsupported content part type %q", part.Type)
	}
}

// bedrockToolInput parses raw JSON arguments into a Bedrock document. Empty
// input becomes an empty object, which is what Bedrock expects for no-arg tools.
func bedrockToolInput(raw string) (brdoc.Interface, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return brdoc.NewLazyDocument(map[string]any{}), nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, fmt.Errorf("bedrock: tool arguments are not valid JSON: %w", err)
	}
	return brdoc.NewLazyDocument(parsed), nil
}

// bedrockFromMessages splits internal messages into Converse system blocks and
// conversation messages. Tool results become tool_result blocks on a user
// message, and consecutive same-role messages are merged because Converse
// requires strictly alternating roles.
func bedrockFromMessages(messages []Message) ([]brtypes.SystemContentBlock, []brtypes.Message, error) {
	var system []brtypes.SystemContentBlock
	var out []brtypes.Message

	appendBlocks := func(role brtypes.ConversationRole, blocks []brtypes.ContentBlock) {
		if len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Content = append(out[len(out)-1].Content, blocks...)
			return
		}
		out = append(out, brtypes.Message{Role: role, Content: blocks})
	}

	for _, m := range messages {
		switch m.Role {
		case "system":
			if text := m.Text(); text != "" {
				system = append(system, &brtypes.SystemContentBlockMemberText{Value: text})
			}

		case "tool":
			result := brtypes.ToolResultBlock{ToolUseId: aws.String(m.ToolCallID)}
			result.Content = []brtypes.ToolResultContentBlock{
				&brtypes.ToolResultContentBlockMemberText{Value: m.Text()},
			}
			appendBlocks(brtypes.ConversationRoleUser, []brtypes.ContentBlock{
				&brtypes.ContentBlockMemberToolResult{Value: result},
			})

		case "assistant":
			var blocks []brtypes.ContentBlock
			for _, part := range m.Content {
				b, err := bedrockContentBlock(part)
				if err != nil {
					return nil, nil, err
				}
				blocks = append(blocks, b)
			}
			for _, tc := range m.ToolCalls {
				input, err := bedrockToolInput(tc.Arguments)
				if err != nil {
					return nil, nil, err
				}
				blocks = append(blocks, &brtypes.ContentBlockMemberToolUse{Value: brtypes.ToolUseBlock{
					ToolUseId: aws.String(tc.ID),
					Name:      aws.String(tc.Name),
					Input:     input,
				}})
			}
			if len(blocks) == 0 {
				blocks = []brtypes.ContentBlock{&brtypes.ContentBlockMemberText{Value: ""}}
			}
			appendBlocks(brtypes.ConversationRoleAssistant, blocks)

		default: // user
			var blocks []brtypes.ContentBlock
			for _, part := range m.Content {
				b, err := bedrockContentBlock(part)
				if err != nil {
					return nil, nil, err
				}
				blocks = append(blocks, b)
			}
			if len(blocks) == 0 {
				blocks = []brtypes.ContentBlock{&brtypes.ContentBlockMemberText{Value: ""}}
			}
			appendBlocks(brtypes.ConversationRoleUser, blocks)
		}
	}
	return system, out, nil
}

// bedrockToolConfig converts tool declarations and the tool choice. It returns
// nil when no tools were declared.
func bedrockToolConfig(tools []Tool, choice *ToolChoice) (*brtypes.ToolConfiguration, error) {
	if choice != nil && choice.Mode == "none" {
		return nil, fmt.Errorf("bedrock: tool_choice %q is not supported by the Converse API", choice.Mode)
	}
	if len(tools) == 0 {
		return nil, nil
	}

	cfg := &brtypes.ToolConfiguration{}
	for _, t := range tools {
		schema := strings.TrimSpace(string(t.Parameters))
		if schema == "" {
			schema = `{"type":"object"}`
		}
		var parsed any
		if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
			return nil, fmt.Errorf("bedrock: tool %q has an invalid JSON Schema: %w", t.Name, err)
		}
		spec := brtypes.ToolSpecification{
			Name:        aws.String(t.Name),
			InputSchema: &brtypes.ToolInputSchemaMemberJson{Value: brdoc.NewLazyDocument(parsed)},
		}
		if t.Description != "" {
			spec.Description = aws.String(t.Description)
		}
		cfg.Tools = append(cfg.Tools, &brtypes.ToolMemberToolSpec{Value: spec})
	}

	if choice != nil {
		switch choice.Mode {
		case "", "auto":
			cfg.ToolChoice = &brtypes.ToolChoiceMemberAuto{}
		case "required":
			cfg.ToolChoice = &brtypes.ToolChoiceMemberAny{}
		case "tool":
			if choice.Name == "" {
				return nil, fmt.Errorf("bedrock: tool_choice mode %q requires a tool name", choice.Mode)
			}
			cfg.ToolChoice = &brtypes.ToolChoiceMemberTool{
				Value: brtypes.SpecificToolChoice{Name: aws.String(choice.Name)},
			}
		default:
			return nil, fmt.Errorf("bedrock: unsupported tool_choice mode %q", choice.Mode)
		}
	}
	return cfg, nil
}

// bedrockInferenceConfig converts sampling parameters; nil when none are set.
func bedrockInferenceConfig(params CompletionParams) *brtypes.InferenceConfiguration {
	cfg := &brtypes.InferenceConfiguration{}
	set := false
	if params.Temperature != nil {
		cfg.Temperature = aws.Float32(float32(*params.Temperature))
		set = true
	}
	if params.TopP != nil {
		cfg.TopP = aws.Float32(float32(*params.TopP))
		set = true
	}
	if params.MaxTokens > 0 {
		cfg.MaxTokens = aws.Int32(int32(params.MaxTokens))
		set = true
	}
	if len(params.Stop) > 0 {
		cfg.StopSequences = params.Stop
		set = true
	}
	if !set {
		return nil
	}
	return cfg
}

// bedrockConverseInput builds the full Converse request from internal params.
func bedrockConverseInput(params CompletionParams) (*bedrockruntime.ConverseInput, error) {
	if params.ResponseFormat != nil {
		return nil, fmt.Errorf("bedrock: response_format is not supported for this backend")
	}
	system, messages, err := bedrockFromMessages(params.Messages)
	if err != nil {
		return nil, err
	}
	toolConfig, err := bedrockToolConfig(params.Tools, params.ToolChoice)
	if err != nil {
		return nil, err
	}
	model := params.Model
	if model == "" {
		model = bedrockDefaultModel
	}
	return &bedrockruntime.ConverseInput{
		ModelId:         aws.String(model),
		Messages:        messages,
		System:          system,
		InferenceConfig: bedrockInferenceConfig(params),
		ToolConfig:      toolConfig,
	}, nil
}

// ---- response translation ----

func bedrockFinishReason(reason brtypes.StopReason) string {
	switch reason {
	case brtypes.StopReasonMaxTokens:
		return FinishLength
	case brtypes.StopReasonToolUse:
		return FinishToolCalls
	case brtypes.StopReasonContentFiltered, brtypes.StopReasonGuardrailIntervened:
		return FinishContentFilter
	default: // end_turn, stop_sequence, and anything newer
		return FinishStop
	}
}

// bedrockMessageFromBlocks converts a Converse output message into the internal
// assistant message shape.
func bedrockMessageFromBlocks(blocks []brtypes.ContentBlock) (Message, error) {
	msg := Message{Role: "assistant"}
	for _, block := range blocks {
		switch b := block.(type) {
		case *brtypes.ContentBlockMemberText:
			msg.Content = append(msg.Content, TextPart(b.Value))
		case *brtypes.ContentBlockMemberToolUse:
			args := "{}"
			if b.Value.Input != nil {
				raw, err := b.Value.Input.MarshalSmithyDocument()
				if err != nil {
					return Message{}, fmt.Errorf("bedrock: encoding tool input: %w", err)
				}
				if len(raw) > 0 {
					args = string(raw)
				}
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:        aws.ToString(b.Value.ToolUseId),
				Name:      aws.ToString(b.Value.Name),
				Arguments: args,
			})
		}
		// Reasoning, citations, and other block kinds are intentionally dropped.
	}
	return msg, nil
}

func (p *BedrockProvider) cost(model string, totalTokens int) float64 {
	rate, ok := p.pricing[model]
	if !ok {
		for m, r := range p.pricing {
			if strings.HasPrefix(model, m) {
				rate = r
				break
			}
		}
	}
	return rate * float64(totalTokens) / 1000
}

func (p *BedrockProvider) usage(model string, tu *brtypes.TokenUsage) Usage {
	if tu == nil {
		return Usage{}
	}
	in := int(aws.ToInt32(tu.InputTokens))
	out := int(aws.ToInt32(tu.OutputTokens))
	total := int(aws.ToInt32(tu.TotalTokens))
	if total == 0 {
		total = in + out
	}
	return Usage{
		PromptTokens:     in,
		CompletionTokens: out,
		TotalTokens:      total,
		CostUSD:          p.cost(model, total),
	}
}

// bedrockError normalizes an SDK error into a ProviderError carrying the
// upstream HTTP status when the SDK exposes one.
func bedrockError(err error) error {
	if err == nil {
		return nil
	}
	var already *ProviderError
	if errors.As(err, &already) {
		return already
	}
	status := 502
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.Response != nil {
		status = respErr.HTTPStatusCode()
	}
	message := err.Error()
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorMessage() != "" {
		message = fmt.Sprintf("%s: %s", apiErr.ErrorCode(), apiErr.ErrorMessage())
	}
	return &ProviderError{Provider: "bedrock", StatusCode: status, Message: message}
}

// ---- Provider implementation ----

func (p *BedrockProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	in, err := bedrockConverseInput(params)
	if err != nil {
		return nil, err
	}
	out, err := p.client.Converse(ctx, in)
	if err != nil {
		return nil, bedrockError(err)
	}

	model := aws.ToString(in.ModelId)
	msg := Message{Role: "assistant"}
	if outputMsg, ok := out.Output.(*brtypes.ConverseOutputMemberMessage); ok {
		msg, err = bedrockMessageFromBlocks(outputMsg.Value.Content)
		if err != nil {
			return nil, err
		}
	}

	requestID, _ := awsmiddleware.GetRequestIDMetadata(out.ResultMetadata)
	return &CompletionResult{
		ID:           requestID,
		Message:      msg,
		FinishReason: bedrockFinishReason(out.StopReason),
		Provider:     p.Name(),
		Model:        model,
		Usage:        p.usage(model, out.Usage),
	}, nil
}

func (p *BedrockProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	converseIn, err := bedrockConverseInput(params)
	if err != nil {
		return nil, err
	}
	in := &bedrockruntime.ConverseStreamInput{
		ModelId:         converseIn.ModelId,
		Messages:        converseIn.Messages,
		System:          converseIn.System,
		InferenceConfig: converseIn.InferenceConfig,
		ToolConfig:      converseIn.ToolConfig,
	}
	out, err := p.client.ConverseStream(ctx, in)
	if err != nil {
		return nil, bedrockError(err)
	}

	model := aws.ToString(in.ModelId)
	id, _ := awsmiddleware.GetRequestIDMetadata(out.ResultMetadata)
	stream := out.GetStream()

	chunks := make(chan StreamChunk)
	go func() {
		defer close(chunks)
		defer stream.Close()

		finish := FinishStop
		var usage *Usage
		// Bedrock indexes content blocks; only tool_use blocks consume a
		// sequential tool-call index, mirroring provider_anthropic.go.
		toolIndexByBlock := map[int32]int{}
		nextToolIndex := 0

		for event := range stream.Events() {
			switch ev := event.(type) {
			case *brtypes.ConverseStreamOutputMemberContentBlockStart:
				start, ok := ev.Value.Start.(*brtypes.ContentBlockStartMemberToolUse)
				if !ok {
					continue
				}
				blockIdx := aws.ToInt32(ev.Value.ContentBlockIndex)
				toolIndexByBlock[blockIdx] = nextToolIndex
				chunks <- StreamChunk{
					ID: id,
					ToolCall: &ToolCallDelta{
						Index: nextToolIndex,
						ID:    aws.ToString(start.Value.ToolUseId),
						Name:  aws.ToString(start.Value.Name),
					},
					Provider: p.Name(),
					Model:    model,
				}
				nextToolIndex++

			case *brtypes.ConverseStreamOutputMemberContentBlockDelta:
				blockIdx := aws.ToInt32(ev.Value.ContentBlockIndex)
				switch delta := ev.Value.Delta.(type) {
				case *brtypes.ContentBlockDeltaMemberText:
					if delta.Value != "" {
						chunks <- StreamChunk{ID: id, Delta: delta.Value, Provider: p.Name(), Model: model}
					}
				case *brtypes.ContentBlockDeltaMemberToolUse:
					fragment := aws.ToString(delta.Value.Input)
					idx, ok := toolIndexByBlock[blockIdx]
					if !ok || fragment == "" {
						continue
					}
					chunks <- StreamChunk{
						ID:       id,
						ToolCall: &ToolCallDelta{Index: idx, ArgumentsDelta: fragment},
						Provider: p.Name(),
						Model:    model,
					}
				}

			case *brtypes.ConverseStreamOutputMemberMessageStop:
				finish = bedrockFinishReason(ev.Value.StopReason)

			case *brtypes.ConverseStreamOutputMemberMetadata:
				u := p.usage(model, ev.Value.Usage)
				usage = &u
			}
		}

		if err := stream.Err(); err != nil {
			chunks <- StreamChunk{ID: id, Err: bedrockError(err), Provider: p.Name(), Model: model}
			return
		}

		chunks <- StreamChunk{
			ID:           id,
			Done:         true,
			FinishReason: finish,
			Usage:        usage,
			Provider:     p.Name(),
			Model:        model,
		}
	}()
	return chunks, nil
}

func (p *BedrockProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := params.Model
	if model == "" {
		model = bedrockEmbedModel
	}
	if model != bedrockEmbedModel {
		return nil, fmt.Errorf("bedrock: embeddings are only supported for %s, got %q", bedrockEmbedModel, model)
	}

	embeddings := make([]Embedding, 0, len(params.Texts))
	promptTokens := 0

	// Titan embeddings are not exposed through Converse; invoke the model
	// directly, one text per call (Titan accepts a single inputText).
	for _, text := range params.Texts {
		body, err := json.Marshal(map[string]any{"inputText": text})
		if err != nil {
			return nil, fmt.Errorf("bedrock: encoding embedding request: %w", err)
		}
		out, err := p.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(model),
			Body:        body,
			ContentType: aws.String("application/json"),
			Accept:      aws.String("application/json"),
		})
		if err != nil {
			return nil, bedrockError(err)
		}
		var decoded struct {
			Embedding           []float32 `json:"embedding"`
			InputTextTokenCount int       `json:"inputTextTokenCount"`
		}
		if err := json.Unmarshal(out.Body, &decoded); err != nil {
			return nil, fmt.Errorf("bedrock: invalid embedding response: %w", err)
		}
		promptTokens += decoded.InputTextTokenCount
		embeddings = append(embeddings, Embedding{
			Values:     decoded.Embedding,
			Dimensions: len(decoded.Embedding),
		})
	}

	return &EmbedResult{
		Embeddings: embeddings,
		Model:      model,
		Provider:   p.Name(),
		Usage: Usage{
			PromptTokens: promptTokens,
			TotalTokens:  promptTokens,
			CostUSD:      p.cost(model, promptTokens),
		},
	}, nil
}
