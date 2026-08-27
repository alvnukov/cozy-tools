package tool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Schema is an immutable JSON Schema value.
type Schema struct {
	raw json.RawMessage
}

// NewSchema encodes v as a JSON Schema object.
func NewSchema(v any) (Schema, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return Schema{}, fmt.Errorf("encode schema: %w", err)
	}
	return ParseSchema(raw)
}

// MustSchema is NewSchema for package-level declarations.
func MustSchema(v any) Schema {
	schema, err := NewSchema(v)
	if err != nil {
		panic(err)
	}
	return schema
}

// ParseSchema validates and copies a JSON Schema object.
func ParseSchema(raw []byte) (Schema, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return Schema{}, errors.New("schema is empty")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return Schema{}, fmt.Errorf("schema must be a JSON object: %w", err)
	}
	if object == nil {
		return Schema{}, errors.New("schema must be a JSON object")
	}
	return Schema{raw: bytes.Clone(raw)}, nil
}

// IsZero reports whether no schema was supplied.
func (s Schema) IsZero() bool { return len(s.raw) == 0 }

// Bytes returns a defensive copy of the encoded schema.
func (s Schema) Bytes() []byte { return bytes.Clone(s.raw) }

// Decode unmarshals the schema into target.
func (s Schema) Decode(target any) error {
	if s.IsZero() {
		return errors.New("schema is empty")
	}
	return json.Unmarshal(s.raw, target)
}

// MarshalJSON implements json.Marshaler.
func (s Schema) MarshalJSON() ([]byte, error) {
	if s.IsZero() {
		return []byte("null"), nil
	}
	return s.Bytes(), nil
}
