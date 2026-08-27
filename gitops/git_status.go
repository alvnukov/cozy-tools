package gitops

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// StatusRequest selects the repository whose working state should be inspected.
type StatusRequest struct {
	RepoPath string `json:"repo_path"`
}

// FileStatus describes one repository path in a normalized status category.
type FileStatus struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	XY     string `json:"xy"`
}

// StatusResult summarizes branch divergence and categorized working-tree changes.
type StatusResult struct {
	Branch    string       `json:"branch"`
	Upstream  string       `json:"upstream,omitempty"`
	Ahead     int          `json:"ahead,omitempty"`
	Behind    int          `json:"behind,omitempty"`
	Staged    []FileStatus `json:"staged,omitempty"`
	Modified  []FileStatus `json:"modified,omitempty"`
	Untracked []FileStatus `json:"untracked,omitempty"`
	Deleted   []FileStatus `json:"deleted,omitempty"`
	IsClean   bool         `json:"is_clean"`
	RepoPath  string       `json:"repo_path"`
}

// Status returns structured branch and working-tree state for a repository.
func Status(ctx context.Context, req StatusRequest) (StatusResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return StatusResult{}, errors.New("repo_path is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return StatusResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return StatusResult{}, err
	}

	result := StatusResult{RepoPath: repo, IsClean: true}

	branch, _ := runGit(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD")
	result.Branch = strings.TrimSpace(branch)

	upstream, _ := runGit(ctx, repo, "rev-parse", "--abbrev-ref", "@{upstream}")
	result.Upstream = strings.TrimSpace(upstream)
	if result.Upstream != "" {
		ab, _ := runGit(ctx, repo, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
		parts := strings.Fields(strings.TrimSpace(ab))
		if len(parts) == 2 {
			result.Ahead, _ = strconv.Atoi(parts[0])
			result.Behind, _ = strconv.Atoi(parts[1])
		}
	}

	porcelain, err := runGit(ctx, repo, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return StatusResult{}, err
	}

	for _, line := range splitLines(porcelain) {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case '#':
			continue
		case '?':
			path := strings.TrimSpace(line[2:])
			result.Untracked = append(result.Untracked, FileStatus{Path: path, Status: "untracked", XY: "??"})
			result.IsClean = false
		case '1', '2':
			parts := strings.Fields(line)
			if len(parts) < 9 {
				continue
			}
			xy := parts[1]
			path := parts[8]
			if tabIdx := strings.IndexByte(line, '\t'); tabIdx >= 0 {
				path = line[tabIdx+1:]
			}
			x := xy[0]
			y := xy[1]
			fs := FileStatus{Path: path, XY: xy}
			if x != '.' && x != '?' {
				fs.Status = "staged"
				result.Staged = append(result.Staged, fs)
				result.IsClean = false
			}
			switch y {
			case 'M':
				result.Modified = append(result.Modified, FileStatus{Path: path, XY: xy, Status: "modified"})
				result.IsClean = false
			case 'D':
				result.Deleted = append(result.Deleted, FileStatus{Path: path, XY: xy, Status: "deleted"})
				result.IsClean = false
			}
		case 'u':
			parts := strings.Fields(line)
			if len(parts) >= 11 {
				path := strings.Join(parts[10:], " ")
				if tabIdx := strings.IndexByte(line, '\t'); tabIdx >= 0 {
					path = line[tabIdx+1:]
				}
				result.Modified = append(result.Modified, FileStatus{Path: path, XY: parts[1], Status: "conflict"})
				result.IsClean = false
			}
		}
	}

	return result, nil
}
