package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreflightCommitCleanWorktree(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "initial\n")
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "initial")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"README.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(result.OwnedNew) != 0 {
		t.Fatalf("owned_new = %v, want empty", result.OwnedNew)
	}
	if len(result.OwnedModified) != 0 {
		t.Fatalf("owned_modified = %v, want empty", result.OwnedModified)
	}
	if len(result.OwnedDeleted) != 0 {
		t.Fatalf("owned_deleted = %v, want empty", result.OwnedDeleted)
	}
	if len(result.UnownedModified) != 0 {
		t.Fatalf("unowned_modified = %v, want empty", result.UnownedModified)
	}
	if len(result.StagedOutsideOwned) != 0 {
		t.Fatalf("staged_outside_owned = %v, want empty", result.StagedOutsideOwned)
	}
}

func TestPreflightCommitDetectsNewOwnedFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "initial\n")
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "new.txt"), "new content\n")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"new.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(result.OwnedNew) != 1 || result.OwnedNew[0] != "new.txt" {
		t.Fatalf("owned_new = %v, want [new.txt]", result.OwnedNew)
	}
}

func TestPreflightCommitDetectsModifiedOwnedFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "owned.txt"), "original\n")
	run(t, repo, "add", "owned.txt")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "owned.txt"), "modified\n")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"owned.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(result.OwnedModified) != 1 || result.OwnedModified[0] != "owned.txt" {
		t.Fatalf("owned_modified = %v, want [owned.txt]", result.OwnedModified)
	}
}

func TestPreflightCommitDetectsDeletedOwnedFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "content\n")
	run(t, repo, "add", "tracked.txt")
	run(t, repo, "commit", "-m", "initial")

	if err := os.Remove(filepath.Join(repo, "tracked.txt")); err != nil {
		t.Fatal(err)
	}

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"tracked.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if len(result.OwnedDeleted) != 1 || result.OwnedDeleted[0] != "tracked.txt" {
		t.Fatalf("owned_deleted = %v, want [tracked.txt]", result.OwnedDeleted)
	}
}

func TestPreflightCommitDetectsUnownedModifiedFile(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "owned.txt"), "owned\n")
	writeFile(t, filepath.Join(repo, "external.txt"), "external\n")
	run(t, repo, "add", "owned.txt", "external.txt")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "external.txt"), "changed external\n")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"owned.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "dirty" {
		t.Fatalf("status = %q, want dirty", result.Status)
	}
	if len(result.UnownedModified) != 1 || result.UnownedModified[0] != "external.txt" {
		t.Fatalf("unowned_modified = %v, want [external.txt]", result.UnownedModified)
	}
}

func TestPreflightCommitDetectsStagedOutsideOwned(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "owned.txt"), "owned\n")
	writeFile(t, filepath.Join(repo, "external.txt"), "external\n")
	run(t, repo, "add", "external.txt")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"owned.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
	if len(result.StagedOutsideOwned) != 1 || result.StagedOutsideOwned[0] != "external.txt" {
		t.Fatalf("staged_outside_owned = %v, want [external.txt]", result.StagedOutsideOwned)
	}
}

func TestPreflightCommitRequiresRepoPath(t *testing.T) {
	_, err := PreflightCommit(t.Context(), PreflightRequest{
		Files: []string{"x.txt"},
	})
	if err == nil {
		t.Fatal("expected error for empty repo_path")
	}
}

func TestPreflightCommitRequiresFiles(t *testing.T) {
	repo := initRepo(t)
	_, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{},
	})
	if err == nil {
		t.Fatal("expected error for empty files")
	}
}

func TestPreflightCommitMixedState(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "tracked\n")
	writeFile(t, filepath.Join(repo, "external.txt"), "external\n")
	run(t, repo, "add", "tracked.txt", "external.txt")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "tracked.txt"), "modified tracked\n")
	writeFile(t, filepath.Join(repo, "external.txt"), "modified external\n")
	writeFile(t, filepath.Join(repo, "new.txt"), "new\n")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"tracked.txt", "new.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "dirty" {
		t.Fatalf("status = %q, want dirty", result.Status)
	}
	if len(result.OwnedModified) != 1 || result.OwnedModified[0] != "tracked.txt" {
		t.Fatalf("owned_modified = %v, want [tracked.txt]", result.OwnedModified)
	}
	if len(result.OwnedNew) != 1 || result.OwnedNew[0] != "new.txt" {
		t.Fatalf("owned_new = %v, want [new.txt]", result.OwnedNew)
	}
	if len(result.UnownedModified) != 1 || result.UnownedModified[0] != "external.txt" {
		t.Fatalf("unowned_modified = %v, want [external.txt]", result.UnownedModified)
	}
}

