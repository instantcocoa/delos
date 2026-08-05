// Package conformance holds the gateway's provider conformance suite: a
// record/replay harness that drives the real gateway HTTP surfaces against
// recorded provider responses.
//
// A cassette is a JSON file under cassettes/<provider>/<feature>.json. It
// carries the raw gateway request, the raw upstream provider responses that
// request should meet, and the assertions the gateway's response must satisfy.
// Replay is hermetic: no network, no keys. Recording (DELOS_CONFORMANCE_RECORD=1)
// re-captures the upstream_responses against the live provider APIs, so a
// nightly re-record plus `git diff --exit-code` turns provider drift into a
// failing build rather than a user bug report.
//
// Every translation-layer bug fix should add a cassette.
package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Cassette is one recorded provider interaction plus its expectations.
type Cassette struct {
	// Name is a human-readable label for the scenario.
	Name string `json:"name"`

	// Provider is the backend provider under test: openai, anthropic,
	// gemini, or ollama (an OpenAI-compatible endpoint). It defaults to the
	// containing directory name.
	Provider string `json:"provider,omitempty"`

	// Surface is the gateway API the client speaks: "openai" or "anthropic".
	Surface string `json:"surface"`

	// Endpoint is the gateway path. Defaults to /v1/chat/completions for the
	// openai surface and /v1/messages for the anthropic surface.
	Endpoint string `json:"endpoint,omitempty"`

	// Request is the raw gateway request body, exactly as a client sends it.
	Request json.RawMessage `json:"request"`

	// UpstreamPathContains, when set, asserts the substring appears in the
	// path the gateway called upstream.
	UpstreamPathContains string `json:"upstream_path_contains,omitempty"`

	// UpstreamBodyContains asserts substrings of the request the gateway sent
	// upstream: this is where outbound translation (tools, vision, system
	// prompts) is pinned.
	UpstreamBodyContains []string `json:"upstream_body_contains,omitempty"`

	// UpstreamResponses are replayed in order, one per upstream call. Every
	// queued response must be consumed: retries and failovers are therefore
	// visible in the cassette rather than hidden by the harness.
	UpstreamResponses []UpstreamResponse `json:"upstream_responses"`

	// Expect describes the gateway response the client must observe.
	Expect Expect `json:"expect"`

	// path is the file this cassette was loaded from (not serialized).
	path string
}

// UpstreamResponse is one raw provider HTTP response. SSE bodies carry the
// full event text verbatim.
type UpstreamResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
}

// Expect is the assertion set for a cassette.
type Expect struct {
	Status       int            `json:"status"`
	JSONPaths    map[string]any `json:"json_paths,omitempty"`
	BodyContains []string       `json:"body_contains,omitempty"`
	SSE          *SSEExpect     `json:"sse,omitempty"`
}

// SSEExpect asserts on a streamed gateway response.
type SSEExpect struct {
	// DeltasJoin is the concatenation of every text delta the client sees.
	DeltasJoin string `json:"deltas_join"`
	// Done asserts the stream terminated properly: "data: [DONE]" on the
	// OpenAI surface, a message_stop event on the Anthropic surface. A
	// partial stream must never satisfy this.
	Done bool `json:"done"`
	// EventsContain asserts SSE event names present (Anthropic surface).
	EventsContain []string `json:"events_contain,omitempty"`
}

// Path returns the file the cassette was loaded from.
func (c *Cassette) Path() string { return c.path }

// endpoint resolves the gateway path for the cassette.
func (c *Cassette) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	if c.Surface == "anthropic" {
		return "/v1/messages"
	}
	return "/v1/chat/completions"
}

// model returns the model named in the request.
func (c *Cassette) model() string {
	var probe struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(c.Request, &probe)
	return probe.Model
}

// LoadCassettes reads every cassette under root, sorted by path.
func LoadCassettes(root string) ([]*Cassette, error) {
	var out []*Cassette
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		c, err := LoadCassette(path)
		if err != nil {
			return err
		}
		out = append(out, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// LoadCassette reads and validates a single cassette file.
func LoadCassette(path string) (*Cassette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Cassette
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.path = path
	if c.Provider == "" {
		c.Provider = filepath.Base(filepath.Dir(path))
	}
	if c.Surface == "" {
		c.Surface = "openai"
	}
	if c.Name == "" {
		c.Name = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	if len(c.Request) == 0 {
		return nil, fmt.Errorf("%s: cassette has no request", path)
	}
	return &c, nil
}

// Save writes the cassette back to its file with stable formatting.
func (c *Cassette) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(c.path, data, 0o644)
}

// ---- minimal dotted-path resolver ----

// ResolvePath walks a decoded JSON document by a dotted path. Numeric
// segments index arrays: "choices.0.message.content".
func ResolvePath(doc any, path string) (any, error) {
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, fmt.Errorf("key %q not found", seg)
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil {
				return nil, fmt.Errorf("segment %q is not an array index", seg)
			}
			if idx < 0 || idx >= len(node) {
				return nil, fmt.Errorf("index %d out of range (len %d)", idx, len(node))
			}
			cur = node[idx]
		default:
			return nil, fmt.Errorf("cannot descend into %T at segment %q", cur, seg)
		}
	}
	return cur, nil
}
