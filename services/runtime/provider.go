package runtime

import (
	"bufio"
	"context"
	"io"
	"strings"
)

// Provider is the interface a backend LLM provider implements. Adding a
// provider means implementing these five methods and registering the value in
// the gateway's provider registration file; routing, auth, and telemetry code
// never change for a new provider.
type Provider interface {
	// Name returns the provider name (e.g. "openai").
	Name() string

	// Models returns the models this provider serves. Implementations may
	// consult the backend (and should cache); ctx bounds that lookup.
	Models(ctx context.Context) []string

	// Complete performs a completion request.
	Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error)

	// CompleteStream performs a streaming completion request. The returned
	// channel is closed after the final chunk (Done or Err).
	CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error)

	// Embed generates embeddings.
	Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error)
}

// Registry manages available providers.
type Registry struct {
	providers map[string]Provider
	order     []string
}

// NewRegistry creates a new provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds a provider to the registry. Registration order is preserved
// for listings so output is deterministic.
func (r *Registry) Register(p Provider) {
	if _, exists := r.providers[p.Name()]; !exists {
		r.order = append(r.order, p.Name())
	}
	r.providers[p.Name()] = p
}

// Get retrieves a provider by name.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// List returns all registered providers in registration order.
func (r *Registry) List() []Provider {
	providers := make([]Provider, 0, len(r.providers))
	for _, name := range r.order {
		providers = append(providers, r.providers[name])
	}
	return providers
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
