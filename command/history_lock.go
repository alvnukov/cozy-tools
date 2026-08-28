package command

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	// indexLockName is one lock file for the whole command-log root. Retention
	// policy is global — Cleanup reads every repository's index in one pass —
	// so per-index locks would only add an ordering discipline between
	// cleanups. Critical sections are one index line for appends and one
	// rewrite pass for cleanup, so contention is not a concern.
	indexLockName = "command-history.lock"

	// indexLockWait bounds how long an append or a cleanup waits for the lock
	// before failing. Sections are milliseconds; anything longer means another
	// holder is wedged mid-cleanup or the filesystem cannot honor the lock,
	// and a visible error beats an unbounded hang or a silently lost record.
	indexLockWait = 5 * time.Second

	indexLockRetry = 10 * time.Millisecond
)

// indexLock is the inter-process coordination for command-log index mutations.
//
// Several helper processes share one log root. Put appends rows to each
// repository's index.jsonl; Cleanup rewrites those files from a snapshot.
// Rewriting while another process appends loses the append — its bytes land in
// the inode the rename discards — which is how a finished command stayed
// "running" for the rest of a helper's life, its result unreachable. Two
// alternatives were considered and rejected:
//
//   - keeping the index append-only and marking retention watermarks instead
//     of rewriting: index files would grow without bound and every readEntries
//     would pay for the helper's entire history;
//   - re-reading each index just before the rename and merging new rows in:
//     the gap between that read and the rename is the same race, one window
//     narrower.
//
// The lock closes the window by mutual exclusion: the append path holds it
// across open plus write (an append into the pre-rename inode is lost even
// with O_APPEND), cleanup holds it across read-through-rename, and readers
// never take it — rewrites go through a temp file and a rename, so a reader
// holds either the complete old index or the complete new one. The kernel
// releases the lock when a holder dies, so a crashed helper cannot wedge the
// log root.
type indexLock struct {
	file *os.File
}

// acquireIndexLock takes the lock guarding command-log index mutations for
// root, waiting up to indexLockWait for a current holder to finish.
func acquireIndexLock(root string) (*indexLock, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create command log root: %w", err)
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open command log root: %w", err)
	}
	file, err := rootFS.OpenFile(indexLockName, os.O_CREATE|os.O_RDWR, 0o600)
	closeRootErr := rootFS.Close()
	if err != nil {
		return nil, fmt.Errorf("open command log lock file: %w", err)
	}
	if closeRootErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("close command log root: %w", closeRootErr)
	}
	deadline := time.Now().Add(indexLockWait)
	for {
		err := lockIndexFile(file)
		if err == nil {
			return &indexLock{file: file}, nil
		}
		if !errors.Is(err, errIndexLockBusy) {
			_ = file.Close()
			return nil, fmt.Errorf("lock command log index: %w", err)
		}
		if !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("command log lock still held after %s; another helper appears stuck mid-append or mid-cleanup", indexLockWait)
		}
		time.Sleep(indexLockRetry)
	}
}

// release drops the lock so the next holder can proceed.
func (l *indexLock) release() {
	unlockIndexFile(l.file)
	_ = l.file.Close()
}
