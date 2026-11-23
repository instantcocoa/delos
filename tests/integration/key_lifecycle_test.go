// Package integration holds the D2 key-lifecycle suite (gap 4 / note 0001
// §"Wire behavior changes" and the D2 exit criteria). It drives the new
// control-plane KeyService - a thin gRPC API over Bifrost governance - and the
// gateway's enforcement of the keys it mints.
//
// The service is owned by DL-K (proto/keys/v1 + services/runtime/keys*). Until
// it lands this file is a contract scaffold: the case table and the intended
// assertions are written, the wire helpers are placeholders, and the whole
// suite skips. At D2 integration, wire the three helpers below and remove the
// availability gate.
//
//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// keyServiceEnv is set when the control plane under test exposes the D2
// KeyService. Until DL-K lands proto/keys/v1 this is the gate that keeps the
// scaffold from failing against a service that does not exist.
const keyServiceEnv = "DELOS_KEYS_ENABLED"

func keyServiceAvailable() bool {
	v := os.Getenv(keyServiceEnv)
	return v == "1" || strings.EqualFold(v, "true")
}

// testKeySpec is everything the lifecycle table needs to mint a key. It is a
// plain mirror of the KeyService mint request; DL-K owns the authoritative
// shape and may rename fields without touching the table's intent.
type testKeySpec struct {
	models           []string // empty = all models
	dailyTokenBudget int64    // 0 = unlimited
	totalTokenBudget int64    // 0 = unlimited
	expiresIn        time.Duration
}

// TestKeyLifecycle drives the D2 exit criteria for a single key:
//   - expired        -> 401 authentication_error
//   - revoked        -> 401 authentication_error
//   - wrong model    -> 403 permission_error (model_not_allowed)
//   - over daily     -> 429 rate_limit_error naming the key and the budget
//   - over total     -> 429 rate_limit_error naming the key and the budget
func TestKeyLifecycle(t *testing.T) {
	if !keyServiceAvailable() {
		t.Skipf("KeyService not available (DL-K owns proto/keys/v1); set %s=1 at D2 integration", keyServiceEnv)
	}

	const modelA = "gpt-4o"
	const modelB = "gpt-4o-mini"

	cases := []struct {
		name        string
		spec        testKeySpec
		request     string // model to request
		seedTokens  int64  // usage to seed before the request (budget cases)
		revokeFirst bool
		wantStatus  int
		wantError   string // substring of the error envelope (type or message)
	}{
		{
			name:       "expired key is refused",
			spec:       testKeySpec{expiresIn: -time.Minute},
			request:    modelA,
			wantStatus: http.StatusUnauthorized,
			wantError:  "authentication_error",
		},
		{
			name:        "revoked key is refused",
			spec:        testKeySpec{},
			request:     modelA,
			revokeFirst: true,
			wantStatus:  http.StatusUnauthorized,
			wantError:   "authentication_error",
		},
		{
			name:       "key scoped to another model is refused",
			spec:       testKeySpec{models: []string{modelA}},
			request:    modelB,
			wantStatus: http.StatusForbidden,
			wantError:  "permission_error",
		},
		{
			name:       "over daily token budget is refused",
			spec:       testKeySpec{dailyTokenBudget: 100},
			request:    modelA,
			seedTokens: 100,
			wantStatus: http.StatusTooManyRequests,
			wantError:  "rate_limit_error",
		},
		{
			name:       "over total token budget is refused",
			spec:       testKeySpec{totalTokenBudget: 100},
			request:    modelA,
			seedTokens: 100,
			wantStatus: http.StatusTooManyRequests,
			wantError:  "rate_limit_error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secret := mintKeyForTest(t, tc.spec)
			if tc.revokeFirst {
				revokeKeyForTest(t, secret)
			}
			if tc.seedTokens > 0 {
				seedUsageForTest(t, secret, tc.seedTokens)
			}
			status, body := completeWithKey(t, secret, tc.request)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d\nbody: %s", status, tc.wantStatus, body)
			}
			if !strings.Contains(string(body), tc.wantError) {
				t.Errorf("response body does not contain %q\nbody: %s", tc.wantError, body)
			}
		})
	}
}

// TestKeyRevokePropagation drives the multi-replica exit criterion: a session
// key stops working within 1 s of revoke on every gateway replica.
func TestKeyRevokePropagation(t *testing.T) {
	if !keyServiceAvailable() {
		t.Skipf("KeyService not available (DL-K owns proto/keys/v1); set %s=1 at D2 integration", keyServiceEnv)
	}

	secret := mintKeyForTest(t, testKeySpec{})
	// DL-T: start N gateway replicas sharing the Bifrost governance store, and
	// record each replica's base URL here. With the control-plane binaries the
	// suite normally drives (tests/integration), replicas are additional
	// delos-gateway processes pointed at the same Postgres governance store.
	replicas := startGatewayReplicas(t, 2)

	// Sanity: the key works everywhere before revocation.
	for _, url := range replicas {
		if status, _ := completeAt(t, url, secret, "gpt-4o"); status != http.StatusOK {
			t.Fatalf("pre-revoke request should succeed, got %d at %s", status, url)
		}
	}

	revokeKeyForTest(t, secret)

	deadline := time.Now().Add(time.Second)
	for _, url := range replicas {
		url := url
		for {
			status, _ := completeAt(t, url, secret, "gpt-4o")
			if status == http.StatusUnauthorized {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("replica %s still served a revoked key after 1s (last status %d)", url, status)
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

// ---- placeholders: wire to the KeyService at D2 integration ----

func mintKeyForTest(t *testing.T, spec testKeySpec) string {
	t.Helper()
	t.Fatal("KeyService not wired: mintKeyForTest must call the D2 KeyService (proto/keys/v1) once DL-K lands")
	return ""
}

func revokeKeyForTest(t *testing.T, secret string) {
	t.Helper()
	t.Fatal("KeyService not wired: revokeKeyForTest must call the D2 KeyService once DL-K lands")
}

func seedUsageForTest(t *testing.T, secret string, tokens int64) {
	t.Helper()
	t.Fatal("KeyService not wired: seedUsageForTest must record usage against the key once DL-K lands")
}

func startGatewayReplicas(t *testing.T, n int) []string {
	t.Helper()
	t.Fatal("replicas not wired: startGatewayReplicas must start n delos-gateway processes on one governance store once DL-K lands")
	return nil
}

// ---- gateway request helpers ----

// completeWithKey issues a chat completion against the gateway using the given
// virtual key and returns the status and raw body.
func completeWithKey(t *testing.T, secret, model string) (int, []byte) {
	t.Helper()
	return completeAt(t, gatewayBaseURL(), secret, model)
}

// completeAt issues a chat completion against a specific gateway base URL with
// the given key. Kept separate so the revoke-propagation test can target
// individual replicas.
func completeAt(t *testing.T, baseURL, secret, model string) (int, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("marshalling request: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return resp.StatusCode, body
}
