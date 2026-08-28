package safefs

import (
	"os"
	"path/filepath"
	"testing"
)

func parentDirWriters() []struct {
	name string
	call func(r *Root, name string) error
} {
	return []struct {
		name string
		call func(r *Root, name string) error
	}{
		{"WriteFile", func(r *Root, n string) error { return r.WriteFile(n, []byte("x"), 0o644) }},
		{"WriteFileAtomic", func(r *Root, n string) error { return r.WriteFileAtomic(n, []byte("x"), 0o644) }},
		{"CreateExclusive", func(r *Root, n string) error { return r.CreateExclusive(n, []byte("x"), 0o644) }},
	}
}

// edit.write promises to create parent directories; every root write has to
// keep that promise instead of failing on the missing lstat of a parent.
func TestWritesCreateMissingParents(t *testing.T) {
	base := t.TempDir()
	root, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	for _, write := range parentDirWriters() {
		t.Run(write.name, func(t *testing.T) {
			target := filepath.ToSlash(filepath.Join("deep", write.name, "file.txt"))
			if err := write.call(root, target); err != nil {
				t.Fatalf("write with missing parents: %v", err)
			}
			data, err := root.ReadFile(target)
			if err != nil || string(data) != "x" {
				t.Fatalf("read back: %v, %q", err, data)
			}
		})
	}
}

// Creating a parent must not open a hole: a symlink that would take the
// write outside the root is refused, and nothing lands on the other side.
func TestWritesRefuseParentSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	root, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	if err := os.Symlink(outside, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	for _, write := range parentDirWriters() {
		t.Run(write.name, func(t *testing.T) {
			target := filepath.ToSlash(filepath.Join("link", "new", "file.txt"))
			if err := write.call(root, target); err == nil {
				t.Fatalf("%s wrote through a symlink escaping the root", write.name)
			}
			if _, statErr := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(statErr) {
				t.Fatalf("%s escaped the root: %v", write.name, statErr)
			}
		})
	}
}
