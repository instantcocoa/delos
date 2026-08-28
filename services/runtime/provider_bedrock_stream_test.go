package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// The bedrockAPI interface exists so the provider can be driven without AWS,
// and until now nothing used it: the whole ConverseStream event loop —
// including the content-block-index to tool-call-index state machine, which is
// the only place the gateway can mis-associate a tool call's arguments with
// the wrong tool — ran untested.
//
// These tests drive two seams:
//
//   - streamChunks, with a real SDK *ConverseStreamEventStream wired to a fake
//     ConverseStreamOutputReader, so the SDK's own Events/Err/Close plumbing is
//     in the path and only the transport is faked;
//   - bedrockAPI, with a fake client, for the request-translation half.
//
// They are separate because ConverseStreamOutput carries its event stream in
// an unexported field, so a fake client cannot hand one back.

// fakeStreamReader is a ConverseStreamOutputReader replaying a scripted list
// of events, optionally followed by a stream error.
type fakeStreamReader struct {
	events []brtypes.ConverseStreamOutput
	err    error

	ch     chan brtypes.ConverseStreamOutput
	closed chan struct{}
}

func newFakeStreamReader(err error, events ...brtypes.ConverseStreamOutput) *fakeStreamReader {
	r := &fakeStreamReader{
		events: events,
		err:    err,
		ch:     make(chan brtypes.ConverseStreamOutput),
		closed: make(chan struct{}),
	}
	go func() {
		defer close(r.ch)
		for _, ev := range r.events {
			select {
			case r.ch <- ev:
			case <-r.closed:
				return
			}
		}
	}()
	return r
}

func (r *fakeStreamReader) Events() <-chan brtypes.ConverseStreamOutput { return r.ch }
func (r *fakeStreamReader) Err() error                                  { return r.err }
func (r *fakeStreamReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

// bedrockTestStream wraps a scripted reader in the SDK's own event-stream type.
func bedrockTestStream(err error, events ...brtypes.ConverseStreamOutput) *bedrockruntime.ConverseStreamEventStream {
	return bedrockruntime.NewConverseStreamEventStream(func(es *bedrockruntime.ConverseStreamEventStream) {
		es.Reader = newFakeStreamReader(err, events...)
	})
}

// ---- scripted event helpers ----

func blockStartToolUse(blockIndex int32, id, name string) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberContentBlockStart{
		Value: brtypes.ContentBlockStartEvent{
			ContentBlockIndex: aws.Int32(blockIndex),
			Start: &brtypes.ContentBlockStartMemberToolUse{
				Value: brtypes.ToolUseBlockStart{
					ToolUseId: aws.String(id),
					Name:      aws.String(name),
				},
			},
		},
	}
}

func blockStartText(blockIndex int32) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberContentBlockStart{
		Value: brtypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(blockIndex)},
	}
}

func blockDeltaText(blockIndex int32, text string) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(blockIndex),
			Delta:             &brtypes.ContentBlockDeltaMemberText{Value: text},
		},
	}
}

func blockDeltaToolUse(blockIndex int32, fragment string) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(blockIndex),
			Delta: &brtypes.ContentBlockDeltaMemberToolUse{
				Value: brtypes.ToolUseBlockDelta{Input: aws.String(fragment)},
			},
		},
	}
}

func messageStop(reason brtypes.StopReason) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberMessageStop{
		Value: brtypes.MessageStopEvent{StopReason: reason},
	}
}

func metadataEvent(in, out, total int32) brtypes.ConverseStreamOutput {
	return &brtypes.ConverseStreamOutputMemberMetadata{
		Value: brtypes.ConverseStreamMetadataEvent{
			Usage: &brtypes.TokenUsage{
				InputTokens:  aws.Int32(in),
				OutputTokens: aws.Int32(out),
				TotalTokens:  aws.Int32(total),
			},
		},
	}
}

func drain(t *testing.T, ch <-chan StreamChunk) []StreamChunk {
	t.Helper()
	var out []StreamChunk
	for chunk := range ch {
		out = append(out, chunk)
	}
	return out
}

