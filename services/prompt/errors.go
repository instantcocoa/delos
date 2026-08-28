package prompt

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned by store operations that address a prompt which does
// not exist, or which has been soft-deleted (see Store.Delete). Handlers map it
// to gRPC NOT_FOUND.
var ErrNotFound = errors.New("prompt not found")

// SlugConflictError reports that a prompt slug is already taken. Stores return
// it instead of a driver-specific unique-violation error so the handler can
// answer ALREADY_EXISTS naming only the slug - never a driver string or a
// database constraint name.
type SlugConflictError struct {
	Slug string
}

func (e *SlugConflictError) Error() string {
	return fmt.Sprintf("prompt slug already exists: %s", e.Slug)
}

// AsSlugConflict reports whether err is (or wraps) a SlugConflictError.
func AsSlugConflict(err error) (*SlugConflictError, bool) {
	var conflict *SlugConflictError
	if errors.As(err, &conflict) {
		return conflict, true
	}
	return nil, false
}
