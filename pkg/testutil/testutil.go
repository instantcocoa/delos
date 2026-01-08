// Package testutil provides testing utilities for Delos services.
package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

// TestServer provides an in-memory gRPC server for testing.
type TestServer struct {
	Listener *bufconn.Listener
	Server   *grpc.Server
}

// NewTestServer creates a new in-memory test server.
func NewTestServer() *TestServer {
	return &TestServer{
		Listener: bufconn.Listen(bufSize),
		Server:   grpc.NewServer(),
	}
}

// Start starts the test server in a goroutine.
func (ts *TestServer) Start() {
	go func() {
		if err := ts.Server.Serve(ts.Listener); err != nil {
			// Server stopped, this is expected during cleanup
		}
	}()
}

// Stop stops the test server.
func (ts *TestServer) Stop() {
	ts.Server.Stop()
}

// Dial creates a client connection to the test server.
func (ts *TestServer) Dial(ctx context.Context) (*grpc.ClientConn, error) {
	return grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return ts.Listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}

// TestLogger returns a logger for testing.
func TestLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})).With("test", t.Name())
}

// DiscardLogger returns a logger that discards all output.
func DiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.Level(100), // above any real level
	}))
}

// WaitFor waits for a condition to become true.
func WaitFor(t *testing.T, timeout time.Duration, condition func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timeout waiting for condition: %s", msg)
}

// RequireNoError fails the test if err is not nil.
func RequireNoError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	if err != nil {
		if len(msgAndArgs) > 0 {
			t.Fatalf("%s: %v", fmt.Sprint(msgAndArgs...), err)
		}
		t.Fatalf("unexpected error: %v", err)
	}
}

// RequireError fails the test if err is nil.
func RequireError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	if err == nil {
		if len(msgAndArgs) > 0 {
			t.Fatalf("%s: expected error but got nil", fmt.Sprint(msgAndArgs...))
		}
		t.Fatal("expected error but got nil")
	}
}

// RequireEqual fails the test if expected != actual.
func RequireEqual(t *testing.T, expected, actual interface{}, msgAndArgs ...interface{}) {
	t.Helper()
	if expected != actual {
		if len(msgAndArgs) > 0 {
			t.Fatalf("%s: expected %v but got %v", fmt.Sprint(msgAndArgs...), expected, actual)
		}
		t.Fatalf("expected %v but got %v", expected, actual)
	}
}

// TestContext returns a context with a test timeout.
func TestContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// GetFreePort returns an available port for testing.
func GetFreePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}
