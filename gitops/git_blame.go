package gitops

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// BlameRequest selects one repository-relative file for blame inspection.
type BlameRequest struct {
	RepoPath string `json:"repo_path"`
	File     string `json:"file"`
}

// BlameLine describes the commit attribution for one source line.
type BlameLine struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// BlameResult contains bounded line-level attribution records.
type BlameResult struct {
	Lines []BlameLine `json:"lines"`
	Total int         `json:"total"`
}

// Blame returns porcelain blame attribution for a repository-relative file.
func Blame(ctx context.Context, req BlameRequest) (BlameResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return BlameResult{}, errors.New("repo_path is required")
	}
	if strings.TrimSpace(req.File) == "" {
		return BlameResult{}, errors.New("file is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return BlameResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return BlameResult{}, err
	}

	raw, err := runGit(ctx, repo, "blame", "--porcelain", "--", req.File)
	if err != nil {
		return BlameResult{}, err
	}

	result := BlameResult{}
	lines := strings.Split(raw, "\n")
	var current *BlameLine

	for _, line := range lines {
		if strings.HasPrefix(line, "\t") {
			if current != nil {
				current.Content = strings.TrimPrefix(line, "\t")
				result.Lines = append(result.Lines, *current)
			}
			current = nil
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		key := parts[0]
		value := parts[1]
		switch key {
		case "author":
			if current == nil {
				current = &BlameLine{}
			}
			current.Author = value
		case "author-time":
			if current == nil {
				current = &BlameLine{}
			}
			current.Date = value
		case "summary":
			// skip
		default:
			if len(key) == 40 {
				if current == nil {
					current = &BlameLine{}
				}
				current.Hash = key[:8]
				if n, err := strconv.Atoi(value); err == nil {
					current.Line = n
				}
			}
		}
	}
	result.Total = len(result.Lines)
	return result, nil
}
