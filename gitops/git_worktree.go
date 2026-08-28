// Package gitops provides guarded Git repository operations.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CommitRequest defines the repository, owned file set, and message for a guarded commit.
type CommitRequest struct {
	RepoPath string   `json:"repo_path"`
	Repo     string   `json:"repo"`
	Files    []string `json:"files"`
	Message  string   `json:"message"`
}

// CommitResult reports the guarded commit outcome and the files staged for it.
type CommitResult struct {
	Status      string   `json:"status"`
	Commit      string   `json:"commit,omitempty"`
	StagedFiles []string `json:"staged_files"`
	Reason      string   `json:"reason,omitempty"`
}

// PrepareTaskWorktreeRequest identifies the task and repository for an isolated worktree.
type PrepareTaskWorktreeRequest struct {
	RepoPath string `json:"repo_path"`
	TaskID   string `json:"task_id"`
	TaskType string `json:"task_type"`
}

// PrepareTaskWorktreeResult reports the resolved task branch and worktree location.
type PrepareTaskWorktreeResult struct {
	Status       string `json:"status"`
	Branch       string `json:"branch"`
	WorktreePath string `json:"worktree_path"`
	CodePath     string `json:"code_path"`
	Created      bool   `json:"created"`
	Reason       string `json:"reason,omitempty"`
}

// PrepareTaskWorktree creates or reuses the canonical isolated worktree for a task.
func PrepareTaskWorktree(ctx context.Context, req PrepareTaskWorktreeRequest) (PrepareTaskWorktreeResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return PrepareTaskWorktreeResult{}, errors.New("repo_path is required")
	}
	branch, err := BranchForTask(req.TaskType, req.TaskID)
	if err != nil {
		return PrepareTaskWorktreeResult{}, err
	}
	worktreePath := WorktreePathForID(req.TaskID)
	if worktreePath == "" {
		return PrepareTaskWorktreeResult{}, errors.New("task_id is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return PrepareTaskWorktreeResult{}, err
	}
	top, err := runGit(ctx, repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return PrepareTaskWorktreeResult{}, fmt.Errorf("not a git repo: %w", err)
	}
	repo = strings.TrimSpace(top)
	codePath := filepath.Join(repo, filepath.FromSlash(worktreePath))
	worktreesDir := filepath.Join(repo, ".worktrees")
	if rel, err := filepath.Rel(worktreesDir, codePath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return PrepareTaskWorktreeResult{}, fmt.Errorf("worktree path escapes .worktrees: %s", worktreePath)
	}
	if info, statErr := os.Stat(codePath); statErr == nil {
		if !info.IsDir() {
			return PrepareTaskWorktreeResult{Status: "conflict", Branch: branch, WorktreePath: worktreePath, CodePath: codePath, Reason: "worktree path exists and is not a directory"}, nil
		}
		current, err := runGit(ctx, codePath, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return PrepareTaskWorktreeResult{Status: "conflict", Branch: branch, WorktreePath: worktreePath, CodePath: codePath, Reason: "worktree path is not a git checkout"}, nil
		}
		if strings.TrimSpace(current) != branch {
			return PrepareTaskWorktreeResult{Status: "conflict", Branch: branch, WorktreePath: worktreePath, CodePath: codePath, Reason: "worktree path is on branch " + strings.TrimSpace(current)}, nil
		}
		statusOut, err := runGit(ctx, codePath, "status", "--porcelain")
		if err != nil {
			return PrepareTaskWorktreeResult{Status: "conflict", Branch: branch, WorktreePath: worktreePath, CodePath: codePath, Reason: "worktree status cannot be verified"}, nil
		}
		if strings.TrimSpace(statusOut) != "" {
			return PrepareTaskWorktreeResult{Status: "conflict", Branch: branch, WorktreePath: worktreePath, CodePath: codePath, Reason: "worktree has uncommitted changes"}, nil
		}
		commonDir, err := runGit(ctx, codePath, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || strings.TrimSpace(commonDir) != filepath.Join(repo, ".git") {
			return PrepareTaskWorktreeResult{Status: "conflict", Branch: branch, WorktreePath: worktreePath, CodePath: codePath, Reason: "worktree does not belong to this repository"}, nil
		}
		return PrepareTaskWorktreeResult{Status: "ok", Branch: branch, WorktreePath: worktreePath, CodePath: codePath, Created: false, Reason: "worktree already exists"}, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return PrepareTaskWorktreeResult{}, statErr
	}
	if err := os.MkdirAll(worktreesDir, 0o700); err != nil {
		return PrepareTaskWorktreeResult{}, err
	}
	args := []string{"worktree", "add"}
	if gitBranchExists(ctx, repo, branch) {
		args = append(args, codePath, branch)
	} else {
		args = append(args, "-b", branch, codePath, "HEAD")
	}
	if _, err := runGit(ctx, repo, args...); err != nil {
		return PrepareTaskWorktreeResult{}, err
	}
	return PrepareTaskWorktreeResult{Status: "ok", Branch: branch, WorktreePath: worktreePath, CodePath: codePath, Created: true}, nil
}
