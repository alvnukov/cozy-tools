//go:build !windows

package command

import (
	"errors"
	"os"
	"syscall"
)

var errIndexLockBusy = errors.New("command log lock held by another holder")

func lockIndexFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
		return errIndexLockBusy
	}
	return err
}

func unlockIndexFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
