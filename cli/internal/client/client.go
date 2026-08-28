// Package client dials the Delos control plane. Every CLI command that speaks
// gRPC should go through Dial so the shared-secret token is attached
// consistently - a command that builds its own grpc.ClientConn will silently
// talk to an authenticated control plane without credentials and get
// Unauthenticated back.
package client

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/instantcocoa/delos/cli/internal/config"
	"github.com/instantcocoa/delos/pkg/grpcutil"
)

// Dial connects to the control plane at cfg.ControlPlaneAddr. The connection
// is plaintext h2c (TLS is expected to be terminated by a proxy in front of
// the control plane; see docs/deploy.md), and carries the shared secret from
// DELOS_AUTH_TOKEN as per-RPC credentials when one is configured.
func Dial(cfg *config.Config) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if cfg != nil && cfg.AuthToken != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(grpcutil.TokenCredentials{Token: cfg.AuthToken}))
	}
	conn, err := grpc.NewClient(cfg.ControlPlaneAddr, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the control plane at %s: %w", cfg.ControlPlaneAddr, err)
	}
	return conn, nil
}
