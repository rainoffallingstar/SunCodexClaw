package supervisor

import "os/exec"

// Platform-specific process helpers live in proc_*.go.

func configureChildProcess(cmd *exec.Cmd) {
	platformConfigureChildProcess(cmd)
}

func terminateProcessTree(pid int, force bool) {
	platformTerminateProcessTree(pid, force)
}

func isRunning(pid int) bool {
	return platformIsRunning(pid)
}
