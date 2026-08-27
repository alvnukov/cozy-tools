package tool

import (
	"errors"
	"fmt"
)

// ErrorCode is a stable machine-readable tool failure category.
type ErrorCode string

const (
	CodeInvalidArgument  ErrorCode = "invalid_argument"
	CodeNotFound         ErrorCode = "not_found"
	CodeConflict         ErrorCode = "conflict"
	CodePermissionDenied ErrorCode = "permission_denied"
	CodeLimitExceeded    ErrorCode = "limit_exceeded"
	CodeUnavailable      ErrorCode = "unavailable"
	CodeInternal         ErrorCode = "internal"
)

// Error carries a stable code without exposing transport-specific failures.
type Error struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Cause)
}

// Unwrap exposes the underlying implementation error.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewError constructs a coded error.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WrapError adds a stable code and message to err.
func WrapError(code ErrorCode, message string, err error) *Error {
	return &Error{Code: code, Message: message, Cause: err}
}

// CodeOf returns the nearest tool Error code, or CodeInternal.
func CodeOf(err error) ErrorCode {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return CodeInternal
}
