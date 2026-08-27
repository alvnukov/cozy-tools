//go:build !darwin && !linux && !windows

package command

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// legacyTermination keeps the pre-redesign lifecycle for unix platforms this
// package does not carry a waitid wrapper for: the group kill after
// command.Run remains best-effort with the same theoretical pid-reuse window
// the darwin/linux ordering removes. Porting waitProcessExitWithoutReap next
// to this file is what it would take to extend the sound order here.
type legacyTermination struct{}

func prepareCommandTermination(command *exec.Cmd) *legacyTermination {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return killCommandProcessGroup(command)
	}
	command.WaitDelay = processWaitDelay
	return &legacyTermination{}
}

func (t *legacyTermination) run(command *exec.Cmd) error {
	err := command.Run()
	// Best-effort cleanup for descendants that outlive a normally completed shell.
	_ = killCommandProcessGroup(command)
	return err
}

func killCommandProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
