//go:build plan9 || js || wasip1

package command

import (
	"os"
	"os/exec"
)

// unsupportedTermination provides best-effort single-process cancellation on
// platforms without Unix process groups. os/exec may still reject execution
// when the target platform has no process support.
type unsupportedTermination struct{}

func prepareCommandTermination(command *exec.Cmd) *unsupportedTermination {
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		return command.Process.Kill()
	}
	command.WaitDelay = processWaitDelay
	return &unsupportedTermination{}
}

func (t *unsupportedTermination) run(command *exec.Cmd) error {
	return command.Run()
}
