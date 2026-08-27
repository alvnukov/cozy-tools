//go:build windows

package command

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var errIndexLockBusy = errors.New("command log lock held by another holder")

// errorLockViolation is ERROR_LOCK_VIOLATION from winerror.h; stdlib syscall
// names only the handful of error codes its own callers need, so this one is
// spelled out here.
const errorLockViolation = syscall.Errno(33)

// The byte range [0, max) mirrors flock's whole-file semantics, and
// LOCKFILE_FAIL_IMMEDIATELY turns LockFileEx into the non-blocking probe the
// shared retry loop in history_lock.go expects. Stdlib syscall exposes only
// the flag-less LockFile, so LockFileEx and UnlockFileEx come straight from
// kernel32, which is already mapped into every process and therefore cannot
// be swapped by a DLL search-path hijack.
const (
	lockFileExclusiveLock   = 0x00000002
	lockFileFailImmediately = 0x00000001
	lockFileRangeLow        = 0xFFFFFFFF
	lockFileRangeHigh       = 0xFFFFFFFF
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func lockIndexFile(file *os.File) error {
	var overlapped syscall.Overlapped
	ret, _, err := procLockFileEx.Call(
		file.Fd(),
		lockFileExclusiveLock|lockFileFailImmediately,
		0,
		lockFileRangeLow,
		lockFileRangeHigh,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ret == 0 {
		if errors.Is(err, errorLockViolation) {
			return errIndexLockBusy
		}
		return err
	}
	return nil
}

func unlockIndexFile(file *os.File) {
	var overlapped syscall.Overlapped
	_, _, _ = procUnlockFileEx.Call(
		file.Fd(),
		0,
		lockFileRangeLow,
		lockFileRangeHigh,
		uintptr(unsafe.Pointer(&overlapped)),
	)
}
