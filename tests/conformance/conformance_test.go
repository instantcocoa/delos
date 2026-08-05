package conformance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const cassetteRoot = "cassettes"

// TestCassettes replays every cassette through the real gateway surfaces.
// It is hermetic: the only network is loopback to the fake upstream.
//
// With DELOS_CONFORMANCE_RECORD=1 each cassette is first re-recorded against
// the live provider API (skipped per provider when no key is configured) and
// then replayed, so a re-record that breaks the mapping fails immediately.
func TestCassettes(t *testing.T) {
	cassettes, err := LoadCassettes(cassetteRoot)
	if err != nil {
		t.Fatalf("loading cassettes: %v", err)
	}
	if len(cassettes) == 0 {
		t.Fatal("no cassettes found")
	}

	for _, c := range cassettes {
		name := strings.TrimSuffix(mustRel(t, cassetteRoot, c.Path()), ".json")
		t.Run(name, func(t *testing.T) {
			if RecordEnabled() {
				switch err := RecordCassette(c); {
				case err == nil:
					t.Logf("re-recorded %d upstream response(s)", len(c.UpstreamResponses))
				case IsSkipRecording(err):
					t.Skipf("recording skipped: no credentials for provider %q", c.Provider)
				default:
					t.Fatalf("recording failed: %v", err)
				}
			}
			replay(t, c)
		})
	}
}

// replay drives one cassette against a fake upstream and asserts the client's
// view of the gateway response.
func replay(t *testing.T, c *Cassette) {
	t.Helper()

	spec, ok := providerSpecs[c.Provider]
	if !ok {
		t.Fatalf("unknown provider %q", c.Provider)
	}

	upstream := newFakeUpstream(c.UpstreamResponses, []string{c.model()})
	upstreamSrv := newLoopbackServer(t, upstream)

	provider := spec.build(upstreamSrv, spec.replayKey)
	gwURL, stop := serveGateway(provider)
	t.Cleanup(stop)

	resp, err := callGateway(gwURL, c.endpoint(), c.Request, c.Surface)
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}

	assertStatus(t, c, resp)
	assertBody(t, c, resp)
	assertStream(t, c, resp)
	assertUpstream(t, c, spec, upstream)
}

func assertStatus(t *testing.T, c *Cassette, resp GatewayResponse) {
	t.Helper()
	if c.Expect.Status != 0 && resp.Status != c.Expect.Status {
		t.Fatalf("status = %d, want %d\nbody: %s", resp.Status, c.Expect.Status, truncate(resp.Body, 2000))
	}
}

func assertBody(t *testing.T, c *Cassette, resp GatewayResponse) {
	t.Helper()
	for _, want := range c.Expect.BodyContains {
		if !strings.Contains(resp.Body, want) {
			t.Errorf("response body does not contain %q\nbody: %s", want, truncate(resp.Body, 2000))
		}
	}
	if len(c.Expect.JSONPaths) == 0 {
		return
	}
	doc, err := resp.JSON()
	if err != nil {
		t.Fatalf("%v\nbody: %s", err, truncate(resp.Body, 2000))
	}
	for path, want := range c.Expect.JSONPaths {
		got, err := ResolvePath(doc, path)
		if err != nil {
			t.Errorf("json path %q: %v\nbody: %s", path, err, truncate(resp.Body, 2000))
			continue
		}
		if !jsonEqual(got, want) {
			t.Errorf("json path %q = %v (%T), want %v (%T)", path, got, got, want, want)
		}
	}
}

func assertStream(t *testing.T, c *Cassette, resp GatewayResponse) {
	t.Helper()
	if c.Expect.SSE == nil {
		return
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream\nbody: %s", ct, truncate(resp.Body, 2000))
	}
	view := ReadStream(c.Surface, resp.Body)
	if got := view.Text(); got != c.Expect.SSE.DeltasJoin {
		t.Errorf("stream text = %q, want %q", got, c.Expect.SSE.DeltasJoin)
	}
	if view.Done != c.Expect.SSE.Done {
		t.Errorf("stream done = %v, want %v (a partial stream must never terminate cleanly)", view.Done, c.Expect.SSE.Done)
	}
	for _, want := range c.Expect.SSE.EventsContain {
		if !containsString(view.Events, want) {
			t.Errorf("stream is missing event %q; got %v", want, view.Events)
		}
	}
}

