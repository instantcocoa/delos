package grpcutil

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNotFoundError(t *testing.T) {
	err := NotFoundError("user", "123")

	s, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}

	if s.Code() != codes.NotFound {
		t.Errorf("Code() = %v, want %v", s.Code(), codes.NotFound)
	}

	if s.Message() != "user not found: 123" {
		t.Errorf("Message() = %v, want %v", s.Message(), "user not found: 123")
	}
}

func TestInvalidArgumentError(t *testing.T) {
	err := InvalidArgumentError("email", "invalid format")

	s, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}

	if s.Code() != codes.InvalidArgument {
		t.Errorf("Code() = %v, want %v", s.Code(), codes.InvalidArgument)
	}

	if s.Message() != "invalid email: invalid format" {
		t.Errorf("Message() = %v, want %v", s.Message(), "invalid email: invalid format")
	}
}

func TestFailedPreconditionError(t *testing.T) {
	err := FailedPreconditionError("resource is locked")

	s, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}

	if s.Code() != codes.FailedPrecondition {
		t.Errorf("Code() = %v, want %v", s.Code(), codes.FailedPrecondition)
	}

	if s.Message() != "resource is locked" {
		t.Errorf("Message() = %v, want %v", s.Message(), "resource is locked")
	}
}

func TestInternalError(t *testing.T) {
	originalErr := errors.New("database connection failed")
	err := InternalError(originalErr)

	s, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}

	if s.Code() != codes.Internal {
		t.Errorf("Code() = %v, want %v", s.Code(), codes.Internal)
	}

	if s.Message() != "internal error: database connection failed" {
		t.Errorf("Message() = %v, want %v", s.Message(), "internal error: database connection failed")
	}
}

func TestUnavailableError(t *testing.T) {
	err := UnavailableError("payment-service")

	s, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}

	if s.Code() != codes.Unavailable {
		t.Errorf("Code() = %v, want %v", s.Code(), codes.Unavailable)
	}

	if s.Message() != "payment-service is temporarily unavailable" {
		t.Errorf("Message() = %v, want %v", s.Message(), "payment-service is temporarily unavailable")
	}
}

func TestAlreadyExistsError(t *testing.T) {
	err := AlreadyExistsError("user", "user@example.com")

	s, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}

	if s.Code() != codes.AlreadyExists {
		t.Errorf("Code() = %v, want %v", s.Code(), codes.AlreadyExists)
	}

	if s.Message() != "user already exists: user@example.com" {
		t.Errorf("Message() = %v, want %v", s.Message(), "user already exists: user@example.com")
	}
}

func TestPermissionDeniedError(t *testing.T) {
	err := PermissionDeniedError("insufficient privileges")

	s, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}

	if s.Code() != codes.PermissionDenied {
		t.Errorf("Code() = %v, want %v", s.Code(), codes.PermissionDenied)
	}

	if s.Message() != "insufficient privileges" {
		t.Errorf("Message() = %v, want %v", s.Message(), "insufficient privileges")
	}
}

func TestUnauthenticatedError(t *testing.T) {
	err := UnauthenticatedError()

	s, ok := status.FromError(err)
	if !ok {
		t.Fatal("expected gRPC status error")
	}

	if s.Code() != codes.Unauthenticated {
		t.Errorf("Code() = %v, want %v", s.Code(), codes.Unauthenticated)
	}

	if s.Message() != "authentication required" {
		t.Errorf("Message() = %v, want %v", s.Message(), "authentication required")
	}
}

func TestWrapError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		err := WrapError(nil, "context")
		if err != nil {
			t.Errorf("WrapError(nil, ...) = %v, want nil", err)
		}
	})

	t.Run("regular error", func(t *testing.T) {
		originalErr := errors.New("original error")
		err := WrapError(originalErr, "failed to %s", "process")

		s, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected gRPC status error")
		}

		if s.Code() != codes.Internal {
			t.Errorf("Code() = %v, want %v", s.Code(), codes.Internal)
		}

		expected := "failed to process: original error"
		if s.Message() != expected {
			t.Errorf("Message() = %v, want %v", s.Message(), expected)
		}
	})

	t.Run("gRPC status error", func(t *testing.T) {
		originalErr := NotFoundError("item", "456")
		err := WrapError(originalErr, "failed to retrieve")

		s, ok := status.FromError(err)
		if !ok {
			t.Fatal("expected gRPC status error")
		}

		if s.Code() != codes.NotFound {
			t.Errorf("Code() = %v, want %v (should preserve original code)", s.Code(), codes.NotFound)
		}

		expected := "failed to retrieve: item not found: 456"
		if s.Message() != expected {
			t.Errorf("Message() = %v, want %v", s.Message(), expected)
		}
	})
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"not found error", NotFoundError("x", "1"), true},
		{"internal error", InternalError(errors.New("test")), false},
		{"regular error", errors.New("test"), false},
		{"nil error", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotFound(tt.err); got != tt.want {
				t.Errorf("IsNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsInvalidArgument(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"invalid argument error", InvalidArgumentError("x", "bad"), true},
		{"internal error", InternalError(errors.New("test")), false},
		{"regular error", errors.New("test"), false},
		{"nil error", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsInvalidArgument(tt.err); got != tt.want {
				t.Errorf("IsInvalidArgument() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"unavailable error", UnavailableError("svc"), true},
		{"internal error", InternalError(errors.New("test")), false},
		{"regular error", errors.New("test"), false},
		{"nil error", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUnavailable(tt.err); got != tt.want {
				t.Errorf("IsUnavailable() = %v, want %v", got, tt.want)
			}
		})
	}
}
