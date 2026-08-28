//go:build windows

package gitops

import "os"

// Windows has no flock(2). The commit lock degrades to its documented
// best-effort contract: the per-path process mutex still serializes
// goroutines of one process, and the lock file still records intent, but
// cross-process serialization of guarded commits is NOT provided on
// windows. Callers that need the transactional guarantee across processes
// must run the guarded commits from one host process (or add a real
// cross-process primitive before relying on it).
func lockFileExclusive(f *os.File) error { return nil }

func unlockFile(f *os.File) error { return nil }

func isLockRetryable(err error) bool { return false }
