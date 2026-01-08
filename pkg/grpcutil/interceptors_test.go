package grpcutil

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLoggingUnaryInterceptor(t *testing.T) {
	logger := slog.Default()
	interceptor := LoggingUnaryInterceptor(logger)

	t.Run("successful call", func(t *testing.T) {
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			return "response", nil
		}

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor(context.Background(), "request", info, handler)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if resp != "response" {
			t.Errorf("response = %v, want %v", resp, "response")
		}
	})

	t.Run("failed call", func(t *testing.T) {
		expectedErr := status.Error(codes.NotFound, "not found")
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			return nil, expectedErr
		}

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor(context.Background(), "request", info, handler)

		if err != expectedErr {
			t.Errorf("error = %v, want %v", err, expectedErr)
		}
		if resp != nil {
			t.Errorf("response = %v, want nil", resp)
		}
	})
}

type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestLoggingStreamInterceptor(t *testing.T) {
	logger := slog.Default()
	interceptor := LoggingStreamInterceptor(logger)

	t.Run("successful stream", func(t *testing.T) {
		handler := func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		}

		stream := &mockServerStream{ctx: context.Background()}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
		err := interceptor(nil, stream, info, handler)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("failed stream", func(t *testing.T) {
		expectedErr := status.Error(codes.Internal, "internal error")
		handler := func(srv interface{}, stream grpc.ServerStream) error {
			return expectedErr
		}

		stream := &mockServerStream{ctx: context.Background()}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
		err := interceptor(nil, stream, info, handler)

		if err != expectedErr {
			t.Errorf("error = %v, want %v", err, expectedErr)
		}
	})
}

func TestRecoveryUnaryInterceptor(t *testing.T) {
	logger := slog.Default()
	interceptor := RecoveryUnaryInterceptor(logger)

	t.Run("no panic", func(t *testing.T) {
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			return "response", nil
		}

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor(context.Background(), "request", info, handler)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if resp != "response" {
			t.Errorf("response = %v, want %v", resp, "response")
		}
	})

	t.Run("panic recovery", func(t *testing.T) {
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			panic("test panic")
		}

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor(context.Background(), "request", info, handler)

		if resp != nil {
			t.Errorf("response = %v, want nil", resp)
		}
		if err == nil {
			t.Fatal("expected error after panic")
		}

		s, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected gRPC status error")
		}
		if s.Code() != codes.Internal {
			t.Errorf("Code() = %v, want %v", s.Code(), codes.Internal)
		}
	})

	t.Run("handler returns error", func(t *testing.T) {
		expectedErr := errors.New("handler error")
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			return nil, expectedErr
		}

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor(context.Background(), "request", info, handler)

		if resp != nil {
			t.Errorf("response = %v, want nil", resp)
		}
		if err != expectedErr {
			t.Errorf("error = %v, want %v", err, expectedErr)
		}
	})
}

func TestRecoveryStreamInterceptor(t *testing.T) {
	logger := slog.Default()
	interceptor := RecoveryStreamInterceptor(logger)

	t.Run("no panic", func(t *testing.T) {
		handler := func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		}

		stream := &mockServerStream{ctx: context.Background()}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
		err := interceptor(nil, stream, info, handler)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("panic recovery", func(t *testing.T) {
		handler := func(srv interface{}, stream grpc.ServerStream) error {
			panic("stream panic")
		}

		stream := &mockServerStream{ctx: context.Background()}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}
		err := interceptor(nil, stream, info, handler)

		if err == nil {
			t.Fatal("expected error after panic")
		}

		s, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected gRPC status error")
		}
		if s.Code() != codes.Internal {
			t.Errorf("Code() = %v, want %v", s.Code(), codes.Internal)
		}
	})
}

func TestTimeoutUnaryInterceptor(t *testing.T) {
	t.Run("completes before timeout", func(t *testing.T) {
		interceptor := TimeoutUnaryInterceptor(5 * time.Second)
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			return "response", nil
		}

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor(context.Background(), "request", info, handler)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if resp != "response" {
			t.Errorf("response = %v, want %v", resp, "response")
		}
	})

	t.Run("context has deadline", func(t *testing.T) {
		interceptor := TimeoutUnaryInterceptor(100 * time.Millisecond)
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				return nil, errors.New("expected deadline to be set")
			}
			if time.Until(deadline) > 100*time.Millisecond {
				return nil, errors.New("deadline too far in future")
			}
			return "response", nil
		}

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		resp, err := interceptor(context.Background(), "request", info, handler)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if resp != "response" {
			t.Errorf("response = %v, want %v", resp, "response")
		}
	})

	t.Run("times out", func(t *testing.T) {
		interceptor := TimeoutUnaryInterceptor(10 * time.Millisecond)
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return "response", nil
			}
		}

		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
		_, err := interceptor(context.Background(), "request", info, handler)

		if err != context.DeadlineExceeded {
			t.Errorf("error = %v, want %v", err, context.DeadlineExceeded)
		}
	})
}
