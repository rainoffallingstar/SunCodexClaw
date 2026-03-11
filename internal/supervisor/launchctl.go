package supervisor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reLaunchctlPID       = regexp.MustCompile(`"PID"\s*=\s*([0-9]+)\s*;`)
	reLaunchctlLastExit  = regexp.MustCompile(`"LastExitStatus"\s*=\s*(-?[0-9]+)\s*;`)
	reLaunchctlServiceID = regexp.MustCompile(`\bPID\s*=\s*([0-9]+)\b`)
)

func (s *Supervisor) launchctlLabel(account string) string {
	prefix := strings.TrimSpace(s.opts.LaunchctlPrefix)
	if prefix == "" {
		prefix = "com.sunbelife.suncodexclaw.feishu"
	}
	return prefix + "." + strings.TrimSpace(account)
}

func parseLaunchctlPID(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if m := reLaunchctlPID.FindStringSubmatch(raw); len(m) == 2 {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 {
			return n, true
		}
	}
	// Fallback for older/plain formats (best-effort).
	if m := reLaunchctlServiceID.FindStringSubmatch(raw); len(m) == 2 {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 {
			return n, true
		}
	}
	return 0, false
}

func parseLaunchctlLastExit(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if m := reLaunchctlLastExit.FindStringSubmatch(raw); len(m) == 2 {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func (s *Supervisor) launchctlListRaw(account string) string {
	if strings.TrimSpace(s.launchctlPath) == "" {
		return ""
	}
	label := s.launchctlLabel(account)
	out, err := exec.Command(s.launchctlPath, "list", label).CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

func (s *Supervisor) launchAgentPlistPath(account string) string {
	homeFn := s.userHomeDir
	if homeFn == nil {
		homeFn = os.UserHomeDir
	}
	home, err := homeFn()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	label := s.launchctlLabel(account)
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func (s *Supervisor) launchAgentPlistExists(account string) bool {
	p := s.launchAgentPlistPath(account)
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// For tests.
func (s *Supervisor) launchAgentPlistPathForTest(account, home string) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	label := s.launchctlLabel(account)
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func (s *Supervisor) launchctlServiceLoaded(account string) bool {
	if strings.TrimSpace(s.launchctlPath) == "" {
		return false
	}
	label := s.launchctlLabel(account)
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	return exec.Command(s.launchctlPath, "print", target).Run() == nil
}

func launchAgentState(s *Supervisor, account string) string {
	if s == nil {
		return ""
	}
	if !s.launchAgentPlistExists(account) {
		return ""
	}
	if s.launchctlServiceLoaded(account) {
		return "loaded"
	}
	return "file-only"
}

func (s *Supervisor) launchctlBootstrapPlist(plistPath string) error {
	if strings.TrimSpace(s.launchctlPath) == "" {
		return fmt.Errorf("launchctl not available")
	}
	target := fmt.Sprintf("gui/%d", os.Getuid())
	return exec.Command(s.launchctlPath, "bootstrap", target, plistPath).Run()
}

func (s *Supervisor) launchctlEnable(account string) {
	if strings.TrimSpace(s.launchctlPath) == "" {
		return
	}
	label := s.launchctlLabel(account)
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	_ = exec.Command(s.launchctlPath, "enable", target).Run()
}

func (s *Supervisor) launchctlKickstart(account string) {
	if strings.TrimSpace(s.launchctlPath) == "" {
		return
	}
	label := s.launchctlLabel(account)
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	_ = exec.Command(s.launchctlPath, "kickstart", "-k", target).Run()
}

func (s *Supervisor) launchctlBootout(account string) {
	if strings.TrimSpace(s.launchctlPath) == "" {
		return
	}
	label := s.launchctlLabel(account)
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	_ = exec.Command(s.launchctlPath, "bootout", target).Run()
}

func (s *Supervisor) launchctlJobExists(account string) bool {
	if strings.TrimSpace(s.launchctlPath) == "" {
		return false
	}
	label := s.launchctlLabel(account)
	// LaunchAgents use the gui/UID domain; submit/remove jobs are still list-able by label.
	if s.launchctlServiceLoaded(account) {
		return true
	}
	return exec.Command(s.launchctlPath, "list", label).Run() == nil
}

func (s *Supervisor) launchctlRunningPID(account string) (int, bool) {
	raw := s.launchctlListRaw(account)
	if raw == "" {
		return 0, false
	}
	pid, ok := parseLaunchctlPID(raw)
	if !ok || pid <= 0 {
		return 0, false
	}
	if s.isBotPIDForAccount(pid, account) {
		return pid, true
	}
	return 0, false
}

func (s *Supervisor) launchctlLastExitStatus(account string) *int {
	raw := s.launchctlListRaw(account)
	if raw == "" {
		return nil
	}
	n, ok := parseLaunchctlLastExit(raw)
	if !ok {
		return nil
	}
	return &n
}

func (s *Supervisor) isBotPIDForAccount(pid int, account string) bool {
	if pid <= 0 || strings.TrimSpace(account) == "" {
		return false
	}
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "command=").CombinedOutput()
	if err != nil {
		return false
	}
	cmdline := string(bytes.TrimSpace(out))
	needle := "feishu_ws_bot.js --account " + account
	return strings.Contains(cmdline, needle)
}

func shellEscapeSingleQuotes(s string) string {
	// Wrap in single quotes; escape embedded single quotes for POSIX shells.
	// Example: abc'd -> 'abc'\''d'
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (s *Supervisor) startOneLaunchctl(account string) (string, error) {
	if !s.UsingLaunchctl() {
		return "", fmt.Errorf("launchctl not available")
	}
	if ok, err := s.configExistsForAccount(account); err != nil {
		return "", err
	} else if !ok {
		return "", fmt.Errorf("missing config for %s: %s (and no local.yaml entry)", account, filepath.Join(s.opts.ConfigDir, account+".json"))
	}

	if pid, ok := s.launchctlRunningPID(account); ok {
		_ = os.Remove(s.pidFile(account))
		return fmt.Sprintf("[skip] %s already running (pid=%d, manager=launchctl)", account, pid), nil
	}

	label := s.launchctlLabel(account)
	logf := s.logFile(account)
	_ = s.appendLog(account, fmt.Sprintf("[%s] starting account=%s manager=launchctl\n", time.Now().Format("2006-01-02 15:04:05"), account))

	// If a LaunchAgent plist exists, prefer the LaunchAgents flow so we don't clobber it with `launchctl submit`.
	if s.launchAgentPlistExists(account) {
		plistPath := s.launchAgentPlistPath(account)
		// Best-effort: unload then re-bootstrap (mirrors tools/install_feishu_launchagents.sh).
		s.launchctlBootout(account)
		_ = exec.Command(s.launchctlPath, "remove", label).Run()

		// Retry bootstrap a few times (launchctl sometimes races).
		var lastErr error
		for i := 0; i < 5; i++ {
			if err := s.launchctlBootstrapPlist(plistPath); err == nil {
				lastErr = nil
				break
			} else {
				lastErr = err
			}
			time.Sleep(1 * time.Second)
		}
		if lastErr != nil {
			return "", fmt.Errorf("failed to bootstrap launch agent for %s plist=%s: %w", account, plistPath, lastErr)
		}
		s.launchctlEnable(account)
		s.launchctlKickstart(account)
	} else {
		// Remove existing submit job if any.
		_ = exec.Command(s.launchctlPath, "remove", label).Run()

		script := filepath.Join(s.opts.RepoRoot, s.opts.BotScriptRel)
		// Keep the command close to the shell ctl version for behavior parity.
		cmdString := "export PATH=" + shellEscapeSingleQuotes(getenvDefault("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")) +
			"; cd " + shellEscapeSingleQuotes(s.opts.RepoRoot) +
			"; exec " + shellEscapeSingleQuotes(s.opts.NodeBin) +
			" " + shellEscapeSingleQuotes(script) +
			" --account " + shellEscapeSingleQuotes(account) +
			" >> " + shellEscapeSingleQuotes(logf) + " 2>&1"

		if err := exec.Command(s.launchctlPath, "submit", "-l", label, "--", "/bin/zsh", "-lc", cmdString).Run(); err != nil {
			return "", fmt.Errorf("failed to submit launchctl job for %s: %w", account, err)
		}
	}

	time.Sleep(1 * time.Second)

	pid, ok := s.launchctlRunningPID(account)
	if ok {
		_ = os.Remove(s.pidFile(account))
		return fmt.Sprintf("[ok] started %s (pid=%d, manager=launchctl) log=%s", account, pid, logf), nil
	}

	// ctl-like failure: show recent log tail in error message.
	tail, _ := tailFile(logf, 80)
	if s.launchAgentPlistExists(account) {
		s.launchctlBootout(account)
	}
	_ = exec.Command(s.launchctlPath, "remove", label).Run()
	_ = os.Remove(s.pidFile(account))
	return "", fmt.Errorf("failed to start %s via launchctl; recent log:\n%s", account, strings.TrimSpace(tail))
}

func (s *Supervisor) stopOneLaunchctl(account string) (string, error) {
	if !s.UsingLaunchctl() {
		return "", fmt.Errorf("launchctl not available")
	}
	if !s.launchctlJobExists(account) {
		return "", fmt.Errorf("no launchctl job for %s", account)
	}

	label := s.launchctlLabel(account)
	pid := 0
	if p, ok := parseLaunchctlPID(s.launchctlListRaw(account)); ok {
		pid = p
	}

	if s.launchAgentPlistExists(account) {
		s.launchctlBootout(account)
		_ = exec.Command(s.launchctlPath, "remove", label).Run()
	} else {
		_ = exec.Command(s.launchctlPath, "remove", label).Run()
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pid <= 0 {
			break
		}
		if !isRunning(pid) {
			_ = os.Remove(s.pidFile(account))
			return fmt.Sprintf("[ok] stopped %s (pid=%d, manager=launchctl)", account, pid), nil
		}
		time.Sleep(250 * time.Millisecond)
	}

	if pid > 0 && isRunning(pid) {
		_ = exec.Command("kill", "-9", fmt.Sprintf("%d", pid)).Run()
	}
	_ = os.Remove(s.pidFile(account))
	if pid > 0 {
		return fmt.Sprintf("[ok] force-stopped %s (pid=%d, manager=launchctl)", account, pid), nil
	}
	return fmt.Sprintf("[ok] stopped %s (pid=none, manager=launchctl)", account), nil
}
