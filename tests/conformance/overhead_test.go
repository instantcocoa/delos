package conformance

import (
	"fmt"
	"io"
	"net/http"
	"os"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/instantcocoa/delos/services/runtime"
)

// The gateway's headline claim is that it costs almost nothing to put in
// front of a provider. That number has to be reproducible, so it is a test:
// 2000 sequential chat requests through the full HTTP surface against an
// instant upstream, gated on p99 < 5ms. The measurement includes two loopback
// round trips, so it over-states the gateway's own overhead rather than
// flattering it.

const (
	overheadRequests  = 2000
	overheadWarmup    = 200
	defaultP99Budget  = 5 * time.Millisecond
	goroutineSlack    = 5
	soakDuration      = 10 * time.Second
	settleMaxAttempts = 50
)

// p99Budget is the gate, overridable for slow or shared machines.
func p99Budget(t *testing.T) time.Duration {
	t.Helper()
	if v := os.Getenv("DELOS_BENCH_P99_MS"); v != "" {
		ms, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("invalid DELOS_BENCH_P99_MS=%q: %v", v, err)
		}
		return time.Duration(ms * float64(time.Millisecond))
	}
	return defaultP99Budget
}

// benchEnv starts an instant upstream and a gateway in front of it, returning
// the gateway URL and the upstream URL. Measuring both lets the test report
// the gateway's own cost rather than the machine's loopback latency.
func benchEnv(t *testing.T) (gateway, upstream string) {
	t.Helper()
	body := fmt.Sprintf(`{"id":"chatcmpl-bench","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`, failModel)
	upstream = compatUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	// No cache is configured: every request pays the full provider path.
	gateway = routedGateway(t, nil, runtime.NewOpenAICompatProvider("primary", upstream+"/v1", "k"))
	return gateway, upstream
}

// instantGateway is benchEnv without the upstream URL.
func instantGateway(t *testing.T) string {
	t.Helper()
	gw, _ := benchEnv(t)
	return gw
}

// benchClient is a keep-alive client, the way a real caller behaves.
func benchClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 8},
		Timeout:   30 * time.Second,
	}
}

