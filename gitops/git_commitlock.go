package gitops

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// lockToken is a held advisory interprocess lock over one repository.
type lockToken struct {
	file *os.File
	path string
}

func (t *lockToken) release() {
	if t == nil || t.file == nil {
		return
	}
	_ = unlockFile(t.file)
	_ = t.file.Close()
	if mu, ok := commitLockMutexes.Load(t.path); ok {
		mu.(*sync.Mutex).Unlock()
	}
	t.file = nil
}

// commitLockMutexes serializes guarded commits within one process: flock
// guards separate open file descriptions independently, so two goroutines of
// the same process would otherwise both believe they hold the lock.
var commitLockMutexes sync.Map // string -> *sync.Mutex

// acquireCommitLock takes an exclusive lock on <gitdir>/cozy-commit.lock,
// serializing guarded commits against each other both across processes
// (flock) and between goroutines of this process (mutex). Git itself does not
// serialize `add`/`commit` sequences, so this lock is what turns
// read-check-write over the index into a transaction. It blocks until the
// context deadline when another holder is active.
func acquireCommitLock(ctx context.Context, gitDir string) (*lockToken, error) {
	lockPath := filepath.Join(gitDir, "cozy-commit.lock")
	mu, _ := commitLockMutexes.LoadOrStore(lockPath, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		mu.(*sync.Mutex).Unlock()
		return nil, fmt.Errorf("open commit lock: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		err = lockFileExclusive(f)
		if err == nil {
			return &lockToken{file: f, path: lockPath}, nil
		}
		if !isLockRetryable(err) {
			_ = f.Close()
			mu.(*sync.Mutex).Unlock()
			return nil, fmt.Errorf("acquire commit lock: %w", err)
		}
		select {
		case <-waitCtx.Done():
			_ = f.Close()
			mu.(*sync.Mutex).Unlock()
			return nil, fmt.Errorf("acquire commit lock: %w", waitCtx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// withIsolatedIndex runs fn with every git invocation in its context directed
// at a scratch index holding a snapshot of the real index taken under the
// commit lock. The guarded commit therefore contains exactly: pre-existing
// staged content -- which is either owned (and committed) or foreign (and
// rejected) -- plus what it stages itself, and nothing any other process
// stages afterwards. On every path the scratch file is removed, which rolls
// the transaction back and leaves the real index untouched.
func withIsolatedIndex[T any](ctx context.Context, repo string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	gitDir, err := gitDirOf(ctx, repo)
	if err != nil {
		return zero, err
	}
	scratch := filepath.Join(gitDir, fmt.Sprintf("cozy-index-%d-%d", os.Getpid(), time.Now().UnixNano()))
	defer func() { _ = os.Remove(scratch) }()

	if err := snapshotIndex(ctx, repo, gitDir, scratch); err != nil {
		return zero, err
	}
	return fn(context.WithValue(ctx, isolatedIndexKey{}, scratch))
}

// gitDirOf resolves the repository's git directory, which is not necessarily
// <repo>/.git for worktrees.
func gitDirOf(ctx context.Context, repo string) (string, error) {
	out, err := runGitPlain(ctx, repo, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("resolve git dir: %w", err)
	}
	dir := strings.TrimSpace(out)
	if dir == "" {
		return "", errors.New("resolve git dir: empty result")
	}
	return dir, nil
}

// emptyIndex is a valid git index with zero entries: "DIRC", version 2,
// entry count 0, and the trailing SHA-1 checksum over the preceding bytes.
var emptyIndex = buildEmptyIndex()

func buildEmptyIndex() []byte {
	hdr := []byte{'D', 'I', 'R', 'C', 0, 0, 0, 2, 0, 0, 0, 0}
	sum := sha1.Sum(hdr)
	return append(hdr, sum[:]...)
}

// snapshotIndex copies the real index to the scratch path in one byte copy.
// The real index already materializes "HEAD plus staged entries", so a copy
// is the exact seed a guarded commit needs. Git replaces the index with an
// atomic rename, so the copy is never torn; it is taken under the commit
// lock, so cooperating processes cannot change it mid-copy.
func snapshotIndex(ctx context.Context, repo, gitDir, scratch string) error {
	data, err := os.ReadFile(filepath.Join(gitDir, "index"))
	if errors.Is(err, os.ErrNotExist) {
		// No index yet (unborn repository, nothing ever staged): an empty
		// index is the correct seed.
		data = emptyIndex
	} else if err != nil {
		return fmt.Errorf("read index: %w", err)
	}
	if err := os.WriteFile(scratch, data, 0o600); err != nil {
		return fmt.Errorf("write isolated index: %w", err)
	}
	return nil
}

// syncIndexAfterCommit re-points the real index at the commit the guarded
// transaction just created, the way a plain `git commit` would, so `git
// status` does not report phantom modifications afterwards. The worktree is
// never touched.
func syncIndexAfterCommit(ctx context.Context, repo string) error {
	if _, err := runGit(ctx, repo, "reset", "--quiet", "--mixed"); err != nil {
		return fmt.Errorf("sync index after commit: %w", err)
	}
	return nil
}

type isolatedIndexKey struct{}

// isolatedIndex returns the scratch index path for git invocations in ctx, if
// the context is inside a guarded commit transaction.
func isolatedIndex(ctx context.Context) (string, bool) {
	index, ok := ctx.Value(isolatedIndexKey{}).(string)
	return index, ok
}
