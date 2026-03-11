package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"suncodexclaw/internal/configstore"
)

type LogFileNotFoundError struct {
	Path string
}

func (e *LogFileNotFoundError) Error() string {
	return fmt.Sprintf("log file not found: %s", e.Path)
}

type AccountRunError struct {
	Account string
	Err     error
}

func (e *AccountRunError) Error() string {
	if e == nil {
		return "account run error"
	}
	if e.Err == nil {
		return fmt.Sprintf("account=%s error", e.Account)
	}
	return fmt.Sprintf("account=%s error=%s", e.Account, e.Err.Error())
}

type Options struct {
	RepoRoot     string
	ConfigDir    string // config/feishu
	RuntimeDir   string // .runtime/feishu
	NodeBin      string // node
	BotScriptRel string // tools/feishu_ws_bot.js
	// macOS launchctl (when available)
	LaunchctlPrefix  string // label prefix; default com.sunbelife.suncodexclaw.feishu
	DisableLaunchctl bool   // force pidfile/manual mode on macOS
	// Restart policy
	AutoRestart    bool
	MaxRestarts    int
	RestartWindow  time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type Supervisor struct {
	opts          Options
	launchctlPath string
	userHomeDir   func() (string, error)
}

type StatusInfo struct {
	Account     string
	State       string // running|stopped
	PID         int
	Manager     string // pidfile|manual
	StalePID    int
	LastExit    *int
	LaunchAgent string // file-only|loaded
	LastError   string
	LogPath     string
}

func New(opts Options) *Supervisor {
	if strings.TrimSpace(opts.NodeBin) == "" {
		opts.NodeBin = "node"
	}
	if strings.TrimSpace(opts.BotScriptRel) == "" {
		opts.BotScriptRel = filepath.Join("tools", "feishu_ws_bot.js")
	}
	if strings.TrimSpace(opts.LaunchctlPrefix) == "" {
		opts.LaunchctlPrefix = getenvDefault("SUNCODEXCLAW_LAUNCHCTL_PREFIX", "com.sunbelife.suncodexclaw.feishu")
	}
	if !opts.DisableLaunchctl {
		opts.DisableLaunchctl = getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false)
	}
	if strings.TrimSpace(opts.ConfigDir) == "" {
		opts.ConfigDir = filepath.Join(opts.RepoRoot, "config", "feishu")
	}
	if strings.TrimSpace(opts.RuntimeDir) == "" {
		opts.RuntimeDir = filepath.Join(opts.RepoRoot, ".runtime", "feishu")
	}
	if opts.AutoRestart == false {
		// keep as-is (explicitly disabled)
	} else {
		opts.AutoRestart = true
	}
	if opts.MaxRestarts <= 0 {
		opts.MaxRestarts = 20
	}
	if opts.RestartWindow <= 0 {
		opts.RestartWindow = 10 * time.Minute
	}
	if opts.InitialBackoff <= 0 {
		opts.InitialBackoff = 250 * time.Millisecond
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 10 * time.Second
	}

	launchctlPath := ""
	if runtime.GOOS == "darwin" {
		if p, err := exec.LookPath("launchctl"); err == nil {
			launchctlPath = p
		}
	}

	return &Supervisor{opts: opts, launchctlPath: launchctlPath, userHomeDir: os.UserHomeDir}
}

func (s *Supervisor) pidDir() string { return filepath.Join(s.opts.RuntimeDir, "pids") }
func (s *Supervisor) logDir() string { return filepath.Join(s.opts.RuntimeDir, "logs") }
func (s *Supervisor) errDir() string { return filepath.Join(s.opts.RuntimeDir, "errors") }

func (s *Supervisor) pidFile(account string) string { return filepath.Join(s.pidDir(), account+".pid") }
func (s *Supervisor) logFile(account string) string { return filepath.Join(s.logDir(), account+".log") }
func (s *Supervisor) errFile(account string) string { return filepath.Join(s.errDir(), account+".err") }

