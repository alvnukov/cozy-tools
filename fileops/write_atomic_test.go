package fileops

import (
	"os"
	"path/filepath"
	"testing"
)

// The atomic write replaces the file by rename; the original file's mode
// must survive that replacement exactly as os.WriteFile's in-place write
// preserved it.
func TestGuardedReplacePreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	content := "#!/bin/sh\necho old\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyGuardedReplace(ReplaceRequest{
		RepoPath:     dir,
		Path:         "script.sh",
		ExpectedHash: Hash([]byte(content)),
		Old:          "echo old",
		New:          "echo new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %#v", result)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want the original 0755 preserved", info.Mode().Perm())
	}
}
