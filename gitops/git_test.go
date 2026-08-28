package gitops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitOwnedRejectsPreStagedOutsideOwnedSet(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "owned.txt"), "owned\n")
	writeFile(t, filepath.Join(repo, "external.txt"), "external\n")
	run(t, repo, "add", "external.txt")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"owned.txt"},
		Message:  "owned change",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
	staged := run(t, repo, "diff", "--cached", "--name-only")
	if staged != "external.txt\n" {
		t.Fatalf("unexpected staged files after conflict: %q", staged)
	}
}

func TestCommitOwnedCommitsOnlyOwnedFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "owned.txt"), "owned\n")
	writeFile(t, filepath.Join(repo, "external.txt"), "external\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"owned.txt"},
		Message:  "owned change",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if got := run(t, repo, "status", "--short"); got != "?? external.txt\n" {
		t.Fatalf("unexpected status: %q", got)
	}
}

func TestCommitOwnedCommitsIgnoredOwnedFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), "*.log\n")
	run(t, repo, "add", ".gitignore")
	run(t, repo, "commit", "-m", "ignore logs")
	writeFile(t, filepath.Join(repo, "owned.log"), "owned\n")
	writeFile(t, filepath.Join(repo, "external.log"), "external\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"owned.log"},
		Message:  "owned ignored change",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if got := run(t, repo, "show", "--name-status", "--format=", "HEAD"); got != "A\towned.log\n" {
		t.Fatalf("unexpected commit diff: %q", got)
	}
	if got := run(t, repo, "status", "--short", "--ignored"); got != "!! external.log\n" {
		t.Fatalf("unexpected status: %q", got)
	}
}

func TestCommitOwnedCommitsDeletedOwnedFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "tracked\n")
	run(t, repo, "add", "tracked.txt")
	run(t, repo, "commit", "-m", "initial")
	if err := os.Remove(filepath.Join(repo, "tracked.txt")); err != nil {
		t.Fatal(err)
	}

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"tracked.txt"},
		Message:  "delete tracked",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if got := run(t, repo, "show", "--name-status", "--format=", "HEAD"); got != "D\ttracked.txt\n" {
		t.Fatalf("unexpected commit diff: %q", got)
	}
	if got := run(t, repo, "status", "--short"); got != "" {
		t.Fatalf("unexpected status: %q", got)
	}
}

func TestPrepareTaskWorktreeCreatesTypedBranchInTaskPath(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "initial\n")
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "initial")

	result, err := PrepareTaskWorktree(t.Context(), PrepareTaskWorktreeRequest{RepoPath: repo, TaskID: "task-123", TaskType: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Created {
		t.Fatalf("result = %#v, want created ok", result)
	}
	if result.Branch != "feature/task-123" {
		t.Fatalf("branch = %q", result.Branch)
	}
	if result.WorktreePath != ".worktrees/task-123" {
		t.Fatalf("worktree_path = %q", result.WorktreePath)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if result.CodePath != filepath.Join(canonicalRepo, ".worktrees", "task-123") {
		t.Fatalf("code_path = %q", result.CodePath)
	}
	if got := run(t, result.CodePath, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature/task-123\n" {
		t.Fatalf("worktree branch = %q", got)
	}
}

func TestPrepareTaskWorktreeRequiresTaskType(t *testing.T) {
	repo := initRepo(t)
	_, err := PrepareTaskWorktree(t.Context(), PrepareTaskWorktreeRequest{RepoPath: repo, TaskID: "task-123"})
	if err == nil {
		t.Fatal("expected task_type error")
	}
}

func TestPrepareTaskWorktreeIsIdempotentForSameBranch(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "initial\n")
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "initial")

	first, err := PrepareTaskWorktree(t.Context(), PrepareTaskWorktreeRequest{RepoPath: repo, TaskID: "task-123", TaskType: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareTaskWorktree(t.Context(), PrepareTaskWorktreeRequest{RepoPath: repo, TaskID: "task-123", TaskType: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || second.Status != "ok" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run(t, repo, "init")
	run(t, repo, "config", "user.email", "test@example.invalid")
	run(t, repo, "config", "user.name", "Test User")
	return repo
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommitOwnedSkipsUnownedModifiedFiles(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "owned.txt"), "owned\n")
	writeFile(t, filepath.Join(repo, "external.txt"), "external\n")
	run(t, repo, "add", "owned.txt", "external.txt")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "owned.txt"), "owned changed\n")
	writeFile(t, filepath.Join(repo, "external.txt"), "external changed\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"owned.txt"},
		Message:  "only owned",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	diff := run(t, repo, "show", "--format=", "--name-only", "HEAD")
	if diff != "owned.txt\n" {
		t.Fatalf("commit should contain only owned.txt, got: %q", diff)
	}
	status := run(t, repo, "status", "--short")
	if !strings.Contains(status, " M external.txt") {
		t.Fatalf("external.txt should remain modified in worktree: %q", status)
	}
}

func TestCommitOwnedRejectsAbsolutePaths(t *testing.T) {
	repo := initRepo(t)
	_, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"/etc/passwd"},
		Message:  "bad",
	})
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestCommitOwnedRejectsParentRelativePaths(t *testing.T) {
	repo := initRepo(t)
	_, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"../outside.txt"},
		Message:  "bad",
	})
	if err == nil {
		t.Fatal("expected error for parent-relative path")
	}
}

