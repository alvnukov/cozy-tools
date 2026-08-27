package command

import (
	"os"
	"testing"
	"time"
)

const terminalTestRepo = "/tmp/example-repo"

func terminalTestHistory(t *testing.T, dir string, maxRecords int) *History {
	t.Helper()
	history, err := NewHistory(HistoryPolicy{Dir: dir, RetentionDays: 30, MaxRecords: maxRecords})
	if err != nil {
		t.Fatalf("new history: %v", err)
	}
	return history
}

func runningRecord(commandID string, started time.Time) Record {
	return Record{
		CommandID: commandID,
		Status:    "running",
		RepoPath:  terminalTestRepo,
		Command:   "go test ./...",
		CWD:       terminalTestRepo,
		ExitCode:  -1,
		StartedAt: started,
	}
}

func finishedRecord(commandID string, started time.Time) Record {
	return Record{
		CommandID:   commandID,
		Status:      "ok",
		RepoPath:    terminalTestRepo,
		Command:     "go test ./...",
		CWD:         terminalTestRepo,
		ExitCode:    0,
		DurationMS:  409939,
		Stdout:      []string{"ok  	example/pkg	1.0s"},
		Combined:    []string{"ok  	example/pkg	1.0s"},
		OutputHash:  "abc",
		StartedAt:   started,
		CompletedAt: started.Add(410 * time.Second),
	}
}

// TestGetRecordPrefersTheTerminalRecordAnotherHelperWrote is the bug this file
// exists for. Several helper processes share one log directory, and the one
// holding a running snapshot in memory is not necessarily the one that finishes
// the command. Before the fix the cached snapshot was authoritative, so a
// command whose output was already on disk kept reporting itself as running for
// as long as that process lived, and its result could not be reached at all.
func TestGetRecordPrefersTheTerminalRecordAnotherHelperWrote(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().UTC().Add(-410 * time.Second)
	const commandID = "aaaaaaaaaaaaaaaa"

	first := terminalTestHistory(t, dir, 100)
	if err := first.Put(runningRecord(commandID, started)); err != nil {
		t.Fatalf("put running: %v", err)
	}

	// A second helper over the same directory finishes the command.
	second := terminalTestHistory(t, dir, 100)
	if err := second.Put(finishedRecord(commandID, started)); err != nil {
		t.Fatalf("put finished: %v", err)
	}

	result, err := first.Filter(commandID, Filter{})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if result.Status != "ok" || result.ExitCode != 0 {
		t.Fatalf("status=%q exit=%d, want the terminal record the other helper wrote", result.Status, result.ExitCode)
	}
	if result.DurationMS != 409939 {
		t.Errorf("duration=%d, want the recorded duration rather than time since start", result.DurationMS)
	}
	if len(result.StdoutTail) == 0 {
		t.Error("the terminal record's output should be reachable")
	}
}

// TestGetRecordKeepsTheLiveSnapshotWhileTheCommandRuns is the other side of the
// same fix: consulting the durable index must not cost a running command the
// output it has streamed so far, which is only in memory until it exits.
func TestGetRecordKeepsTheLiveSnapshotWhileTheCommandRuns(t *testing.T) {
	dir := t.TempDir()
	const commandID = "bbbbbbbbbbbbbbbb"

	history := terminalTestHistory(t, dir, 100)
	if err := history.Put(runningRecord(commandID, time.Now().UTC())); err != nil {
		t.Fatalf("put running: %v", err)
	}
	live := []string{"compiling", "still going"}
	if err := history.UpdateRunningOutput(commandID, live, nil, live, false, "live"); err != nil {
		t.Fatalf("update running output: %v", err)
	}

	result, err := history.Filter(commandID, Filter{})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if result.Status != "running" {
		t.Fatalf("status=%q, want running", result.Status)
	}
	if len(result.StdoutTail) != len(live) {
		t.Errorf("stdout tail = %v, want the %d lines streamed so far", result.StdoutTail, len(live))
	}
}

// TestListReportsAFinishedCommandOnceAndNotAsRunning covers what made a healthy
// helper look broken: every command writes a running row and then a terminal
// one, and a reader that treats rows as commands shows each finished command a
// second time as a run that never came back.
func TestListReportsAFinishedCommandOnceAndNotAsRunning(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().UTC()
	const commandID = "cccccccccccccccc"

	history := terminalTestHistory(t, dir, 100)
	if err := history.Put(runningRecord(commandID, started)); err != nil {
		t.Fatalf("put running: %v", err)
	}
	if err := history.Put(finishedRecord(commandID, started)); err != nil {
		t.Fatalf("put finished: %v", err)
	}

	all, err := history.List(ListRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if all.Total != 1 || len(all.Entries) != 1 {
		t.Fatalf("total=%d entries=%d, want one row for one command", all.Total, len(all.Entries))
	}
	if all.Entries[0].Status != "ok" {
		t.Errorf("status=%q, want the command listed by how it finished", all.Entries[0].Status)
	}

	running, err := history.List(ListRequest{Status: "running"})
	if err != nil {
		t.Fatalf("list running: %v", err)
	}
	if running.Total != 0 {
		t.Errorf("a finished command must not be listed as running, got %d", running.Total)
	}
}

// TestCleanupKeepsTheRecordFileOfACommandItRetains covers the sharpest edge of
// counting rows instead of commands: both rows of one command name the same
// record file, so evicting the superseded running row deleted the file the
// surviving terminal row still pointed at, and the result became unreadable.
func TestCleanupKeepsTheRecordFileOfACommandItRetains(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().UTC()
	const commandID = "dddddddddddddddd"

	history := terminalTestHistory(t, dir, 1)
	if err := history.Put(runningRecord(commandID, started)); err != nil {
		t.Fatalf("put running: %v", err)
	}
	if err := history.Put(finishedRecord(commandID, started)); err != nil {
		t.Fatalf("put finished: %v", err)
	}

	entries, err := history.readEntries()
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("index holds %d rows, expected the running row and the terminal one", len(entries))
	}
	recordFile := entries[0].File

	if err := history.Cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(recordFile); err != nil {
		t.Fatalf("cleanup deleted the record of a command it kept: %v", err)
	}

	collapsed, err := history.readEntries()
	if err != nil {
		t.Fatalf("read entries after cleanup: %v", err)
	}
	if len(collapsed) != 1 {
		t.Errorf("index holds %d rows after cleanup, want one per command", len(collapsed))
	}

	result, err := history.Filter(commandID, Filter{})
	if err != nil {
		t.Fatalf("filter after cleanup: %v", err)
	}
	if result.Status != "ok" {
		t.Errorf("status=%q after cleanup, want the retained terminal record", result.Status)
	}
}

// TestATerminalRowWinsOverALaterRunningRow pins the ordering rule directly: the
// running row records an intention and the terminal row records a result, so a
// clock that disagrees must not be able to resurrect a finished command.
func TestATerminalRowWinsOverALaterRunningRow(t *testing.T) {
	now := time.Now().UTC()
	finished := indexEntry{CommandID: "e", Status: "ok", CreatedAt: now}
	stillRunning := indexEntry{CommandID: "e", Status: "running", CreatedAt: now.Add(time.Minute)}

	collapsed := latestEntries([]indexEntry{finished, stillRunning})
	if len(collapsed) != 1 {
		t.Fatalf("collapsed to %d rows, want one", len(collapsed))
	}
	if collapsed[0].Status != "ok" {
		t.Errorf("status=%q, want the terminal row to win regardless of timestamps", collapsed[0].Status)
	}
}
