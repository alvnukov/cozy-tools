package command

import (
	"testing"
	"time"
)

// An unset history directory means volatile history, not a hidden default
// into a host-specific namespace.
func TestNewHistoryEmptyDirIsInMemory(t *testing.T) {
	h, err := NewHistory(HistoryPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Put(Record{CommandID: "mem-1", Status: "ok", Command: "true", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := h.getRecord("mem-1"); !ok {
		t.Fatal("in-memory history must retain the record")
	}
}