func TestCommitOwnedSkipsEmptyAndDuplicateFiles(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "f.txt"), "content\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"f.txt", "", "  ", "f.txt"},
		Message:  "dedup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
}

func TestCommitOwnedReturnsSkippedWhenNoDiff(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "f.txt"), "content\n")
	run(t, repo, "add", "f.txt")
	run(t, repo, "commit", "-m", "initial")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"f.txt"},
		Message:  "no change",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "skipped" {
		t.Fatalf("status = %q, want skipped", result.Status)
	}
}

func TestCommitOwnedRequiresMessage(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "f.txt"), "v\n")
	_, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"f.txt"},
	})
	if err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestCommitOwnedRequiresRepoPath(t *testing.T) {
	_, err := CommitOwned(t.Context(), CommitRequest{
		Files:   []string{"f.txt"},
		Message: "m",
	})
	if err == nil {
		t.Fatal("expected error for empty repo_path")
	}
}

func TestCommitOwnedRejectsStagedContentAfterAdd(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	run(t, repo, "add", "a.txt")

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
}

func TestCommitOwnedMultipleFilesMixedState(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	run(t, repo, "add", "a.txt", "b.txt")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "a.txt"), "a2\n")
	if err := os.Remove(filepath.Join(repo, "b.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "c.txt"), "c\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"a.txt", "b.txt", "c.txt"},
		Message:  "mixed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	diff := run(t, repo, "show", "--name-status", "--format=", "HEAD")
	if !strings.Contains(diff, "M\ta.txt") {
		t.Fatalf("commit should modify a.txt: %q", diff)
	}
	if !strings.Contains(diff, "D\tb.txt") {
		t.Fatalf("commit should delete b.txt: %q", diff)
	}
	if !strings.Contains(diff, "A\tc.txt") {
		t.Fatalf("commit should add c.txt: %q", diff)
	}
}

func TestCommitOwnedWorktreeWithGitignore(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), "*.log\n.env\n")
	run(t, repo, "add", ".gitignore")
	run(t, repo, "commit", "-m", "ignore")

	writeFile(t, filepath.Join(repo, "debug.log"), "log data\n")
	writeFile(t, filepath.Join(repo, ".env"), "SECRET=1\n")
	writeFile(t, filepath.Join(repo, "code.go"), "package main\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"debug.log", ".env", "code.go"},
		Message:  "force-add ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	diff := run(t, repo, "show", "--name-status", "--format=", "HEAD")
	for _, want := range []string{"A\tdebug.log", "A\t.env", "A\tcode.go"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("commit should contain %s: %q", want, diff)
		}
	}
}

