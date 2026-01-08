package grpcutil

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NotFoundError creates a NOT_FOUND gRPC error.
func NotFoundError(resource, id string) error {
	return status.Errorf(codes.NotFound, "%s not found: %s", resource, id)
}

// InvalidArgumentError creates an INVALID_ARGUMENT gRPC error.
func InvalidArgumentError(field, reason string) error {
	return status.Errorf(codes.InvalidArgument, "invalid %s: %s", field, reason)
}

// FailedPreconditionError creates a FAILED_PRECONDITION gRPC error.
func FailedPreconditionError(reason string) error {
	return status.Errorf(codes.FailedPrecondition, "%s", reason)
}

// InternalError creates an INTERNAL gRPC error.
func InternalError(err error) error {
	return status.Errorf(codes.Internal, "internal error: %v", err)
}

// UnavailableError creates an UNAVAILABLE gRPC error.
func UnavailableError(service string) error {
	return status.Errorf(codes.Unavailable, "%s is temporarily unavailable", service)
}

// AlreadyExistsError creates an ALREADY_EXISTS gRPC error.
func AlreadyExistsError(resource, id string) error {
	return status.Errorf(codes.AlreadyExists, "%s already exists: %s", resource, id)
}

// PermissionDeniedError creates a PERMISSION_DENIED gRPC error.
func PermissionDeniedError(reason string) error {
	return status.Errorf(codes.PermissionDenied, "%s", reason)
}

// UnauthenticatedError creates an UNAUTHENTICATED gRPC error.
func UnauthenticatedError() error {
	return status.Error(codes.Unauthenticated, "authentication required")
}

// WrapError wraps an error with context and converts to appropriate gRPC status.
func WrapError(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}

	msg := fmt.Sprintf(format, args...)

	// If already a gRPC status error, preserve the code
	if s, ok := status.FromError(err); ok {
		return status.Errorf(s.Code(), "%s: %s", msg, s.Message())
	}

	// Default to internal error for unknown errors
	return status.Errorf(codes.Internal, "%s: %v", msg, err)
}

// IsNotFound checks if an error is a NOT_FOUND error.
func IsNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

// IsInvalidArgument checks if an error is an INVALID_ARGUMENT error.
func IsInvalidArgument(err error) bool {
	return status.Code(err) == codes.InvalidArgument
}

// IsUnavailable checks if an error is an UNAVAILABLE error.
func IsUnavailable(err error) bool {
	return status.Code(err) == codes.Unavailable
}
