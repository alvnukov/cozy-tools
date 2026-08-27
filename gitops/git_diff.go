package gitops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// DiffRequest selects a working-tree or index diff, optionally limited to one path.
type DiffRequest struct {
	RepoPath string `json:"repo_path"`
	Cached   bool   `json:"cached,omitempty"`
	Path     string `json:"path,omitempty"`
}

// DiffHunk contains one unified-diff hunk header and its body lines.
type DiffHunk struct {
	Header string   `json:"header"`
	Lines  []string `json:"lines"`
}

// DiffFile describes one changed file and its parsed hunks.
type DiffFile struct {
	Path     string     `json:"path"`
	OldPath  string     `json:"old_path,omitempty"`
	Status   string     `json:"status"`
	Hunks    []DiffHunk `json:"hunks,omitempty"`
	IsBinary bool       `json:"is_binary,omitempty"`
}

// DiffResult contains the structured files in a Git diff.
type DiffResult struct {
	Files []DiffFile `json:"files"`
	Empty bool       `json:"empty"`
}

// Diff returns a structured working-tree or index diff.
func Diff(ctx context.Context, req DiffRequest) (DiffResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return DiffResult{}, errors.New("repo_path is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return DiffResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return DiffResult{}, err
	}

	args := []string{"diff", "--no-color", "-U3"}
	if req.Cached {
		args = append(args, "--cached")
	}
	if req.Path != "" {
		args = append(args, "--", req.Path)
	}

	raw, err := runGit(ctx, repo, args...)
	if err != nil {
		return DiffResult{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return DiffResult{Empty: true}, nil
	}

	result := DiffResult{}
	var currentFile *DiffFile
	var currentHunk *DiffHunk

	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			if currentFile != nil {
				if currentHunk != nil {
					currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
				}
				result.Files = append(result.Files, *currentFile)
			}
			currentFile = &DiffFile{Status: "modified"}
			currentHunk = nil
		case strings.HasPrefix(line, "rename from"):
			if currentFile != nil {
				currentFile.OldPath = strings.TrimPrefix(line, "rename from ")
				currentFile.Status = "renamed"
			}
		case strings.HasPrefix(line, "rename to"):
			if currentFile != nil {
				currentFile.Path = strings.TrimPrefix(line, "rename to ")
			}
		case strings.HasPrefix(line, "new file"):
			if currentFile != nil {
				currentFile.Status = "added"
			}
		case strings.HasPrefix(line, "deleted file"):
			if currentFile != nil {
				currentFile.Status = "deleted"
			}
		case strings.HasPrefix(line, "--- a/"):
			if currentFile != nil && currentFile.Path == "" {
				currentFile.Path = strings.TrimPrefix(line, "--- a/")
			}
		case strings.HasPrefix(line, "+++ b/"):
			if currentFile != nil {
				currentFile.Path = strings.TrimPrefix(line, "+++ b/")
			}
		case strings.HasPrefix(line, "@@"):
			if currentFile != nil {
				if currentHunk != nil {
					currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
				}
				currentHunk = &DiffHunk{Header: line}
			}
		case strings.HasPrefix(line, "Binary"):
			if currentFile != nil {
				currentFile.IsBinary = true
			}
		default:
			if currentHunk != nil {
				currentHunk.Lines = append(currentHunk.Lines, line)
			}
		}
	}

	if currentFile != nil {
		if currentHunk != nil {
			currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
		}
		result.Files = append(result.Files, *currentFile)
	}

	return result, nil
}
