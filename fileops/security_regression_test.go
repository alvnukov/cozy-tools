package fileops

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadFilesRejectsOversizedFileBeforeReadingContents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not provide the same read barrier on Windows")
	}
	repo := t.TempDir()
	path := filepath.Join(repo, "large.txt")
	if err := os.WriteFile(path, make([]byte, maxReadFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(path, 0o600) }()

	result, err := ReadFilesInRepo(repo, []string{"large.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || !result.Files[0].Truncated {
		t.Fatalf("result = %#v, want oversized file omitted before content read", result)
	}
	if result.Files[0].Size != maxReadFileBytes+1 {
		t.Fatalf("size = %d, want %d", result.Files[0].Size, maxReadFileBytes+1)
	}
}
