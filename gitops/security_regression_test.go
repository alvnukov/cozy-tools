package gitops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCommitOwnedTreatsOwnedNamesAsLiteralPathspecs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("colon is not a valid Windows filename character")
	}
	repo := initRepo(t)
	owned := ":(glob)**"
	if err := os.WriteFile(filepath.Join(repo, owned), []byte("owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "outside.txt"), []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := CommitOwned(t.Context(), CommitRequest{RepoPath: repo, Files: []string{owned}, Message: "literal pathspec"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, reason = %q", result.Status, result.Reason)
	}
	tracked := run(t, repo, "ls-files")
	if strings.Contains(tracked, "outside.txt") {
		t.Fatalf("outside file was staged by pathspec expansion: %q", tracked)
	}
}
