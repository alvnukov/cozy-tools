package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvnukov/cozy-tools/config"
	"github.com/alvnukov/cozy-tools/project"
)

func TestRunnerRejectsCWDOutsidePolicy(t *testing.T) {
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{t.TempDir()}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})
	_, err := runner.Run(t.Context(), "echo ok", "/", 1)
	if err == nil {
		t.Fatal("expected cwd policy error")
	}
}

func TestRunnerExtractsEvidence(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})
	result, err := runner.Run(t.Context(), "printf 'ok\\nerror: bad\\n'", dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.EvidenceLines) == 0 {
		t.Fatal("expected evidence lines")
	}
}

func TestRunnerStoresHistoryAndRefilters(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logDir})
	result, err := runner.RunFiltered(t.Context(), "printf 'info\\nerror: bad\\nwarn\\n'", dir, 1, Filter{Preset: "errors"})
	if err != nil {
		t.Fatal(err)
	}
	if result.CommandID == "" {
		t.Fatal("expected command id")
	}
	if len(result.FilteredLines) != 1 || result.FilteredLines[0] != "error: bad" {
		t.Fatalf("filtered lines = %#v", result.FilteredLines)
	}
	refiltered, err := runner.FilterHistory(result.CommandID, Filter{Include: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if len(refiltered.FilteredLines) != 1 || refiltered.FilteredLines[0] != "warn" {
		t.Fatalf("refiltered lines = %#v", refiltered.FilteredLines)
	}
}

func TestRunnerStoresRepoHistoryUnderProjectLogs(t *testing.T) {
	repoPath := t.TempDir()
	logRoot := t.TempDir()
	policy := config.CommandPolicy{
		AllowedCWDs:           []string{repoPath},
		DefaultTimeoutSeconds: 1,
		MaxOutputBytes:        1000,
		MaxLines:              20,
		LogDir:                logRoot,
	}
	runner := NewRunner(policy)
	result, err := runner.RunFilteredInRepo(t.Context(), "printf 'ok\\nwarn: keep\\n'", repoPath, "", 1, Filter{Include: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	projectName, err := project.Name(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(logRoot, "repos", projectName, "logs", "index.jsonl")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("stat repo log index: %v", err)
	}
	reloaded := NewRunner(policy)
	refiltered, err := reloaded.FilterHistory(result.CommandID, Filter{Include: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(refiltered.FilteredLines) != 1 || refiltered.FilteredLines[0] != "ok" {
		t.Fatalf("refiltered lines = %#v", refiltered.FilteredLines)
	}
}

func TestHistoryCleanupRemovesRecordsBeyondLimits(t *testing.T) {
	repoPath := t.TempDir()
	logRoot := t.TempDir()
	policy := config.CommandPolicy{
		AllowedCWDs:           []string{repoPath},
		DefaultTimeoutSeconds: 1,
		MaxOutputBytes:        1000,
		MaxLines:              20,
		LogDir:                logRoot,
		LogRetentionDays:      0,
		LogMaxRecords:         2,
	}

	runner := NewRunner(policy)
	for i := 0; i < 5; i++ {
		_, err := runner.RunFilteredInRepo(t.Context(), "printf 'record"+string(rune('0'+i))+"\n'", repoPath, "", 1, Filter{})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := runner.CleanupHistory(); err != nil {
		t.Fatal(err)
	}

	projectName, err := project.Name(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(logRoot, "repos", projectName, "logs", "index.jsonl")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 2 {
		t.Fatalf("expected 2 index entries after cleanup, got %d", lines)
	}
}

func TestRunnerRunInRepoRejectsEscapingCWD(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})
	_, err := runner.RunInRepo(t.Context(), "echo ok", dir, "../outside", 1)
	if err == nil {
		t.Fatal("expected repo escape error")
	}
}

func TestRunnerRunInRepoRejectsLeanSourceCommand(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})
	_, err := runner.RunInRepo(t.Context(), "cat MCPAIHelperProject/ActiveTasks.lean", dir, "", 1)
	if err == nil || !strings.Contains(err.Error(), "policy_denied") || strings.Contains(err.Error(), "task-owned") {
		t.Fatalf("error = %v, want local policy denial", err)
	}
}

func TestRunnerRunInRepoAllowsProtectedPathSearchTerm(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})
	result, err := runner.RunInRepo(t.Context(), "printf '%s\\n' MCPAIHelperProject", dir, "", 1)
	if err != nil {
		t.Fatalf("search-term command should not be denied: %v", err)
	}
	joined := strings.Join(result.StdoutTail, "\n")
	if !strings.Contains(joined, "MCPAIHelperProject") {
		t.Fatalf("stdout = %q", joined)
	}
}

func TestRunnerRunInRepoRejectsHelperConfigCommand(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), ".mcp-ai-helper", "config.yaml")
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, ProtectedConfigPath: configPath})
	_, err := runner.RunInRepo(t.Context(), "sed -n '1p' "+configPath, dir, "", 1)
	if err == nil || !strings.Contains(err.Error(), "current helper config") || !strings.Contains(err.Error(), "config action=reload") {
		t.Fatalf("error = %v, want helper config denial with config tool recommendation", err)
	}
}

