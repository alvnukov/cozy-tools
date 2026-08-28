package safefs

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// WriteFileAtomicOpts is the one authoritative atomic replacement in the
// file family: mode preservation, exact modes and create-only installs all
// behave the same no matter which higher-level API called in.
func TestWriteFileAtomicOptsPreservesExistingMode(t *testing.T) {
	root, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile("f.txt", []byte("v1"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFileAtomicOpts("f.txt", []byte("v2"), 0o644, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	info, err := root.Stat("f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want preserved 0640", info.Mode().Perm())
	}
	data, err := root.ReadFile("f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2" {
		t.Fatalf("content = %q, want v2", data)
	}
}

func TestWriteFileAtomicOptsExactMode(t *testing.T) {
	root, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFileAtomicOpts("g.txt", []byte("x"), 0o600, WriteOptions{ExactMode: true}); err != nil {
		t.Fatal(err)
	}
	info, err := root.Stat("g.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want exact 0600", info.Mode().Perm())
	}
}

func TestWriteFileAtomicOptsCreateOnly(t *testing.T) {
	dir := t.TempDir()
	root, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFileAtomicOpts("c.txt", []byte("first"), 0o644, WriteOptions{CreateOnly: true}); err != nil {
		t.Fatal(err)
	}
	err = root.WriteFileAtomicOpts("c.txt", []byte("second"), 0o644, WriteOptions{CreateOnly: true})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	data, readErr := root.ReadFile("c.txt")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "first" {
		t.Fatalf("content = %q, want untouched first", data)
	}
	// A failed install must not leave temp litter behind.
	entries, readErr := root.ReadDir(".")
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cozy-tmp-") {
			t.Fatalf("temp litter %q left behind", entry.Name())
		}
	}
}

func TestWriteFileAtomicOptsCreatesParents(t *testing.T) {
	root, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFileAtomicOpts("a/b/c.txt", []byte("deep"), 0o644, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := root.ReadFile("a/b/c.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep" {
		t.Fatalf("content = %q, want deep", data)
	}
}
