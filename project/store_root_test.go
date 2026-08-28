package project

import "testing"

// The root is explicit host state: an empty root is a configuration error,
// not a silent default into a host-specific directory.
func TestNewStoreRequiresRoot(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("empty root must be rejected")
	}
	if _, err := NewStore(t.TempDir()); err != nil {
		t.Fatalf("explicit root rejected: %v", err)
	}
}