func TestRunnerRunInRepoAllowsRestrictedSubdir(t *testing.T) {
	dir := t.TempDir()
	safe := filepath.Join(dir, "safe")
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(safe, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{safe}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})
	if _, err := runner.RunInRepo(t.Context(), "pwd", dir, "safe", 1); err != nil {
		t.Fatalf("safe subdir should be allowed: %v", err)
	}
	if _, err := runner.RunInRepo(t.Context(), "pwd", dir, "blocked", 1); err == nil {
		t.Fatal("blocked subdir should be rejected")
	}
}

func TestRunnerTimeoutKillsDescendantProcessGroup(t *testing.T) {
	repoPath := t.TempDir()
	logRoot := t.TempDir()
	runner := NewRunner(config.CommandPolicy{
		AllowedCWDs:           []string{repoPath},
		DefaultTimeoutSeconds: 1,
		MaxOutputBytes:        1000,
		MaxLines:              20,
		LogDir:                logRoot,
	})

	started := time.Now()
	result, err := runner.RunInRepo(
		t.Context(),
		"(trap '' TERM; while :; do sleep 30; done) & wait",
		repoPath,
		"",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("timeout took %s, want at most 4s", elapsed)
	}
	if result.Status != "timeout" || result.ExitCode != 124 {
		t.Fatalf("result = %#v, want timeout exit 124", result)
	}

	persisted, err := runner.FilterHistory(result.CommandID, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "timeout" || persisted.ExitCode != 124 {
		t.Fatalf("persisted result = %#v, want timeout exit 124", persisted)
	}

	listed, err := runner.ListCommands(ListRequest{RepoPath: repoPath, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range listed.Entries {
		if entry.CommandID != result.CommandID {
			continue
		}
		found = true
		if entry.Status != "timeout" || entry.ExitCode != 124 {
			t.Fatalf("listed result = %#v, want timeout exit 124", entry)
		}
		break
	}
	if !found {
		t.Fatalf("timed-out command %s missing from list", result.CommandID)
	}
}

func TestRunnerReturnsRunningAndPersistsResultAfterWaitBudget(t *testing.T) {
	repoPath := t.TempDir()
	logRoot := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{repoPath}, DefaultTimeoutSeconds: 5, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logRoot})

	result, err := runner.RunFilteredInRepoWithWait(t.Context(), "sleep 2; printf 'done\n'", repoPath, "", 5, 1, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" {
		t.Fatalf("status = %q, want running", result.Status)
	}
	if result.CommandID == "" || result.NextCall == nil || result.NextCall.Tool != "command" || result.NextCall.Action != "get" {
		t.Fatalf("missing durable lookup metadata: %#v", result)
	}

	deadline := time.Now().Add(4 * time.Second)
	var completed Result
	for time.Now().Before(deadline) {
		completed, err = runner.FilterHistory(result.CommandID, Filter{Include: "done"})
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status != "running" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if completed.Status != "ok" || completed.ExitCode != 0 {
		t.Fatalf("completed result = %#v, want ok exit 0", completed)
	}
	if len(completed.FilteredLines) != 1 || completed.FilteredLines[0] != "done" {
		t.Fatalf("filtered lines = %#v", completed.FilteredLines)
	}
}

func TestRunnerFiltersFullRetainedOutputBeyondMaxLines(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 4000, MaxLines: 5})
	var cmd strings.Builder
	cmd.WriteString("printf 'target\\n")
	for i := 0; i < 30; i++ {
		cmd.WriteString("noise\\n")
	}
	cmd.WriteString("'")

	result, err := runner.RunFiltered(t.Context(), cmd.String(), dir, 1, Filter{Include: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FilteredLines) != 1 || result.FilteredLines[0] != "target" {
		t.Fatalf("filtered lines = %#v, want target from full retained output", result.FilteredLines)
	}
}

func TestRunnerUpdatesRunningHistoryTailAndFilter(t *testing.T) {
	repoPath := t.TempDir()
	logRoot := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{repoPath}, DefaultTimeoutSeconds: 6, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logRoot})

	result, err := runner.RunFilteredInRepoWithWait(t.Context(), "printf 'ready\\n'; sleep 3; printf 'done\\n'", repoPath, "", 6, 1, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" {
		t.Fatalf("status = %q, want running", result.Status)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		live, err := runner.FilterHistory(result.CommandID, Filter{Include: "ready"})
		if err != nil {
			t.Fatal(err)
		}
		if live.Status == "running" && len(live.FilteredLines) == 1 && live.FilteredLines[0] == "ready" && strings.Join(live.StdoutTail, "\n") == "ready" {
			goto waitForCompletion
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("running command history did not expose current output tail/filter")

waitForCompletion:
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		completed, err := runner.FilterHistory(result.CommandID, Filter{Include: "done"})
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "ok" && len(completed.FilteredLines) == 1 && completed.FilteredLines[0] == "done" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("running command did not complete with final output")
}

func TestApplyFilterPresetProfiles(t *testing.T) {
	tests := []struct {
		name   string
		lines  []string
		filter Filter
		want   []string
	}{
		{
			name:   "errors-only",
			lines:  []string{"info", "panic: boom", "warning: heads up", "summary: 1 failed"},
			filter: Filter{Preset: "errors-only"},
			want:   []string{"panic: boom", "summary: 1 failed"},
		},
		{
			name:   "test-failures",
			lines:  []string{"=== RUN   TestOne", "--- FAIL: TestOne", "panic: boom", "PASS"},
			filter: Filter{Preset: "test-failures"},
			want:   []string{"--- FAIL: TestOne", "panic: boom"},
		},
		{
			name:   "compile-errors",
			lines:  []string{"ok", "main.go:12:3: undefined: missing", "done"},
			filter: Filter{Preset: "compile-errors"},
			want:   []string{"main.go:12:3: undefined: missing"},
		},
		{
			name:   "git-status",
			lines:  []string{"## main...origin/main", " M internal/command/runner.go", "plain text"},
			filter: Filter{Preset: "git-status"},
			want:   []string{"## main...origin/main", " M internal/command/runner.go"},
		},
		{
			name:   "changed-files",
			lines:  []string{" M internal/command/runner.go", "internal/command/runner_test.go", "note"},
			filter: Filter{Preset: "changed-files"},
			want:   []string{" M internal/command/runner.go", "internal/command/runner_test.go"},
		},
		{
			name:   "summary-with-context",
			lines:  []string{"setup", "warning: deprecated", "summary: 1 failed", "done in 0.1s"},
			filter: Filter{Preset: "summary-with-context"},
			want:   []string{"warning: deprecated", "summary: 1 failed", "done in 0.1s"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated, err := applyFilter(tc.lines, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if truncated {
				t.Fatal("did not expect truncation")
			}
			if !sameStrings(got, tc.want) {
				t.Fatalf("filtered lines = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRunnerStoresHistoryAndRefiltersWithPacks(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logDir})
	result, err := runner.RunFiltered(t.Context(), "printf 'ok\nmain.go:12:3: undefined: missing\n M internal/command/runner.go\ninternal/command/runner_test.go\n'", dir, 1, Filter{Preset: "compile-errors"})
	if err != nil {
		t.Fatal(err)
	}
	if !sameStrings(result.FilteredLines, []string{"main.go:12:3: undefined: missing"}) {
		t.Fatalf("filtered lines = %#v", result.FilteredLines)
	}
	refiltered, err := runner.FilterHistory(result.CommandID, Filter{Packs: []string{"git-status", "changed-files"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{" M internal/command/runner.go", "internal/command/runner_test.go"}
	if !sameStrings(refiltered.FilteredLines, want) {
		t.Fatalf("refiltered lines = %#v, want %#v", refiltered.FilteredLines, want)
	}
}

func sameStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestListCommandsReturnsRecentEntries(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logDir})

	for i := 0; i < 3; i++ {
		_, err := runner.Run(t.Context(), "echo ok", dir, 1)
		if err != nil {
			t.Fatal(err)
		}
	}

	result, err := runner.ListCommands(ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(result.Entries))
	}
	if result.Total != 3 {
		t.Fatalf("total = %d, want 3", result.Total)
	}
	if result.Entries[0].Status != "ok" {
		t.Fatalf("entry[0].status = %q, want ok", result.Entries[0].Status)
	}
	if result.Entries[0].CommandID == "" {
		t.Fatal("entry[0].command_id is empty")
	}
}

func TestListCommandsRespectsLimit(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logDir})

	for i := 0; i < 5; i++ {
		_, err := runner.Run(t.Context(), "echo ok", dir, 1)
		if err != nil {
			t.Fatal(err)
		}
	}

	result, err := runner.ListCommands(ListRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 (limit)", len(result.Entries))
	}
	if result.Total != 5 {
		t.Fatalf("total = %d, want 5 (all records)", result.Total)
	}
}

func TestListCommandsFiltersByStatus(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logDir})

	_, err := runner.Run(t.Context(), "echo ok", dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(t.Context(), "false", dir, 1)
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.ListCommands(ListRequest{Status: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 (failed only)", len(result.Entries))
	}
	if result.Entries[0].ExitCode == 0 {
		t.Fatal("expected non-zero exit code for failed entry")
	}
}

func TestListCommandsFiltersByRepoPath(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	logDir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir1, dir2}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logDir})

	_, err := runner.RunFilteredInRepo(t.Context(), "echo ok", dir1, "", 1, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.RunFilteredInRepo(t.Context(), "echo ok", dir2, "", 1, Filter{})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.ListCommands(ListRequest{RepoPath: dir1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 (repo filter)", len(result.Entries))
	}
}

func TestListCommandsInMemoryFallback(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})

	_, err := runner.Run(t.Context(), "echo in-memory-test-marker", dir, 1)
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.ListCommands(ListRequest{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) == 0 {
		t.Fatal("expected at least 1 entry from in-memory history")
	}
	found := false
	for _, e := range result.Entries {
		if strings.Contains(e.Command, "in-memory-test-marker") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected to find our command in the list")
	}
}

func TestListCommandsReturnsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logDir})

	for i := 0; i < 3; i++ {
		_, err := runner.Run(t.Context(), "echo "+string(rune('a'+i)), dir, 1)
		if err != nil {
			t.Fatal(err)
		}
	}

	result, err := runner.ListCommands(ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) < 2 {
		t.Fatal("need at least 2 entries to check ordering")
	}
	if result.Entries[0].CreatedAt.Before(result.Entries[1].CreatedAt) {
		t.Fatal("entries should be newest-first")
	}
}

