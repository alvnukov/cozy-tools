package tooltest_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alvnukov/cozy-tools/tool"
	"github.com/alvnukov/cozy-tools/tooltest"
)

func TestValidateCatalog(t *testing.T) {
	catalog := tool.MustCatalog(tool.Tool{
		Spec: tool.Spec{
			Name:        "file",
			Description: "File operations.",
			InputSchema: tool.MustSchema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"read"}},
				},
				"required": []string{"action"},
			}),
			OperationField: "action",
			Operations: map[string]tool.Operation{
				"read": {Effects: tool.MustEffects(tool.EffectFilesystemRead), Hints: tool.Hints{ReadOnly: true}},
			},
		},
		Handler: func(context.Context, json.RawMessage) (tool.Result, error) {
			return tool.Result{Content: "ok"}, nil
		},
	})

	tooltest.ValidateCatalog(t, catalog)
	tooltest.RequireNames(t, catalog, "file")
}
