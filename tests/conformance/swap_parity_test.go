package conformance

import (
	"os"
	"strings"
	"testing"

	"github.com/instantcocoa/delos/services/runtime"
)

// Swap parity (D1, note 0001 §"What must NOT change"): the existing
// conformance cassettes must produce the same client-visible contract through
// the old Delos translators and the new Bifrost path - same status, same
// pinned response fields (json_paths/body_contains/sse), same usage mapping.
//
// The old translators stay behind a flag for one release and are then deleted
// (note 0001 §"Rollout"), so this suite runs each cassette through both paths
// and compares. Parity here is defined as: both paths satisfy the cassette's
// expectations and return the same HTTP status. Byte equality is deliberately
// NOT asserted - streamed responses carry a per-request message id - and the
// cassette's json_paths already pin the usage fields that must not regress.

// bifrostEnv is the switch DL-G installs to run the Bifrost path in the
// gateway. The value is provisional until agreed with DL-G (via LEAD); the
// suite reads the variable so it can flip the path under test without code
// changes. See docs/design/notes/0001-gateway-on-bifrost.md.
const bifrostEnv = "DELOS_GATEWAY_BIFROST"

func bifrostEnabled() bool {
	v := os.Getenv(bifrostEnv)
	return v == "1" || strings.EqualFold(v, "true")
}

// TestSwapParity replays every cassette through both gateway paths and asserts
// parity. When the Bifrost path is disabled it skips: TestCassettes already
// holds the old path to the contract, and there is no second path to compare.
func TestSwapParity(t *testing.T) {
	if !bifrostEnabled() {
		t.Skipf("Bifrost path disabled: set %s=1 to run the swap-parity comparison", bifrostEnv)
	}
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
				t.Errorf("status parity: old=%d bifrost=%d\nold body: %s\nbifrost body: %s",
					old.Status, bifrost.Status, truncate(old.Body, 1200), truncate(bifrost.Body, 1200))
			}
		})
	}
}

// runCassettePath runs one cassette through either the old translators or the
// Bifrost path, holding it to the cassette's full expectation set, and returns
// the client-visible response.
func runCassettePath(t *testing.T, c *Cassette, bifrost bool) GatewayResponse {
	t.Helper()

	spec, ok := providerSpecs[c.Provider]
	if !ok {
		t.Fatalf("unknown provider %q", c.Provider)
	}

	upstream := newFakeUpstream(c.UpstreamResponses, []string{c.model()})
	upstreamSrv := newLoopbackServer(t, upstream)
	provider := spec.build(upstreamSrv, spec.replayKey)

	var gwURL string
	if bifrost {
		gwURL = bifrostGateway(t, provider)
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
	assertBody(t, c, resp)
	assertStream(t, c, resp)
	assertUpstream(t, c, spec, upstream)
	return resp
}

// bifrostGateway builds a gateway with the Bifrost path enabled and returns
// its base URL. DL-G owns services/runtime and the switch this flips; until
// that lands this helper cannot construct the Bifrost gateway. If the flag is
// set before the hook exists, failing loudly here is the point: it names the
// exact place DL-T must wire once DL-G's switch is available.
func bifrostGateway(t *testing.T, providers ...runtime.Provider) string {
	t.Helper()
	t.Fatal("Bifrost gateway path not wired yet (DL-G owns services/runtime); implement bifrostGateway once the switch lands")
	return ""
}
