package command

import (
	"context"
	"testing"
	"time"

	"github.com/alvnukov/cozy-tools/config"
)

func waitTestRunner(t *testing.T, repoPath string) *Runner {
	t.Helper()
	return NewRunner(config.CommandPolicy{
		AllowedCWDs:           []string{repoPath},
		DefaultTimeoutSeconds: 30,
		MaxOutputBytes:        4000,
		MaxLines:              40,
		LogDir:                t.TempDir(),
		LogRetentionDays:      30,
		LogMaxRecords:         100,
	})
}

func TestWaitForHistoryReturnsWhenTheCommandFinishes(t *testing.T) {
	repoPath := t.TempDir()
	runner := waitTestRunner(t, repoPath)

	started, err := runner.RunInRepoWithWait(t.Context(), "sleep 1; printf 'late\\n'", repoPath, "", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "running" {
		t.Fatalf("handoff status = %q, want running", started.Status)
	}

	waited, err := runner.WaitForHistory(t.Context(), started.CommandID, Filter{}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if waited.Status != "ok" || waited.ExitCode != 0 {
		t.Fatalf("waited result = status %q exit %d", waited.Status, waited.ExitCode)
	}
	if len(waited.StdoutTail) == 0 || waited.StdoutTail[0] != "late" {
		t.Fatalf("stdout tail = %#v", waited.StdoutTail)
	}
}

// A wait that runs out hands back the running record rather than failing, so the
// caller can decide whether the command is worth more time.
func TestWaitForHistoryReturnsTheRunningRecordWhenTheBudgetRunsOut(t *testing.T) {
	repoPath := t.TempDir()
	runner := waitTestRunner(t, repoPath)

	started, err := runner.RunInRepoWithWait(t.Context(), "sleep 20", repoPath, "", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = runner.Abort(started.CommandID)
	}()

	begin := time.Now()
	waited, err := runner.WaitForHistory(t.Context(), started.CommandID, Filter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if waited.Status != "running" {
		t.Fatalf("status after an exhausted wait = %q, want running", waited.Status)
	}
	if elapsed := time.Since(begin); elapsed > 10*time.Second {
		t.Fatalf("wait of 1s took %s", elapsed)
	}
	if waited.NextCall == nil || waited.NextCall.CommandID != started.CommandID {
		t.Fatalf("next_call = %#v", waited.NextCall)
	}
}

func TestWaitForHistoryReturnsAFinishedCommandImmediately(t *testing.T) {
	repoPath := t.TempDir()
	runner := waitTestRunner(t, repoPath)

	done, err := runner.RunInRepo(t.Context(), "printf 'now\\n'", repoPath, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	waited, err := runner.WaitForHistory(t.Context(), done.CommandID, Filter{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if waited.Status != "ok" {
		t.Fatalf("status = %q", waited.Status)
	}
	if elapsed := time.Since(begin); elapsed > 2*time.Second {
		t.Fatalf("waiting on a finished command took %s", elapsed)
	}
}

// Several helper processes share one log directory, so the process asked to wait
// is often not the one running the command and has no channel to wait on.
func TestWaitForHistoryPollsACommandAnotherProcessIsRunning(t *testing.T) {
	repoPath := t.TempDir()
	logRoot := t.TempDir()
	policy := config.CommandPolicy{
		AllowedCWDs:           []string{repoPath},
		DefaultTimeoutSeconds: 30,
		MaxOutputBytes:        4000,
		MaxLines:              40,
		LogDir:                logRoot,
		LogRetentionDays:      30,
		LogMaxRecords:         100,
	}
	owner := NewRunner(policy)
	observer := NewRunner(policy)

	started, err := owner.RunInRepoWithWait(t.Context(), "sleep 1; printf 'elsewhere\\n'", repoPath, "", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "running" {
		t.Fatalf("handoff status = %q, want running", started.Status)
	}

	waited, err := observer.WaitForHistory(t.Context(), started.CommandID, Filter{}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if waited.Status != "ok" {
		t.Fatalf("observer saw status %q, want ok", waited.Status)
	}
}

func TestWaitForHistoryStopsWhenTheCallerGivesUp(t *testing.T) {
	repoPath := t.TempDir()
	runner := waitTestRunner(t, repoPath)

	started, err := runner.RunInRepoWithWait(t.Context(), "sleep 20", repoPath, "", 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = runner.Abort(started.CommandID)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if _, err := runner.WaitForHistory(ctx, started.CommandID, Filter{}, 60); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(begin); elapsed > 10*time.Second {
		t.Fatalf("cancelled wait took %s", elapsed)
	}
}