// A realistic stream: text interleaved with two tool blocks, then MessageStop
// and Metadata. Bedrock numbers *content* blocks; only tool_use blocks consume
// a tool-call index, and the mapping between the two is the state machine
// under test.
func TestBedrockStreamInterleavedTextAndTools(t *testing.T) {
	p := bedrockTestProvider()
	stream := bedrockTestStream(nil,
		blockStartText(0),
		blockDeltaText(0, "Let me "),
		blockDeltaText(0, "check both."),
		blockStartToolUse(1, "call-a", "get_weather"),
		blockDeltaToolUse(1, `{"city":`),
		blockDeltaToolUse(1, `"SF"}`),
		blockStartText(2),
		blockDeltaText(2, " and also"),
		blockStartToolUse(3, "call-b", "get_time"),
		blockDeltaToolUse(3, `{"tz":"UTC"}`),
		messageStop(brtypes.StopReasonToolUse),
		metadataEvent(120, 45, 165),
	)

	chunks := drain(t, p.streamChunks(stream, "anthropic.claude-sonnet-4-5-20250929-v1:0", "req-1"))

	var text string
	byIndex := map[int]*ToolCallDelta{}
	var order []int
	for _, c := range chunks {
		if c.Err != nil {
			t.Fatalf("unexpected stream error: %v", c.Err)
		}
		text += c.Delta
		if c.ToolCall == nil {
			continue
		}
		tc := c.ToolCall
		if _, seen := byIndex[tc.Index]; !seen {
			byIndex[tc.Index] = &ToolCallDelta{Index: tc.Index}
			order = append(order, tc.Index)
		}
		agg := byIndex[tc.Index]
		if tc.ID != "" {
			agg.ID = tc.ID
		}
		if tc.Name != "" {
			agg.Name = tc.Name
		}
		agg.ArgumentsDelta += tc.ArgumentsDelta
	}

	if text != "Let me check both. and also" {
		t.Errorf("text = %q", text)
	}
	// Tool indexes are sequential from zero regardless of the content-block
	// index they came from: text blocks 0 and 2 must not consume one.
	if len(order) != 2 || order[0] != 0 || order[1] != 1 {
		t.Fatalf("tool indexes = %v, want [0 1]", order)
	}
	if got := byIndex[0]; got.ID != "call-a" || got.Name != "get_weather" || got.ArgumentsDelta != `{"city":"SF"}` {
		t.Errorf("tool 0 = %+v", got)
	}
	if got := byIndex[1]; got.ID != "call-b" || got.Name != "get_time" || got.ArgumentsDelta != `{"tz":"UTC"}` {
		t.Errorf("tool 1 = %+v", got)
	}

	final := chunks[len(chunks)-1]
	if !final.Done {
		t.Fatalf("last chunk not Done: %+v", final)
	}
	if final.ID != "req-1" || final.Provider != "bedrock" {
		t.Errorf("final identity = %+v", final)
	}
	if final.FinishReason != FinishToolCalls {
		t.Errorf("FinishReason = %q, want %q", final.FinishReason, FinishToolCalls)
	}
	if final.Usage == nil {
		t.Fatal("final chunk carried no usage")
	}
	if final.Usage.PromptTokens != 120 || final.Usage.CompletionTokens != 45 || final.Usage.TotalTokens != 165 {
		t.Errorf("usage = %+v", final.Usage)
	}
	want := (120*3.00 + 45*15.00) / 1e6
	if !closeEnough(final.Usage.CostUSD, want) {
		t.Errorf("CostUSD = %v, want %v", final.Usage.CostUSD, want)
	}
}

// A tool-use delta for a block that never had a start event has no tool index
// to attach to and must be dropped rather than mis-attributed to tool 0.
func TestBedrockStreamOrphanToolDeltaIsDropped(t *testing.T) {
	p := bedrockTestProvider()
	stream := bedrockTestStream(nil,
		blockStartToolUse(0, "call-a", "known"),
		blockDeltaToolUse(0, `{"ok":true}`),
		blockDeltaToolUse(7, `{"orphan":true}`),
		blockDeltaToolUse(0, ""), // empty fragments carry nothing
		messageStop(brtypes.StopReasonToolUse),
	)

	for _, c := range drain(t, p.streamChunks(stream, "amazon.nova-lite-v1:0", "req-2")) {
		if c.ToolCall != nil && c.ToolCall.Index != 0 {
			t.Errorf("orphan delta produced tool index %d", c.ToolCall.Index)
		}
		if c.ToolCall != nil && c.ToolCall.ArgumentsDelta == `{"orphan":true}` {
			t.Error("orphan tool-use delta was forwarded")
		}
	}
}

// A stream that breaks partway must surface the error, and must not also
// deliver a Done chunk that would look like a clean finish.
func TestBedrockStreamErrorIsSurfaced(t *testing.T) {
	p := bedrockTestProvider()
	boom := errors.New("connection reset by peer")
	stream := bedrockTestStream(boom,
		blockStartText(0),
		blockDeltaText(0, "partial"),
	)

	chunks := drain(t, p.streamChunks(stream, "amazon.nova-lite-v1:0", "req-3"))
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	last := chunks[len(chunks)-1]
	if last.Err == nil {
		t.Fatalf("stream error not surfaced; last chunk = %+v", last)
	}
	if last.Done {
		t.Error("a broken stream must not report Done")
	}
	var provErr *ProviderError
	if !errors.As(last.Err, &provErr) {
		t.Fatalf("error type = %T (%v)", last.Err, last.Err)
	}
	if provErr.Provider != "bedrock" || provErr.StatusCode != 502 {
		t.Errorf("provider error = %+v", provErr)
	}
	for _, c := range chunks {
		if c.Done {
			t.Error("Done chunk emitted alongside a stream error")
		}
	}
}

