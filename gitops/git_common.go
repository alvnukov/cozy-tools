package gitops

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runGit(ctx context.Context, repo string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// #nosec G204 -- the executable is fixed to git and arguments are passed directly without a shell.
	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = repo
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func splitLines(text string) []string {
	raw := strings.Split(strings.TrimSpace(text), "\n")
	if len(raw) == 1 && raw[0] == "" {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func normalizeOwnedFiles(files []string) ([]string, error) {
	owned := make([]string, 0, len(files))
	seen := map[string]struct{}{}
	for _, file := range files {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(file)))
		if clean == "" || clean == "." {
			continue
		}
		if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || strings.HasPrefix(clean, "../") || clean == ".." {
			return nil, fmt.Errorf("owned file must be repo-relative: %q", file)
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		owned = append(owned, clean)
	}
	return owned, nil
}

// literalOwnedPathspecs prevents a repository filename such as `:(glob)**`
// from being interpreted as Git pathspec magic. The leading ./ is accepted by
// check-ignore (which rejects Git's literal pathspec magic) and by add/ls-files.
func literalOwnedPathspecs(files []string) []string {
	literal := make([]string, len(files))
	for i, file := range files {
		literal[i] = "./" + file
	}
	return literal
}

func stagedFiles(ctx context.Context, repo string) ([]string, error) {
	diff, err := runGit(ctx, repo, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	return splitLines(diff), nil
}

func trackedOwnedFiles(ctx context.Context, repo string, owned []string) ([]string, error) {
	args := append([]string{"ls-files", "--"}, literalOwnedPathspecs(owned)...)
	out, err := runGit(ctx, repo, args...)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func ignoredOwnedFiles(ctx context.Context, repo string, files []string) map[string]bool {
	if len(files) == 0 {
		return nil
	}
	args := append([]string{"check-ignore", "--"}, literalOwnedPathspecs(files)...)
	out, err := runGit(ctx, repo, args...)
	if err != nil {
		return nil
	}
	ignored := map[string]bool{}
	for _, line := range splitLines(out) {
		ignored[strings.TrimPrefix(line, "./")] = true
	}
	return ignored
}

func gitBranchExists(ctx context.Context, repo string, branch string) bool {
	_, err := runGit(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}
