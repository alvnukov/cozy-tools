package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Index parsing must enforce a per-entry bound: a corrupt or hostile index
// row cannot make the reader allocate an arbitrarily long line.
func TestReadEntriesFileRejectsOversizedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.jsonl")
	huge := strings.Repeat("x", maxIndexLineBytes+1)
	line := `{"command_id":"c1","status":"ok","file":"records/c1.json","command":"` + huge + `"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readEntriesFile(path)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want oversized-line error", err)
	}
}

// The whole index read is bounded too: an index larger than the documented
// budget fails instead of streaming into memory line by line.
func TestReadEntriesFileRejectsOversizedTotalRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.jsonl")
	huge := strings.Repeat("x", 4096)
	line := `{"command_id":"c1","status":"ok","file":"records/c1.json","command":"` + huge + `"}` + "\n"
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString(line)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readEntriesFileBounded(path, maxIndexLineBytes, 64*1024)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want total-read-limit error", err)
	}
}

// A record that cannot be read back under the record hard cap must never be
// written: Put rejects it with a stable error instead of creating a ghost
// command whose record reads as broken forever after.
func TestPutRejectsRecordExceedingReadCap(t *testing.T) {
	history, err := NewHistory(HistoryPolicy{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", maxHistoryRecordBytes)
	err = history.Put(Record{
		CommandID: "c1",
		Status:    "ok",
		Command:   "true",
		Combined:  []string{big},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want record-cap error", err)
	}
	if _, ok := history.records["c1"]; ok {
		t.Fatal("oversized record must not be cached in memory")
	}
}

// The command string is embedded in every index row, so a command whose row
// cannot fit the index line budget is rejected before anything is written.
func TestPutRejectsCommandExceedingIndexLineCap(t *testing.T) {
	history, err := NewHistory(HistoryPolicy{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", maxIndexLineBytes)
	err = history.Put(Record{
		CommandID: "c2",
		Status:    "ok",
		Command:   long,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want index-line-cap error", err)
	}
	if _, ok := history.records["c2"]; ok {
		t.Fatal("oversized entry must not be cached in memory")
	}
}
