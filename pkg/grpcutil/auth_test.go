package grpcutil

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testToken = "s3cret-control-plane-token"

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func ctxWithMD(pairs ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

func TestTokenAuthenticator_Valid(t *testing.T) {
	a := NewTokenAuthenticator(testToken)
	if !a.Enabled() {
		t.Fatal("authenticator should be enabled with a token")
	}

	tests := []struct {
		name      string
		presented string
		want      bool
	}{
		{"exact match", testToken, true},
		{"empty", "", false},
		{"wrong, same length", strings.Repeat("x", len(testToken)), false},
		{"wrong, shorter (length must not leak)", testToken[:len(testToken)-1], false},
		{"wrong, longer", testToken + "x", false},
		{"case differs", strings.ToUpper(testToken), false},
		{"whitespace padded", " " + testToken, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.Valid(tt.presented); got != tt.want {
				t.Fatalf("Valid(%q) = %v, want %v", tt.presented, got, tt.want)
			}
		})
	}
}

func TestTokenAuthenticator_DisabledRejectsEverything(t *testing.T) {
	a := NewTokenAuthenticator("")
	if a.Enabled() {
		t.Fatal("empty token must not enable the authenticator")
	}
	if a.Valid("") || a.Valid("anything") {
		t.Fatal("a disabled authenticator must never validate a token")
	}
}

func TestTokenFromMetadata(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"bearer", ctxWithMD("authorization", "Bearer "+testToken), testToken},
		{"bearer lowercase", ctxWithMD("authorization", "bearer "+testToken), testToken},
		{"x-delos-token", ctxWithMD("x-delos-token", testToken), testToken},
		{"basic auth ignored", ctxWithMD("authorization", "Basic abc"), ""},
		{"no metadata", context.Background(), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md, _ := metadata.FromIncomingContext(tt.ctx)
			if got := TokenFromMetadata(md); got != tt.want {
				t.Fatalf("TokenFromMetadata = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthUnaryInterceptor(t *testing.T) {
	a := NewTokenAuthenticator(testToken)
	interceptor := AuthUnaryInterceptor(a, discardLogger())
	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/delos.prompt.v1.PromptService/DeletePrompt"}

	tests := []struct {
		name     string
		ctx      context.Context
		wantCode codes.Code
	}{
		{"valid bearer token", ctxWithMD("authorization", "Bearer "+testToken), codes.OK},
		{"valid x-delos-token", ctxWithMD("x-delos-token", testToken), codes.OK},
		{"missing token", context.Background(), codes.Unauthenticated},
		{"empty metadata", ctxWithMD("other", "value"), codes.Unauthenticated},
		{"wrong token", ctxWithMD("authorization", "Bearer nope"), codes.Unauthenticated},
		{"token in the wrong header", ctxWithMD("x-api-key", testToken), codes.Unauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called = false
			_, err := interceptor(tt.ctx, nil, info, handler)
			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v (err=%v)", got, tt.wantCode, err)
			}
			if wantCalled := tt.wantCode == codes.OK; called != wantCalled {
				t.Fatalf("handler called = %v, want %v", called, wantCalled)
			}
		})
	}
}

// fakeStream is a grpc.ServerStream that only carries a context.
type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f fakeStream) Context() context.Context { return f.ctx }

func TestAuthStreamInterceptor(t *testing.T) {
	a := NewTokenAuthenticator(testToken)
	interceptor := AuthStreamInterceptor(a, discardLogger())
	called := false
	handler := func(srv any, ss grpc.ServerStream) error {
		called = true
		return nil
	}
	info := &grpc.StreamServerInfo{FullMethod: "/delos.observe.v1.ObserveService/StreamTraces"}

	called = false
	if err := interceptor(nil, fakeStream{ctx: ctxWithMD("authorization", "Bearer "+testToken)}, info, handler); err != nil {
		t.Fatalf("valid token: unexpected error %v", err)
	}
	if !called {
		t.Fatal("valid token: handler was not called")
	}

	called = false
	err := interceptor(nil, fakeStream{ctx: context.Background()}, info, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing token: code = %v, want Unauthenticated", status.Code(err))
	}
	if called {
		t.Fatal("missing token: handler must not run")
	}

	called = false
	err = interceptor(nil, fakeStream{ctx: ctxWithMD("authorization", "Bearer wrong")}, info, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong token: code = %v, want Unauthenticated", status.Code(err))
	}
	if called {
		t.Fatal("wrong token: handler must not run")
	}
}

func TestHealthCheckIsExempt(t *testing.T) {
	if !ExemptMethod("/grpc.health.v1.Health/Check") {
		t.Fatal("health check should be exempt so load balancers work")
	}
	if ExemptMethod("/delos.prompt.v1.PromptService/ListPrompts") {
		t.Fatal("service RPCs must never be exempt")
	}

	a := NewTokenAuthenticator(testToken)
	interceptor := AuthUnaryInterceptor(a, discardLogger())
	_, err := interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"},
		func(ctx context.Context, req any) (any, error) { return "SERVING", nil })
	if err != nil {
		t.Fatalf("unauthenticated health check should pass: %v", err)
	}
}

func TestHTTPAuthMiddleware(t *testing.T) {
	a := NewTokenAuthenticator(testToken)
	var reached bool
	protected := HTTPAuthMiddleware(a, discardLogger(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		header     [2]string
		wantStatus int
	}{
		{"valid bearer", [2]string{"Authorization", "Bearer " + testToken}, http.StatusOK},
		{"valid x-delos-token", [2]string{"X-Delos-Token", testToken}, http.StatusOK},
		{"no header", [2]string{}, http.StatusUnauthorized},
		{"wrong token", [2]string{"Authorization", "Bearer wrong"}, http.StatusUnauthorized},
		{"bearer prefix missing", [2]string{"Authorization", testToken}, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader("{}"))
			if tt.header[0] != "" {
				req.Header.Set(tt.header[0], tt.header[1])
			}
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if wantReached := tt.wantStatus == http.StatusOK; reached != wantReached {
				t.Fatalf("handler reached = %v, want %v", reached, wantReached)
			}
		})
	}
}

func TestTokenCredentials(t *testing.T) {
	md, err := TokenCredentials{Token: testToken}.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := md["authorization"]; got != "Bearer "+testToken {
		t.Fatalf("authorization = %q", got)
	}
	if (TokenCredentials{}).RequireTransportSecurity() {
		t.Fatal("must not require TLS: the control plane leg is h2c behind a proxy")
	}

	// The credentials round-trip through the interceptor.
	a := NewTokenAuthenticator(testToken)
	if !a.Valid(TokenFromMetadata(metadata.New(md))) {
		t.Fatal("credentials produced metadata the server does not accept")
	}
}
