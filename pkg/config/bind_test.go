package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"[::]", false},
		{"127.0.0.1", true},
		{"127.0.0.53", true},
		{"localhost", true},
		{"::1", true},
		{"[::1]", true},
		{"10.0.0.5", false},
		{"192.168.1.10", false},
		{"delos.internal", false},
	}
	for _, tt := range tests {
		if got := IsLoopbackHost(tt.host); got != tt.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestResolveBind(t *testing.T) {
	tests := []struct {
		name        string
		explicit    string
		wideDefault bool
		wantAddr    string
		wantLoop    bool
	}{
		{"no token: loopback default", "", false, "127.0.0.1:8081", true},
		{"token set: every interface", "", true, ":8081", false},
		{"explicit wildcard", "0.0.0.0", false, "0.0.0.0:8081", false},
		{"explicit loopback overrides wide default", "127.0.0.1", true, "127.0.0.1:8081", true},
		{"explicit private address", "10.1.2.3", false, "10.1.2.3:8081", false},
		{"host:port form is tolerated", "0.0.0.0:9999", false, "0.0.0.0:8081", false},
		{"ipv6 loopback", "::1", true, "[::1]:8081", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := ResolveBind(tt.explicit, 8081, tt.wideDefault)
			if b.Addr() != tt.wantAddr {
				t.Errorf("Addr() = %q, want %q", b.Addr(), tt.wantAddr)
			}
			if b.Loopback != tt.wantLoop {
				t.Errorf("Loopback = %v, want %v", b.Loopback, tt.wantLoop)
			}
			if b.Explicit != (tt.explicit != "") {
				t.Errorf("Explicit = %v", b.Explicit)
			}
		})
	}
}

// TestDecideGatewayAuth is the decision table for the gateway's fail-closed
// startup check: it must never front provider credentials on a reachable port
// without either key enforcement or an explicit acknowledgement.
func TestDecideGatewayAuth(t *testing.T) {
	tests := []struct {
		name      string
		in        GatewayExposure
		wantMode  string
		wantError bool
	}{
		{
			name:     "postgres key store, public bind: enforced",
			in:       GatewayExposure{KeyStoreConfigured: true, ProviderCredentials: true},
			wantMode: GatewayAuthEnforced,
		},
		{
			name:     "no key store, loopback bind: dev mode is fine",
			in:       GatewayExposure{ProviderCredentials: true, BindLoopback: true},
			wantMode: GatewayAuthDevLoopback,
		},
		{
			name:     "no key store, public bind, acknowledged: allowed",
			in:       GatewayExposure{ProviderCredentials: true, AllowUnauthenticated: true},
			wantMode: GatewayAuthOpenAcknowledged,
		},
		{
			name:     "no key store, public bind, no provider creds: nothing to steal",
			in:       GatewayExposure{},
			wantMode: GatewayAuthNoProviders,
		},
		{
			name:      "no key store, public bind, provider creds: REFUSE",
			in:        GatewayExposure{ProviderCredentials: true},
			wantError: true,
		},
		{
			name:      "container, no key store, provider creds: REFUSE",
			in:        GatewayExposure{ProviderCredentials: true, InContainer: true},
			wantError: true,
		},
		{
			name:     "container, acknowledged: allowed (the docker quickstart)",
			in:       GatewayExposure{ProviderCredentials: true, InContainer: true, AllowUnauthenticated: true},
			wantMode: GatewayAuthOpenAcknowledged,
		},
		{
			name:     "container with postgres keys: enforced",
			in:       GatewayExposure{ProviderCredentials: true, InContainer: true, KeyStoreConfigured: true},
			wantMode: GatewayAuthEnforced,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, err := DecideGatewayAuth(tt.in)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected a refusal, got mode %q", mode)
				}
				var exposure *ErrUnauthenticatedExposure
				if !errors.As(err, &exposure) {
					t.Fatalf("error type = %T, want *ErrUnauthenticatedExposure", err)
				}
				// The message must name both escape hatches.
				msg := err.Error()
				for _, want := range []string{"DELOS_STORAGE_BACKEND=postgres", "DELOS_ALLOW_UNAUTHENTICATED=true"} {
					if !strings.Contains(msg, want) {
						t.Errorf("refusal message does not mention %q:\n%s", want, msg)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if mode != tt.wantMode {
				t.Fatalf("mode = %q, want %q", mode, tt.wantMode)
			}
		})
	}
}

func TestInContainerHonoursOverride(t *testing.T) {
	t.Setenv("DELOS_IN_CONTAINER", "true")
	if !InContainer() {
		t.Fatal("DELOS_IN_CONTAINER=true should force container mode")
	}
	t.Setenv("DELOS_IN_CONTAINER", "0")
	if InContainer() {
		t.Fatal("DELOS_IN_CONTAINER=0 should force non-container mode")
	}
}

func TestAuthTokenIsEnvironmentOnly(t *testing.T) {
	// delos.yaml has no schema for the token: KnownFields(true) means adding
	// one to the file is a hard error, which is the point.
	if _, err := LoadFile(writeTempYAML(t, "auth_token: hunter2\n")); err == nil {
		t.Fatal("delos.yaml must reject auth_token; secrets are environment-only")
	}

	t.Setenv("DELOS_AUTH_TOKEN", "from-env")
	cfg, err := loadWith("delos", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthToken != "from-env" {
		t.Fatalf("AuthToken = %q, want %q", cfg.AuthToken, "from-env")
	}
}

func writeTempYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "delos.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
