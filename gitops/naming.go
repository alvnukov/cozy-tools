package gitops

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Task naming parity with mcp-ai-helper's task registry: the branch and
// worktree names PrepareTaskWorktree resolves must stay byte-identical to
// the ones its task layer derives, so a worktree created through either
// surface reuses the other's canonical location.
var (
	taskIDPattern     = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	branchTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// WorktreePathForID returns the canonical repository-relative worktree path for a task ID.
func WorktreePathForID(id string) string {
	id = cleanTaskID(id)
	if id == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(".worktrees", id))
}

// BranchForTask returns the canonical branch name for a validated task type and ID.
func BranchForTask(taskType string, taskID string) (string, error) {
	taskType = cleanTaskType(taskType)
	taskID = cleanTaskID(taskID)
	if taskType == "" {
		return "", errors.New("task_type is required for task worktree branch")
	}
	if taskID == "" {
		return "", errors.New("task id is required for task worktree branch")
	}
	if !branchTypePattern.MatchString(taskType) {
		return "", fmt.Errorf("task_type %q is not a valid branch type", taskType)
	}
	return taskType + "/" + taskID, nil
}

func cleanTaskID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = taskIDPattern.ReplaceAllString(value, "-")
	return strings.Trim(value, ".-")
}

func cleanTaskType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = taskIDPattern.ReplaceAllString(value, "-")
	return strings.Trim(value, ".-")
}
