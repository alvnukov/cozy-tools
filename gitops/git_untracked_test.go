package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInitForTest(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", dir, err, out)
	}
}

func TestUntrackedCountScopesToDirectory(t *testing.T) {
	dir := t.TempDir()
	gitInitForTest(t, dir)
	registry := filepath.Join(dir, "obsidian-tasks")
	if err := os.MkdirAll(registry, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registry, "a.md"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.md"), []byte("o\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	count, ok := UntrackedCount(context.Background(), registry)
	if !ok {
		t.Fatal("registry inside a git repository reported no git tracking")
	}
	if count != 1 {
		t.Fatalf("untracked count = %d, want 1 (only the registry file)", count)
	}
}

func TestUntrackedCountCountsIndexAsTracked(t *testing.T) {
	dir := t.TempDir()
	gitInitForTest(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "a.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	count, ok := UntrackedCount(context.Background(), dir)
	if !ok || count != 0 {
		t.Fatalf("untracked = %d, ok = %v, want staged file counted as tracked", count, ok)
	}
}

func TestUntrackedCountOutsideGitRepository(t *testing.T) {
	count, ok := UntrackedCount(context.Background(), t.TempDir())
	if ok || count != 0 {
		t.Fatalf("untracked = %d, ok = %v, want outside-git semantics", count, ok)
	}
}