func TestAbortKillsRunningCommand(t *testing.T) {
	repoPath := t.TempDir()
	logRoot := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{repoPath}, DefaultTimeoutSeconds: 10, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logRoot})

	result, err := runner.RunFilteredInRepoWithWait(t.Context(), "sleep 30", repoPath, "", 10, 1, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" {
		t.Fatalf("status = %q, want running", result.Status)
	}

	abortResult, err := runner.Abort(result.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if abortResult.Status != "ok" {
		t.Fatalf("abort status = %q, want ok", abortResult.Status)
	}

	completed, err := runner.FilterHistory(result.CommandID, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "aborted" || completed.ExitCode != 130 {
		t.Fatalf("completed = %#v, want aborted exit 130 immediately after abort", completed)
	}
}

func TestAbortDoesNotReportRunningHistoryAsCompleted(t *testing.T) {
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{t.TempDir()}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})
	const commandID = "orphaned-running-command"
	if err := runner.history.Put(Record{CommandID: commandID, Status: "running", ExitCode: -1, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	abortResult, err := runner.Abort(commandID)
	if err != nil {
		t.Fatal(err)
	}
	if abortResult.Status != "running" {
		t.Fatalf("abort status = %q, want running while history is running", abortResult.Status)
	}
	current, err := runner.FilterHistory(commandID, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != abortResult.Status {
		t.Fatalf("abort status = %q, get status = %q", abortResult.Status, current.Status)
	}
}

func TestAbortReturnsNotFoundForUnknownID(t *testing.T) {
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{t.TempDir()}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})

	result, err := runner.Abort("nonexistent-id")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "not_found" {
		t.Fatalf("status = %q, want not_found", result.Status)
	}
}

