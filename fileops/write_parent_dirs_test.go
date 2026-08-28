package fileops

import (
	"os"
	"path/filepath"
	"testing"
)

// The canonical edit.write path must create parent directories itself; the
// missing lstat of a new directory used to push callers toward shell writes
// the command policy refuses.
func TestWriteFileCreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	result, err := WriteFile(WriteFileRequest{
		RepoPath: dir,
		Path:     "nested/deeper/new-file.md",
		Content:  "created with parents",
	})
	if err != nil {
		t.Fatalf("write with missing parents: %v", err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok/changed", result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nested", "deeper", "new-file.md"))
	if err != nil || string(data) != "created with parents" {
		t.Fatalf("read back: %v, %q", err, data)
	}
}
