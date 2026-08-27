package command

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// The guarded edit path exists so a change can be refused when the file moved
// under the caller. A shell heredoc writing the same file has none of that: no
// snapshot, no expected hash, no conflict. The durable log shows whole source
// files arriving that way, the largest of them 432 lines.
//
// The denial names tools that exist. A guard that points at a missing tool does
// not protect anything -- it just sends the caller to a different shell.
const shellSourceWriteMessage = "policy_denied: writing repository source through a shell command bypasses the guarded edit path; " +
	"use file action=snapshot then edit action=replace to change a file, or edit action=write to create one"

// sourceFileExtensions are the files worth protecting. Everything else a
// command might redirect into -- reports, temp files, archives -- is left alone.
var sourceFileExtensions = map[string]bool{
	".c": true, ".cpp": true, ".go": true, ".h": true, ".hpp": true,
	".java": true, ".js": true, ".json": true, ".jsx": true, ".kt": true,
	".lean": true, ".md": true, ".mod": true, ".py": true, ".rb": true,
	".rs": true, ".sh": true, ".sql": true, ".toml": true, ".ts": true,
	".tsx": true, ".yaml": true, ".yml": true,
}

var (
	// applyPatchCommand matches the codex file writer in command position.
	applyPatchCommand = regexp.MustCompile(`(^|[\s;&|(])apply_patch(\s|$)`)
	// shellRedirectTarget matches `> path` and `>> path`, but not `2>&1`.
	shellRedirectTarget = regexp.MustCompile(`>>?\s*'?"?([^\s'";|&<>()]+)`)
	// teeTarget matches the paths tee writes to.
	teeTarget = regexp.MustCompile(`(^|[\s;&|(])tee(\s+-[a-z]+)*\s+'?"?([^\s'";|&<>()]+)`)
	// sedInPlaceTarget matches sed -i and its file arguments.
	sedInPlaceTarget = regexp.MustCompile(`(^|[\s;&|(])sed\s+(-[a-zA-Z]*\s+)*-i`)
)

// rejectShellSourceWrite refuses a command that writes repository source
// directly, and lets everything else through.
func rejectShellSourceWrite(cmd string, repoPath string) error {
	if applyPatchCommand.MatchString(cmd) {
		return fmt.Errorf("%s: command runs apply_patch", shellSourceWriteMessage)
	}
	for _, match := range shellRedirectTarget.FindAllStringSubmatch(cmd, -1) {
		if target := repoSourceTarget(match[1], repoPath); target != "" {
			return fmt.Errorf("%s: command redirects into %q", shellSourceWriteMessage, target)
		}
	}
	for _, match := range teeTarget.FindAllStringSubmatch(cmd, -1) {
		if target := repoSourceTarget(match[3], repoPath); target != "" {
			return fmt.Errorf("%s: command tees into %q", shellSourceWriteMessage, target)
		}
	}
	if sedInPlaceTarget.MatchString(cmd) {
		for _, field := range strings.Fields(cmd) {
			if target := repoSourceTarget(strings.Trim(field, `'"`), repoPath); target != "" {
				return fmt.Errorf("%s: command edits %q in place", shellSourceWriteMessage, target)
			}
		}
	}
	return nil
}

// repoSourceTarget returns path when it names a source file inside the
// repository, and "" otherwise.
//
// A relative path is a repository path, because commands run with the repository
// as their working directory. An absolute path is only a repository path when it
// says so: a redirect into /tmp or a shell variable is the caller's own business.
func repoSourceTarget(path string, repoPath string) string {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "$") || strings.HasPrefix(path, "~") {
		return ""
	}
	if !sourceFileExtensions[strings.ToLower(filepath.Ext(path))] {
		return ""
	}
	if !filepath.IsAbs(path) {
		return path
	}
	if repoPath == "" {
		return ""
	}
	repo, err := filepath.Abs(repoPath)
	if err != nil {
		return ""
	}
	if insideDir(repo, filepath.Clean(path)) {
		return path
	}
	return ""
}
