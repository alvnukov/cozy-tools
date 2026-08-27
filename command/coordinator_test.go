package command

import (
	"path/filepath"
	"testing"

	"github.com/alvnukov/cozy-tools/config"
)

func coordinatorTestPolicy(logDir string, cwd string) config.CommandPolicy {
	return config.CommandPolicy{
		AllowedCWDs:           []string{cwd},
		DefaultTimeoutSeconds: 30,
		MaxOutputBytes:        4000,
		MaxLines:              40,
		LogDir:                logDir,
	}
}

// The lifecycle test pins the ownership property the coordinator exists for:
// with fully private histories — the base runner's log directory never sees
// the repo record — run, get, abort and list still agree on one owner.
func TestCoordinatorRoutesRepoCommandLifecycle(t *testing.T) {
	repo := t.TempDir()
	base := NewRunner(coordinatorTestPolicy(filepath.Join(t.TempDir(), "base"), t.TempDir()))
	coordinator := NewCoordinator(base)
	repoRunner := coordinator.ForRepo(repo, coordinatorTestPolicy(filepath.Join(t.TempDir(), "repo"), repo), nil)

	started, err := repoRunner.RunFilteredInRepoWithWait(t.Context(), "sleep 2; printf 'done\n'", repo, "", 30, 1, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "running" {
		t.Fatalf("status = %q, want running", started.Status)
	}
	if resolved := coordinator.Resolve(started.CommandID); resolved != repoRunner {
		t.Fatal("Resolve did not return the owning repo runner while the command is active")
	}

	finished, err := coordinator.Resolve(started.CommandID).WaitForHistory(t.Context(), started.CommandID, Filter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "ok" {
		t.Fatalf("polled status = %q, want ok", finished.Status)
	}

	listed, err := coordinator.Resolve(started.CommandID).ListCommands(ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, entry := range listed.Entries {
		if entry.CommandID != started.CommandID {
			continue
		}
		rows++
		if entry.Status == "running" {
			t.Fatalf("command %s listed as running after reaching its terminal state", started.CommandID)
		}
	}
	if rows != 1 {
		t.Fatalf("command %s listed %d times, want one collapsed terminal row", started.CommandID, rows)
	}
}

func TestCoordinatorAbortReachesRepoOwnedProcess(t *testing.T) {
	repo := t.TempDir()
	base := NewRunner(coordinatorTestPolicy(filepath.Join(t.TempDir(), "base"), t.TempDir()))
	coordinator := NewCoordinator(base)
	repoRunner := coordinator.ForRepo(repo, coordinatorTestPolicy(filepath.Join(t.TempDir(), "repo"), repo), nil)

	started, err := repoRunner.RunFilteredInRepoWithWait(t.Context(), "sleep 30", repo, "", 30, 1, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "running" {
		t.Fatalf("status = %q, want running", started.Status)
	}

	abort, err := coordinator.Resolve(started.CommandID).Abort(started.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if abort.Status != "ok" {
		t.Fatalf("abort status = %q (%s), want ok", abort.Status, abort.Reason)
	}
}

func TestCoordinatorRebuildsRunnerOnPolicyChangeAndKeepsRetiredReachable(t *testing.T) {
	repo := t.TempDir()
	base := NewRunner(coordinatorTestPolicy(filepath.Join(t.TempDir(), "base"), t.TempDir()))
	coordinator := NewCoordinator(base)

	firstPolicy := coordinatorTestPolicy(filepath.Join(t.TempDir(), "one"), repo)
	first := coordinator.ForRepo(repo, firstPolicy, nil)
	if again := coordinator.ForRepo(repo, firstPolicy, nil); again != first {
		t.Fatal("an unchanged effective policy rebuilt the repo runner")
	}

	started, err := first.RunFilteredInRepoWithWait(t.Context(), "sleep 2; printf 'retired\n'", repo, "", 30, 1, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "running" {
		t.Fatalf("status = %q, want running", started.Status)
	}

	second := coordinator.ForRepo(repo, coordinatorTestPolicy(filepath.Join(t.TempDir(), "two"), repo), nil)
	if second == first {
		t.Fatal("a changed effective policy kept the stale repo runner")
	}
	if resolved := coordinator.Resolve(started.CommandID); resolved != first {
		t.Fatal("the retired runner no longer resolves the command it still owns")
	}

	finished, err := coordinator.Resolve(started.CommandID).WaitForHistory(t.Context(), started.CommandID, Filter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "ok" {
		t.Fatalf("polled status = %q, want ok", finished.Status)
	}
}

func TestCoordinatorResolvesUnknownIDToBase(t *testing.T) {
	base := NewRunner(coordinatorTestPolicy(filepath.Join(t.TempDir(), "base"), t.TempDir()))
	coordinator := NewCoordinator(base)
	if resolved := coordinator.Resolve("no-such-command"); resolved != base {
		t.Fatal("an unknown command id must fall back to the base runner")
	}
}
