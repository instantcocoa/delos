package conformance

import (
	"strings"
	"testing"
)

// Swap parity (D1, note 0001 §"What must NOT change"): the existing
// conformance cassettes must produce the same client-visible contract through
// the old Delos translators and the new Bifrost path - same status, same
// pinned response fields (json_paths/body_contains/sse), same usage mapping.
//
// The suite runs each cassette through both paths and compares. The legacy
// path is runtime.NewLegacyHTTPServer; the Bifrost path is
// runtime.NewBifrostHTTPServer with its env account pointed at the same fake
// upstream (DELOS_BIFROST_<PROVIDER>_BASE_URL + the provider key env). Parity
// is defined as: both paths satisfy the cassette's expectations and return the
// same HTTP status. Byte equality is deliberately NOT asserted - streamed
// responses carry a per-request message id - and the cassette's json_paths
// already pin the usage fields that must not regress.
//
// Cross-provider cassettes (gemini/ollama backends behind the OpenAI surface,
// anthropic<->openai cross_surface) are served by the legacy translator on
// both paths (the Bifrost handlers delegate to it), so they hold trivially;
// the same-provider openai/anthropic cassettes exercise the Bifrost
// passthrough and are the real parity signal.
func TestSwapParity(t *testing.T) {
	cassettes, err := LoadCassettes(cassetteRoot)
	if err != nil {
		t.Fatalf("loading cassettes: %v", err)
	}

	for _, c := range cassettes {
		name := strings.TrimSuffix(mustRel(t, cassetteRoot, c.Path()), ".json")
		t.Run(name, func(t *testing.T) {
			old := runCassettePath(t, c, false)
			bifrost := runCassettePath(t, c, true)
			if old.Status != bifrost.Status {
				t.Errorf("status parity: legacy=%d bifrost=%d\nlegacy body: %s\nbifrost body: %s",
					old.Status, bifrost.Status, truncate(old.Body, 1200), truncate(bifrost.Body, 1200))
			}
		})
	}
}

// runCassettePath runs one cassette through either the legacy translators or
// the Bifrost path, holding it to the cassette's full expectation set, and
// returns the client-visible response.
func runCassettePath(t *testing.T, c *Cassette, bifrost bool) GatewayResponse {
	t.Helper()

	spec, ok := providerSpecs[c.Provider]
	if !ok {
		t.Fatalf("unknown provider %q", c.Provider)
	}

	upstream := newFakeUpstream(c.UpstreamResponses, []string{c.model()})
	upstreamSrv := newLoopbackServer(t, upstream)
	// The legacy provider is registered on both paths: the legacy path serves
	// through it directly, and the Bifrost path delegates cross-provider
	// requests to it while same-provider requests go through Bifrost
	// passthrough (which resolves credentials from the env account).
	provider := spec.build(upstreamSrv, spec.replayKey)

	var gwURL string
	if bifrost {
		setBifrostEnv(t, c.Provider, upstreamSrv)
		var stop func()
		gwURL, stop = serveBifrostService(newService(provider))
		t.Cleanup(stop)
	} else {
		var stop func()
		gwURL, stop = serveGateway(provider)
		t.Cleanup(stop)
	}

	resp, err := callGateway(gwURL, c.endpoint(), c.Request, c.Surface)
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	assertStatus(t, c, resp)
	if bifrost {
		assertBodyBifrost(t, c, resp)
	} else {
		assertBody(t, c, resp)
	}
	assertStream(t, c, resp)
	if bifrost {
		assertUpstreamBifrost(t, c, spec, upstream)
	} else {
		assertUpstream(t, c, spec, upstream)
	}
	return resp
}

