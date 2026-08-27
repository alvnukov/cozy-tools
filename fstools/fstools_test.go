package fstools_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alvnukov/cozy-tools/filesystem"
	"github.com/alvnukov/cozy-tools/fstools"
	"github.com/alvnukov/cozy-tools/tool"
	"github.com/alvnukov/cozy-tools/tooltest"
)

func catalogs(t *testing.T, config fstools.Config) (*tool.Catalog, *tool.Catalog) {
	t.Helper()
	native, err := fstools.NativeCatalog(config)
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := fstools.MCPCatalog(config)
	if err != nil {
		t.Fatal(err)
	}
	return native, mcp
}

func execute(t *testing.T, catalog *tool.Catalog, name string, input any) (tool.Result, error) {
	t.Helper()
	candidate, ok := catalog.Lookup(name)
	if !ok {
		t.Fatalf("tool %q not found", name)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return candidate.Execute(context.Background(), raw)
}

func executeOK(t *testing.T, catalog *tool.Catalog, name string, input any) tool.Result {
	t.Helper()
	result, err := execute(t, catalog, name, input)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func structuredMap(t *testing.T, result tool.Result) map[string]any {
	t.Helper()
	raw, err := json.Marshal(result.Structured)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func sha(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func TestCatalogSurfacesSchemasAndActionEffects(t *testing.T) {
	root := t.TempDir()
	native, mcp := catalogs(t, fstools.Config{RootCapability: fstools.FixedRoot(root)})
	tooltest.RequireNames(t, native, "read", "ls", "find", "grep", "write", "edit")
	tooltest.RequireNames(t, mcp, "file", "edit")
	tooltest.ValidateCatalog(t, native)
	tooltest.ValidateCatalog(t, mcp)

	for _, catalog := range []*tool.Catalog{native, mcp} {
		for _, candidate := range catalog.Tools() {
			copy := candidate.Spec.InputSchema.Bytes()
			copy[0] = 'x'
			fresh, _ := catalog.Lookup(candidate.Spec.Name)
			if !json.Valid(fresh.Spec.InputSchema.Bytes()) {
				t.Fatalf("%s schema mutated through Bytes", candidate.Spec.Name)
			}
		}
	}

	wantFileReads := map[string]bool{"read": true, "read_many": true, "search": true, "list": true}
	file, _ := mcp.Lookup("file")
	for action, operation := range file.Spec.Operations {
		if wantFileReads[action] {
			if !operation.Hints.ReadOnly || !operation.Effects.Contains(tool.EffectFilesystemRead) || operation.Effects.Mutates() {
				t.Errorf("file/%s metadata = %+v", action, operation)
			}
		} else if operation.Hints.ReadOnly || !operation.Effects.Contains(tool.EffectFilesystemWrite) || !operation.Effects.Mutates() {
			t.Errorf("file/%s metadata = %+v", action, operation)
		}
	}
	edit, _ := mcp.Lookup("edit")
	for action, operation := range edit.Spec.Operations {
		if operation.Hints.ReadOnly || !operation.Effects.Contains(tool.EffectFilesystemWrite) || !operation.Effects.Mutates() {
			t.Errorf("edit/%s metadata = %+v", action, operation)
		}
	}
}

func TestRootCapabilityRequestsAndDenial(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests []string
	capability := fstools.RootCapabilityFunc(func(_ context.Context, requested string) (string, error) {
		requests = append(requests, requested)
		return root, nil
	})
	native, mcp := catalogs(t, fstools.Config{RootCapability: capability})
	executeOK(t, native, "read", map[string]any{"path": "a.txt"})
	executeOK(t, mcp, "file", map[string]any{"repo_path": "workspace-A", "action": "read", "path": "a.txt"})
	if got := fmt.Sprint(requests); got != "[ workspace-A]" {
		t.Fatalf("root requests = %q, want native empty then MCP repo_path", got)
	}

	denied := errors.New("host policy denied root")
	denying := fstools.RootCapabilityFunc(func(context.Context, string) (string, error) { return "", denied })
	deniedCatalog, err := fstools.NativeCatalog(fstools.Config{RootCapability: denying})
	if err != nil {
		t.Fatal(err)
	}
	_, err = execute(t, deniedCatalog, "read", map[string]any{"path": "a.txt"})
	if tool.CodeOf(err) != tool.CodePermissionDenied || !errors.Is(err, denied) {
		t.Fatalf("denial = %v (%s)", err, tool.CodeOf(err))
	}
	_, err = fstools.NativeCatalog(fstools.Config{})
	if tool.CodeOf(err) != tool.CodePermissionDenied {
		t.Fatalf("missing capability = %v (%s)", err, tool.CodeOf(err))
	}
	empty, _ := fstools.NativeCatalog(fstools.Config{RootCapability: fstools.FixedRoot("")})
	_, err = execute(t, empty, "read", map[string]any{"path": "a.txt"})
	if tool.CodeOf(err) != tool.CodePermissionDenied {
		t.Fatalf("empty root = %v (%s)", err, tool.CodeOf(err))
	}
}

func TestNativeReadEditAndGrepAnchorsRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	native, _ := catalogs(t, fstools.Config{RootCapability: fstools.FixedRoot(root)})
	read := executeOK(t, native, "read", map[string]any{"path": "note.txt"})
	header := regexp.MustCompile(`(?m)^@file note\.txt#([A-F0-9]{8})$`).FindStringSubmatch(read.Content)
	line := regexp.MustCompile(`(?m)^2#([a-z]{3})\|beta$`).FindStringSubmatch(read.Content)
	if header == nil || line == nil {
		t.Fatalf("unexpected read rendering:\n%s", read.Content)
	}
	executeOK(t, native, "edit", map[string]any{
		"path": "note.txt", "hash": header[1],
		"edits": []any{map[string]any{"from": "2#" + line[1], "content": "BETA"}},
	})
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("edited file = %q", got)
	}
	_, err := execute(t, native, "edit", map[string]any{
		"path": "note.txt", "hash": header[1],
		"edits": []any{map[string]any{"from": "2#" + line[1], "content": "again"}},
	})
	if tool.CodeOf(err) != tool.CodeConflict {
		t.Fatalf("stale edit = %v (%s)", err, tool.CodeOf(err))
	}

	grep := executeOK(t, native, "grep", map[string]any{"pattern": "gamma", "literal": true})
	grepHeader := regexp.MustCompile(`(?m)^@file note\.txt#([A-F0-9]{8})$`).FindStringSubmatch(grep.Content)
	grepLine := regexp.MustCompile(`(?m)^3#([a-z]{3})\|gamma$`).FindStringSubmatch(grep.Content)
	if grepHeader == nil || grepLine == nil {
		t.Fatalf("grep output is not editable:\n%s", grep.Content)
	}
	executeOK(t, native, "edit", map[string]any{
		"path": "note.txt", "hash": grepHeader[1],
		"edits": []any{map[string]any{"from": "3#" + grepLine[1], "content": "GAMMA"}},
	})
}

func TestNativeTreeAndRendererTruncation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/one.txt", "a/two.txt", "b.txt"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("needle "+strings.Repeat("x", 600)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	limits := filesystem.Limits{ListEntries: 2}
	native, _ := catalogs(t, fstools.Config{RootCapability: fstools.FixedRoot(root), Limits: limits, ModelBytes: 1024})
	listed := executeOK(t, native, "ls", map[string]any{"max_depth": 4})
	if !strings.Contains(listed.Content, "|-- ") || !strings.Contains(listed.Content, "[truncated:") {
		t.Fatalf("tree/truncation rendering:\n%s", listed.Content)
	}
	for len(listed.Content) > 0 {
		r, size := utf8.DecodeRuneInString(listed.Content)
		if r > 127 {
			t.Fatalf("tree contains non-ASCII rune %q", r)
		}
		listed.Content = listed.Content[size:]
	}
	grep := executeOK(t, native, "grep", map[string]any{"pattern": "needle", "literal": true})
	if len(grep.Content) > 1024 || !strings.Contains(grep.Content, "[truncated: renderer byte limit]") {
		t.Fatalf("bounded grep len=%d:\n%s", len(grep.Content), grep.Content)
	}
}

func TestMCPReadBudgetsBinaryAndReadMany(t *testing.T) {
	root := t.TempDir()
	binary := append([]byte{0, 1, 2}, []byte(strings.Repeat("z", 4000))...)
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), binary, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "text.txt"), []byte(strings.Repeat("line\n", 1000)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, mcp := catalogs(t, fstools.Config{RootCapability: fstools.FixedRoot(root), ModelBytes: 2048})
	read := executeOK(t, mcp, "file", map[string]any{"repo_path": "repo", "action": "read", "path": "binary.dat"})
	raw, _ := json.Marshal(read.Structured)
	if len(raw) > 2048 {
		t.Fatalf("binary structured result is %d bytes", len(raw))
	}
	value := structuredMap(t, read)
	if value["binary"] != true || value["sha256"] != sha(binary) {
		t.Fatalf("binary metadata = %#v", value)
	}
	encoded, ok := value["content_b64"].(string)
	if !ok {
		t.Fatalf("missing content_b64: %#v", value)
	}
	if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
		t.Fatalf("invalid returned base64: %v", err)
	}

	many := executeOK(t, mcp, "file", map[string]any{
		"repo_path": "repo", "action": "read_many", "paths": []string{"binary.dat", "text.txt"},
	})
	raw, _ = json.Marshal(many.Structured)
	if len(raw) > 2048 || len(many.Content) > 256 {
		t.Fatalf("read_many budgets: structured=%d display=%d", len(raw), len(many.Content))
	}
	manyValue := structuredMap(t, many)
	if manyValue["requested"] != float64(2) {
		t.Fatalf("read_many metadata = %#v", manyValue)
	}
}

