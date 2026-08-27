package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
)

// ErrorCode is a stable, machine-readable failure category.
type ErrorCode string

const (
	// CodeInvalidInput indicates a malformed request or path.
	CodeInvalidInput ErrorCode = "invalid_argument"
	// CodeNotFound indicates that the requested path does not exist.
	CodeNotFound ErrorCode = "not_found"
	// CodeConflict indicates stale state, an anchor mismatch, or an existing target.
	CodeConflict ErrorCode = "conflict"
	// CodeLimit indicates that a hard safety limit prevented the operation.
	CodeLimit ErrorCode = "limit_exceeded"
	// CodePermission indicates an access denial or unsafe path traversal.
	CodePermission ErrorCode = "permission_denied"
	// CodeInternal indicates an unexpected local failure.
	CodeInternal ErrorCode = "internal"

	// Adapter-friendly aliases use the same stable wire values.
	CodeInvalidArgument  = CodeInvalidInput
	CodeStale            = CodeConflict
	CodeLimitExceeded    = CodeLimit
	CodePermissionDenied = CodePermission
)

var (
	// ErrInvalidInput is matched by errors.Is for invalid requests.
	ErrInvalidInput = errors.New("filesystem: invalid input")
	// ErrNotFound is matched by errors.Is when a path is absent.
	ErrNotFound = errors.New("filesystem: not found")
	// ErrConflict is matched by errors.Is for stale or conflicting changes.
	ErrConflict = errors.New("filesystem: conflict")
	// ErrLimit is matched by errors.Is when a hard limit is exceeded.
	ErrLimit = errors.New("filesystem: limit exceeded")
	// ErrPermission is matched by errors.Is for denied or unsafe access.
	ErrPermission = errors.New("filesystem: permission denied")
	// ErrInternal is matched by errors.Is for unexpected failures.
	ErrInternal = errors.New("filesystem: internal error")
	// ErrStale aliases ErrConflict for optimistic-concurrency adapters.
	ErrStale = ErrConflict
	// ErrInvalidArgument aliases ErrInvalidInput.
	ErrInvalidArgument = ErrInvalidInput
	// ErrLimitExceeded aliases ErrLimit.
	ErrLimitExceeded = ErrLimit
	// ErrPermissionDenied aliases ErrPermission.
	ErrPermissionDenied = ErrPermission
)

// Error adds a stable code and operation to an underlying failure.
type Error struct {
	Code    ErrorCode `json:"code"`
	Op      string    `json:"operation,omitempty"`
	Path    string    `json:"path,omitempty"`
	Message string    `json:"message"`
	Cause   error     `json:"-"`
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := "filesystem"
	if e.Op != "" {
		prefix += " " + e.Op
	}
	if e.Path != "" {
		prefix += " " + e.Path
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", prefix, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", prefix, e.Message)
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is makes coded errors comparable to the package sentinel errors.
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	return target == sentinelForCode(e.Code)
}

// CodeOf returns the nearest filesystem error code, or CodeInternal.
func CodeOf(err error) ErrorCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

func sentinelForCode(code ErrorCode) error {
	switch code {
	case CodeInvalidInput:
		return ErrInvalidInput
	case CodeNotFound:
		return ErrNotFound
	case CodeConflict:
		return ErrConflict
	case CodeLimit:
		return ErrLimit
	case CodePermission:
		return ErrPermission
	default:
		return ErrInternal
	}
}

func newError(code ErrorCode, op, name, msg string, cause error) error {
	return &Error{Code: code, Op: op, Path: name, Message: msg, Cause: cause}
}

func classifyError(op, name string, err error) error {
	if err == nil {
		return nil
	}
	var coded *Error
	if errors.As(err, &coded) {
		return err
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return newError(CodeNotFound, op, name, "path not found", err)
	case errors.Is(err, fs.ErrPermission):
		return newError(CodePermission, op, name, "access denied", err)
	case errors.Is(err, fs.ErrExist):
		return newError(CodeConflict, op, name, "path already exists", err)
	default:
		return newError(CodeInternal, op, name, "operation failed", err)
	}
}
