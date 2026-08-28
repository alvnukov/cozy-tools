package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A search scoped to a single file always answered with zero matches: the
// walk names its root entry ".", and the skip that guards the directory
// root also ate the one file the caller asked to search. Grounding built
// on this tool trusts empty results, so a false negative is worse than no
// answer at all.
func TestSearchFilesInSingleFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "target.go"), []byte("alpha\nbeta NewTool(\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "other.go"), []byte("NewTool( elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SearchFilesInRepo(dir, "sub/target.go", "NewTool(", 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Matches) != 1 {
		t.Fatalf("result = %#v, want the one match in the scoped file", result)
	}
	if !strings.Contains(result.Matches[0], "target.go:2:") {
		t.Fatalf("match = %q, want the scoped file's name and line", result.Matches[0])
	}
}

// Patterns are literal substrings, not regular expressions — pipes,
// groups, and anchors must match as text.
func TestSearchFilesLiteralPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("has a|b literal\nfunc (c *Config) Validate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"a|b", "func (c *Config) Validate"} {
		result, err := SearchFilesInRepo(dir, "", pattern, 10)
		if err != nil {
			t.Fatal(err)
		}
		if result.Total != 1 {
			t.Fatalf("pattern %q: total = %d, want the literal match", pattern, result.Total)
		}
	}
}