func TestMCPWritesGuardedEditsCRLFAndIdempotency(t *testing.T) {
	root := t.TempDir()
	native, mcp := catalogs(t, fstools.Config{RootCapability: fstools.FixedRoot(root)})
	created := executeOK(t, mcp, "file", map[string]any{
		"repo_path": "repo", "action": "create", "path": "crlf.txt", "content": "one\r\ntwo\r\n",
	})
	createdData := structuredMap(t, created)
	initialHash := createdData["new_sha256"].(string)
	if initialHash != sha([]byte("one\r\ntwo\r\n")) {
		t.Fatalf("create hash = %s", initialHash)
	}

	replaced := executeOK(t, mcp, "edit", map[string]any{
		"repo_path": "repo", "path": "crlf.txt", "action": "replace",
		"old_text": "one", "new_text": "ONE", "expected_hash": initialHash[:8],
	})
	replacedData := structuredMap(t, replaced)
	replacedHash := replacedData["new_sha256"].(string)
	got, _ := os.ReadFile(filepath.Join(root, "crlf.txt"))
	if string(got) != "ONE\r\ntwo\r\n" {
		t.Fatalf("CRLF replace = %q", got)
	}
	noop := executeOK(t, mcp, "edit", map[string]any{
		"repo_path": "repo", "path": "crlf.txt", "action": "replace",
		"old_text": "one", "new_text": "ONE", "expected_hash": replacedHash,
	})
	if structuredMap(t, noop)["changed"] != false {
		t.Fatalf("idempotent replace = %#v", structuredMap(t, noop))
	}
	_, err := execute(t, mcp, "edit", map[string]any{
		"repo_path": "repo", "path": "crlf.txt", "action": "replace",
		"old_text": "two", "new_text": "TWO", "expected_hash": initialHash,
	})
	if tool.CodeOf(err) != tool.CodeConflict {
		t.Fatalf("stale replace = %v (%s)", err, tool.CodeOf(err))
	}

	appended := executeOK(t, mcp, "edit", map[string]any{
		"repo_path": "repo", "path": "crlf.txt", "action": "append_unique",
		"content": "three\r\n", "expected_hash": replacedHash,
	})
	appendHash := structuredMap(t, appended)["new_sha256"].(string)
	got, _ = os.ReadFile(filepath.Join(root, "crlf.txt"))
	if string(got) != "ONE\r\ntwo\r\nthree\r\n" {
		t.Fatalf("CRLF append = %q", got)
	}
	appendNoop := executeOK(t, mcp, "edit", map[string]any{
		"repo_path": "repo", "path": "crlf.txt", "action": "append_unique",
		"content": "three\r\n", "expected_hash": appendHash,
	})
	if structuredMap(t, appendNoop)["changed"] != false {
		t.Fatalf("idempotent append = %#v", structuredMap(t, appendNoop))
	}

	nativeRead := executeOK(t, native, "read", map[string]any{"path": "crlf.txt"})
	if !strings.Contains(nativeRead.Content, "#"+strings.ToUpper(appendHash[:8])) {
		t.Fatalf("native/MCP hash parity failed:\n%s", nativeRead.Content)
	}
}