func (s *Supervisor) UsingLaunchctl() bool {
	if s.opts.DisableLaunchctl {
		return false
	}
	return runtime.GOOS == "darwin" && strings.TrimSpace(s.launchctlPath) != ""
}

func (s *Supervisor) DiscoverAccounts() ([]string, error) {
	names := map[string]bool{}

	// config/feishu/*.json
	entries, _ := os.ReadDir(s.opts.ConfigDir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		base := strings.TrimSuffix(n, ".json")
		if base == "default" || strings.HasSuffix(base, ".example") {
			continue
		}
		names[base] = true
	}

	// local.yaml config.feishu.* and legacy values.feishu.*
	store := configstore.NewStore(s.opts.RepoRoot)
	secretNames, err := store.ListSecretsEntryNames("feishu")
	if err == nil {
		for _, n := range secretNames {
			if n == "default" || strings.HasSuffix(n, ".example") {
				continue
			}
			names[n] = true
		}
	}

	out := []string{}
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Supervisor) StartDetached(accounts []string) ([]string, error) {
	if !s.UsingLaunchctl() {
		return nil, fmt.Errorf("detached start is only supported on macOS with launchctl")
	}
	if len(accounts) == 0 {
		var err error
		accounts, err = s.DiscoverAccounts()
		if err != nil {
			return nil, err
		}
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts found under %s or local.yaml", s.opts.ConfigDir)
	}
	if err := os.MkdirAll(s.pidDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.logDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.errDir(), 0o755); err != nil {
		return nil, err
	}

	lines := []string{}
	for _, a := range accounts {
		ln, err := s.startOneLaunchctl(a)
		if err != nil {
			lines = append(lines, fmt.Sprintf("[error] %s", err.Error()))
			continue
		}
		lines = append(lines, ln)
	}
	return lines, nil
}

func (s *Supervisor) StartAll(ctx context.Context, accounts []string) error {
	if len(accounts) == 0 {
		var err error
		accounts, err = s.DiscoverAccounts()
		if err != nil {
			return err
		}
	}
	if len(accounts) == 0 {
		return fmt.Errorf("no accounts found under %s or local.yaml", s.opts.ConfigDir)
	}

	if err := os.MkdirAll(s.pidDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.logDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(s.errDir(), 0o755); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, len(accounts))

	for _, account := range accounts {
		acct := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.runWithRestart(ctx, acct); err != nil && ctx.Err() == nil {
				// Do not cancel the whole supervisor: keep other accounts running.
				errCh <- err
			}
		}()
	}

	// Wait for cancel.
	<-ctx.Done()
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func (s *Supervisor) StartReport(accounts []string) ([]string, error) {
	if len(accounts) == 0 {
		var err error
		accounts, err = s.DiscoverAccounts()
		if err != nil {
			return nil, err
		}
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts found under %s or local.yaml", s.opts.ConfigDir)
	}
	if err := os.MkdirAll(s.pidDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.logDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.errDir(), 0o755); err != nil {
		return nil, err
	}

	lines := []string{}
	for _, a := range accounts {
		if ok, err := s.configExistsForAccount(a); err != nil {
			return nil, err
		} else if !ok {
			lines = append(lines, fmt.Sprintf("[error] missing config for %s: %s (and no local.yaml entry)", a, filepath.Join(s.opts.ConfigDir, a+".json")))
			continue
		}

		if s.UsingLaunchctl() {
			if pid, ok := s.launchctlRunningPID(a); ok && isRunning(pid) {
				_ = os.Remove(s.pidFile(a))
				lines = append(lines, fmt.Sprintf("[skip] %s already running (pid=%d, manager=launchctl)", a, pid))
				continue
			}
		}

		if pid, _ := s.readPID(a); pid > 0 && isRunning(pid) {
			lines = append(lines, fmt.Sprintf("[skip] %s already running (pid=%d, manager=pidfile)", a, pid))
			continue
		}
		if pid, _ := s.findRunningBotPID(a); pid > 0 && isRunning(pid) {
			_ = os.WriteFile(s.pidFile(a), []byte(fmt.Sprintf("%d\n", pid)), 0o644)
			lines = append(lines, fmt.Sprintf("[skip] %s already running (pid=%d, manager=manual); pid file adopted", a, pid))
			continue
		}
		// Preflight: run bot dry-run to surface config issues early (ctl-like).
		if err := s.preflightAccount(a); err != nil {
			lines = append(lines, fmt.Sprintf("[error] %s preflight failed: %s", a, err.Error()))
			continue
		}
		_ = os.Remove(s.errFile(a))
		lines = append(lines, fmt.Sprintf("[ok] starting %s (manager=supervisor) log=%s", a, s.logFile(a)))
	}
	return lines, nil
}