func TestCommitOwnedStagedFilesArePreserved(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	run(t, repo, "add", "a.txt", "b.txt")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "a.txt"), "a2\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b2\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"a.txt"},
		Message:  "partial",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	diff := run(t, repo, "show", "--name-only", "--format=", "HEAD")
	if diff != "a.txt\n" {
		t.Fatalf("commit should contain only a.txt, got: %q", diff)
	}
	status := run(t, repo, "status", "--short")
	if !strings.Contains(status, " M b.txt") {
		t.Fatalf("b.txt should remain modified: %q", status)
	}
}

func TestPrepareTaskWorktreeSanitizesDangerousTaskID(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "init\n")
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "init")

	result, err := PrepareTaskWorktree(t.Context(), PrepareTaskWorktreeRequest{
		RepoPath: repo,
		TaskID:   "../../escape",
		TaskType: "feature",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok (sanitized id)", result.Status)
	}
	if !strings.HasPrefix(result.WorktreePath, ".worktrees/") {
		t.Fatalf("worktree_path = %q, must stay under .worktrees/", result.WorktreePath)
	}
}

func TestPrepareTaskWorktreeSanitizesUppercaseTaskType(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "init\n")
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "init")

	result, err := PrepareTaskWorktree(t.Context(), PrepareTaskWorktreeRequest{
		RepoPath: repo,
		TaskID:   "task-1",
		TaskType: "FEATURE",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch != "feature/task-1" {
		t.Fatalf("branch = %q, want feature/task-1 (lowercased)", result.Branch)
	}
}

func TestNormalizeOwnedFilesRejectsDuplicates(t *testing.T) {
	in := []string{"a.txt", "b.txt", "a.txt", "  b.txt  "}
	out, err := normalizeOwnedFiles(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 unique files, got %d: %v", len(out), out)
	}
}

func TestLogReturnsCommits(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "first commit")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	run(t, repo, "add", "b.txt")
	run(t, repo, "commit", "-m", "second commit")

	result, err := Log(t.Context(), LogRequest{RepoPath: repo, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Fatalf("expected 2 commits, got %d", result.Total)
	}
	if result.Commits[0].Message != "second commit" {
		t.Fatalf("first commit message = %q, want 'second commit'", result.Commits[0].Message)
	}
	if result.Commits[1].Message != "first commit" {
		t.Fatalf("second commit message = %q, want 'first commit'", result.Commits[1].Message)
	}
}

func TestLogRespectsLimit(t *testing.T) {
	repo := initRepo(t)
	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(repo, "a.txt"), fmt.Sprintf("v%d\n", i))
		run(t, repo, "add", "a.txt")
		run(t, repo, "commit", "-m", fmt.Sprintf("commit %d", i))
	}

	result, err := Log(t.Context(), LogRequest{RepoPath: repo, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 3 {
		t.Fatalf("expected 3 commits, got %d", result.Total)
	}
}

func TestLogFiltersByPath(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "add a")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	run(t, repo, "add", "b.txt")
	run(t, repo, "commit", "-m", "add b")

	result, err := Log(t.Context(), LogRequest{RepoPath: repo, Path: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 commit for a.txt, got %d", result.Total)
	}
	if result.Commits[0].Message != "add a" {
		t.Fatalf("commit message = %q, want 'add a'", result.Commits[0].Message)
	}
}

func TestLogDiffReturnsCommitDetails(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "hello\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "add a")

	hash := strings.TrimSpace(run(t, repo, "rev-parse", "HEAD"))

	result, err := LogDiff(t.Context(), LogDiffRequest{RepoPath: repo, Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "add a" {
		t.Fatalf("message = %q, want 'add a'", result.Message)
	}
	if len(result.Files) == 0 {
		t.Fatal("expected files in diff")
	}
}

func TestStashListEmpty(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")

	result, err := StashList(t.Context(), StashRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 {
		t.Fatalf("expected 0 stashes, got %d", result.Total)
	}
}

func TestStashListReturnsEntries(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")
	writeFile(t, filepath.Join(repo, "a.txt"), "modified\n")
	run(t, repo, "stash", "push", "-m", "my stash")

	result, err := StashList(t.Context(), StashRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 stash, got %d", result.Total)
	}
	if !strings.Contains(result.Entries[0].Message, "my stash") {
		t.Fatalf("stash message = %q, want to contain 'my stash'", result.Entries[0].Message)
	}
}

func TestBranchListReturnsCurrent(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")

	result, err := BranchList(t.Context(), BranchRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if result.Current == "" {
		t.Fatal("expected current branch to be set")
	}
	found := false
	for _, b := range result.Branches {
		if b.IsCurrent {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected to find current branch in list")
	}
}

func TestRemoteListEmpty(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")

	result, err := RemoteList(t.Context(), RemoteRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Remotes) != 0 {
		t.Fatalf("expected 0 remotes, got %d", len(result.Remotes))
	}
}

func TestTagListEmpty(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")

	result, err := TagList(t.Context(), TagRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 {
		t.Fatalf("expected 0 tags, got %d", result.Total)
	}
}

func TestTagListReturnsTags(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")
	run(t, repo, "tag", "-a", "v1.0", "-m", "release 1.0")

	result, err := TagList(t.Context(), TagRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 tag, got %d", result.Total)
	}
	if result.Tags[0].Name != "v1.0" {
		t.Fatalf("tag name = %q, want 'v1.0'", result.Tags[0].Name)
	}
	if !result.Tags[0].IsAnnotated {
		t.Fatal("expected annotated tag")
	}
}

func TestBlameReturnsLines(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "line1\nline2\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")

	result, err := Blame(t.Context(), BlameRequest{RepoPath: repo, File: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Fatalf("expected 2 lines, got %d", result.Total)
	}
	if result.Lines[0].Content != "line1" {
		t.Fatalf("line 1 content = %q, want 'line1'", result.Lines[0].Content)
	}
}

func TestStatusReturnsBranch(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")

	result, err := Status(t.Context(), StatusRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if result.Branch == "" {
		t.Fatal("expected branch to be set")
	}
	if !result.IsClean {
		t.Fatal("expected clean worktree")
	}
}

func TestDiffReturnsEmpty(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-m", "init")

	result, err := Diff(t.Context(), DiffRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Empty {
		t.Fatal("expected empty diff")
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	// #nosec G204 -- tests execute fixed git commands only.
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
	return string(out)
}

func TestCommitOwnedCommitsModifiedTrackedIgnoredFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), "ignored/\n")
	run(t, repo, "add", ".gitignore")
	run(t, repo, "commit", "-m", "ignore dir")
	writeFile(t, filepath.Join(repo, "ignored", "task.md"), "v1\n")
	run(t, repo, "add", "-f", "ignored/task.md")
	run(t, repo, "commit", "-m", "track ignored")

	writeFile(t, filepath.Join(repo, "ignored", "task.md"), "v2\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"ignored/task.md"},
		Message:  "update tracked ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok (reason %q)", result.Status, result.Reason)
	}
	diff := run(t, repo, "show", "--name-status", "--format=", "HEAD")
	if !strings.Contains(diff, "M\tignored/task.md") {
		t.Fatalf("commit should modify ignored/task.md: %q", diff)
	}
	if got := run(t, repo, "status", "--short"); got != "" {
		t.Fatalf("unexpected status: %q", got)
	}
}

func TestCommitOwnedCommitsPrestagedTrackedIgnoredFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), "ignored/\n")
	run(t, repo, "add", ".gitignore")
	run(t, repo, "commit", "-m", "ignore dir")
	writeFile(t, filepath.Join(repo, "ignored", "task.md"), "v1\n")
	run(t, repo, "add", "-f", "ignored/task.md")
	run(t, repo, "commit", "-m", "track ignored")

	writeFile(t, filepath.Join(repo, "ignored", "task.md"), "v2\n")
	run(t, repo, "add", "-f", "ignored/task.md")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: repo,
		Files:    []string{"ignored/task.md"},
		Message:  "commit staged tracked ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok (reason %q)", result.Status, result.Reason)
	}
	diff := run(t, repo, "show", "--name-status", "--format=", "HEAD")
	if !strings.Contains(diff, "M\tignored/task.md") {
		t.Fatalf("commit should modify ignored/task.md: %q", diff)
	}
	if got := run(t, repo, "status", "--short"); got != "" {
		t.Fatalf("unexpected status: %q", got)
	}
}