func TestMCPBinaryWriteCreateIfAbsentAndValidation(t *testing.T) {
	root := t.TempDir()
	_, mcp := catalogs(t, fstools.Config{RootCapability: fstools.FixedRoot(root)})
	binary := []byte{0, 9, 8, 7}
	executeOK(t, mcp, "file", map[string]any{
		"repo_path": "repo", "action": "write", "path": "data.bin",
		"content_b64": base64.StdEncoding.EncodeToString(binary),
	})
	got, _ := os.ReadFile(filepath.Join(root, "data.bin"))
	if string(got) != string(binary) {
		t.Fatalf("binary write = %v", got)
	}
	created := executeOK(t, mcp, "edit", map[string]any{
		"repo_path": "repo", "path": "new.txt", "action": "create_if_absent", "content": "new",
	})
	if structuredMap(t, created)["status"] != "created" {
		t.Fatalf("create_if_absent = %#v", structuredMap(t, created))
	}
	unchanged := executeOK(t, mcp, "edit", map[string]any{
		"repo_path": "repo", "path": "new.txt", "action": "create_if_absent", "content": "new",
	})
	if structuredMap(t, unchanged)["status"] != "unchanged" {
		t.Fatalf("idempotent create_if_absent = %#v", structuredMap(t, unchanged))
	}

	invalidInputs := []struct {
		tool  string
		input map[string]any
	}{
		{"file", map[string]any{"repo_path": "repo", "action": "unknown"}},
		{"file", map[string]any{"repo_path": "repo", "action": "write", "path": "x"}},
		{"edit", map[string]any{"repo_path": "repo", "path": "new.txt", "action": "delete_exact", "content": "new"}},
		{"edit", map[string]any{"repo_path": "repo", "path": "new.txt", "action": "replace", "old_text": "new", "new_text": "x", "expected_hash": "zzzz"}},
	}
	for _, test := range invalidInputs {
		_, err := execute(t, mcp, test.tool, test.input)
		if tool.CodeOf(err) != tool.CodeInvalidArgument {
			t.Errorf("%s %#v error = %v (%s)", test.tool, test.input, err, tool.CodeOf(err))
		}
	}
	_, err := execute(t, mcp, "file", map[string]any{"repo_path": "repo", "action": "list", "unexpected": true})
	if tool.CodeOf(err) != tool.CodeInvalidArgument {
		t.Fatalf("unknown field error = %v (%s)", err, tool.CodeOf(err))
	}
}
