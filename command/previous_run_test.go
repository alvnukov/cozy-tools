package command

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvnukov/cozy-tools/config"
)

func previousRunPolicy(t *testing.T, repoPath string, logRoot string) config.CommandPolicy {
	t.Helper()
	return config.CommandPolicy{
		AllowedCWDs:           []string{repoPath},
		DefaultTimeoutSeconds: 5,
		MaxOutputBytes:        4000,
		MaxLines:              40,
		LogDir:                logRoot,
		LogRetentionDays:      30,
		LogMaxRecords:         100,
	}
}

func TestRunReportsAnIdenticalRunAsARepeat(t *testing.T) {
	repoPath := t.TempDir()
	runner := NewRunner(previousRunPolicy(t, repoPath, t.TempDir()))

	first, err := runner.RunInRepo(t.Context(), "printf 'same\\n'", repoPath, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if first.Previous != nil {
		t.Fatalf("first run reported a previous run: %#v", first.Previous)
	}

	second, err := runner.RunInRepo(t.Context(), "printf 'same\\n'", repoPath, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if second.Previous == nil {
		t.Fatal("second run of an identical command reported no previous run")
	}
	if second.Previous.CommandID != first.CommandID {
		t.Fatalf("previous command_id = %q, want %q", second.Previous.CommandID, first.CommandID)
	}
	if !second.Previous.SameOutput {
		t.Fatal("byte-identical output was not reported as same_output")
	}
	if second.Previous.Status != "ok" || second.Previous.ExitCode != 0 {
		t.Fatalf("previous run summary = %#v", second.Previous)
	}
	if second.Previous.AgeSeconds < 0 {
		t.Fatalf("previous run age = %d", second.Previous.AgeSeconds)
	}
}

func TestRunReportsDifferentOutputFromTheSameCommand(t *testing.T) {
	repoPath := t.TempDir()
	marker := filepath.Join(repoPath, "marker.txt")
	if err := os.WriteFile(marker, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(previousRunPolicy(t, repoPath, t.TempDir()))

	if _, err := runner.RunInRepo(t.Context(), "cat marker.txt", repoPath, "", 5); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := runner.RunInRepo(t.Context(), "cat marker.txt", repoPath, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if second.Previous == nil {
		t.Fatal("re-reading a changed file reported no previous run")
	}
	if second.Previous.SameOutput {
		t.Fatal("output changed between runs but same_output was reported")
	}
}

func TestADifferentCommandIsNotARepeat(t *testing.T) {
	repoPath := t.TempDir()
	runner := NewRunner(previousRunPolicy(t, repoPath, t.TempDir()))

	if _, err := runner.RunInRepo(t.Context(), "printf 'one\\n'", repoPath, "", 5); err != nil {
		t.Fatal(err)
	}
	second, err := runner.RunInRepo(t.Context(), "printf 'two\\n'", repoPath, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if second.Previous != nil {
		t.Fatalf("a different command was reported as a repeat: %#v", second.Previous)
	}
}

// Several helper processes share one log directory, so the run being repeated is
// often not one this process performed.
func TestARepeatIsSeenAcrossHelperProcesses(t *testing.T) {
	repoPath := t.TempDir()
	logRoot := t.TempDir()
	policy := previousRunPolicy(t, repoPath, logRoot)

	first, err := NewRunner(policy).RunInRepo(t.Context(), "printf 'shared\\n'", repoPath, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRunner(policy).RunInRepo(t.Context(), "printf 'shared\\n'", repoPath, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if second.Previous == nil || second.Previous.CommandID != first.CommandID {
		t.Fatalf("previous run across processes = %#v, want command_id %q", second.Previous, first.CommandID)
	}
	if !second.Previous.SameOutput {
		t.Fatal("byte-identical output across processes was not reported as same_output")
	}
}

func TestARunOutsideARepoReportsNoPrevious(t *testing.T) {
	repoPath := t.TempDir()
	runner := NewRunner(previousRunPolicy(t, repoPath, t.TempDir()))

	if _, err := runner.RunFiltered(t.Context(), "printf 'loose\\n'", repoPath, 5, Filter{}); err != nil {
		t.Fatal(err)
	}
	second, err := runner.RunFiltered(t.Context(), "printf 'loose\\n'", repoPath, 5, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Previous != nil {
		t.Fatalf("a run without repo_path reported a previous run: %#v", second.Previous)
	}
}

func TestARunOlderThanTheWindowIsNotARepeat(t *testing.T) {
	repoPath := t.TempDir()
	runner := NewRunner(previousRunPolicy(t, repoPath, t.TempDir()))

	first, err := runner.RunInRepo(t.Context(), "printf 'aged\\n'", repoPath, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().UTC().Add(previousRunWindow + time.Minute)
	if aged := runner.previousRun(repoPath, first.Command, "", first.OutputHash, later); aged != nil {
		t.Fatalf("a run outside the window was reported as a repeat: %#v", aged)
	}
	soon := time.Now().UTC().Add(time.Minute)
	if fresh := runner.previousRun(repoPath, first.Command, "", first.OutputHash, soon); fresh == nil {
		t.Fatal("a run inside the window was not reported as a repeat")
	}
}