// assertBodyBifrost is assertBody for the Bifrost path. Same-provider passthrough
// is byte-faithful to the provider (note 0001), and the legacy translators
// re-emit SSE with alphabetically sorted JSON keys - so a streaming cassette's
// body_contains pins the legacy byte layout, not a semantic contract. The
// semantic stream contract lives in the cassette's sse expectation (deltas,
// done, events), which assertStream still enforces. body_contains is therefore
// skipped for streaming cassettes on the Bifrost side and kept otherwise.
func assertBodyBifrost(t *testing.T, c *Cassette, resp GatewayResponse) {
	t.Helper()
	if c.Expect.SSE == nil {
		for _, want := range c.Expect.BodyContains {
			if !strings.Contains(resp.Body, want) {
				t.Errorf("response body does not contain %q\nbody: %s", want, truncate(resp.Body, 2000))
			}
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

// sameProvider reports whether the cassette is served by Bifrost passthrough
// (same-provider: openai surface over an openai backend, anthropic surface
// over an anthropic backend) rather than the legacy translator.
func sameProvider(c *Cassette) bool {
	return c.Surface == c.Provider
}

// assertUpstreamBifrost holds the Bifrost path to the cassette's upstream
// contract. Same-provider requests are byte-faithful passthrough: the upstream
// body must equal the request the client sent, not the legacy translator's
// re-marshalled form, so the cassette's upstream_body_contains/excludes (which
// pin legacy outbound translation) are skipped there. Cross-provider requests
// delegate to the legacy translator and keep the full body checks.
func assertUpstreamBifrost(t *testing.T, c *Cassette, spec providerSpec, upstream *fakeUpstream) {
	t.Helper()
	consumed, queued, overflow, _ := upstream.stats()
	if overflow > 0 {
		t.Errorf("gateway made %d more upstream call(s) than the cassette records", overflow)
	}
	// Bifrost owns retries now (note 0001), so it may legitimately consume
	// fewer of the cassette's queued responses than the legacy retry loop did;
	// the client-visible status/body is the parity signal, not the call count.
	_ = queued
	_ = consumed

	call, ok := upstream.firstCall()
	if !ok {
		if len(c.UpstreamResponses) > 0 {
			t.Fatal("gateway never called the provider")
		}
		return
	}
	if c.UpstreamPathContains != "" && !strings.Contains(call.Path, c.UpstreamPathContains) {
		t.Errorf("upstream path = %q, want it to contain %q", call.Path, c.UpstreamPathContains)
	}
	if spec.authHeader != "" && spec.replayKey != "" {
		if call.Header.Get(spec.authHeader) == "" {
			t.Errorf("upstream call is missing the %s credential header", spec.authHeader)
		}
	}
	if call.Method != "POST" {
		t.Errorf("upstream method = %s, want POST", call.Method)
	}

	if sameProvider(c) {
		if call.Body != string(c.Request) {
			t.Errorf("passthrough upstream body is not byte-faithful to the request\nwant: %s\ngot:  %s",
				truncate(string(c.Request), 1200), truncate(call.Body, 1200))
		}
		return
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
}

// setBifrostEnv points Bifrost's env account at the fake upstream for the
// cassette's provider. The base URL is the upstream root: Bifrost appends the
// request path (/v1/messages, /v1/chat/completions, ...) itself, mirroring the
// legacy providers which add their own /v1 suffix.
func setBifrostEnv(t *testing.T, provider, upstreamURL string) {
	t.Helper()
	switch provider {
	case "openai":
		t.Setenv("OPENAI_API_KEY", "sk-conformance-openai")
		t.Setenv("DELOS_BIFROST_OPENAI_BASE_URL", upstreamURL)
	case "anthropic":
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-conformance")
		t.Setenv("DELOS_BIFROST_ANTHROPIC_BASE_URL", upstreamURL)
	case "gemini":
		t.Setenv("GEMINI_API_KEY", "conformance-gemini")
		t.Setenv("DELOS_BIFROST_GEMINI_BASE_URL", upstreamURL)
	case "ollama":
		t.Setenv("DELOS_RUNTIME_OLLAMA_URL", upstreamURL)
	default:
		t.Fatalf("no Bifrost env mapping for provider %q", provider)
	}
}