func (s *Supervisor) configExistsForAccount(account string) (bool, error) {
	if _, err := os.Stat(filepath.Join(s.opts.ConfigDir, account+".json")); err == nil {
		return true, nil
	}
	store := configstore.NewStore(s.opts.RepoRoot)
	entry, err := store.ReadSecretsEntry("feishu", account)
	if err != nil {
		return false, err
	}
	return len(entry) > 0, nil
}

func (s *Supervisor) preflightAccount(account string) error {
	script := filepath.Join(s.opts.RepoRoot, s.opts.BotScriptRel)
	cmd := exec.Command(s.opts.NodeBin, script, "--account", account, "--dry-run")
	cmd.Dir = s.opts.RepoRoot
	cmd.Env = os.Environ()
	// Keep output small; errors bubble up.
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		// Truncate to keep start report readable.
		if len(msg) > 1200 {
			msg = msg[len(msg)-1200:]
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (s *Supervisor) StatusInfos(accounts []string) ([]StatusInfo, error) {
	if len(accounts) == 0 {
		var err error
		accounts, err = s.DiscoverAccounts()
		if err != nil {
			return nil, err
		}
	}
	out := []StatusInfo{}
	for _, a := range accounts {
		if s.UsingLaunchctl() {
			if pid, ok := s.launchctlRunningPID(a); ok && isRunning(pid) {
				_ = os.Remove(s.pidFile(a))
				_ = os.Remove(s.errFile(a))
				out = append(out, StatusInfo{
					Account: a, State: "running", PID: pid, Manager: "launchctl", LaunchAgent: launchAgentState(s, a), LogPath: s.logFile(a),
				})
				continue
			}
			if s.launchctlJobExists(a) {
				_ = os.Remove(s.pidFile(a))
				lastExit := s.launchctlLastExitStatus(a)
				if msg, ok := s.readErr(a); ok {
					out = append(out, StatusInfo{
						Account: a, State: "stopped", PID: 0, Manager: "launchctl", LaunchAgent: launchAgentState(s, a), LastExit: lastExit, LastError: msg, LogPath: s.logFile(a),
					})
				} else {
					out = append(out, StatusInfo{
						Account: a, State: "stopped", PID: 0, Manager: "launchctl", LaunchAgent: launchAgentState(s, a), LastExit: lastExit, LogPath: s.logFile(a),
					})
				}
				continue
			}
			// LaunchAgent plist exists but service not loaded: mirror tools/install_feishu_launchagents.sh status "file-only".
			if s.launchAgentPlistExists(a) {
				_ = os.Remove(s.pidFile(a))
				lastExit := s.launchctlLastExitStatus(a)
				if msg, ok := s.readErr(a); ok {
					out = append(out, StatusInfo{
						Account: a, State: "stopped", PID: 0, Manager: "launchctl", LaunchAgent: "file-only", LastExit: lastExit, LastError: msg, LogPath: s.logFile(a),
					})
				} else {
					out = append(out, StatusInfo{
						Account: a, State: "stopped", PID: 0, Manager: "launchctl", LaunchAgent: "file-only", LastExit: lastExit, LogPath: s.logFile(a),
					})
				}
				continue
			}
		}

		pid, pidErr := s.readPID(a)
		manager := ""
		if pidErr == nil && pid > 0 {
			manager = "pidfile"
		}

		if pid > 0 && isRunning(pid) {
			_ = os.Remove(s.errFile(a))
			out = append(out, StatusInfo{
				Account: a, State: "running", PID: pid, Manager: manager, LogPath: s.logFile(a),
			})
			continue
		}

		// Fallback: find a manually started bot.
		if found, _ := s.findRunningBotPID(a); found > 0 {
			pid = found
			_ = os.WriteFile(s.pidFile(a), []byte(fmt.Sprintf("%d\n", pid)), 0o644)
			_ = os.Remove(s.errFile(a))
			out = append(out, StatusInfo{
				Account: a, State: "running", PID: pid, Manager: "manual", LogPath: s.logFile(a),
			})
			continue
		}

		if pid > 0 {
			_ = os.Remove(s.pidFile(a))
			if msg, ok := s.readErr(a); ok {
				out = append(out, StatusInfo{
					Account: a, State: "stopped", StalePID: pid, Manager: "pidfile", LastError: msg, LogPath: s.logFile(a),
				})
			} else {
				out = append(out, StatusInfo{
					Account: a, State: "stopped", StalePID: pid, Manager: "pidfile", LogPath: s.logFile(a),
				})
			}
		} else {
			if msg, ok := s.readErr(a); ok {
				out = append(out, StatusInfo{
					Account: a, State: "stopped", LastError: msg, LogPath: s.logFile(a),
				})
			} else {
				out = append(out, StatusInfo{
					Account: a, State: "stopped", LogPath: s.logFile(a),
				})
			}
		}
	}
	return out, nil
}

func (s *Supervisor) Status(accounts []string) ([]string, error) {
	infos, err := s.StatusInfos(accounts)
	if err != nil {
		return nil, err
	}
	lines := []string{}
	for _, it := range infos {
		if it.State == "running" {
			agent := ""
			if it.Manager == "launchctl" && strings.TrimSpace(it.LaunchAgent) != "" {
				agent = fmt.Sprintf(" launchagent=%s", it.LaunchAgent)
			}
			lines = append(lines, fmt.Sprintf("[running] %s pid=%d manager=%s%s log=%s", it.Account, it.PID, it.Manager, agent, it.LogPath))
			continue
		}
		if it.Manager == "launchctl" {
			lastExit := "unknown"
			if it.LastExit != nil {
				lastExit = fmt.Sprintf("%d", *it.LastExit)
			}
			agent := ""
			if strings.TrimSpace(it.LaunchAgent) != "" {
				agent = fmt.Sprintf(" launchagent=%s", it.LaunchAgent)
			}
			if it.LastError != "" {
				lines = append(lines, fmt.Sprintf("[stopped] %s pid=(none) manager=launchctl last_exit=%s%s last_error=%s log=%s", it.Account, lastExit, agent, it.LastError, it.LogPath))
			} else {
				lines = append(lines, fmt.Sprintf("[stopped] %s pid=(none) manager=launchctl last_exit=%s%s log=%s", it.Account, lastExit, agent, it.LogPath))
			}
			continue
		}
		if it.StalePID > 0 {
			if it.LastError != "" {
				lines = append(lines, fmt.Sprintf("[stopped] %s stale_pid=%d manager=%s last_error=%s log=%s", it.Account, it.StalePID, it.Manager, it.LastError, it.LogPath))
			} else {
				lines = append(lines, fmt.Sprintf("[stopped] %s stale_pid=%d manager=%s log=%s", it.Account, it.StalePID, it.Manager, it.LogPath))
			}
			continue
		}
		if it.LastError != "" {
			lines = append(lines, fmt.Sprintf("[stopped] %s pid=(none) last_error=%s log=%s", it.Account, it.LastError, it.LogPath))
		} else {
			lines = append(lines, fmt.Sprintf("[stopped] %s pid=(none) log=%s", it.Account, it.LogPath))
		}
	}
	return lines, nil
}

func (s *Supervisor) Stop(accounts []string) ([]string, error) {
	if len(accounts) == 0 {
		var err error
		accounts, err = s.DiscoverAccounts()
		if err != nil {
			return nil, err
		}
	}
	lines := []string{}
	for _, a := range accounts {
		if s.UsingLaunchctl() {
			if s.launchctlJobExists(a) {
				ln, err := s.stopOneLaunchctl(a)
				if err != nil {
					lines = append(lines, fmt.Sprintf("[error] %s", err.Error()))
				} else {
					lines = append(lines, ln)
				}
				continue
			}
		}

		pid, pidErr := s.readPID(a)
		hasPidFile := pidErr == nil && pid > 0
		manager := ""
		if hasPidFile {
			manager = "pidfile"
		}
		if pid <= 0 {
			if found, _ := s.findRunningBotPID(a); found > 0 {
				pid = found
				_ = os.WriteFile(s.pidFile(a), []byte(fmt.Sprintf("%d\n", pid)), 0o644)
				manager = "manual"
			}
		}
		if pid <= 0 || !isRunning(pid) {
			_ = os.Remove(s.pidFile(a))
			if hasPidFile {
				lines = append(lines, fmt.Sprintf("[skip] %s stale pid file removed", a))
			} else {
				lines = append(lines, fmt.Sprintf("[skip] %s not running", a))
			}
			continue
		}
		// Kill process group first (child processes), then the pid as fallback.
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		_ = syscall.Kill(pid, syscall.SIGTERM)
		for i := 0; i < 40; i++ {
			if !isRunning(pid) {
				_ = os.Remove(s.pidFile(a))
				lines = append(lines, fmt.Sprintf("[ok] stopped %s (pid=%d, manager=%s)", a, pid, manager))
				goto next
			}
			time.Sleep(250 * time.Millisecond)
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_ = os.Remove(s.pidFile(a))
		lines = append(lines, fmt.Sprintf("[ok] force-stopped %s (pid=%d, manager=%s)", a, pid, manager))
	next:
	}
	return lines, nil
}

func getenvDefault(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func getenvBool(key string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func (s *Supervisor) Restart(ctx context.Context, accounts []string) error {
	_, err := s.Stop(accounts)
	if err != nil {
		return err
	}
	return s.StartAll(ctx, accounts)
}

func (s *Supervisor) Logs(account string, follow bool, lines int) error {
	if lines <= 0 {
		lines = 120
	}
	account = strings.TrimSpace(account)
	if account != "" && account != "all" {
		p := s.logFile(account)
		if !follow {
			out, err := tailFile(p, lines)
			if err != nil {
				if os.IsNotExist(err) {
					return &LogFileNotFoundError{Path: p}
				}
				return err
			}
			_, _ = io.WriteString(os.Stdout, out)
			return nil
		}
		if err := followFile(p, lines); err != nil {
			if os.IsNotExist(err) {
				return &LogFileNotFoundError{Path: p}
			}
			return err
		}
		return nil
	}
	accounts, err := s.DiscoverAccounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return fmt.Errorf("no accounts found")
	}
	paths := []string{}
	for _, a := range accounts {
		paths = append(paths, s.logFile(a))
	}
	if !follow {
		missing := 0
		for i, a := range accounts {
			p := paths[i]
			out, err := tailFile(p, lines)
			if err != nil {
				_, _ = io.WriteString(os.Stdout, fmt.Sprintf("===== %s =====\n", a))
				_, _ = io.WriteString(os.Stdout, fmt.Sprintf("[error] log file not found: %s\n", p))
				missing++
				continue
			}
			_, _ = io.WriteString(os.Stdout, fmt.Sprintf("===== %s =====\n", a))
			_, _ = io.WriteString(os.Stdout, out)
		}
		if missing > 0 {
			return fmt.Errorf("one or more log files missing (%d)", missing)
		}
		return nil
	}
	return followFiles(paths, lines)
}

func (s *Supervisor) LogsSelected(accounts []string, follow bool, lines int) error {
	if len(accounts) == 0 {
		return fmt.Errorf("no accounts specified")
	}
	if lines <= 0 {
		lines = 120
	}
	paths := []string{}
	for _, a := range accounts {
		paths = append(paths, s.logFile(a))
	}
	if !follow {
		missing := 0
		for i, a := range accounts {
			out, err := tailFile(paths[i], lines)
			if err != nil {
				_, _ = io.WriteString(os.Stdout, fmt.Sprintf("===== %s =====\n", a))
				_, _ = io.WriteString(os.Stdout, fmt.Sprintf("[error] log file not found: %s\n", paths[i]))
				missing++
				continue
			}
			_, _ = io.WriteString(os.Stdout, fmt.Sprintf("===== %s =====\n", a))
			_, _ = io.WriteString(os.Stdout, out)
		}
		if missing > 0 {
			return fmt.Errorf("one or more log files missing (%d)", missing)
		}
		return nil
	}
	return followFiles(paths, lines)
}

func (s *Supervisor) runWithRestart(ctx context.Context, account string) error {
	backoff := s.opts.InitialBackoff
	maxBackoff := s.opts.MaxBackoff
	restarts := []time.Time{}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if adopted, err := s.adoptExisting(ctx, account); err != nil {
			return err
		} else if adopted {
			// Existing process exited; restart with backoff.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		code, err := s.spawnOnce(ctx, account)
		if err != nil {
			if !s.opts.AutoRestart {
				return &AccountRunError{Account: account, Err: err}
			}

			now := time.Now()
			restarts = append(restarts, now)
			cutoff := now.Add(-s.opts.RestartWindow)
			j := 0
			for ; j < len(restarts); j++ {
				if restarts[j].After(cutoff) {
					break
				}
			}
			if j > 0 {
				restarts = restarts[j:]
			}
			if len(restarts) > s.opts.MaxRestarts {
				limErr := fmt.Errorf("restart limit exceeded restarts=%d window=%s", len(restarts), s.opts.RestartWindow)
				_ = s.writeErr(account, limErr.Error())
				_ = s.appendLog(account, fmt.Sprintf("[%s] %s account=%s\n", time.Now().Format("2006-01-02 15:04:05"), limErr.Error(), account))
				return nil
			}

			_ = s.appendLog(account, fmt.Sprintf("[%s] spawn error account=%s err=%s; retrying in %s\n", time.Now().Format("2006-01-02 15:04:05"), account, err.Error(), backoff))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		_ = os.Remove(s.errFile(account))
		// If context canceled, exit cleanly.
		if ctx.Err() != nil {
			return nil
		}
		if !s.opts.AutoRestart {
			_ = s.appendLog(account, fmt.Sprintf("[%s] exited account=%s code=%d; auto_restart=disabled\n", time.Now().Format("2006-01-02 15:04:05"), account, code))
			return nil
		}

		now := time.Now()
		restarts = append(restarts, now)
		cutoff := now.Add(-s.opts.RestartWindow)
		j := 0
		for ; j < len(restarts); j++ {
			if restarts[j].After(cutoff) {
				break
			}
		}
		if j > 0 {
			restarts = restarts[j:]
		}
		if len(restarts) > s.opts.MaxRestarts {
			limErr := fmt.Errorf("restart limit exceeded restarts=%d window=%s", len(restarts), s.opts.RestartWindow)
			_ = s.writeErr(account, limErr.Error())
			_ = s.appendLog(account, fmt.Sprintf("[%s] %s account=%s\n", time.Now().Format("2006-01-02 15:04:05"), limErr.Error(), account))
			return nil
		}
		// Restart loop.
		_ = s.appendLog(account, fmt.Sprintf("[%s] exited account=%s code=%d; restarting in %s\n", time.Now().Format("2006-01-02 15:04:05"), account, code, backoff))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (s *Supervisor) adoptExisting(ctx context.Context, account string) (bool, error) {
	pid, err := s.readPID(account)
	if err != nil || pid <= 0 {
		if found, _ := s.findRunningBotPID(account); found > 0 {
			pid = found
			_ = os.WriteFile(s.pidFile(account), []byte(fmt.Sprintf("%d\n", pid)), 0o644)
		} else {
			return false, nil
		}
	}
	if !isRunning(pid) {
		_ = os.Remove(s.pidFile(account))
		return false, nil
	}

	_ = s.appendLog(account, fmt.Sprintf("[%s] adopting existing pid=%d account=%s\n", time.Now().Format("2006-01-02 15:04:05"), pid, account))

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Best-effort stop: kill process group and pid.
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			_ = syscall.Kill(pid, syscall.SIGTERM)
			_ = os.Remove(s.pidFile(account))
			return true, nil
		case <-ticker.C:
			if !isRunning(pid) {
				_ = os.Remove(s.pidFile(account))
				_ = s.appendLog(account, fmt.Sprintf("[%s] adopted process exited pid=%d account=%s\n", time.Now().Format("2006-01-02 15:04:05"), pid, account))
				return true, nil
			}
		}
	}
}

func (s *Supervisor) findRunningBotPID(account string) (int, error) {
	// Try to detect a manually started bot process via ps output.
	// Match `feishu_ws_bot.js --account <account>` anywhere in the command line.
	scriptName := filepath.Base(s.opts.BotScriptRel)
	// Prefer matching the repo-local path to avoid false positives across repos.
	repoScript := filepath.Join(s.opts.RepoRoot, s.opts.BotScriptRel)
	repoScriptSlash := filepath.ToSlash(repoScript)
	repoRelSlash := filepath.ToSlash(s.opts.BotScriptRel)
	re := regexp.MustCompile(
		`(` +
			regexp.QuoteMeta(repoScriptSlash) + `|` +
			regexp.QuoteMeta(repoRelSlash) + `|` +
			regexp.QuoteMeta(scriptName) +
			`)` +
			`(\s|")*--account\s+` + regexp.QuoteMeta(account) + `(\s|$)`,
	)

	out, err := exec.Command("ps", "-eo", "pid=,args=").Output()
	if err != nil {
		return 0, err
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Format: "<pid> <args...>"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		cmdline := strings.Join(fields[1:], " ")
		if re.MatchString(cmdline) {
			return pid, nil
		}
	}
	return 0, nil
}

func (s *Supervisor) spawnOnce(ctx context.Context, account string) (int, error) {
	logf, err := os.OpenFile(s.logFile(account), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 1, err
	}
	defer logf.Close()
	prefix := fmt.Sprintf("[%s] starting account=%s\n", time.Now().Format("2006-01-02 15:04:05"), account)
	_, _ = io.WriteString(logf, prefix)

	script := filepath.Join(s.opts.RepoRoot, s.opts.BotScriptRel)
	cmd := exec.CommandContext(ctx, s.opts.NodeBin, script, "--account", account)
	cmd.Dir = s.opts.RepoRoot
	cmd.Env = os.Environ()
	// Create new process group so we can terminate the whole subtree if needed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Tee output to file and also to supervisor stdout with account prefix.
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return 1, err
	}

	// Ensure we terminate the whole process group on shutdown (CmdContext may not kill children).
	done := make(chan struct{})
	go func(pid int) {
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			_ = syscall.Kill(pid, syscall.SIGTERM)
		case <-done:
		}
	}(cmd.Process.Pid)

	if err := os.WriteFile(s.pidFile(account), []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
		close(done)
		return 1, err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pumpOutput(account, stdout, logf, os.Stdout)
	}()
	go func() {
		defer wg.Done()
		pumpOutput(account, stderr, logf, os.Stderr)
	}()

	err = cmd.Wait()
	close(done)
	wg.Wait()

	_ = os.Remove(s.pidFile(account))

	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if st, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				code = st.ExitStatus()
			} else {
				code = 1
			}
		} else if ctx.Err() != nil {
			return 0, nil
		} else {
			return 1, err
		}
	}
	return code, nil
}