func assertUpstream(t *testing.T, c *Cassette, spec providerSpec, upstream *fakeUpstream) {
	t.Helper()
	consumed, queued, overflow, _ := upstream.stats()
	if overflow > 0 {
		t.Errorf("gateway made %d more upstream call(s) than the cassette records", overflow)
	}
	if consumed != queued {
		t.Errorf("cassette queues %d upstream response(s) but the gateway made %d call(s)", queued, consumed)
	}

	call, ok := upstream.firstCall()
	if !ok {
		t.Fatal("gateway never called the provider")
	}
	if c.UpstreamPathContains != "" && !strings.Contains(call.Path, c.UpstreamPathContains) {
		t.Errorf("upstream path = %q, want it to contain %q", call.Path, c.UpstreamPathContains)
	}
	for _, want := range c.UpstreamBodyContains {
		if !strings.Contains(call.Body, want) {
			t.Errorf("upstream request body does not contain %q\nbody: %s", want, truncate(call.Body, 2000))
		}
	}
	if spec.authHeader != "" && spec.replayKey != "" {
		if call.Header.Get(spec.authHeader) == "" {
			t.Errorf("upstream call is missing the %s credential header", spec.authHeader)
		}
	}
	if call.Method != "POST" {
		t.Errorf("upstream method = %s, want POST", call.Method)
	}
}

// TestMatrixCoverage keeps the suite honest about what it covers: every
// provider in the curated set must exercise the core feature set.
func TestMatrixCoverage(t *testing.T) {
	cassettes, err := LoadCassettes(cassetteRoot)
	if err != nil {
		t.Fatalf("loading cassettes: %v", err)
	}
	byProvider := map[string][]string{}
	for _, c := range cassettes {
		byProvider[c.Provider] = append(byProvider[c.Provider], strings.TrimSuffix(filepath.Base(c.Path()), ".json"))
	}

	required := map[string][]string{
		"openai":    {"chat_basic", "chat_streaming", "tool_call", "error_429", "error_400", "usage_mapping", "embeddings", "vision_data_uri"},
		"anthropic": {"chat_basic", "chat_streaming", "tool_call", "tool_call_streaming", "error_429", "error_400", "usage_mapping", "vision_data_uri"},
		"gemini":    {"chat_basic", "chat_streaming", "tool_call", "error_429", "error_400", "usage_mapping", "embeddings"},
		"ollama":    {"chat_basic", "chat_streaming", "tool_call", "error_400", "usage_mapping"},
	}
	for provider, features := range required {
		have := byProvider[provider]
		for _, f := range features {
			if !containsString(have, f) {
				t.Errorf("provider %q has no %q cassette (have: %v)", provider, f, have)
			}
		}
	}
}

// TestCassettesAreCanonical keeps hand-authored cassettes byte-identical to
// what record mode writes, so a nightly re-record diffs provider drift only,
// never formatting. Run with DELOS_CONFORMANCE_NORMALIZE=1 to rewrite them.
func TestCassettesAreCanonical(t *testing.T) {
	cassettes, err := LoadCassettes(cassetteRoot)
	if err != nil {
		t.Fatalf("loading cassettes: %v", err)
	}
	normalize := os.Getenv("DELOS_CONFORMANCE_NORMALIZE") == "1"
	for _, c := range cassettes {
		before, err := os.ReadFile(c.Path())
		if err != nil {
			t.Fatalf("reading %s: %v", c.Path(), err)
		}
		canonical, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatalf("marshalling %s: %v", c.Path(), err)
		}
		canonical = append(canonical, '\n')
		if string(before) == string(canonical) {
			continue
		}
		if normalize {
			if err := c.Save(); err != nil {
				t.Fatalf("rewriting %s: %v", c.Path(), err)
			}
			t.Logf("normalized %s", c.Path())
			continue
		}
		t.Errorf("%s is not canonically formatted; run DELOS_CONFORMANCE_NORMALIZE=1 go test ./tests/conformance/", c.Path())
	}
}

// ---- helpers ----

// jsonEqual compares a resolved value against a cassette expectation,
// normalizing numeric types.
func jsonEqual(got, want any) bool {
	if reflect.DeepEqual(got, want) {
		return true
	}
	gf, gok := toFloat(got)
	wf, wok := toFloat(want)
	if gok && wok {
		return gf == wf
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// newLoopbackServer starts an httptest server that lives for the test.
func newLoopbackServer(t *testing.T, h http.Handler) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("... (%d bytes truncated)", len(s)-max)
}

func mustRel(t *testing.T, base, path string) string {
	t.Helper()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}
	return filepath.ToSlash(rel)
}
