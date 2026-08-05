package conformance

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Record mode (DELOS_CONFORMANCE_RECORD=1) re-captures every cassette's
// upstream_responses against the live provider APIs.
//
// Providers hold a plain *http.Client with no injection point, so recording
// works at the network edge instead: the provider is pointed at a local
// reverse proxy that forwards to the real endpoint and captures the raw
// response. No provider internals are touched.

// RecordEnabled reports whether the suite should re-record cassettes.
func RecordEnabled() bool {
	v := os.Getenv("DELOS_CONFORMANCE_RECORD")
	return v == "1" || strings.EqualFold(v, "true")
}

// recordingProxy forwards requests to a live provider and records the raw
// responses in call order. Model-discovery calls (GET .../models) are proxied
// but not recorded: they are an artifact of routing, not of the scenario.
type recordingProxy struct {
	target *url.URL
	client *http.Client

	mu       sync.Mutex
	captured []UpstreamResponse
}

func newRecordingProxy(rawTarget string) (*recordingProxy, error) {
	u, err := url.Parse(rawTarget)
	if err != nil {
		return nil, fmt.Errorf("invalid live base URL %q: %w", rawTarget, err)
	}
	return &recordingProxy{
		target: u,
		client: &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (p *recordingProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	out := *r.URL
	out.Scheme = p.target.Scheme
	out.Host = p.target.Host
	out.Path = strings.TrimSuffix(p.target.Path, "/") + r.URL.Path

	req, err := http.NewRequestWithContext(r.Context(), r.Method, out.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, "Accept-Encoding") {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// Identity encoding keeps recorded bodies readable and diffable.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	discovery := r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models")
	if !discovery {
		p.mu.Lock()
		p.captured = append(p.captured, UpstreamResponse{
			Status:      resp.StatusCode,
			ContentType: resp.Header.Get("Content-Type"),
			Body:        string(respBody),
		})
		p.mu.Unlock()
	}

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (p *recordingProxy) responses() []UpstreamResponse {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]UpstreamResponse(nil), p.captured...)
}

// liveKey returns the configured credential for a provider, if any.
func liveKey(spec providerSpec) string {
	for _, name := range spec.keyEnv {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// liveBase returns the endpoint record mode should proxy to. Ollama's URL is
// configurable; the hosted providers are fixed.
func liveBase(name string, spec providerSpec) string {
	if name == "ollama" {
		if v := os.Getenv("DELOS_RUNTIME_OLLAMA_URL"); v != "" {
			return strings.TrimSuffix(v, "/")
		}
	}
	return spec.liveBaseURL
}

// RecordCassette drives the cassette against the live provider and rewrites
// its upstream_responses. Expectations are left untouched on purpose: if a
// provider changes its wire format, the re-recorded responses stop satisfying
// the authored assertions and the nightly fails.
func RecordCassette(c *Cassette) error {
	spec, ok := providerSpecs[c.Provider]
	if !ok {
		return fmt.Errorf("unknown provider %q", c.Provider)
	}
	key := liveKey(spec)
	if key == "" && c.Provider != "ollama" {
		return errSkipRecording
	}
	if c.Provider == "ollama" && os.Getenv("DELOS_RUNTIME_OLLAMA_URL") == "" {
		return errSkipRecording
	}

	proxy, err := newRecordingProxy(liveBase(c.Provider, spec))
	if err != nil {
		return err
	}
	proxySrv := httptest.NewServer(proxy)
	defer proxySrv.Close()

	gwURL, stop := serveGateway(spec.build(proxySrv.URL, key))
	defer stop()

	if _, err := callGateway(gwURL, c.endpoint(), c.Request, c.Surface); err != nil {
		return fmt.Errorf("live call failed: %w", err)
	}
	captured := proxy.responses()
	if len(captured) == 0 {
		return fmt.Errorf("no upstream responses captured")
	}
	c.UpstreamResponses = captured
	return c.Save()
}

// errSkipRecording marks a provider with no configured credentials.
var errSkipRecording = fmt.Errorf("provider credentials not configured")

// IsSkipRecording reports whether recording was skipped for want of a key.
func IsSkipRecording(err error) bool { return err == errSkipRecording }
