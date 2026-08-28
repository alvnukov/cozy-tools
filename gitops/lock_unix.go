//go:build unix

package gitops

import (
	"errors"
	"os"
	"syscall"
)

// lockFileExclusive takes an exclusive non-blocking advisory lock on the
// open file description. It is flock(2): best-effort, released on process
// exit, and invisible to other processes that do not cooperate.
func lockFileExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFile releases the advisory lock taken by lockFileExclusive.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// isLockRetryable reports whether err means "someone else holds the lock"
// (or the call was interrupted) and waiting is meaningful.
func isLockRetryable(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EINTR)
}
