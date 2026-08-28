package gitops

// Transactional CommitOwned regression tests: a second writer must not be able
// to slip a path into the guarded commit, and every failure path must leave
// the repository index exactly as it found it.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The lock serializes competing CommitOwned calls: two concurrent guarded
// commits over disjoint owned sets must each commit exactly their own files,
// even though each opens with a pre-staged file from the other.
func TestCommitOwnedConcurrentCallsCommitOnlyTheirOwnFiles(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	run(t, repo, "add", "a.txt", "b.txt")
	run(t, repo, "commit", "-m", "initial")
	writeFile(t, filepath.Join(repo, "a.txt"), "a2\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b2\n")

	var wg sync.WaitGroup
	results := make([]CommitResult, 2)
	errs := make([]error, 2)
	for i, file := range []string{"a.txt", "b.txt"} {
		wg.Add(1)
		go func(i int, file string) {
			defer wg.Done()
			results[i], errs[i] = CommitOwned(t.Context(), CommitRequest{
				RepoPath: repo,
				Files:    []string{file},
				Message:  "commit " + file,
			})
		}(i, file)
	}
	wg.Wait()

	for i := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent commit %d: %v", i, errs[i])
		}
		if results[i].Status != "ok" {
			t.Fatalf("concurrent commit %d: status=%q reason=%s", i, results[i].Status, results[i].Reason)
		}
	}
	for i, file := range []string{"a.txt", "b.txt"} {
		if len(results[i].StagedFiles) != 1 || results[i].StagedFiles[0] != file {
			t.Fatalf("commit %d staged %v, want exactly [%s]", i, results[i].StagedFiles, file)
		}
	}
	for _, file := range []string{"a.txt", "b.txt"} {
		if out := run(t, repo, "log", "--oneline", "-1", "--", file); strings.TrimSpace(out) == "" {
			t.Fatalf("%s should be reachable from history", file)
		}
	}
	status := run(t, repo, "status", "--short")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("worktree should be clean after both commits, got %q", status)
	}
}

// A writer that does not cooperate with the lock must not be able to change
// what a guarded commit contains. Two outcomes are correct: if the racer's
// foreign `git add` lands before the transaction snapshots the index, the
// guarded commit refuses to run (conflict, HEAD unchanged); if it lands after,
// the commit succeeds and contains exactly the owned file. In neither case
// can the intruder path enter the commit.
func TestCommitOwnedIsolatedFromConcurrentIndexWriter(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	writeFile(t, filepath.Join(repo, "intruder.txt"), "intruder\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "initial")
	writeFile(t, filepath.Join(repo, "a.txt"), "a2\n")
	headBefore := strings.TrimSpace(run(t, repo, "rev-parse", "HEAD"))

	// Race a hostile `git add` against the guarded commit.
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			_, err := runGit(t.Context(), repo, "add", "--", "./intruder.txt")
			if err != nil {
				done <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		done <- nil
	}()
	time.Sleep(20 * time.Millisecond)

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"a.txt"},
		Message:  "guarded",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("racing writer failed: %v", err)
	}

	switch result.Status {
	case "ok":
		commit := run(t, repo, "show", "--name-only", "--format=", "HEAD")
		if strings.TrimSpace(commit) != "a.txt" {
			t.Fatalf("guarded commit must contain exactly a.txt, got %q", commit)
		}
	case "conflict":
		headAfter := strings.TrimSpace(run(t, repo, "rev-parse", "HEAD"))
		if headAfter != headBefore {
			t.Fatal("a rejected guarded commit must not move HEAD")
		}
	default:
		t.Fatalf("status = %q (%s), want ok or conflict", result.Status, result.Reason)
	}
	// Either way, the intruder never entered history.
	if out := run(t, repo, "log", "--all", "--name-only", "--pretty=format:"); strings.Contains(out, "intruder.txt") {
		t.Fatal("intruder.txt must never be committed")
	}
}

// Failure after staging must not leave the victim's index dirtied: the
// conflict check runs against the isolated index, so rejecting a foreign
// pre-staged entry leaves the real index untouched.
func TestCommitOwnedConflictLeavesRealIndexIntact(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	run(t, repo, "add", "a.txt")
	before := run(t, repo, "diff", "--cached", "--name-only")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"b.txt"},
		Message:  "conflict",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", result.Status)
	}

	after := run(t, repo, "diff", "--cached", "--name-only")
	if after != before {
		t.Fatalf("real index changed across a rejected commit: before %q after %q", before, after)
	}
	if _, err := os.Stat(filepath.Join(repo, "a.txt")); err != nil {
		t.Fatalf("worktree file removed: %v", err)
	}
}
