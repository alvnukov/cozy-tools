package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/alvnukov/cozy-tools/tool"
)

func TestCatalogPreservesOrderAndRejectsDuplicates(t *testing.T) {
	first := testTool("read")
	second := testTool("write")

	catalog, err := tool.NewCatalog(first, second)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := catalog.Names(), []string{"read", "write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if _, err := tool.NewCatalog(first, first); err == nil {
		t.Fatal("duplicate tool was accepted")
	}
}

func TestCatalogDefensivelyCopiesOperationMetadata(t *testing.T) {
	effects := tool.MustEffects(tool.EffectFilesystemRead)
	candidate := testTool("file")
	candidate.Spec.Operation.Effects = effects
	catalog := tool.MustCatalog(candidate)

	effects[0] = tool.EffectFilesystemWrite
	resolved, ok := catalog.Lookup("file")
	if !ok {
		t.Fatal("file not found")
	}
	if !resolved.Spec.Operation.Effects.Contains(tool.EffectFilesystemRead) {
		t.Fatalf("catalog effects mutated: %v", resolved.Spec.Operation.Effects)
	}
}

func TestResolveActionDispatchOperation(t *testing.T) {
	spec := testTool("file").Spec
	spec.Operation = tool.Operation{}
	spec.OperationField = "action"
	spec.Operations = map[string]tool.Operation{
		"read":  {Effects: tool.MustEffects(tool.EffectFilesystemRead), Hints: tool.Hints{ReadOnly: true}},
		"write": {Effects: tool.MustEffects(tool.EffectFilesystemWrite)},
	}
	spec.InputSchema = tool.MustSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"read", "write"}},
		},
		"required": []string{"action"},
	})
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}

	operation, err := spec.ResolveOperation(json.RawMessage(`{"action":"write"}`))
	if err != nil {
		t.Fatal(err)
	}
	if operation.Name != "write" || !operation.Effects.Contains(tool.EffectFilesystemWrite) {
		t.Fatalf("unexpected operation: %+v", operation)
	}

	_, err = spec.ResolveOperation(json.RawMessage(`{"action":"missing"}`))
	if tool.CodeOf(err) != tool.CodeInvalidArgument {
		t.Fatalf("error = %v, code = %q", err, tool.CodeOf(err))
	}
}

func TestSpecRejectsOperationSchemaDriftAndUnsafeHints(t *testing.T) {
	spec := testTool("file").Spec
	spec.Operation = tool.Operation{}
	spec.OperationField = "action"
	spec.Operations = map[string]tool.Operation{
		"read": {Effects: tool.MustEffects(tool.EffectFilesystemRead), Hints: tool.Hints{ReadOnly: true}},
	}
	spec.InputSchema = tool.MustSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"read", "missing"}},
		},
		"required": []string{"action"},
	})
	if err := spec.Validate(); err == nil {
		t.Fatal("schema/operation drift was accepted")
	}

	unsafe := testTool("write").Spec
	unsafe.Operation = tool.Operation{
		Name:    "write",
		Effects: tool.MustEffects(tool.EffectFilesystemWrite),
		Hints:   tool.Hints{ReadOnly: true},
	}
	if err := unsafe.Validate(); err == nil {
		t.Fatal("read-only hint with write effect was accepted")
	}
}

func TestResultRenderingPrecedence(t *testing.T) {
	result := tool.Result{Content: "compact", Structured: map[string]any{"ok": true}, Output: "display"}
	if got, err := result.ModelContent(); err != nil || got != "compact" {
		t.Fatalf("ModelContent() = %q, %v", got, err)
	}
	if got, err := result.DisplayOutput(); err != nil || got != "display" {
		t.Fatalf("DisplayOutput() = %q, %v", got, err)
	}

	result = tool.Result{Structured: map[string]any{"ok": true}}
	if got, err := result.ModelContent(); err != nil || got != `{"ok":true}` {
		t.Fatalf("structured ModelContent() = %q, %v", got, err)
	}
}

func TestToolExecuteAndCodedErrors(t *testing.T) {
	sentinel := errors.New("boom")
	candidate := testTool("read")
	candidate.Handler = func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{}, tool.WrapError(tool.CodeUnavailable, "read failed", sentinel)
	}
	_, err := candidate.Execute(context.Background(), nil)
	if !errors.Is(err, sentinel) || tool.CodeOf(err) != tool.CodeUnavailable {
		t.Fatalf("unexpected error: %v (%q)", err, tool.CodeOf(err))
	}
}

func testTool(name string) tool.Tool {
	return tool.Tool{
		Spec: tool.Spec{
			Name:        name,
			Description: name + " tool",
			InputSchema: tool.MustSchema(map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}),
			Operation: tool.Operation{Name: name, Hints: tool.Hints{ReadOnly: true}},
		},
		Handler: func(context.Context, json.RawMessage) (tool.Result, error) {
			return tool.Result{Content: "ok"}, nil
		},
	}
}
