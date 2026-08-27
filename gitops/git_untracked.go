package gitops

import (
	"context"
)

// UntrackedCount reports how many files under dir git does not track. The
// second return value is false when dir is outside any git repository or git
// is unavailable, in which case the count is meaningless. Only read-only git
// queries are used: ls-files reads the index without taking the index lock,
// so the call cannot mutate the repository. The ls-files query runs with dir
// as the working directory and "." as the pathspec, so symlinked absolute
// roots (macOS /var versus /private/var) never break a path comparison.
func UntrackedCount(ctx context.Context, dir string) (int, bool) {
	if _, err := runGit(ctx, dir, "rev-parse", "--show-toplevel"); err != nil {
		return 0, false
	}
	out, err := runGit(ctx, dir, "ls-files", "--others", "--exclude-standard", "--", ".")
	if err != nil {
		return 0, false
	}
	return len(splitLines(out)), true
}
