package gitops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// LogRequest defines bounded filters for repository history.
type LogRequest struct {
	RepoPath string `json:"repo_path"`
	Limit    int    `json:"limit,omitempty"`
	Path     string `json:"path,omitempty"`
	Author   string `json:"author,omitempty"`
	Since    string `json:"since,omitempty"`
	Until    string `json:"until,omitempty"`
	Grep     string `json:"grep,omitempty"`
}

// LogCommit describes one commit returned by a history query.
type LogCommit struct {
	Hash      string   `json:"hash"`
	ShortHash string   `json:"short_hash"`
	Author    string   `json:"author"`
	Date      string   `json:"date"`
	Message   string   `json:"message"`
	Files     []string `json:"files,omitempty"`
}

// LogResult contains the bounded commit history and its returned size.
type LogResult struct {
	Commits []LogCommit `json:"commits"`
	Total   int         `json:"total"`
}

// Log returns structured repository history matching the requested filters.
func Log(ctx context.Context, req LogRequest) (LogResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return LogResult{}, errors.New("repo_path is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return LogResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return LogResult{}, err
	}

	args := []string{"log", "--format=%H%x1f%h%x1f%an%x1f%ai%x1f%s", "--no-color"}
	if req.Limit > 0 {
		args = append(args, fmt.Sprintf("-%d", req.Limit))
	} else {
		args = append(args, "-20")
	}
	if req.Author != "" {
		args = append(args, "--author="+req.Author)
	}
	if req.Since != "" {
		args = append(args, "--since="+req.Since)
	}
	if req.Until != "" {
		args = append(args, "--until="+req.Until)
	}
	if req.Grep != "" {
		args = append(args, "--grep="+req.Grep)
	}
	if req.Path != "" {
		args = append(args, "--", req.Path)
	}

	raw, err := runGit(ctx, repo, args...)
	if err != nil {
		return LogResult{}, err
	}

	result := LogResult{}
	for _, line := range splitLines(raw) {
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		commit := LogCommit{
			Hash:      parts[0],
			ShortHash: parts[1],
			Author:    parts[2],
			Date:      parts[3],
			Message:   parts[4],
		}
		result.Commits = append(result.Commits, commit)
	}
	result.Total = len(result.Commits)
	return result, nil
}

// LogDiffRequest identifies one commit whose metadata and patch should be inspected.
type LogDiffRequest struct {
	RepoPath string `json:"repo_path"`
	Hash     string `json:"hash"`
}

// LogDiffResult contains one commit's metadata, structured patch, and file statistics.
type LogDiffResult struct {
	Hash      string     `json:"hash"`
	ShortHash string     `json:"short_hash"`
	Author    string     `json:"author"`
	Date      string     `json:"date"`
	Message   string     `json:"message"`
	Files     []DiffFile `json:"files"`
	Stats     []FileStat `json:"stats"`
}

// FileStat summarizes additions and deletions for one changed path.
type FileStat struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// LogDiff returns metadata and a structured patch for one commit.
func LogDiff(ctx context.Context, req LogDiffRequest) (LogDiffResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return LogDiffResult{}, errors.New("repo_path is required")
	}
	if strings.TrimSpace(req.Hash) == "" {
		return LogDiffResult{}, errors.New("hash is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return LogDiffResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return LogDiffResult{}, err
	}

	raw, err := runGit(ctx, repo, "show", "--format=%H|%h|%an|%ai|%s", "--no-color", "-U3", req.Hash)
	if err != nil {
		return LogDiffResult{}, err
	}

	result := LogDiffResult{}
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 {
		header := lines[0]
		parts := strings.SplitN(header, "|", 5)
		if len(parts) >= 5 {
			result.Hash = parts[0]
			result.ShortHash = parts[1]
			result.Author = parts[2]
			result.Date = parts[3]
			result.Message = parts[4]
		}
	}

	var currentFile *DiffFile
	var currentHunk *DiffHunk

	for _, line := range lines[1:] {
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
		case strings.HasPrefix(line, "Binary files"):
			if currentFile != nil {
				currentFile.IsBinary = true
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

	statRaw, err := runGit(ctx, repo, "show", "--stat", "--format=", "--no-color", req.Hash)
	if err == nil {
		for _, line := range splitLines(statRaw) {
			if strings.Contains(line, "|") {
				parts := strings.SplitN(line, "|", 2)
				if len(parts) == 2 {
					path := strings.TrimSpace(parts[0])
					changeStr := strings.TrimSpace(parts[1])
					additions := strings.Count(changeStr, "+")
					deletions := strings.Count(changeStr, "-")
					result.Stats = append(result.Stats, FileStat{Path: path, Additions: additions, Deletions: deletions})
				}
			}
		}
	}

	return result, nil
}
