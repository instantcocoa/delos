package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// `delos tail` streams one event per request that passes through the gateway.
// It is the demo feature and the debugging feature: what model was asked for,
// which provider answered, how long it took, what it cost, whether the cache
// answered instead, and which virtual key paid.
//
// The broadcaster is deliberately lossy. A slow subscriber never slows the
// hot path down: a full subscriber buffer drops events and counts the drop.

// TailEvent is one completed gateway request.
type TailEvent struct {
	Time             time.Time `json:"time"`
	RequestID        string    `json:"request_id"`
	Surface          string    `json:"surface"` // chat | embeddings | messages | models
	Model            string    `json:"model"`
	Provider         string    `json:"provider,omitempty"`
	Status           int       `json:"status"`
	LatencyMS        float64   `json:"latency_ms"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	CacheHit         bool      `json:"cache_hit"`
	KeyName          string    `json:"key_name,omitempty"`
	Error            string    `json:"error,omitempty"`

	// KeyID identifies the virtual key that made the request. It is the
	// tenant boundary for /v1/events and is deliberately never serialized:
	// subscribers are filtered by it, they do not get to see it.
	KeyID string `json:"-"`
}

// OK reports whether the request succeeded.
func (e TailEvent) OK() bool { return e.Status > 0 && e.Status < 400 }

const defaultTailRing = 200

// TailBroadcaster fans completed-request events out to live subscribers and
// keeps the most recent events for replay to a subscriber that just attached.
type TailBroadcaster struct {
	mu      sync.Mutex
	ring    []TailEvent
	ringMax int
	subs    map[int]chan TailEvent
	nextID  int
	dropped uint64
}

// NewTailBroadcaster creates a broadcaster retaining ringSize recent events
// (<= 0 uses the default).
func NewTailBroadcaster(ringSize int) *TailBroadcaster {
	if ringSize <= 0 {
		ringSize = defaultTailRing
	}
	return &TailBroadcaster{
		ringMax: ringSize,
		subs:    make(map[int]chan TailEvent),
	}
}

// Publish records an event and delivers it to every subscriber that has room.
func (b *TailBroadcaster) Publish(ev TailEvent) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ring = append(b.ring, ev)
	if len(b.ring) > b.ringMax {
		b.ring = b.ring[len(b.ring)-b.ringMax:]
	}
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.dropped++
		}
	}
}

// Subscribe returns a channel of events and a cancel function that must be
// called to release the subscription. buffer <= 0 uses a small default.
func (b *TailBroadcaster) Subscribe(buffer int) (<-chan TailEvent, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan TailEvent, buffer)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
			close(ch)
		})
	}
}

// Recent returns up to n of the most recent events, oldest first.
func (b *TailBroadcaster) Recent(n int) []TailEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.ring) {
		n = len(b.ring)
	}
	out := make([]TailEvent, n)
	copy(out, b.ring[len(b.ring)-n:])
	return out
}

// Dropped returns the number of events dropped because a subscriber could not
// keep up.
func (b *TailBroadcaster) Dropped() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// Subscribers returns the current subscriber count.
func (b *TailBroadcaster) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// ---- per-request state ----

type tailStateKey struct{}

// tailState accumulates what the handlers learn about a request as it runs.
type tailState struct {
	mu      sync.Mutex
	start   time.Time
	model   string
	emitted bool
}

func withTailState(ctx context.Context, start time.Time) context.Context {
	return context.WithValue(ctx, tailStateKey{}, &tailState{start: start})
}

func tailStateFrom(ctx context.Context) *tailState {
	st, _ := ctx.Value(tailStateKey{}).(*tailState)
	return st
}

// tailModel records the requested model as soon as the body is decoded, so
// that errors raised later still name it.
func (s *HTTPServer) tailModel(ctx context.Context, model string) {
	st := tailStateFrom(ctx)
	if st == nil {
		return
	}
	st.mu.Lock()
	st.model = model
	st.mu.Unlock()
}

// tailSurface names the API surface from the request path.
func tailSurface(path string) string {
	switch {
	case path == "/v1/chat/completions":
		return "chat"
	case path == "/v1/embeddings":
		return "embeddings"
	case path == "/v1/messages":
		return "messages"
	case len(path) >= 10 && path[:10] == "/v1/models":
		return "models"
	default:
		return path
	}
}

// tailEmit publishes one event, at most once per request.
func (s *HTTPServer) tailEmit(r *http.Request, ev TailEvent) {
	if s.tail == nil {
		return
	}
	ctx := r.Context()
	st := tailStateFrom(ctx)
	if st != nil {
		st.mu.Lock()
		if st.emitted {
			st.mu.Unlock()
			return
		}
		st.emitted = true
		if ev.Model == "" {
			ev.Model = st.model
		}
		if !st.start.IsZero() {
			ev.LatencyMS = float64(nowFunc().Sub(st.start).Microseconds()) / 1000.0
		}
		st.mu.Unlock()
	}
	ev.Time = nowFunc().UTC()
	if ev.RequestID == "" {
		ev.RequestID = RequestID(ctx)
	}
	if ev.Surface == "" {
		ev.Surface = tailSurface(r.URL.Path)
	}
	s.tail.Publish(ev)
}

// tailComplete reports a successful request.
func (s *HTTPServer) tailComplete(r *http.Request, key *VirtualKey, model, provider string, usage Usage, cached bool) {
	if s.tail == nil {
		return
	}
	ev := TailEvent{
		Model:            model,
		Provider:         provider,
		Status:           http.StatusOK,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CostUSD:          usage.CostUSD,
		CacheHit:         cached,
	}
	if key != nil {
		ev.KeyName = key.Name
		ev.KeyID = key.ID
	}
	s.tailEmit(r, ev)
}

// tailFailure reports a refused or failed request. It is called from the
// error-envelope writers, so every error response produces exactly one event.
func (s *HTTPServer) tailFailure(r *http.Request, status int, code, msg string) {
	if s.tail == nil {
		return
	}
	detail := msg
	if code != "" {
		detail = code + ": " + msg
	}
	ev := TailEvent{Status: status, Error: detail}
	if key := AuthedKey(r.Context()); key != nil {
		ev.KeyName = key.Name
		ev.KeyID = key.ID
	}
	s.tailEmit(r, ev)
}

// ---- SSE endpoint ----

// handleTailEvents streams TailEvents as Server-Sent Events. `delos tail`
// consumes this; so does `curl -N localhost:8080/v1/events`.
//
// A subscriber sees its own key's requests and nothing else. The event
// carries the model, the provider, the cost and the upstream error text of
// every request it describes, so an unscoped stream handed any valid key a
// live feed of every other tenant's traffic - what they build, who they buy
// from, and what it costs them.
//
// There is no operator-wide view yet: an admin flag belongs on the key record
// (keys.go, plus a migration), so this scopes to the caller's own key and an
// admin view remains a follow-up. Dev mode (no key store configured) has no
// tenants to separate and streams everything.
func (s *HTTPServer) handleTailEvents(w http.ResponseWriter, r *http.Request) {
	if s.tail == nil {
		s.writeOpenAIError(w, r, http.StatusNotFound, "tail_disabled",
			"live request streaming is disabled on this gateway")
		return
	}
	key, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	r = withKey(r, key)
	// An event with no key (a request refused before authentication, or dev
	// traffic) belongs to no tenant, so it reaches no scoped subscriber.
	visible := func(ev TailEvent) bool {
		return key == nil || ev.KeyID == key.ID
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeOpenAIError(w, r, http.StatusInternalServerError, "streaming_unsupported",
			"response writer does not support streaming")
		return
	}

	events, cancel := s.tail.Subscribe(256)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	write := func(ev TailEvent) {
		if !visible(ev) {
			return
		}
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// ?replay=N sends the caller's own events from among the N most recent.
	// N bounds the history scanned, not the number of events delivered.
	if v := r.URL.Query().Get("replay"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			for _, ev := range s.tail.Recent(n) {
				write(ev)
			}
		}
	}

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			write(ev)
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
