package tool

import (
	"context"
	"encoding/json"
	"errors"
)

// Handler executes a tool with transport-decoded JSON arguments.
type Handler func(context.Context, json.RawMessage) (Result, error)

// Detailer derives a short pre-execution description from arguments.
type Detailer func(json.RawMessage) string

// Tool pairs a transport-neutral definition with its implementation.
type Tool struct {
	Spec           Spec
	Handler        Handler
	DetailFromArgs Detailer
}

// Validate checks the complete tool contract.
func (t Tool) Validate() error {
	if err := t.Spec.Validate(); err != nil {
		return err
	}
	if t.Handler == nil {
		return errors.New("tool handler is required")
	}
	return nil
}

// Execute invokes the tool implementation.
func (t Tool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Handler == nil {
		return Result{}, NewError(CodeInternal, "tool handler is not configured")
	}
	return t.Handler(ctx, input)
}

// Detail returns the optional pre-execution description.
func (t Tool) Detail(input json.RawMessage) string {
	if t.DetailFromArgs == nil {
		return ""
	}
	return t.DetailFromArgs(input)
}
