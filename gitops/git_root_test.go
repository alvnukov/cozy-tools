package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

// diff --cached --name-only reports paths relative to the command's cwd, so
// committing through a repo subdirectory used to mismatch the
// toplevel-relative owned set and fail as a phantom conflict. CommitOwned
// has to resolve the repo root like PrepareTaskWorktree does.
func TestCommitOwnedFromSubdirectoryResolvesRepoRoot(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "owned.txt"), "owned\n")
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "placeholder.txt"), "x\n")

	result, err := CommitOwned(t.Context(), CommitRequest{
		RepoPath: sub,
		Files:    []string{"owned.txt"},
		Message:  "commit from a subdirectory",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q (%s), want ok", result.Status, result.Reason)
	}
}
