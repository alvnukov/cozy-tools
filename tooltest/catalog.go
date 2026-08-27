package tooltest

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/alvnukov/cozy-tools/tool"
)

// ValidateCatalog checks transport-neutral invariants for every tool.
func ValidateCatalog(t testing.TB, catalog *tool.Catalog) {
	t.Helper()
	if catalog == nil {
		t.Fatal("catalog is nil")
	}
	for _, candidate := range catalog.Tools() {
		name := candidate.Spec.Name
		if err := candidate.Validate(); err != nil {
			t.Fatalf("tool %q is invalid: %v", name, err)
		}
		if !json.Valid(candidate.Spec.InputSchema.Bytes()) {
			t.Fatalf("tool %q input schema is not valid JSON", name)
		}
		if candidate.Spec.OperationField == "" {
			if _, err := candidate.Spec.ResolveOperation(json.RawMessage(`{}`)); err != nil {
				t.Fatalf("tool %q resolve direct operation: %v", name, err)
			}
			continue
		}
		for operationName := range candidate.Spec.Operations {
			input, err := json.Marshal(map[string]string{candidate.Spec.OperationField: operationName})
			if err != nil {
				t.Fatal(err)
			}
			operation, err := candidate.Spec.ResolveOperation(input)
			if err != nil {
				t.Fatalf("tool %q resolve operation %q: %v", name, operationName, err)
			}
			if operation.Name != operationName {
				t.Fatalf("tool %q resolved operation = %q, want %q", name, operation.Name, operationName)
			}
		}
	}
}

// RequireNames checks the complete catalog surface in declaration order.
func RequireNames(t testing.TB, catalog *tool.Catalog, want ...string) {
	t.Helper()
	if catalog == nil {
		t.Fatal("catalog is nil")
	}
	if got := catalog.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}
}
