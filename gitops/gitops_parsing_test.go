package gitops

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The porcelain v2 "u" record takes its path from strings.Fields, which
// truncates at the first space; records 1/2 already take the tab override.
func TestStatusReportsUnmergedPathsWithSpaces(t *testing.T) {
	repo := initRepo(t)
	path := filepath.Join(repo, "file with spaces.txt")
	writeFile(t, path, "base\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-m", "base")
	run(t, repo, "checkout", "-b", "side")
	writeFile(t, path, "side\n")
	run(t, repo, "commit", "-am", "side")
	run(t, repo, "checkout", "-")
	writeFile(t, path, "mainline\n")
	run(t, repo, "commit", "-am", "mainline")

	merge := exec.Command("git", "-C", repo, "merge", "side")
	mergeOut, _ := merge.CombinedOutput()
	if !strings.Contains(string(mergeOut), "CONFLICT") {
		t.Fatalf("expected a conflict, got: %s", mergeOut)
	}

	result, err := Status(t.Context(), StatusRequest{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fs := range result.Modified {
		if fs.Path == "file with spaces.txt" && fs.Status == "conflict" {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflict path misparsed, modified = %#v", result.Modified)
	}
}

// The log format joined fields with "|", which an author name containing a
// pipe shifts; the unit separator cannot appear in these fields.
func TestLogParsesAuthorWithPipe(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	run(t, repo, "add", ".")
	run(t, repo, "-c", "user.name=A|B", "-c", "user.email=a@b.c", "commit", "-m", "pipe author")

	result, err := Log(t.Context(), LogRequest{RepoPath: repo, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Commits) == 0 {
		t.Fatal("no commits parsed")
	}
	commit := result.Commits[0]
	if commit.Author != "A|B" || commit.Message != "pipe author" {
		t.Fatalf("author/message misparsed: %q / %q", commit.Author, commit.Message)
	}
}
