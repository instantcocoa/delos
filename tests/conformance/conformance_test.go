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
		// A cassette that queues no upstream responses asserts exactly
		// this: the gateway rejected the request on its own, without
		// spending an upstream call on a request it already knew would
		// fail.
		if len(c.UpstreamResponses) > 0 {
			t.Fatal("gateway never called the provider")
		}
		return
	}
	if c.UpstreamPathContains != "" && !strings.Contains(call.Path, c.UpstreamPathContains) {
		t.Errorf("upstream path = %q, want it to contain %q", call.Path, c.UpstreamPathContains)
	}
	for _, want := range c.UpstreamBodyContains {
		if !strings.Contains(call.Body, want) {
			t.Errorf("upstream request body does not contain %q\nbody: %s", want, truncate(call.Body, 2000))
		}
	}
	for _, unwanted := range c.UpstreamBodyExcludes {
		if strings.Contains(call.Body, unwanted) {
			t.Errorf("upstream request body contains %q, which the client never sent\nbody: %s", unwanted, truncate(call.Body, 2000))
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

// ---- the target matrix ----

// targetFeatures is what the conformance suite is *aiming* to prove for every
// backend in the curated set, not a description of what happens to exist. A
// cell is covered when the provider has a cassette whose file name starts with
// the feature name, so response_format_json_object covers "response_format".
var targetFeatures = []string{
	"chat_basic",
	"chat_streaming",
	"tool_call",
	"tool_call_streaming",
	"usage_mapping",
	"error_400",
	"error_429",
	"vision_data_uri",
	"embeddings",
	"stop_sequences",
	"response_format",
	"max_completion_tokens",
	"cross_surface",
}

// targetProviders is the provider set the matrix applies to. It is listed
// explicitly so that adding a backend to cassettes/ without deciding what it
// must cover fails the build.
var targetProviders = []string{"openai", "anthropic", "gemini", "ollama"}

// exemptions record cells the suite deliberately leaves uncovered, each with
// the reason. An exemption is a stated debt, not a free pass: an exempt cell
// that later gains a cassette fails this test, so the note has to be deleted
// when the gap is closed.
var exemptions = map[string]string{
	// Real capability limits.
	"anthropic/embeddings": "the Anthropic API has no embeddings endpoint",
	"ollama/embeddings":    "the OpenAI-compatible slot is exercised for chat; /v1/embeddings support varies per server",
	"ollama/error_429":     "local runtimes do not rate limit; the shared 429 path is pinned on openai and gemini",

	// Recording debt: covered elsewhere or simply not recorded yet.
	"openai/tool_call_streaming":   "TODO: not recorded; streamed tool-call assembly is pinned on anthropic and in services/runtime unit tests",
	"gemini/tool_call_streaming":   "TODO: not recorded",
	"gemini/vision_data_uri":       "TODO: not recorded",
	"gemini/stop_sequences":        "TODO: not recorded",
	"gemini/response_format":       "TODO: not recorded; Gemini expresses structured output as responseSchema, which needs its own mapping cassette",
	"gemini/max_completion_tokens": "TODO: not recorded",
	"gemini/cross_surface":         "TODO: not recorded; /v1/messages over a Gemini backend is untested",
	"ollama/tool_call_streaming":   "TODO: not recorded",
	"ollama/vision_data_uri":       "TODO: not recorded",
	"ollama/stop_sequences":        "TODO: not recorded",
	"ollama/response_format":       "TODO: not recorded",
	"ollama/max_completion_tokens": "TODO: not recorded",
	"ollama/cross_surface":         "TODO: not recorded",
}

// TestMatrixCoverage holds the suite to the target matrix above. It used to
// assert exactly the cassettes that existed, which meant it could never fail;
// the point of a matrix is that the cells nobody has filled in are visible.
func TestMatrixCoverage(t *testing.T) {
	cassettes, err := LoadCassettes(cassetteRoot)
	if err != nil {
		t.Fatalf("loading cassettes: %v", err)
	}
	byProvider := map[string][]string{}
	for _, c := range cassettes {
		byProvider[c.Provider] = append(byProvider[c.Provider], strings.TrimSuffix(filepath.Base(c.Path()), ".json"))
	}

	// Every provider with cassettes must be a provider the matrix covers.
	for provider := range byProvider {
		if !containsString(targetProviders, provider) {
			t.Errorf("provider %q has cassettes but is not in the target matrix; add it (and its exemptions)", provider)
		}
	}

	covered, exempt := 0, 0
	for _, provider := range targetProviders {
		have := byProvider[provider]
		for _, feature := range targetFeatures {
			cell := provider + "/" + feature
			hit := hasFeature(have, feature)
			reason, isExempt := exemptions[cell]
			switch {
			case hit && isExempt:
				t.Errorf("%s is covered but still listed as exempt (%q); delete the exemption", cell, reason)
			case hit:
				covered++
			case isExempt:
				exempt++
			default:
				t.Errorf("%s has no cassette and no exemption (have: %v)", cell, have)
			}
		}
	}

	// Exemptions for cells outside the matrix are dead notes.
	for cell := range exemptions {
		provider, feature, _ := strings.Cut(cell, "/")
		if !containsString(targetProviders, provider) || !containsString(targetFeatures, feature) {
			t.Errorf("exemption %q does not name a cell of the matrix", cell)
		}
	}

	total := len(targetProviders) * len(targetFeatures)
	t.Logf("matrix coverage: %d/%d cells (%d exempt, %d unaccounted)",
		covered, total, exempt, total-covered-exempt)
}

// hasFeature reports whether any cassette name covers the feature. A name
// covers a feature when it equals it or extends it with an underscore
// ("response_format_json_schema" covers "response_format"), never when it
// merely shares a prefix ("error_400" does not cover "error_4").
func hasFeature(names []string, feature string) bool {
	for _, name := range names {
		if name == feature || strings.HasPrefix(name, feature+"_") {
			return true
		}
	}
	return false
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
