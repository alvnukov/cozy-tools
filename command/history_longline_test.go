package command

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The index reader used bufio.Scanner with its default 64KB token limit,
// but Put embeds the full command string in each index line. One command
// past the limit poisoned every later read: reopening history failed, the
// runner silently lost persistence, and even cleanup could not read the
// file it was supposed to repair.
func TestHistoryIndexSurvivesLinesBeyondScannerLimit(t *testing.T) {
	dir := t.TempDir()
	h, err := NewHistory(HistoryPolicy{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("a", 70_000)
	if err := h.Put(Record{CommandID: "long-line", Status: "ok", RepoPath: "/tmp/review", Command: "echo " + long, CWD: ".", ExitCode: 0}); err != nil {
		t.Fatal(err)
	}

	h2, err := NewHistory(HistoryPolicy{Dir: dir})
	if err != nil {
		t.Fatalf("reopen history over a >64KB index line: %v", err)
	}
	list, err := h2.List(ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range list.Entries {
		if e.CommandID == "long-line" {
			found = true
		}
	}
	if !found {
		t.Fatalf("long-command entry not listed: %#v", list)
	}
}

// The in-memory fallback computed Total after truncating to the limit, so
// pagination in degraded mode reported wrong page counts.
func TestInMemoryHistoryListTotalIgnoresLimit(t *testing.T) {
	h := NewInMemoryHistory()
	for i := range 3 {
		if err := h.Put(Record{CommandID: fmt.Sprintf("c%d", i), Status: "ok", RepoPath: "/x", Command: "true", CWD: ".", CreatedAt: time.Now().Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := h.List(ListRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 {
		t.Fatalf("entries = %d, want the limit", len(list.Entries))
	}
	if list.Total != 3 {
		t.Fatalf("total = %d, want the pre-limit count", list.Total)
	}
}
