// Package project resolves per-repository helper storage paths.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultRootDirName = ".mcp-ai-helper"

var unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Store resolves helper data directories and repo-local task paths.
type Store struct {
	root string
}

// NewStore creates a project store rooted at root. The root is explicit
// host state: an empty root is a configuration error, not a silent default
// into a host-specific directory.
func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("project store root is required")
	}
	if strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, strings.TrimPrefix(root, "~/"))
	}
	return &Store{root: root}, nil
}

// Root returns the helper root directory.
func (s *Store) Root() string {
	return s.root
}

// Name returns a stable filesystem-safe project name for repoPath.
func Name(repoPath string) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", errors.New("repo_path is required")
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}
	base := unsafeNameChars.ReplaceAllString(filepath.Base(abs), "-")
	base = strings.Trim(base, ".-")
	if base == "" {
		base = "repo"
	}
	sum := sha256.Sum256([]byte(abs))
	return base + "-" + hex.EncodeToString(sum[:4]), nil
}

// RepoDir returns <root>/repos/<project>.
func (s *Store) RepoDir(repoPath string) (string, error) {
	name, err := Name(repoPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "repos", name), nil
}

// LogsDir returns the per-repo command log directory.
func (s *Store) LogsDir(repoPath string) (string, error) {
	repoDir, err := s.RepoDir(repoPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(repoDir, "logs"), nil
}
