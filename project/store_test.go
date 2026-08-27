package project

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreReturnsRepoScopedDirs(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(t.TempDir(), "my repo")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	name, err := Name(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "my-repo-") {
		t.Fatalf("project name = %q", name)
	}
	logsDir, err := store.LogsDir(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	wantLogs := filepath.Join(root, "repos", name, "logs")
	if logsDir != wantLogs {
		t.Fatalf("logs dir = %q, want %q", logsDir, wantLogs)
	}
}