func pumpOutput(account string, r io.Reader, logf *os.File, out io.Writer) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		ts := time.Now().Format("2006-01-02 15:04:05")
		msg := fmt.Sprintf("[%s] [%s] %s\n", ts, account, line)
		_, _ = logf.WriteString(msg)
		_, _ = io.WriteString(out, msg)
	}
}

func (s *Supervisor) readPID(account string) (int, error) {
	b, err := os.ReadFile(s.pidFile(account))
	if err != nil {
		return 0, err
	}
	var pid int
	_, _ = fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid)
	return pid, nil
}

func isRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func (s *Supervisor) appendLog(account, line string) error {
	f, err := os.OpenFile(s.logFile(account), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

func (s *Supervisor) writeErr(account, msg string) error {
	if err := os.MkdirAll(s.errDir(), 0o755); err != nil {
		return err
	}
	body := strings.TrimSpace(msg)
	if len(body) > 400 {
		body = body[len(body)-400:]
	}
	return os.WriteFile(s.errFile(account), []byte(body+"\n"), 0o644)
}

func (s *Supervisor) readErr(account string) (string, bool) {
	b, err := os.ReadFile(s.errFile(account))
	if err != nil {
		return "", false
	}
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		return "", false
	}
	// keep status output short
	if len(msg) > 120 {
		msg = msg[len(msg)-120:]
	}
	return msg, true
}

func tailFile(path string, n int) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(b), "\n")
	// drop trailing empty due to ending newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if n > len(lines) {
		n = len(lines)
	}
	if n < 0 {
		n = 0
	}
	return strings.Join(lines[len(lines)-n:], "\n") + "\n", nil
}