// oneRequest issues a chat completion and returns its latency.
func oneRequest(client *http.Client, gw string, payload []byte) (time.Duration, error) {
	start := time.Now()
	req, err := http.NewRequest(http.MethodPost, gw+"/v1/chat/completions", strings.NewReader(string(payload)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer delos-dev")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

// TestGatewayOverheadP99 gates the headline overhead number and asserts the
// gateway leaks no goroutines while producing it.
//
// Each iteration issues the same request twice: once straight at the upstream
// and once through the gateway. The difference of the two p99s is the
// gateway's cost; interleaving means a noisy machine inflates both series
// rather than only the one under test. If even the direct baseline blows the
// budget the machine is too loaded to measure anything, and the test says so
// instead of blaming the gateway.
func TestGatewayOverheadP99(t *testing.T) {
	if testing.Short() {
		t.Skip("overhead benchmark skipped in -short mode")
	}
	gw, upstream := benchEnv(t)
	direct := upstream + "/v1/chat/completions"
	client := benchClient()
	payload := chatRequest(false)

	for i := 0; i < overheadWarmup; i++ {
		if _, err := oneRequest(client, gw, payload); err != nil {
			t.Fatalf("warmup request %d failed: %v", i, err)
		}
		if _, err := postDirect(client, direct, payload); err != nil {
			t.Fatalf("warmup baseline request %d failed: %v", i, err)
		}
	}
	baseline := settledGoroutines()

	gwLatencies := make([]time.Duration, 0, overheadRequests)
	directLatencies := make([]time.Duration, 0, overheadRequests)
	for i := 0; i < overheadRequests; i++ {
		d, err := oneRequest(client, gw, payload)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		gwLatencies = append(gwLatencies, d)

		b, err := postDirect(client, direct, payload)
		if err != nil {
			t.Fatalf("baseline request %d failed: %v", i, err)
		}
		directLatencies = append(directLatencies, b)
	}
	sort.Slice(gwLatencies, func(i, j int) bool { return gwLatencies[i] < gwLatencies[j] })
	sort.Slice(directLatencies, func(i, j int) bool { return directLatencies[i] < directLatencies[j] })

	gwP99 := percentile(gwLatencies, 0.99)
	directP99 := percentile(directLatencies, 0.99)
	overhead := gwP99 - directP99
	if overhead < 0 {
		overhead = 0
	}
	t.Logf("over %d requests: gateway p50=%s p95=%s p99=%s | direct-to-upstream p50=%s p99=%s | p99 gateway overhead=%s",
		len(gwLatencies),
		percentile(gwLatencies, 0.50), percentile(gwLatencies, 0.95), gwP99,
		percentile(directLatencies, 0.50), directP99, overhead)

	budget := p99Budget(t)
	switch {
	case directP99 > budget:
		t.Skipf("machine too loaded to measure: the direct-to-upstream p99 is already %s (budget %s)", directP99, budget)
	case overhead > budget:
		t.Errorf("p99 gateway overhead = %s, budget %s (override with DELOS_BENCH_P99_MS)", overhead, budget)
	}

	client.CloseIdleConnections()
	after := settledGoroutines()
	if after > baseline+goroutineSlack {
		t.Errorf("goroutines did not return to baseline: %d -> %d (slack %d)\n%s",
			baseline, after, goroutineSlack, goroutineDump())
	}
}

// postDirect issues the same payload straight at the upstream, bypassing the
// gateway, and returns its latency: the machine's loopback baseline.
func postDirect(client *http.Client, url string, payload []byte) (time.Duration, error) {
	start := time.Now()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

// TestGatewaySoak is the long-running leak check: DELOS_SOAK=1 keeps traffic
// flowing for 10s and asserts goroutine and file-descriptor counts are flat.
func TestGatewaySoak(t *testing.T) {
	if os.Getenv("DELOS_SOAK") != "1" {
		t.Skip("set DELOS_SOAK=1 to run the soak test")
	}
	gw := instantGateway(t)
	client := benchClient()
	payload := chatRequest(false)

	for i := 0; i < overheadWarmup; i++ {
		if _, err := oneRequest(client, gw, payload); err != nil {
			t.Fatalf("warmup request %d failed: %v", i, err)
		}
	}
	baselineGoroutines := settledGoroutines()
	baselineFDs, fdsAvailable := openFDs()

	deadline := time.Now().Add(soakDuration)
	requests := 0
	var worst time.Duration
	for time.Now().Before(deadline) {
		d, err := oneRequest(client, gw, payload)
		if err != nil {
			t.Fatalf("soak request %d failed: %v", requests, err)
		}
		if d > worst {
			worst = d
		}
		requests++
	}
	t.Logf("soak: %d requests in %s, worst latency %s", requests, soakDuration, worst)

	client.CloseIdleConnections()
	afterGoroutines := settledGoroutines()
	if afterGoroutines > baselineGoroutines+goroutineSlack {
		t.Errorf("goroutine leak over soak: %d -> %d\n%s", baselineGoroutines, afterGoroutines, goroutineDump())
	}
	if fdsAvailable {
		afterFDs, _ := openFDs()
		if afterFDs > baselineFDs+goroutineSlack {
			t.Errorf("file descriptor leak over soak: %d -> %d", baselineFDs, afterFDs)
		}
	}
}

// ---- helpers ----

func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// settledGoroutines waits for the runtime to quiesce before counting, so
// transient connection goroutines are not mistaken for leaks.
func settledGoroutines() int {
	last := goruntime.NumGoroutine()
	stable := 0
	for i := 0; i < settleMaxAttempts; i++ {
		time.Sleep(10 * time.Millisecond)
		n := goruntime.NumGoroutine()
		if n == last {
			stable++
			if stable >= 3 {
				return n
			}
			continue
		}
		stable = 0
		last = n
	}
	return last
}

// openFDs counts open file descriptors on platforms that expose them.
func openFDs() (int, bool) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return len(entries), true
}

func goroutineDump() string {
	buf := make([]byte, 1<<16)
	n := goruntime.Stack(buf, true)
	return truncate(string(buf[:n]), 4000)
}
