package gitops

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

// PreflightRequest describes the files to check before commit.
type PreflightRequest struct {
	RepoPath string   `json:"repo_path"`
	Files    []string `json:"files"`
}

// PreflightResult reports the state of owned files and the worktree
// before any staging or commit mutation occurs.
//
// Status priority: conflict > dirty > ok.
//   - conflict: staged files exist outside the owned set; commit would fail.
//   - dirty: unowned tracked files are modified or untracked files exist.
//   - ok: only owned files have changes (or worktree is clean).
type PreflightResult struct {
	Status             string   `json:"status"`
	RepoPath           string   `json:"repo_path"`
	Branch             string   `json:"branch,omitempty"`
	OwnedNew           []string `json:"owned_new,omitempty"`
	OwnedModified      []string `json:"owned_modified,omitempty"`
	OwnedDeleted       []string `json:"owned_deleted,omitempty"`
	UnownedModified    []string `json:"unowned_modified,omitempty"`
	StagedOutsideOwned []string `json:"staged_outside_owned,omitempty"`
	UntrackedFiles     []string `json:"untracked_files,omitempty"`
}

// PreflightCommit performs a read-only analysis of the worktree before
// a commit attempt. It classifies owned files, detects unowned changes,
// and reports staged content outside the owned set.
func PreflightCommit(ctx context.Context, req PreflightRequest) (PreflightResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return PreflightResult{}, errors.New("repo_path is required")
	}
	if len(req.Files) == 0 {
		return PreflightResult{}, errors.New("files must not be empty")
	}

	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return PreflightResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return PreflightResult{}, err
	}

	branch, _ := runGit(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD")

	owned, err := normalizeOwnedFiles(req.Files)
	if err != nil {
		return PreflightResult{}, err
	}
	ownedSet := make(map[string]struct{}, len(owned))
	for _, f := range owned {
		ownedSet[f] = struct{}{}
	}

	result := PreflightResult{
		RepoPath: repo,
		Branch:   strings.TrimSpace(branch),
		Status:   "ok",
	}

	if err := classifyOwnedFiles(ctx, repo, owned, &result); err != nil {
		return PreflightResult{}, err
	}
	if err := detectStagedOutsideOwned(ctx, repo, ownedSet, &result); err != nil {
		return PreflightResult{}, err
	}
	if err := detectUnownedModified(ctx, repo, ownedSet, &result); err != nil {
		return PreflightResult{}, err
	}
	if err := detectUntrackedFiles(ctx, repo, ownedSet, &result); err != nil {
		return PreflightResult{}, err
	}

	result.Status = computeStatus(result)
	return result, nil
}

// classifyOwnedFiles partitions owned files into new, modified, and deleted.
func classifyOwnedFiles(ctx context.Context, repo string, owned []string, result *PreflightResult) error {
	tracked, err := trackedOwnedFiles(ctx, repo, owned)
	if err != nil {
		return err
	}
	trackedSet := make(map[string]struct{}, len(tracked))
	for _, f := range tracked {
		trackedSet[f] = struct{}{}
	}

	for _, f := range owned {
		absPath := filepath.Join(repo, filepath.FromSlash(f))
		_, statErr := filepath.EvalSymlinks(absPath)
		if statErr != nil {
			if _, tracked := trackedSet[f]; tracked {
				result.OwnedDeleted = append(result.OwnedDeleted, f)
			}
			continue
		}

		if _, tracked := trackedSet[f]; !tracked {
			result.OwnedNew = append(result.OwnedNew, f)
			continue
		}

		modified, err := isFileModified(ctx, repo, f)
		if err != nil {
			return err
		}
		if modified {
			result.OwnedModified = append(result.OwnedModified, f)
		}
	}

	sort.Strings(result.OwnedNew)
	sort.Strings(result.OwnedModified)
	sort.Strings(result.OwnedDeleted)
	return nil
}

// isFileModified checks if a tracked file has unstaged changes.
func isFileModified(ctx context.Context, repo string, file string) (bool, error) {
	out, err := runGit(ctx, repo, "diff", "--name-only", "--", file)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// detectStagedOutsideOwned finds files in the index that are not in the owned set.
func detectStagedOutsideOwned(ctx context.Context, repo string, ownedSet map[string]struct{}, result *PreflightResult) error {
	staged, err := stagedFiles(ctx, repo)
	if err != nil {
		return err
	}
	for _, f := range staged {
		if _, ok := ownedSet[f]; !ok {
			result.StagedOutsideOwned = append(result.StagedOutsideOwned, f)
		}
	}
	sort.Strings(result.StagedOutsideOwned)
	return nil
}

// detectUnownedModified finds tracked files outside the owned set that have
// unstaged modifications. These indicate the worktree is dirty.
func detectUnownedModified(ctx context.Context, repo string, ownedSet map[string]struct{}, result *PreflightResult) error {
	modified, err := runGit(ctx, repo, "diff", "--name-only")
	if err != nil {
		return err
	}
	for _, f := range splitLines(modified) {
		if _, ok := ownedSet[f]; !ok {
			result.UnownedModified = append(result.UnownedModified, f)
		}
	}
	sort.Strings(result.UnownedModified)
	return nil
}

// detectUntrackedFiles lists untracked files in the worktree (excluding .git and owned files).
func detectUntrackedFiles(ctx context.Context, repo string, ownedSet map[string]struct{}, result *PreflightResult) error {
	out, err := runGit(ctx, repo, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	for _, f := range splitLines(out) {
		if f == ".git" {
			continue
		}
		if _, owned := ownedSet[f]; owned {
			continue
		}
		result.UntrackedFiles = append(result.UntrackedFiles, f)
	}
	sort.Strings(result.UntrackedFiles)
	return nil
}

// computeStatus determines the overall preflight status.
// Priority: conflict > dirty > ok.
func computeStatus(result PreflightResult) string {
	if len(result.StagedOutsideOwned) > 0 {
		return "conflict"
	}
	if len(result.UnownedModified) > 0 || len(result.UntrackedFiles) > 0 {
		return "dirty"
	}
	return "ok"
}
