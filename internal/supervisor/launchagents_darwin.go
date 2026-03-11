//go:build darwin

package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Supervisor) plistDir() (string, error) {
	homeFn := s.userHomeDir
	if homeFn == nil {
		homeFn = os.UserHomeDir
	}
	home, err := homeFn()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("failed to resolve home dir")
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

func (s *Supervisor) plistPath(account string) (string, error) {
	dir, err := s.plistDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, s.launchctlLabel(account)+".plist"), nil
}

func xmlEscape(v string) string {
	v = strings.ReplaceAll(v, "&", "&amp;")
	v = strings.ReplaceAll(v, "<", "&lt;")
	v = strings.ReplaceAll(v, ">", "&gt;")
	return v
}

func (s *Supervisor) ensureLaunchctl() error {
	if strings.TrimSpace(s.launchctlPath) == "" {
		return fmt.Errorf("launchctl not available")
	}
	return nil
}

func (s *Supervisor) writePlist(account string, opts LaunchAgentOptions) (string, error) {
	plistPath, err := s.plistPath(account)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(s.opts.RuntimeDir, "logs"), 0o755); err != nil {
		return "", err
	}

	throttle := opts.ThrottleInterval
	if throttle <= 0 {
		throttle = 10
	}
	if throttle < 1 {
		throttle = 1
	}

	runMode := strings.TrimSpace(strings.ToLower(opts.RunMode))
	if runMode == "" {
		runMode = "node"
	}
	if runMode != "node" && runMode != "supervisor" {
		return "", fmt.Errorf("invalid run mode: %s (expected node|supervisor)", opts.RunMode)
	}

	pathValue := strings.TrimSpace(opts.PathValue)
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	if pathValue == "" {
		pathValue = "/usr/bin:/bin:/usr/sbin:/sbin"
	}

	homeFn := s.userHomeDir
	if homeFn == nil {
		homeFn = os.UserHomeDir
	}
	homeValue, _ := homeFn()

	codexHome := strings.TrimSpace(opts.CodexHome)
	if codexHome == "" {
		if v := os.Getenv("CODEX_HOME"); v != "" {
			codexHome = v
		} else {
			if homeValue != "" {
				codexHome = filepath.Join(homeValue, ".codex")
			}
		}
	}

	codexBin := strings.TrimSpace(opts.CodexBin)
	if codexBin == "" {
		if v := os.Getenv("CODEX_BIN"); v != "" {
			codexBin = v
		} else if p, err := exec.LookPath("codex"); err == nil {
			codexBin = p
		}
	}

	label := s.launchctlLabel(account)
	logPath := s.logFile(account)
	botScript := filepath.Join(s.opts.RepoRoot, s.opts.BotScriptRel)

	var programArgs string
	var extraEnv strings.Builder

	switch runMode {
	case "supervisor":
		daemon := strings.TrimSpace(opts.DaemonBin)
		if daemon == "" {
			daemon = filepath.Join(s.opts.RepoRoot, "bin", "suncodexclawd")
		}
		if fi, err := os.Stat(daemon); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			return "", fmt.Errorf("suncodexclawd not executable: %s (build first: bash tools/build_go_bins.sh)", daemon)
		}
		cmd := fmt.Sprintf("cd %s && exec %s start %s --no-launchctl",
			xmlEscape(s.opts.RepoRoot),
			xmlEscape(daemon),
			xmlEscape(account),
		)
		programArgs = fmt.Sprintf(`  <key>ProgramArguments</key>
  <array>
    <string>/bin/zsh</string>
    <string>-lc</string>
    <string>%s</string>
  </array>`, xmlEscape(cmd))
		extraEnv.WriteString("    <key>SUNCODEXCLAW_DISABLE_LAUNCHCTL</key>\n    <string>true</string>\n")
	default:
		nodeBin := strings.TrimSpace(s.opts.NodeBin)
		if nodeBin == "" {
			nodeBin = "node"
		}
		programArgs = fmt.Sprintf(`  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>%s</string>
    <string>--account</string>
    <string>%s</string>
  </array>`, xmlEscape(nodeBin), xmlEscape(botScript), xmlEscape(account))
	}

	keepAliveXML := ""
	if opts.KeepAlive {
		keepAliveXML = `  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>`
	} else {
		keepAliveXML = `  <key>KeepAlive</key>
  <false/>`
	}

	if codexHome != "" {
		extraEnv.WriteString("    <key>CODEX_HOME</key>\n    <string>" + xmlEscape(codexHome) + "</string>\n")
	}
	if codexBin != "" {
		extraEnv.WriteString("    <key>FEISHU_CODEX_BIN</key>\n    <string>" + xmlEscape(codexBin) + "</string>\n")
	}

	homeEnv := strings.TrimSpace(os.Getenv("HOME"))
	if strings.TrimSpace(homeValue) != "" {
		homeEnv = homeValue
	}

	out := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
