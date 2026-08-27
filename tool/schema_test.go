package tool_test

import (
	"encoding/json"
	"testing"

	"github.com/alvnukov/cozy-tools/tool"
)

func TestSchemaCopiesInputAndOutput(t *testing.T) {
	raw := []byte(`{"type":"object"}`)
	schema, err := tool.ParseSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[2] = 'X'
	if got := string(schema.Bytes()); got != `{"type":"object"}` {
		t.Fatalf("schema changed with input: %s", got)
	}

	copy := schema.Bytes()
	copy[2] = 'X'
	if got := string(schema.Bytes()); got != `{"type":"object"}` {
		t.Fatalf("schema changed with output: %s", got)
	}

	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); got != `{"type":"object"}` {
		t.Fatalf("encoded schema = %s", got)
	}
}

func TestSpecRejectsNonObjectInputSchema(t *testing.T) {
	candidate := testTool("bad")
	candidate.Spec.InputSchema = tool.MustSchema(map[string]any{"type": "string"})
	if err := candidate.Spec.Validate(); err == nil {
		t.Fatal("non-object input schema was accepted")
	}
}