// No Metadata event means no usage figure; the stream still finishes cleanly.
func TestBedrockStreamWithoutMetadata(t *testing.T) {
	p := bedrockTestProvider()
	stream := bedrockTestStream(nil,
		blockDeltaText(0, "hi"),
		messageStop(brtypes.StopReasonEndTurn),
	)
	chunks := drain(t, p.streamChunks(stream, "amazon.nova-lite-v1:0", "req-4"))
	final := chunks[len(chunks)-1]
	if !final.Done || final.FinishReason != FinishStop {
		t.Errorf("final = %+v", final)
	}
	if final.Usage != nil {
		t.Errorf("usage = %+v, want nil when no Metadata event arrived", final.Usage)
	}
}

// ---- fake bedrockAPI: request translation ----

// fakeBedrockClient records what the provider asked the Converse API for.
type fakeBedrockClient struct {
	converseIn  *bedrockruntime.ConverseInput
	converseOut *bedrockruntime.ConverseOutput
	converseErr error

	streamIn  *bedrockruntime.ConverseStreamInput
	streamErr error
}

func (c *fakeBedrockClient) Converse(_ context.Context, in *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	c.converseIn = in
	if c.converseErr != nil {
		return nil, c.converseErr
	}
	return c.converseOut, nil
}

func (c *fakeBedrockClient) ConverseStream(_ context.Context, in *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	c.streamIn = in
	if c.streamErr != nil {
		return nil, c.streamErr
	}
	return &bedrockruntime.ConverseStreamOutput{}, nil
}

func (c *fakeBedrockClient) InvokeModel(_ context.Context, _ *bedrockruntime.InvokeModelInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	return nil, errors.New("not used")
}

var _ bedrockAPI = (*fakeBedrockClient)(nil)

func TestBedrockCompleteThroughFakeClient(t *testing.T) {
	client := &fakeBedrockClient{
		converseOut: &bedrockruntime.ConverseOutput{
			StopReason: brtypes.StopReasonEndTurn,
			Output: &brtypes.ConverseOutputMemberMessage{
				Value: brtypes.Message{
					Role:    brtypes.ConversationRoleAssistant,
					Content: []brtypes.ContentBlock{&brtypes.ContentBlockMemberText{Value: "hello"}},
				},
			},
			Usage: &brtypes.TokenUsage{InputTokens: aws.Int32(11), OutputTokens: aws.Int32(7)},
		},
	}
	p := &BedrockProvider{client: client, region: "us-east-1", models: bedrockModels, pricing: bedrockPriceTable()}

	result, err := p.Complete(context.Background(), CompletionParams{
		Model:    "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if client.converseIn == nil || aws.ToString(client.converseIn.ModelId) != "us.anthropic.claude-sonnet-4-5-20250929-v1:0" {
		t.Fatalf("ConverseInput = %+v", client.converseIn)
	}
	if result.Text() != "hello" || result.FinishReason != FinishStop {
		t.Errorf("result = %+v", result)
	}
	// The cross-region profile prices as the base model, and the total is
	// derived because the SDK reported none.
	if result.Usage.TotalTokens != 18 {
		t.Errorf("TotalTokens = %d, want 18", result.Usage.TotalTokens)
	}
	if want := (11*3.00 + 7*15.00) / 1e6; !closeEnough(result.Usage.CostUSD, want) {
		t.Errorf("CostUSD = %v, want %v", result.Usage.CostUSD, want)
	}
}

// A response with no event stream must be an error, not a nil dereference.
func TestBedrockCompleteStreamWithoutEventStream(t *testing.T) {
	client := &fakeBedrockClient{}
	p := &BedrockProvider{client: client, region: "us-east-1", models: bedrockModels, pricing: bedrockPriceTable()}

	_, err := p.CompleteStream(context.Background(), CompletionParams{
		Model:    "amazon.nova-lite-v1:0",
		Messages: []Message{TextMessage("user", "hi")},
		Tools:    []Tool{{Name: "t"}},
		// tool_choice "none" is legal and must reach the client, not error.
		ToolChoice: &ToolChoice{Mode: "none"},
	})
	if err == nil {
		t.Fatal("expected an error for a response with no event stream")
	}
	if client.streamIn == nil {
		t.Fatal("ConverseStream was never called")
	}
	if client.streamIn.ToolConfig != nil {
		t.Errorf(`tool_choice "none" produced a tool config: %+v`, client.streamIn.ToolConfig)
	}
}