%s
  <key>WorkingDirectory</key>
  <string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>%s</string>
    <key>HOME</key>
    <string>%s</string>
%s  </dict>
  <key>RunAtLoad</key>
  <true/>
%s
  <key>ThrottleInterval</key>
  <integer>%d</integer>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, xmlEscape(label), programArgs, xmlEscape(s.opts.RepoRoot), xmlEscape(pathValue), xmlEscape(homeEnv), extraEnv.String(), keepAliveXML, throttle, xmlEscape(logPath), xmlEscape(logPath))

	if err := os.WriteFile(plistPath, []byte(out+"\n"), 0o644); err != nil {
		return "", err
	}
	_ = exec.Command("plutil", "-lint", plistPath).Run()
	return plistPath, nil
}

func (s *Supervisor) bootoutLabel(label string) {
	_ = exec.Command(s.launchctlPath, "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
	_ = exec.Command(s.launchctlPath, "remove", label).Run()
}

func (s *Supervisor) waitAbsent(label string) {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	for i := 0; i < 5; i++ {
		if exec.Command(s.launchctlPath, "print", target).Run() != nil {
			return
		}
		time.Sleep(1 * time.Second)
	}
}

func (s *Supervisor) bootstrapWithRetry(plistPath string) error {
	target := fmt.Sprintf("gui/%d", os.Getuid())
	var lastOut []byte
	for i := 0; i < 5; i++ {
		cmd := exec.Command(s.launchctlPath, "bootstrap", target, plistPath)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastOut = out
		time.Sleep(1 * time.Second)
	}
	msg := strings.TrimSpace(string(lastOut))
	if msg == "" {
		msg = "bootstrap failed"
	}
	return fmt.Errorf("%s", msg)
}

func (s *Supervisor) InstallLaunchAgents(accounts []string, opts LaunchAgentOptions) ([]string, error) {
	if err := s.ensureLaunchctl(); err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		var err error
		accounts, err = s.DiscoverAccounts()
		if err != nil {
			return nil, err
		}
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts found")
	}

	lines := []string{}
	for _, a := range accounts {
		plistPath, err := s.writePlist(a, opts)
		if err != nil {
			lines = append(lines, fmt.Sprintf("[error] %s", err.Error()))
			continue
		}
		label := s.launchctlLabel(a)
		s.bootoutLabel(label)
		s.waitAbsent(label)
		if err := s.bootstrapWithRetry(plistPath); err != nil {
			lines = append(lines, fmt.Sprintf("[error] failed to bootstrap %s: %s", a, err.Error()))
			continue
		}
		_ = exec.Command(s.launchctlPath, "enable", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
		_ = exec.Command(s.launchctlPath, "kickstart", "-k", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
		lines = append(lines, fmt.Sprintf("[ok] installed %s plist=%s", a, plistPath))
	}
	return lines, nil
}

func (s *Supervisor) UninstallLaunchAgents(accounts []string) ([]string, error) {
	if err := s.ensureLaunchctl(); err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		var err error
		accounts, err = s.DiscoverAccounts()
		if err != nil {
			return nil, err
		}
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts found")
	}
	lines := []string{}
	for _, a := range accounts {
		plistPath, _ := s.plistPath(a)
		label := s.launchctlLabel(a)
		s.bootoutLabel(label)
		if plistPath != "" {
			_ = os.Remove(plistPath)
		}
		lines = append(lines, fmt.Sprintf("[ok] uninstalled %s plist=%s", a, plistPath))
	}
	return lines, nil
}

func (s *Supervisor) StatusLaunchAgents(accounts []string, opts LaunchAgentOptions) ([]string, error) {
	if err := s.ensureLaunchctl(); err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		var err error
		accounts, err = s.DiscoverAccounts()
		if err != nil {
			return nil, err
		}
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts found")
	}

	// Stable ordering
	sort.Strings(accounts)

	lines := []string{}
	for _, a := range accounts {
		label := s.launchctlLabel(a)
		plistPath, _ := s.plistPath(a)
		mode := "unknown"
		if plistPath != "" {
			b, _ := os.ReadFile(plistPath)
			txt := string(b)
			if strings.Contains(txt, "suncodexclawd") {
				mode = "supervisor"
			} else if strings.Contains(txt, "feishu_ws_bot.js") {
				mode = "node"
			}
		}

		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
		if exec.Command(s.launchctlPath, "print", target).Run() == nil {
			lines = append(lines, fmt.Sprintf("[loaded] %s mode=%s plist=%s", a, mode, plistPath))
			continue
		}
		if plistPath != "" {
			if _, err := os.Stat(plistPath); err == nil {
				lines = append(lines, fmt.Sprintf("[file-only] %s mode=%s plist=%s", a, mode, plistPath))
				continue
			}
		}
		lines = append(lines, fmt.Sprintf("[missing] %s mode=%s plist=%s", a, mode, plistPath))
	}
	return lines, nil
}

func getenvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
