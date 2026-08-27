package fstools

import "github.com/alvnukov/cozy-tools/tool"

func stringProperty(extra ...map[string]any) map[string]any {
	property := map[string]any{"type": "string"}
	for _, values := range extra {
		for key, value := range values {
			property[key] = value
		}
	}
	return property
}

func integerProperty(minimum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum}
}

func objectSchema(properties map[string]any, required ...string) tool.Schema {
	value := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		value["required"] = required
	}
	return tool.MustSchema(value)
}

func nativeReadSchema() tool.Schema {
	return objectSchema(map[string]any{
		"path":   stringProperty(map[string]any{"minLength": 1}),
		"offset": integerProperty(1),
		"limit":  integerProperty(1),
	}, "path")
}

func nativeListSchema() tool.Schema {
	return objectSchema(map[string]any{
		"path":        stringProperty(),
		"max_depth":   integerProperty(0),
		"limit":       integerProperty(1),
		"show_hidden": map[string]any{"type": "boolean"},
	})
}

func nativeFindSchema() tool.Schema {
	return objectSchema(map[string]any{
		"pattern":     stringProperty(map[string]any{"minLength": 1}),
		"path":        stringProperty(),
		"limit":       integerProperty(1),
		"show_hidden": map[string]any{"type": "boolean"},
	}, "pattern")
}

func nativeGrepSchema() tool.Schema {
	return objectSchema(map[string]any{
		"pattern":     stringProperty(map[string]any{"minLength": 1}),
		"path":        stringProperty(),
		"glob":        stringProperty(),
		"literal":     map[string]any{"type": "boolean"},
		"ignore_case": map[string]any{"type": "boolean"},
		"before":      integerProperty(0),
		"after":       integerProperty(0),
		"context":     integerProperty(0),
		"limit":       integerProperty(1),
	}, "pattern")
}

func nativeWriteSchema() tool.Schema {
	return objectSchema(map[string]any{
		"path":    stringProperty(map[string]any{"minLength": 1}),
		"content": stringProperty(),
	}, "path", "content")
}

func nativeEditSchema() tool.Schema {
	edit := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from":    stringProperty(map[string]any{"pattern": `^[1-9][0-9]*#[a-z]{3}$`}),
			"to":      stringProperty(map[string]any{"pattern": `^[1-9][0-9]*#[a-z]{3}$`}),
			"content": stringProperty(),
		},
		"required":             []string{"from"},
		"additionalProperties": false,
	}
	return objectSchema(map[string]any{
		"path": stringProperty(map[string]any{"minLength": 1}),
		"hash": stringProperty(map[string]any{"minLength": 4, "maxLength": 64, "pattern": `^[A-Fa-f0-9]+$`}),
		"edits": map[string]any{
			"type": "array", "minItems": 1, "items": edit,
		},
	}, "path", "hash", "edits")
}

func mcpFileSchema() tool.Schema {
	return objectSchema(map[string]any{
		"repo_path":   stringProperty(map[string]any{"minLength": 1}),
		"action":      stringProperty(map[string]any{"enum": []string{"read", "read_many", "search", "list", "write", "create"}}),
		"path":        stringProperty(),
		"paths":       map[string]any{"type": "array", "minItems": 1, "maxItems": maxReadManyFiles, "items": stringProperty(map[string]any{"minLength": 1})},
		"offset":      integerProperty(1),
		"limit":       integerProperty(1),
		"max_depth":   integerProperty(0),
		"show_hidden": map[string]any{"type": "boolean"},
		"pattern":     stringProperty(),
		"regex":       map[string]any{"type": "boolean"},
		"literal":     map[string]any{"type": "boolean"},
		"ignore_case": map[string]any{"type": "boolean"},
		"glob":        stringProperty(),
		"globs":       map[string]any{"type": "array", "maxItems": 32, "items": stringProperty(map[string]any{"minLength": 1})},
		"before":      integerProperty(0),
		"after":       integerProperty(0),
		"context":     integerProperty(0),
		"content":     stringProperty(),
		"content_b64": stringProperty(),
		"expected_hash": stringProperty(map[string]any{
			"minLength": 4, "maxLength": 64, "pattern": `^[A-Fa-f0-9]+$`,
		}),
	}, "repo_path", "action")
}

func mcpEditSchema() tool.Schema {
	return objectSchema(map[string]any{
		"repo_path":   stringProperty(map[string]any{"minLength": 1}),
		"path":        stringProperty(map[string]any{"minLength": 1}),
		"action":      stringProperty(map[string]any{"enum": []string{"replace", "append_unique", "delete_exact", "create_if_absent"}}),
		"old_text":    stringProperty(),
		"new_text":    stringProperty(),
		"content":     stringProperty(),
		"content_b64": stringProperty(),
		"expected_hash": stringProperty(map[string]any{
			"minLength": 4, "maxLength": 64, "pattern": `^[A-Fa-f0-9]+$`,
		}),
	}, "repo_path", "path", "action")
}
