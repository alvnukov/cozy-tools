package command

import (
	"sync"
	"testing"
	"time"
)

// TestPutWaitsForTheInterProcessIndexLock pins the exclusion the fix is made
// of: while another holder — here the test, standing in for a second helper —
// is inside its append-or-rewrite section, Put must not touch the index.
// Before the lock existed, an append that started between a cleanup's
// readEntries and its rename wrote into the inode the rename threw away.
func TestPutWaitsForTheInterProcessIndexLock(t *testing.T) {
	dir := t.TempDir()
	const commandID = "lock-put-waiter"
	history := terminalTestHistory(t, dir, 100)

	hold, err := acquireIndexLock(history.root)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	putDone := make(chan error, 1)
	go func() {
		putDone <- history.Put(finishedRecord(commandID, time.Now().UTC()))
	}()
	select {
	case err := <-putDone:
		hold.release()
		t.Fatalf("put finished while another holder held the index lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	hold.release()

	select {
	case err := <-putDone:
		if err != nil {
			t.Fatalf("put after lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("put did not finish after the lock was released")
	}

	entries, err := history.readEntries()
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	terminal := false
	for _, entry := range entries {
		if entry.CommandID == commandID && entry.Status == "ok" {
			terminal = true
		}
	}
	if !terminal {
		t.Fatalf("index rows = %+v, want the terminal row for %q", entries, commandID)
	}
}

// TestCleanupWaitsForTheInterProcessIndexLock is the mirror image: cleanup
// holds off while another holder is mid-append, so the rewrite never races a
// concurrent write.
func TestCleanupWaitsForTheInterProcessIndexLock(t *testing.T) {
	dir := t.TempDir()
	const commandID = "lock-cleanup-waiter"
	history := terminalTestHistory(t, dir, 100)
	if err := history.Put(runningRecord(commandID, time.Now().UTC())); err != nil {
		t.Fatalf("put running: %v", err)
	}

	hold, err := acquireIndexLock(history.root)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	cleanupDone := make(chan error, 1)
	go func() {
		cleanupDone <- history.Cleanup()
	}()
	select {
	case err := <-cleanupDone:
		hold.release()
		t.Fatalf("cleanup finished while another holder held the index lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	hold.release()

	select {
	case err := <-cleanupDone:
		if err != nil {
			t.Fatalf("cleanup after lock release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not finish after the lock was released")
	}

	entries, err := history.readEntries()
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	kept := false
	for _, entry := range entries {
		if entry.CommandID == commandID {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("index rows = %+v, want the fresh row retained, not cleaned", entries)
	}
}

// TestCleanupKeepsATerminalRowAnotherHelperAppendsMidFlight races the two
// operations that used to lose records, from two History instances over one
// root the way two helper processes share it. Whichever order the scheduler
// picks, the terminal row must survive every rewrite and the command must not
// be listed as running afterwards.
func TestCleanupKeepsATerminalRowAnotherHelperAppendsMidFlight(t *testing.T) {
	dir := t.TempDir()
	const commandID = "lock-race-terminal"
	started := time.Now().UTC()

	first := terminalTestHistory(t, dir, 100)
	second := terminalTestHistory(t, dir, 100)
	if err := first.Put(runningRecord(commandID, started)); err != nil {
		t.Fatalf("put running: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := first.Cleanup(); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := second.Put(finishedRecord(commandID, started)); err != nil {
				t.Errorf("put finished: %v", err)
			}
		}()
	}
	wg.Wait()

	entries, err := first.readEntries()
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	status := ""
	for _, entry := range latestEntries(entries) {
		if entry.CommandID == commandID {
			status = entry.Status
		}
	}
	if status != "ok" {
		t.Fatalf("collapsed status=%q, want the terminal row to survive every rewrite", status)
	}

	result, err := first.Filter(commandID, Filter{})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if result.Status != "ok" {
		t.Errorf("filter status=%q, want the finished record reachable", result.Status)
	}
	running, err := first.List(ListRequest{Status: "running"})
	if err != nil {
		t.Fatalf("list running: %v", err)
	}
	if running.Total != 0 {
		t.Errorf("running total=%d, want the finished command never listed as running", running.Total)
	}
}
