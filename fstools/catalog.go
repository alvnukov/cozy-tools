package fstools

import (
	"github.com/alvnukov/cozy-tools/tool"
)

func readOperation(name string) tool.Operation {
	return tool.Operation{
		Name:    name,
		Effects: tool.MustEffects(tool.EffectFilesystemRead),
		Hints:   tool.Hints{ReadOnly: true, Idempotent: true},
	}
}

func writeOperation(name string, destructive bool) tool.Operation {
	return tool.Operation{
		Name:    name,
		Effects: tool.MustEffects(tool.EffectFilesystemWrite),
		Hints:   tool.Hints{Destructive: destructive, Idempotent: true},
	}
}

// NativeCatalog returns an independently usable native filesystem catalog in
// declaration order: read, ls, find, grep, write, edit.
func NativeCatalog(config Config) (*tool.Catalog, error) {
	adapter, err := newAdapter(config)
	if err != nil {
		return nil, err
	}
	return tool.NewCatalog(
		tool.Tool{Spec: tool.Spec{Name: "read", Description: "Read a bounded root-relative file with editable line anchors.", InputSchema: nativeReadSchema(), Operation: readOperation("read")}, Handler: adapter.nativeRead},
		tool.Tool{Spec: tool.Spec{Name: "ls", Description: "List a root-relative directory as a deterministic tree.", InputSchema: nativeListSchema(), Operation: readOperation("ls")}, Handler: adapter.nativeList},
		tool.Tool{Spec: tool.Spec{Name: "find", Description: "Find root-relative paths matching a glob.", InputSchema: nativeFindSchema(), Operation: readOperation("find")}, Handler: adapter.nativeFind},
		tool.Tool{Spec: tool.Spec{Name: "grep", Description: "Search files and return editable anchored matches grouped by file.", InputSchema: nativeGrepSchema(), Operation: readOperation("grep")}, Handler: adapter.nativeGrep},
		tool.Tool{Spec: tool.Spec{Name: "write", Description: "Atomically create or overwrite a root-relative file.", InputSchema: nativeWriteSchema(), Operation: writeOperation("write", true)}, Handler: adapter.nativeWrite},
		tool.Tool{Spec: tool.Spec{Name: "edit", Description: "Apply guarded hashline edits to one file snapshot.", InputSchema: nativeEditSchema(), Operation: writeOperation("edit", true)}, Handler: adapter.nativeEdit},
	)
}

// MCPCatalog returns an independently usable MCP filesystem catalog containing
// the aggregate file and edit tools.
func MCPCatalog(config Config) (*tool.Catalog, error) {
	adapter, err := newAdapter(config)
	if err != nil {
		return nil, err
	}
	return tool.NewCatalog(
		tool.Tool{
			Spec: tool.Spec{
				Name:           "file",
				Description:    "Bounded repository file reads, searches, listings, writes, and creates.",
				InputSchema:    mcpFileSchema(),
				OperationField: "action",
				Operations: map[string]tool.Operation{
					"read":      readOperation("read"),
					"read_many": readOperation("read_many"),
					"search":    readOperation("search"),
					"list":      readOperation("list"),
					"write":     writeOperation("write", true),
					"create":    writeOperation("create", false),
				},
			},
			Handler: adapter.mcpFile,
		},
		tool.Tool{
			Spec: tool.Spec{
				Name:           "edit",
				Description:    "Guarded idempotent repository text edits and create-if-absent.",
				InputSchema:    mcpEditSchema(),
				OperationField: "action",
				Operations: map[string]tool.Operation{
					"replace":          writeOperation("replace", true),
					"append_unique":    writeOperation("append_unique", false),
					"delete_exact":     writeOperation("delete_exact", true),
					"create_if_absent": writeOperation("create_if_absent", false),
				},
			},
			Handler: adapter.mcpEdit,
		},
	)
}
