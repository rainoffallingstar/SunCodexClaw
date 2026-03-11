//go:build windows

package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func platformConfigureChildProcess(cmd *exec.Cmd) {
	// No process groups on Windows in the same sense; keep default.
}

func platformTerminateProcessTree(pid int, force bool) {
	if pid <= 0 {
		return
	}
	// Best-effort: taskkill can terminate the whole subtree.
	args := []string{"/T", "/PID", fmt.Sprintf("%d", pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	_ = exec.Command("taskkill", args...).Run()

	// Fallback: kill just the pid.
	if p, err := os.FindProcess(pid); err == nil && p != nil {
		_ = p.Kill()
	}
}

func platformIsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Use Windows process handle check.
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	// STILL_ACTIVE == 259
	return code == 259
}
