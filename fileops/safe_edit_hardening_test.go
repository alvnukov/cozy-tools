package fileops

import (
	"os"
	"path/filepath"
	"testing"
)

// The read tool displays LF-normalized lines, so a caller copying what it
// saw sends LF text even for a CRLF file — and the raw-bytes match then
// fails on every edit. When the file carries CRLF endings, LF-span edits
// are translated to the file's own style before matching and writing.
func TestGuardedReplaceSupportsCRLFFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	content := "alpha\r\nbeta\r\ngamma"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyGuardedReplace(ReplaceRequest{
		RepoPath:     dir,
		Path:         "crlf.txt",
		ExpectedHash: Hash([]byte(content)),
		Old:          "alpha\nbeta",
		New:          "alpha\nBETA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %#v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "alpha\r\nBETA\r\ngamma"; string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

// Deleting one block used to collapse every triple newline in the file,
// silently rewriting unrelated content such as a legitimate run inside
// another section. The collapse now happens at the deletion seam only.
func TestDeleteExactBlockLeavesUnrelatedNewlineRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	content := "intro\n\n\nspaced\n\nBLOCK\n\ntail\n\n\noutro"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := DeleteExactBlock(DeleteExactBlockRequest{
		RepoPath:     dir,
		Path:         "doc.txt",
		ExpectedHash: Hash([]byte(content)),
		Block:        "BLOCK\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %#v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "intro\n\n\nspaced\n\ntail\n\n\noutro"; string(got) != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}