func followFile(path string, n int) error {
	// Wait for the log file to exist (common right after start).
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}

	// Print last N lines first (best-effort).
	if out, err := tailFile(path, n); err == nil {
		_, _ = io.WriteString(os.Stdout, out)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Seek to end for follow.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	buf := make([]byte, 32*1024)
	for {
		nr, er := f.Read(buf)
		if nr > 0 {
			_, _ = os.Stdout.Write(buf[:nr])
		}
		if er == io.EOF {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if er != nil {
			return er
		}
	}
}

func followFiles(paths []string, lines int) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(paths))

	for _, p := range paths {
		pathCopy := p
		label := strings.TrimSuffix(filepath.Base(pathCopy), filepath.Ext(pathCopy))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := followFileLinesWithPrefix(pathCopy, label, lines); err != nil {
				errCh <- err
			}
		}()
	}

	// Wait forever unless a goroutine errors unexpectedly.
	select {
	case err := <-errCh:
		return err
	}
}

func followFileLinesWithPrefix(path, label string, lines int) error {
	// Wait for file to exist.
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return &LogFileNotFoundError{Path: path}
		}
		return err
	}

	// Print tail first (ctl-like).
	if lines > 0 {
		if out, err := tailFile(path, lines); err == nil && strings.TrimSpace(out) != "" {
			_, _ = io.WriteString(os.Stdout, fmt.Sprintf("===== %s =====\n", label))
			_, _ = io.WriteString(os.Stdout, out)
		}
	}

	var f *os.File
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	open := func() error {
		if f != nil {
			_ = f.Close()
			f = nil
		}
		ff, err := os.Open(path)
		if err != nil {
			return err
		}
		_, _ = ff.Seek(0, io.SeekEnd)
		f = ff
		return nil
	}

	if err := open(); err != nil {
		// keep retrying
	}

	for {
		if f == nil {
			_ = open()
			time.Sleep(250 * time.Millisecond)
			continue
		}

		sc := bufio.NewScanner(f)
		for sc.Scan() {
			ts := time.Now().Format("2006-01-02 15:04:05")
			_, _ = io.WriteString(os.Stdout, fmt.Sprintf("[%s] [%s] %s\n", ts, label, sc.Text()))
		}
		if err := sc.Err(); err != nil {
			_ = open()
			time.Sleep(250 * time.Millisecond)
			continue
		}

		// EOF: wait and retry.
		time.Sleep(250 * time.Millisecond)
	}
}
