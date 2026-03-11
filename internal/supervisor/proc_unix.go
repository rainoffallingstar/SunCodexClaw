//go:build !windows

package supervisor

import (
	"os/exec"
	"syscall"
)

func platformConfigureChildProcess(cmd *exec.Cmd) {
	// Create new process group so we can terminate the whole subtree if needed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func platformTerminateProcessTree(pid int, force bool) {
	if pid <= 0 {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	// Kill process group first (child processes), then the pid as fallback.
	_ = syscall.Kill(-pid, sig)
	_ = syscall.Kill(pid, sig)
}

func platformIsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
