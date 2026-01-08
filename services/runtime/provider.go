package runtime

import (
	"bufio"
	"context"
	"io"
	"strings"
)

// Provider defines the interface for LLM providers.
type Provider interface {
	// Name returns the provider name.
	Name() string

	// Models returns available models.
	Models() []string

	// Available checks if the provider is available.
	Available(ctx context.Context) bool

	// Complete performs a completion request.
	Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error)

	// CompleteStream performs a streaming completion request.
	CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error)

	// Embed generates embeddings.
	Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error)

	// CostPer1KTokens returns the cost per 1K tokens for each model.
	CostPer1KTokens() map[string]float64
}

// Registry manages available providers.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

// Get retrieves a provider by name.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// List returns all registered providers.
func (r *Registry) List() []Provider {
	providers := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		providers = append(providers, p)
	}
	return providers
}

// Available returns all available providers.
func (r *Registry) Available(ctx context.Context) []Provider {
	var available []Provider
	for _, p := range r.providers {
		if p.Available(ctx) {
			available = append(available, p)
		}
	}
	return available
}

// SSEEvent represents a Server-Sent Event.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
	Err   error // Non-nil if there was a parse/read error
}

// ParseSSE reads SSE events from a reader and sends them to the events channel.
// The channel is closed when the reader is exhausted or an error occurs.
func ParseSSE(r io.Reader, events chan<- SSEEvent) {
	defer close(events)

	scanner := bufio.NewScanner(r)
	// Increase buffer size for potentially large JSON payloads
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var event SSEEvent
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = dispatch event
			if len(dataLines) > 0 {
				event.Data = strings.Join(dataLines, "\n")
				events <- event
			}
			event = SSEEvent{}
			dataLines = nil
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "data:") {
			// Handle "data:" without space (some APIs)
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		} else if strings.HasPrefix(line, "event: ") {
			event.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "event:") {
			event.Event = strings.TrimPrefix(line, "event:")
		} else if strings.HasPrefix(line, "id: ") {
			event.ID = strings.TrimPrefix(line, "id: ")
		} else if strings.HasPrefix(line, "id:") {
			event.ID = strings.TrimPrefix(line, "id:")
		}
		// Ignore other lines (comments starting with :, etc.)
	}

	// Send any remaining event
	if len(dataLines) > 0 {
		event.Data = strings.Join(dataLines, "\n")
		events <- event
	}

	// Report scanner errors
	if err := scanner.Err(); err != nil {
		events <- SSEEvent{Err: err}
	}
}

// NDJSONLine represents a line from newline-delimited JSON stream.
type NDJSONLine struct {
	Data string
	Err  error // Non-nil if there was a read error
}

// ParseNDJSON reads newline-delimited JSON from a reader and sends each line to the channel.
// This is used by Ollama which doesn't use SSE format.
func ParseNDJSON(r io.Reader, lines chan<- NDJSONLine) {
	defer close(lines)

	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines <- NDJSONLine{Data: line}
		}
	}

	// Report scanner errors
	if err := scanner.Err(); err != nil {
		lines <- NDJSONLine{Err: err}
	}
}
