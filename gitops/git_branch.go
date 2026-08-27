package gitops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"strings"
)

// StashRequest selects the repository whose stash entries should be listed.
type StashRequest struct {
	RepoPath string `json:"repo_path"`
}

// StashEntry describes one entry in a repository stash stack.
type StashEntry struct {
	Index   int    `json:"index"`
	Hash    string `json:"hash"`
	Message string `json:"message"`
}

// StashResult contains the parsed stash stack and its total size.
type StashResult struct {
	Entries []StashEntry `json:"entries"`
	Total   int          `json:"total"`
}

// StashList returns the repository stash stack without modifying it.
func StashList(ctx context.Context, req StashRequest) (StashResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return StashResult{}, errors.New("repo_path is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return StashResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return StashResult{}, err
	}

	raw, err := runGit(ctx, repo, "stash", "list", "--format=%H|%gd|%s")
	if err != nil {
		return StashResult{}, err
	}

	result := StashResult{}
	for _, line := range splitLines(raw) {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		entry := StashEntry{
			Hash:    parts[0],
			Message: parts[2],
		}
		if _, err := fmt.Sscanf(parts[1], "stash@{%d}", &entry.Index); err != nil {
			continue
		}
		result.Entries = append(result.Entries, entry)
	}
	result.Total = len(result.Entries)
	return result, nil
}

// BranchRequest selects local branches and optionally remote-tracking branches.
type BranchRequest struct {
	RepoPath string `json:"repo_path"`
	All      bool   `json:"all,omitempty"`
}

// Branch describes one local or remote-tracking branch reference.
type Branch struct {
	Name      string `json:"name"`
	IsCurrent bool   `json:"is_current"`
	IsRemote  bool   `json:"is_remote,omitempty"`
	Hash      string `json:"hash"`
}

// BranchResult contains branch references and the current branch name.
type BranchResult struct {
	Branches []Branch `json:"branches"`
	Current  string   `json:"current"`
}

// BranchList returns structured branch references for a repository.
func BranchList(ctx context.Context, req BranchRequest) (BranchResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return BranchResult{}, errors.New("repo_path is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return BranchResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return BranchResult{}, err
	}

	args := []string{"branch", "--format=%(refname:short)|%(objectname:short)|%(HEAD)"}
	if req.All {
		args = append(args, "-a")
	}

	raw, err := runGit(ctx, repo, args...)
	if err != nil {
		return BranchResult{}, err
	}

	result := BranchResult{}
	for _, line := range splitLines(raw) {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		branch := Branch{
			Name:      parts[0],
			Hash:      parts[1],
			IsCurrent: parts[2] == "*",
			IsRemote:  strings.Contains(parts[0], "/"),
		}
		if branch.IsCurrent {
			result.Current = branch.Name
		}
		result.Branches = append(result.Branches, branch)
	}
	return result, nil
}

// RemoteRequest selects the repository whose remotes should be listed.
type RemoteRequest struct {
	RepoPath string `json:"repo_path"`
}

// Remote describes one configured remote and its fetch URL.
type Remote struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Fetch string `json:"fetch,omitempty"`
}

// RemoteResult contains the configured repository remotes.
type RemoteResult struct {
	Remotes []Remote `json:"remotes"`
}

// RemoteList returns structured remote configuration without modifying it.
func RemoteList(ctx context.Context, req RemoteRequest) (RemoteResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return RemoteResult{}, errors.New("repo_path is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return RemoteResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return RemoteResult{}, err
	}

	raw, err := runGit(ctx, repo, "remote", "-v")
	if err != nil {
		return RemoteResult{}, err
	}

	remotes := map[string]*Remote{}
	for _, line := range splitLines(raw) {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		name := parts[0]
		url := parts[1]
		direction := parts[2]
		if _, ok := remotes[name]; !ok {
			remotes[name] = &Remote{Name: name}
		}
		switch direction {
		case "(fetch)":
			remotes[name].URL = url
			remotes[name].Fetch = url
		case "(push)":
			remotes[name].URL = url
		}
	}

	result := RemoteResult{}
	for _, r := range remotes {
		result.Remotes = append(result.Remotes, *r)
	}
	return result, nil
}

// TagRequest selects repository tags, optionally filtered by a Git glob pattern.
type TagRequest struct {
	RepoPath string `json:"repo_path"`
	Pattern  string `json:"pattern,omitempty"`
}

// Tag describes one lightweight or annotated repository tag.
type Tag struct {
	Name        string `json:"name"`
	Hash        string `json:"hash"`
	IsAnnotated bool   `json:"is_annotated,omitempty"`
	Message     string `json:"message,omitempty"`
}

// TagResult contains matching repository tags and their returned count.
type TagResult struct {
	Tags  []Tag `json:"tags"`
	Total int   `json:"total"`
}

// TagList returns structured repository tags matching the optional pattern.
func TagList(ctx context.Context, req TagRequest) (TagResult, error) {
	if strings.TrimSpace(req.RepoPath) == "" {
		return TagResult{}, errors.New("repo_path is required")
	}
	repo, err := filepath.Abs(req.RepoPath)
	if err != nil {
		return TagResult{}, err
	}
	if _, err := runGit(ctx, repo, "rev-parse", "--show-toplevel"); err != nil {
		return TagResult{}, err
	}

	args := []string{"tag", "--format=%(refname:short)|%(objectname:short)|%(objecttype)|%(contents)"}
	if req.Pattern != "" {
		args = append(args, "-l", req.Pattern)
	}

	raw, err := runGit(ctx, repo, args...)
	if err != nil {
		return TagResult{}, err
	}

	result := TagResult{}
	for _, line := range splitLines(raw) {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 3 {
			continue
		}
		tag := Tag{
			Name: parts[0],
			Hash: parts[1],
		}
		if parts[2] == "tag" {
			tag.IsAnnotated = true
		}
		if len(parts) > 3 {
			tag.Message = parts[3]
		}
		result.Tags = append(result.Tags, tag)
	}
	result.Total = len(result.Tags)
	return result, nil
}
