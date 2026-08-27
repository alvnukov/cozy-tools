//go:build windows

package command

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

// windowsTermination keeps the historical Windows lifecycle. There is no
// zombie state to pin a pid with: the kernel handle inside os.Process is the
// only authority, and taskkill addresses the tree by pid number, so a
// post-exit tree kill cannot be made reuse-proof the way the darwin/linux
// run order is. The cancellation path is the sound one — Cancel fires before
// command.Wait closes the handles, while the tree still belongs to this
// command — and the post-completion cleanup stays best-effort with its
// documented theoretical reuse window.
type windowsTermination struct{}

func prepareCommandTermination(command *exec.Cmd) *windowsTermination {
	command.Cancel = func() error {
		return killCommandProcessGroup(command)
	}
	command.WaitDelay = processWaitDelay
	return &windowsTermination{}
}

func (t *windowsTermination) run(command *exec.Cmd) error {
	err := command.Run()
	// Best-effort cleanup for descendants that outlive a normally completed shell.
	_ = killCommandProcessGroup(command)
	return err
}

func killCommandProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}

	treeKill := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(command.Process.Pid))
	if err := treeKill.Run(); err == nil {
		return nil
	}

	err := command.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return os.ErrProcessDone
	}
	return err
}
