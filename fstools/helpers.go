package fstools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/alvnukov/cozy-tools/filesystem"
	"github.com/alvnukov/cozy-tools/tool"
)

func decodeStrict(input json.RawMessage, target any) error {
	if len(bytes.TrimSpace(input)) == 0 {
		input = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return tool.WrapError(tool.CodeInvalidArgument, "decode tool input", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return tool.WrapError(tool.CodeInvalidArgument, "decode tool input", err)
	}
	return nil
}

func invalid(message string) error {
	return tool.NewError(tool.CodeInvalidArgument, message)
}

func require(value, field string) error {
	if value == "" {
		return invalid(field + " is required")
	}
	return nil
}

func mapFilesystemError(message string, err error) error {
	if err == nil {
		return nil
	}
	var existing *tool.Error
	if errors.As(err, &existing) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		wrapped := tool.WrapError(tool.CodeUnavailable, message, err)
		wrapped.Retryable = errors.Is(err, context.DeadlineExceeded)
		return wrapped
	}
	var coded *filesystem.Error
	if !errors.As(err, &coded) {
		return tool.WrapError(tool.CodeInternal, message, err)
	}
	var code tool.ErrorCode
	switch coded.Code {
	case filesystem.CodeInvalidInput:
		code = tool.CodeInvalidArgument
	case filesystem.CodeNotFound:
		code = tool.CodeNotFound
	case filesystem.CodeConflict:
		code = tool.CodeConflict
	case filesystem.CodeLimit:
		code = tool.CodeLimitExceeded
	case filesystem.CodePermission:
		code = tool.CodePermissionDenied
	default:
		code = tool.CodeInternal
	}
	return tool.WrapError(code, message, err)
}

func tag(hash string) string {
	if len(hash) < TagLength {
		return ""
	}
	return strings.ToUpper(hash[:TagLength])
}
