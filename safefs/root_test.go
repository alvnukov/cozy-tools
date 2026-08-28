package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRootReadWriteInsideBoundary(t *testing.T) {
	t.Parallel()
	root, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := root.MkdirAll("nested", 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := root.WriteFile("nested/file.txt", []byte("inside"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	data, err := root.ReadFile("nested/file.txt")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "inside" {
		t.Fatalf("ReadFile() = %q, want %q", got, "inside")
	}
}

func TestRootRejectsSymlinkedFileOutsideBoundary(t *testing.T) {
	t.Parallel()
	requireSymlinkSupport(t)
	rootPath := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(rootPath, "link.txt")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	root, err := Open(rootPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if _, err := root.ReadFile("link.txt"); err == nil {
		t.Fatal("ReadFile() unexpectedly followed a symlink outside the root")
	}
	if err := root.WriteFile("link.txt", []byte("changed"), 0o600); err == nil {
		t.Fatal("WriteFile() unexpectedly followed a symlink outside the root")
	}
	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("ReadFile(outside) error = %v", err)
	}
	if got := string(data); got != "outside" {
		t.Fatalf("outside file changed to %q", got)
	}
}

func TestRootRejectsSymlinkedParentOutsideBoundary(t *testing.T) {
	t.Parallel()
	requireSymlinkSupport(t)
	rootPath := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(rootPath, "linked-dir")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	root, err := Open(rootPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := root.MkdirAll("linked-dir/nested", 0o700); err == nil {
		t.Fatal("MkdirAll() unexpectedly followed a symlink outside the root")
	}
	if err := root.WriteFile("linked-dir/file.txt", []byte("changed"), 0o600); err == nil {
		t.Fatal("WriteFile() unexpectedly followed a symlink outside the root")
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "nested")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside nested directory error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file error = %v, want not exist", err)
	}
}

func TestEnsureCreatesNestedRoot(t *testing.T) {
	t.Parallel()
	rootPath := filepath.Join(t.TempDir(), "one", "two")
	root, err := Ensure(rootPath, 0o700)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := root.WriteFile("file.txt", []byte("ok"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(rootPath, "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile(created root) error = %v", err)
	}
	if got := string(data); got != "ok" {
		t.Fatalf("created root content = %q, want %q", got, "ok")
	}
}

func requireSymlinkSupport(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges on Windows")
	}
}
