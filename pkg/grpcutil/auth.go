package grpcutil

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The control plane is a single-tenant operator surface: prompts, evals, gate
// verdicts and captured traces. It authenticates with one shared secret
// (DELOS_AUTH_TOKEN) carried either as `authorization: Bearer <token>` or as
// `x-delos-token: <token>`, on both the gRPC and the HTTP surfaces. That is
// deliberately the same token for gRPC and for CI hitting the verdict
// endpoint with curl.
//
// The token is environment-only. delos.yaml is a commit-safe file (see
// pkg/config/file.go, where `dec.KnownFields(true)` rejects unknown keys), so
// secrets have no schema there on purpose.

const (
	// MetadataAuthorization is the gRPC metadata key for a bearer token.
	MetadataAuthorization = "authorization"
	// MetadataToken is the gRPC metadata key for a bare token.
	MetadataToken = "x-delos-token"
	// HeaderToken is the HTTP header for a bare token.
	HeaderToken = "X-Delos-Token"

	bearerPrefix = "bearer "
)

// TokenAuthenticator validates a shared secret in constant time. It stores
// only the SHA-256 of the configured token, so comparison is over two
// fixed-length digests: neither the length nor any prefix of the real token
// leaks through timing.
type TokenAuthenticator struct {
	digest  [sha256.Size]byte
	enabled bool
}

// NewTokenAuthenticator returns an authenticator for token. An empty token
// yields a disabled authenticator, which rejects everything - callers decide
// whether to install it at all.
func NewTokenAuthenticator(token string) *TokenAuthenticator {
	a := &TokenAuthenticator{}
	if token == "" {
		return a
	}
	a.digest = sha256.Sum256([]byte(token))
	a.enabled = true
	return a
}

// Enabled reports whether a token was configured.
func (a *TokenAuthenticator) Enabled() bool { return a != nil && a.enabled }

// Valid reports whether presented matches the configured token. The
// comparison is constant time with respect to the secret.
func (a *TokenAuthenticator) Valid(presented string) bool {
	if !a.Enabled() || presented == "" {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(got[:], a.digest[:]) == 1
}

// TokenFromMetadata pulls a token out of gRPC metadata, accepting either
// `authorization: Bearer <token>` or `x-delos-token: <token>`.
func TokenFromMetadata(md metadata.MD) string {
	for _, v := range md.Get(MetadataAuthorization) {
		if len(v) > len(bearerPrefix) && strings.EqualFold(v[:len(bearerPrefix)], bearerPrefix) {
			return strings.TrimSpace(v[len(bearerPrefix):])
		}
	}
	for _, v := range md.Get(MetadataToken) {
		if v != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// TokenFromRequest pulls a token out of an HTTP request, accepting either
// `Authorization: Bearer <token>` or `X-Delos-Token: <token>`.
func TokenFromRequest(r *http.Request) string {
	if v := r.Header.Get("Authorization"); len(v) > len(bearerPrefix) &&
		strings.EqualFold(v[:len(bearerPrefix)], bearerPrefix) {
		return strings.TrimSpace(v[len(bearerPrefix):])
	}
	return strings.TrimSpace(r.Header.Get(HeaderToken))
}

// ExemptMethod reports whether a gRPC method bypasses authentication. Only
// the standard health service does: it returns a serving status and nothing
// else, and load balancers check it before they have credentials.
func ExemptMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/grpc.health.v1.Health/")
}

var errUnauthenticated = status.Error(codes.Unauthenticated,
	"missing or invalid control-plane token; send it as `authorization: Bearer <DELOS_AUTH_TOKEN>` or `x-delos-token`")

func (a *TokenAuthenticator) authorize(ctx context.Context, fullMethod string) error {
	if ExemptMethod(fullMethod) {
		return nil
	}
	md, _ := metadata.FromIncomingContext(ctx)
	if a.Valid(TokenFromMetadata(md)) {
		return nil
	}
	return errUnauthenticated
}

// AuthUnaryInterceptor rejects unary RPCs that do not present the token.
func AuthUnaryInterceptor(a *TokenAuthenticator, logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := a.authorize(ctx, info.FullMethod); err != nil {
			logUnauthenticated(ctx, logger, info.FullMethod)
			return nil, err
		}
		return handler(ctx, req)
	}
}

// AuthStreamInterceptor rejects streaming RPCs that do not present the token.
func AuthStreamInterceptor(a *TokenAuthenticator, logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := a.authorize(ss.Context(), info.FullMethod); err != nil {
			logUnauthenticated(ss.Context(), logger, info.FullMethod)
			return err
		}
		return handler(srv, ss)
	}
}

func logUnauthenticated(ctx context.Context, logger *slog.Logger, method string) {
	if logger == nil {
		return
	}
	logger.WarnContext(ctx, "rejected unauthenticated request", "method", method)
}

// HTTPAuthMiddleware wraps an HTTP handler with the same shared-secret check.
// CI reads gate verdicts with curl, so the bearer form works there too.
func HTTPAuthMiddleware(a *TokenAuthenticator, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Valid(TokenFromRequest(r)) {
			if logger != nil {
				logger.WarnContext(r.Context(), "rejected unauthenticated request",
					"path", r.URL.Path, "method", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", `Bearer realm="delos"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"missing or invalid control-plane token; send it as ` +
				`Authorization: Bearer $DELOS_AUTH_TOKEN or X-Delos-Token"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TokenCredentials is a grpc.PerRPCCredentials that attaches the shared
// secret to every outgoing call. It is safe over h2c (no TLS) only because
// the control-plane leg is expected to be a private network or a TLS
// terminating proxy - see docs/deploy.md.
type TokenCredentials struct {
	Token string
}

// GetRequestMetadata implements credentials.PerRPCCredentials.
func (c TokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	if c.Token == "" {
		return nil, nil
	}
	return map[string]string{MetadataAuthorization: "Bearer " + c.Token}, nil
}

// RequireTransportSecurity implements credentials.PerRPCCredentials. It
// returns false because Delos terminates TLS at a proxy; requiring it here
// would make h2c impossible.
func (c TokenCredentials) RequireTransportSecurity() bool { return false }
