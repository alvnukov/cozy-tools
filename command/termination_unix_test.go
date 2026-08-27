//go:build darwin || linux

package command

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWaitProcessExitWithoutReapLeavesZombie proves the primitive the sound
// ordering rests on: after waitProcessExitWithoutReap returns, the leader is
// still an unreaped zombie, so its pid — and the group it leads — cannot
// have been handed to another process yet.
func TestWaitProcessExitWithoutReapLeavesZombie(t *testing.T) {
	command := exec.Command(shellBin(), shellArgs("exit 3")...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := command.Process.Pid

	if err := waitProcessExitWithoutReap(pid); err != nil {
		t.Fatalf("wait without reap: %v", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("signal 0 after no-reap wait: %v, want the zombie still holding the pid", err)
	}
	if err := command.Wait(); err == nil {
		t.Error("expected exit 3 to report a non-zero exit")
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Error("pid still answers signal 0 after Wait, want it reaped")
	}
}

// TestRunKillsGroupSurvivorsOfANormallyCompletedShell covers the case the
// post-Run kill used to handle by firing into a possibly recycled pgid: the
// shell exits, a background child survives, and run must kill that child
// while the shell is still an unreaped zombie.
func TestRunKillsGroupSurvivorsOfANormallyCompletedShell(t *testing.T) {
	command := exec.CommandContext(context.Background(), shellBin(), shellArgs("sleep 30 & echo $!")...)
	termination := prepareCommandTermination(command)
	output := &strings.Builder{}
	command.Stdout = output
	command.Stderr = output

	if err := termination.run(command); err != nil {
		t.Fatalf("run: %v", err)
	}
	survivor, err := strconv.Atoi(strings.TrimSpace(output.String()))
	if err != nil {
		t.Fatalf("parse survivor pid from %q: %v", output.String(), err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(survivor, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("survivor %d still alive after run returned", survivor)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunReportsTimeoutAfterCancellingTheGroup exercises the cancellation
// path end to end: the context fires mid-run, cancel kills the group while
// the leader is still alive, and run reports the deadline.
func TestRunReportsTimeoutAfterCancellingTheGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, shellBin(), shellArgs("sleep 30")...)
	termination := prepareCommandTermination(command)
	output := &strings.Builder{}
	command.Stdout = output
	command.Stderr = output

	err := termination.run(command)
	if err == nil {
		t.Fatal("expected the timed-out command to fail")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatalf("run failed before the context deadline: %v", err)
	}
}