func TestAbortReturnsAlreadyCompletedForFinishedCommand(t *testing.T) {
	dir := t.TempDir()
	logDir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20, LogDir: logDir})

	completed, err := runner.Run(t.Context(), "echo done", dir, 1)
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Abort(completed.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "already_completed" {
		t.Fatalf("status = %q, want already_completed", result.Status)
	}
}

func TestAbortRequiresCommandID(t *testing.T) {
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{t.TempDir()}, DefaultTimeoutSeconds: 1, MaxOutputBytes: 1000, MaxLines: 20})

	_, err := runner.Abort("")
	if err == nil {
		t.Fatal("expected error for empty command_id")
	}
}

func TestRunnerSubstitutesVarsAndKeepsTemplateInRecords(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 5, MaxOutputBytes: 1000, MaxLines: 20})
	ctx := ContextWithExec(t.Context(), Exec{Vars: map[string]string{"BRANCH": "feature/vars-env-stdin"}})
	result, err := runner.Run(ctx, "printf '%s' \"{{BRANCH}}\"", dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(result.StdoutTail, "\n"); joined != "feature/vars-env-stdin" {
		t.Fatalf("stdout = %q, want substituted value", joined)
	}
	if !strings.Contains(result.Command, "{{BRANCH}}") {
		t.Fatalf("records must keep the template command, got %q", result.Command)
	}
}

func TestRunnerUnknownVarFailsClosedBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 5, MaxOutputBytes: 1000, MaxLines: 20})
	ctx := ContextWithExec(t.Context(), Exec{Vars: map[string]string{"KNOWN": "x"}})
	_, err := runner.Run(ctx, "touch "+marker+"; printf '{{UNKNOWN}}'", dir, 5)
	if err == nil || !strings.Contains(err.Error(), "Known variables: KNOWN") {
		t.Fatalf("err = %v, want fail-closed teaching error listing known names", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatal("command must not run when a template reference has no value")
	}
}

func TestRunnerPipesStdinWithSubstitution(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 5, MaxOutputBytes: 1000, MaxLines: 20})
	ctx := ContextWithExec(t.Context(), Exec{Stdin: "line one\nline {{N}}\n", Vars: map[string]string{"N": "two"}})
	result, err := runner.Run(ctx, "cat", dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.StdoutTail, "\n")
	if !strings.Contains(joined, "line one") || !strings.Contains(joined, "line two") {
		t.Fatalf("stdout = %q, want stdin piped with substitution", joined)
	}
}

func TestRunnerInjectsEnvFromContext(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, DefaultTimeoutSeconds: 5, MaxOutputBytes: 1000, MaxLines: 20})
	ctx := ContextWithSecrets(t.Context(), []string{"GREETING=adios"}, nil)
	result, err := runner.Run(ctx, "printf '%s' \"$GREETING\"", dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(result.StdoutTail, "\n"); joined != "adios" {
		t.Fatalf("stdout = %q, want env-injected greeting", joined)
	}
}

func TestMergeEnvLastWriterWins(t *testing.T) {
	merged := mergeEnv([]string{"A=1", "B=2", "A=0"}, []string{"B=3", "C=4"})
	want := []string{"A=0", "B=3", "C=4"}
	if !sameStrings(merged, want) {
		t.Fatalf("merged = %#v, want %#v", merged, want)
	}
}