func TestPreflightCommitDetectsUntrackedFiles(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "initial\n")
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "scratch.tmp"), "temp\n")
	writeFile(t, filepath.Join(repo, "notes.md"), "notes\n")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"README.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "dirty" {
		t.Fatalf("status = %q, want dirty", result.Status)
	}
	found := 0
	for _, f := range result.UntrackedFiles {
		if f == "scratch.tmp" || f == "notes.md" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("untracked_files = %v, want scratch.tmp and notes.md", result.UntrackedFiles)
	}
}

func TestPreflightCommitSkipsGitDir(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "README.md"), "initial\n")
	run(t, repo, "add", "README.md")
	run(t, repo, "commit", "-m", "initial")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"README.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range result.UntrackedFiles {
		if filepath.Base(f) == ".git" {
			t.Fatalf("untracked_files should not contain .git: %v", result.UntrackedFiles)
		}
	}
}

func TestPreflightCommitHandlesMultipleOwnedFiles(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "a.txt"), "a\n")
	writeFile(t, filepath.Join(repo, "b.txt"), "b\n")
	writeFile(t, filepath.Join(repo, "c.txt"), "c\n")
	run(t, repo, "add", "a.txt", "b.txt", "c.txt")
	run(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "a.txt"), "a modified\n")
	if err := os.Remove(filepath.Join(repo, "b.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "d.txt"), "d new\n")

	result, err := PreflightCommit(t.Context(), PreflightRequest{
		RepoPath: repo,
		Files:    []string{"a.txt", "b.txt", "d.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.OwnedModified) != 1 || result.OwnedModified[0] != "a.txt" {
		t.Fatalf("owned_modified = %v, want [a.txt]", result.OwnedModified)
	}
	if len(result.OwnedDeleted) != 1 || result.OwnedDeleted[0] != "b.txt" {
		t.Fatalf("owned_deleted = %v, want [b.txt]", result.OwnedDeleted)
	}
	if len(result.OwnedNew) != 1 || result.OwnedNew[0] != "d.txt" {
		t.Fatalf("owned_new = %v, want [d.txt]", result.OwnedNew)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok (no unowned changes)", result.Status)
	}
}

func TestPreflightCommitStatusPriority(t *testing.T) {
	tests := []struct {
		name  string
		setup func(repo string)
		files []string
		want  string
	}{
		{
			name: "clean is ok",
			setup: func(repo string) {
				writeFile(t, filepath.Join(repo, "f.txt"), "v\n")
				run(t, repo, "add", "f.txt")
				run(t, repo, "commit", "-m", "init")
			},
			files: []string{"f.txt"},
			want:  "ok",
		},
		{
			name: "only owned changes is ok",
			setup: func(repo string) {
				writeFile(t, filepath.Join(repo, "f.txt"), "v\n")
				run(t, repo, "add", "f.txt")
				run(t, repo, "commit", "-m", "init")
				writeFile(t, filepath.Join(repo, "f.txt"), "v2\n")
			},
			files: []string{"f.txt"},
			want:  "ok",
		},
		{
			name: "unowned changes is dirty",
			setup: func(repo string) {
				writeFile(t, filepath.Join(repo, "f.txt"), "v\n")
				writeFile(t, filepath.Join(repo, "g.txt"), "v\n")
				run(t, repo, "add", "f.txt", "g.txt")
				run(t, repo, "commit", "-m", "init")
				writeFile(t, filepath.Join(repo, "g.txt"), "v2\n")
			},
			files: []string{"f.txt"},
			want:  "dirty",
		},
		{
			name: "staged outside owned is conflict",
			setup: func(repo string) {
				writeFile(t, filepath.Join(repo, "f.txt"), "v\n")
				writeFile(t, filepath.Join(repo, "g.txt"), "v\n")
				run(t, repo, "add", "g.txt")
			},
			files: []string{"f.txt"},
			want:  "conflict",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := initRepo(t)
			writeFile(t, filepath.Join(repo, "README.md"), "init\n")
			run(t, repo, "add", "README.md")
			run(t, repo, "commit", "-m", "init")
			tt.setup(repo)

			result, err := PreflightCommit(t.Context(), PreflightRequest{
				RepoPath: repo,
				Files:    tt.files,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tt.want {
				t.Fatalf("status = %q, want %q", result.Status, tt.want)
			}
		})
	}
}
