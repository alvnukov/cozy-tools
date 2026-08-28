//go:build aix || solaris || plan9 || js || wasip1

package command

import (
	"errors"
	"os"
)

var (
	errIndexLockBusy        = errors.New("command log lock held by another holder")
	errIndexLockUnsupported = errors.New("command history locking is unsupported on this platform")
)

func lockIndexFile(_ *os.File) error {
	return errIndexLockUnsupported
}

func unlockIndexFile(_ *os.File) {}
