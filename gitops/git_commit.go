package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// commitLockedInput bundles everything the transactional commit body needs.
type commitLockedInput struct {
	repo    string
	gitDir  string
	req     CommitRequest
	owned   []string
	allowed map[string]struct{}
}

// CommitOwned stages and commits exactly the declared owned files as a
// transaction: an interprocess lock serializes guarded commits, and staging,
// validation and the commit itself run against an isolated index so that no
// other process can change what the commit contains. Pre-existing staged
// content is preserved when it belongs to the owned set and rejected
// otherwise; every failure path leaves the real index and worktree untouched.
func CommitOwned(ctx context.Context, req CommitRequest) (CommitResult, error) {
	repoInput := req.RepoPath
	if repoInput == "" {
		repoInput = req.Repo
	}
	if strings.TrimSpace(repoInput) == "" {
		return CommitResult{}, errors.New("repo_path is required")
	}
	if len(req.Files) == 0 {
		return CommitResult{Status: "skipped", Reason: "no files to commit"}, nil
	}
	if strings.TrimSpace(req.Message) == "" {
		return CommitResult{}, errors.New("message is required")
	}
	repo, err := filepath.Abs(repoInput)
	if err != nil {
		return CommitResult{}, err
	}
	top, err := runGit(ctx, repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return CommitResult{}, fmt.Errorf("not a git repo: %w", err)
	}
	repo = strings.TrimSpace(top)

	owned, err := normalizeOwnedFiles(req.Files)
	if err != nil {
		return CommitResult{}, err
	}
	if len(owned) == 0 {
		return CommitResult{Status: "skipped", Reason: "no non-empty files to commit"}, nil
	}
	allowed := map[string]struct{}{}
	for _, file := range owned {
		allowed[file] = struct{}{}
	}

	gitDir, err := gitDirOf(ctx, repo)
	if err != nil {
		return CommitResult{}, err
	}
	lock, err := acquireCommitLock(ctx, gitDir)
	if err != nil {
		return CommitResult{}, err
	}
	defer lock.release()

	in := commitLockedInput{repo: repo, gitDir: gitDir, req: req, owned: owned, allowed: allowed}
	result, err := withIsolatedIndex(ctx, repo, func(tctx context.Context) (CommitResult, error) {
		return commitOwnedLocked(tctx, in)
	})
	if err != nil || result.Status != "ok" {
		return result, err
	}
	// Advance the real index to the commit just created, as a plain
	// `git commit` would, so `git status` shows no phantom modifications.
	if err := syncIndexAfterCommit(ctx, repo); err != nil {
		return result, err
	}
	return result, nil
}

// commitOwnedLocked is the transactional body of CommitOwned. Every git
// invocation in its context is directed at the isolated index (see
// git_commitlock.go), so the staged set it validates and commits is exactly
// HEAD + pre-staged entries + what it stages itself.
func commitOwnedLocked(ctx context.Context, in commitLockedInput) (CommitResult, error) {
	repo := in.repo

	preStaged, err := stagedFiles(ctx, repo)
	if err != nil {
		return CommitResult{}, err
	}
	for _, file := range preStaged {
		if _, ok := in.allowed[file]; !ok {
			return CommitResult{Status: "conflict", StagedFiles: preStaged, Reason: "index already contains file outside owned set: " + file}, nil
		}
	}

	trackedFiles, err := trackedOwnedFiles(ctx, repo, in.owned)
	if err != nil {
		return CommitResult{}, err
	}
	trackedSet := make(map[string]struct{}, len(trackedFiles))
	for _, file := range trackedFiles {
		trackedSet[file] = struct{}{}
	}
	if len(trackedFiles) > 0 {
		updateArgs := append([]string{"add", "-u", "--"}, literalOwnedPathspecs(trackedFiles)...)
		if _, err := runGitIn(ctx, repo, updateArgs...); err != nil {
			return CommitResult{}, err
		}
	}
	existingFiles := make([]string, 0, len(in.owned))
	for _, file := range in.owned {
		if _, tracked := trackedSet[file]; tracked {
			continue
		}
		_, statErr := os.Stat(filepath.Join(repo, file))
		if statErr == nil {
			existingFiles = append(existingFiles, file)
			continue
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return CommitResult{}, statErr
		}
	}
	if len(existingFiles) > 0 {
		ignored := ignoredOwnedFiles(ctx, repo, existingFiles)
		normal := make([]string, 0, len(existingFiles))
		force := make([]string, 0, len(ignored))
		for _, f := range existingFiles {
			if ignored[f] {
				force = append(force, f)
			} else {
				normal = append(normal, f)
			}
		}
		if len(normal) > 0 {
			if _, err := runGitIn(ctx, repo, append([]string{"add", "--"}, literalOwnedPathspecs(normal)...)...); err != nil {
				return CommitResult{}, err
			}
		}
		if len(force) > 0 {
			if _, err := runGitIn(ctx, repo, append([]string{"add", "-f", "--"}, literalOwnedPathspecs(force)...)...); err != nil {
				return CommitResult{}, err
			}
		}
	}
	staged, err := stagedFiles(ctx, repo)
	if err != nil {
		return CommitResult{}, err
	}
	if len(staged) == 0 {
		return CommitResult{Status: "skipped", Reason: "no staged diff"}, nil
	}
	for _, file := range staged {
		if _, ok := in.allowed[file]; !ok {
			return CommitResult{Status: "conflict", StagedFiles: staged, Reason: "staged diff contains file outside owned set: " + file}, nil
		}
	}
	if _, err := runGitIn(ctx, repo, "commit", "-m", in.req.Message); err != nil {
		return CommitResult{}, err
	}
	commit, err := runGit(ctx, repo, "rev-parse", "--short", "HEAD")
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{Status: "ok", Commit: strings.TrimSpace(commit), StagedFiles: staged}, nil
}
