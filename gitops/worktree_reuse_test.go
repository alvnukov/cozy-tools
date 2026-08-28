package gitops

import (
	"path/filepath"
	"testing"
)

// Reuse used to accept any checkout on the right branch under
// .worktrees/<id>, with whatever dirty state it carried. A reused worktree
// has to be clean and provably a worktree of this repository.
func TestPrepareTaskWorktreeRefusesDirtyReuse(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-m", "base")

	first, err := PrepareTaskWorktree(t.Context(), PrepareTaskWorktreeRequest{RepoPath: repo, TaskID: "dirty-task", TaskType: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "ok" {
		t.Fatalf("first prepare = %#v", first)
	}

	writeFile(t, filepath.Join(first.CodePath, "dirty.txt"), "left behind\n")
	second, err := PrepareTaskWorktree(t.Context(), PrepareTaskWorktreeRequest{RepoPath: repo, TaskID: "dirty-task", TaskType: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "conflict" {
		t.Fatalf("reuse = %#v, want conflict on a dirty worktree", second)
	}
}
