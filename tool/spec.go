package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type inputSchemaShape struct {
	Type       string                     `json:"type"`
	Properties map[string]json.RawMessage `json:"properties"`
	Required   []string                   `json:"required"`
}

type operationPropertyShape struct {
	Type string   `json:"type"`
	Enum []string `json:"enum"`
}

// Spec is the transport-neutral definition of a tool.
//
// Direct tools use Operation. Action-dispatch tools set OperationField and
// Operations; permission policy can then resolve effects from the input before
// the handler runs.
type Spec struct {
	Name           string
	Description    string
	InputSchema    Schema
	OutputSchema   Schema
	Operation      Operation
	OperationField string
	Operations     map[string]Operation
}

// Validate checks contract invariants without executing the tool.
func (s Spec) Validate() error {
	if !validName.MatchString(s.Name) {
		return fmt.Errorf("invalid tool name %q", s.Name)
	}
	if strings.TrimSpace(s.Description) == "" {
		return errors.New("tool description is required")
	}
	if s.InputSchema.IsZero() {
		return errors.New("input schema is required")
	}
	var schema inputSchemaShape
	if err := s.InputSchema.Decode(&schema); err != nil {
		return fmt.Errorf("decode input schema: %w", err)
	}
	if schema.Type != "object" {
		return fmt.Errorf("input schema type must be object, got %q", schema.Type)
	}
	if s.OperationField == "" {
		if len(s.Operations) != 0 {
			return errors.New("operations require an operation field")
		}
		return validateOperation(s.directOperation())
	}
	if strings.TrimSpace(s.OperationField) != s.OperationField {
		return errors.New("operation field must not contain surrounding whitespace")
	}
	if len(s.Operations) == 0 {
		return errors.New("action-dispatch tool requires operations")
	}
	if err := validateOperationSchema(schema, s.OperationField, s.Operations); err != nil {
		return err
	}
	for name, operation := range s.Operations {
		if strings.TrimSpace(name) == "" {
			return errors.New("operation name is required")
		}
		if operation.Name != "" && operation.Name != name {
			return fmt.Errorf("operation %q declares mismatched name %q", name, operation.Name)
		}
		operation.Name = name
		if err := validateOperation(operation); err != nil {
			return fmt.Errorf("operation %q: %w", name, err)
		}
	}
	return nil
}

func validateOperationSchema(schema inputSchemaShape, field string, operations map[string]Operation) error {
	raw, ok := schema.Properties[field]
	if !ok {
		return fmt.Errorf("operation field %q is missing from input schema properties", field)
	}
	var property operationPropertyShape
	if err := json.Unmarshal(raw, &property); err != nil {
		return fmt.Errorf("decode operation field %q schema: %w", field, err)
	}
	if property.Type != "string" {
		return fmt.Errorf("operation field %q type must be string, got %q", field, property.Type)
	}
	if !slices.Contains(schema.Required, field) {
		return fmt.Errorf("operation field %q must be required", field)
	}
	if len(property.Enum) != len(operations) {
		return fmt.Errorf("operation field %q enum and operations differ", field)
	}
	for _, name := range property.Enum {
		if _, ok := operations[name]; !ok {
			return fmt.Errorf("operation field %q enum contains unknown operation %q", field, name)
		}
	}
	return nil
}

func validateOperation(operation Operation) error {
	if strings.TrimSpace(operation.Name) == "" {
		return errors.New("operation name is required")
	}
	if err := operation.Effects.validate(); err != nil {
		return err
	}
	if operation.Hints.ReadOnly && operation.Effects.Mutates() {
		return errors.New("read-only operation declares mutating effects")
	}
	if operation.Hints.ReadOnly && operation.Hints.Destructive {
		return errors.New("read-only operation cannot be destructive")
	}
	return nil
}

func (s Spec) directOperation() Operation {
	operation := s.Operation.clone()
	if operation.Name == "" {
		operation.Name = s.Name
	}
	return operation
}

// ResolveOperation selects permission metadata for input.
func (s Spec) ResolveOperation(input json.RawMessage) (Operation, error) {
	if s.OperationField == "" {
		return s.directOperation(), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil {
		return Operation{}, WrapError(CodeInvalidArgument, "decode tool input", err)
	}
	raw, ok := object[s.OperationField]
	if !ok {
		return Operation{}, NewError(CodeInvalidArgument, fmt.Sprintf("%s is required", s.OperationField))
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil || strings.TrimSpace(name) == "" {
		return Operation{}, NewError(CodeInvalidArgument, fmt.Sprintf("%s must be a non-empty string", s.OperationField))
	}
	operation, ok := s.Operations[name]
	if !ok {
		return Operation{}, NewError(CodeInvalidArgument, fmt.Sprintf("unknown %s %q", s.OperationField, name))
	}
	operation.Name = name
	return operation.clone(), nil
}

func (s Spec) clone() Spec {
	s.Operation = s.Operation.clone()
	if s.Operations != nil {
		operations := s.Operations
		s.Operations = make(map[string]Operation, len(operations))
		for name, operation := range operations {
			s.Operations[name] = operation.clone()
		}
	}
	return s
}
