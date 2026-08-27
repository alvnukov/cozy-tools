//go:build linux

package command

// waitid arguments from the kernel's include/uapi/linux/wait.h; stdlib
// syscall does not name them because it does not wrap waitid.
const (
	waitidTypePID   = 1          // idtype_t P_PID
	waitidExited    = 0x00000004 // WEXITED
	waitidLeaveWait = 0x01000000 // WNOWAIT: report the exit without reaping
)
