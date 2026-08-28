//go:build darwin || linux

package command

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// groupTermination owns the kill-the-whole-process-group lifecycle of one
// command.
//
// The rule this file implements: kill(-pgid, SIGKILL) is only safe while the
// group leader is alive or an unreaped zombie. A zombie pins its pid, so the
// pgid cannot be recycled to an unrelated process; once Wait reaps the
// leader, the pid is free, and a late kill(-pid) can SIGKILL whoever the
// kernel handed it to next. So the group is killed before the reap and never
// after: run waits for the leader to exit without reaping it (waitid with
// WNOWAIT), kills whatever is left of the group, and only then waits.
//
// The cancellation path (exec.Cmd.Cancel) races command.Wait, so it takes the
// same mutex the reap holds: a cancel that loses the race with the reap
// reports ErrProcessDone instead of firing into a possibly recycled pgid.
type groupTermination struct {
	mu     sync.Mutex
	reaped bool
}

// prepareCommandTermination installs the group-termination hooks on command.
func prepareCommandTermination(command *exec.Cmd) *groupTermination {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	termination := &groupTermination{}
	command.Cancel = func() error { return termination.cancel(command) }
	command.WaitDelay = processWaitDelay
	return termination
}

func (t *groupTermination) cancel(command *exec.Cmd) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reaped {
		return os.ErrProcessDone
	}
	return killCommandProcessGroup(command)
}

// run executes the command and cleans up its process group without ever
// addressing the pgid after the leader has been reaped.
func (t *groupTermination) run(command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	// The leader has exited but is still a zombie: it keeps the pid — and
	// with it the pgid — from being reused while the survivors are killed.
	// A waitid failure is survivable: the kill below would still precede the
	// reap, and command.Wait blocks for the leader either way.
	_ = waitProcessExitWithoutReap(command.Process.Pid)
	_ = killCommandProcessGroup(command)
	t.mu.Lock()
	t.reaped = true
	err := command.Wait()
	t.mu.Unlock()
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

// waitProcessExitWithoutReap blocks until pid exits but leaves it an unreaped
// zombie, which is what keeps the pid — and the process group it leads — from
// being handed to another process while the group is being killed. The
// waitid option values differ per platform and live in the sibling
// termination_waitid_*.go files.
func waitProcessExitWithoutReap(pid int) error {
	if pid <= 0 {
		return syscall.EINVAL
	}
	var info [128]byte
	for {
		// #nosec G103 G115 -- waitid requires its positive pid and siginfo buffer as uintptr syscall arguments.
		_, _, errno := syscall.Syscall6(
			syscall.SYS_WAITID,
			waitidTypePID,
			uintptr(pid),
			uintptr(unsafe.Pointer(&info[0])),
			waitidExited|waitidLeaveWait,
			0,
			0,
		)
		switch errno {
		case 0:
			return nil
		case syscall.EINTR:
			continue
		default:
			return errno
		}
	}
}
