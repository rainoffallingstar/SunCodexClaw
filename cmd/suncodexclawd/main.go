package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"suncodexclaw/internal/supervisor"
	"suncodexclaw/internal/wizard"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "start":
		start(os.Args[2:])
	case "status":
		status(os.Args[2:])
	case "stop":
		stop(os.Args[2:])
	case "restart":
		restart(os.Args[2:])
	case "list":
		list(os.Args[2:])
	case "logs":
		logs(os.Args[2:])
	case "preflight":
		preflight(os.Args[2:])
	case "launchagents":
		launchagents(os.Args[2:])
	case "configure":
		configure(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd start [account|all] [--account a] [--account b] [--node-bin node] [--no-launchctl] [--once] [--no-restart] [--max-restarts 20] [--restart-window 10m] [--strict-start] [--start-check-delay 1s]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd stop [account|all] [--account a] [--account b] [--no-launchctl]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd restart [account|all] [--account a] [--account b] [--no-launchctl] [--strict-start] [--start-check-delay 1s]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd status [account|all] [--account a] [--account b] [--no-launchctl]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd list")
	fmt.Fprintln(os.Stderr, "  suncodexclawd logs <account|all> [--account a] [--follow|-f] [--lines 120] [--no-launchctl]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd preflight [account|all] [--account a] [--account b] [--no-launchctl]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd launchagents <install|uninstall|status> [account|all] [--account a] [--account b] [--node-bin node] [--prefix com.sunbelife.suncodexclaw.feishu] [--run-mode node|supervisor] [--daemon-bin ./bin/suncodexclawd] [--codex-bin <path>] [--codex-home <path>] [--path <PATH>] [--keepalive] [--throttle-interval 10]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd configure [--account assistant] [--yes]")
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func baseFlags(name string) (*flag.FlagSet, *multiFlag, *string, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var accounts multiFlag
	fs.Var(&accounts, "account", "account name (repeatable); default is all discovered accounts")
	nodeBin := fs.String("node-bin", getenvDefault("NODE_BIN", "node"), "node binary")
	repo := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	return fs, &accounts, nodeBin, repo
}

func start(args []string) {
	args = normalizePositionalAccountArgs(args)
	fs, accounts, nodeBin, repoFlag := baseFlags("start")
	healthAddr := fs.String("health-addr", getenvDefault("SUNCODEXCLAW_HEALTH_ADDR", ""), "optional health server addr (e.g. :8080)")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl detached mode and run in foreground supervisor mode")
	noRestart := fs.Bool("no-restart", getenvBool("SUNCODEXCLAW_NO_RESTART", false), "disable auto-restart on crash")
	once := fs.Bool("once", false, "start accounts without auto-restart loop")
	strictStart := fs.Bool("strict-start", getenvBool("SUNCODEXCLAW_STRICT_START", false), "exit non-zero if any account fails to start")
	startCheckDelay := fs.Duration("start-check-delay", getenvDuration("SUNCODEXCLAW_START_CHECK_DELAY", 1*time.Second), "delay before checking status after start")
	maxRestarts := fs.Int("max-restarts", getenvInt("SUNCODEXCLAW_MAX_RESTARTS", 20), "max restarts within restart-window")
	restartWindow := fs.Duration("restart-window", getenvDuration("SUNCODEXCLAW_RESTART_WINDOW", 10*time.Minute), "restart window duration")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)

	autoRestart := !*noRestart && !*once
	sup := supervisor.New(supervisor.Options{
		RepoRoot:         repo,
		NodeBin:          *nodeBin,
		DisableLaunchctl: *noLaunchctl,
		AutoRestart:      autoRestart,
		MaxRestarts:      *maxRestarts,
		RestartWindow:    *restartWindow,
	})
	accts := normalizeAccountsOrAll(*accounts)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	if strings.TrimSpace(*healthAddr) != "" {
		go serveHealth(*healthAddr, sup, accts)
	}

	lines, err := sup.StartReport(accts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	hasError := false
	failedAccounts := []string{}
	for _, ln := range lines {
		fmt.Println(ln)
		if strings.HasPrefix(ln, "[error]") {
			hasError = true
			if acct := parseAccountFromErrorLine(ln); acct != "" {
				failedAccounts = append(failedAccounts, acct)
			}
		}
	}
	if hasError {
		// ctl-like: show recent logs for each account to help debug.
		for _, a := range normalizeForLogTail(accts, failedAccounts) {
			_ = sup.Logs(a, false, 80)
		}
		os.Exit(1)
	}

	// macOS: prefer detached launchctl jobs (parity with tools/feishu_bot_ctl.sh).
	if sup.UsingLaunchctl() {
		startLines, err := sup.StartDetached(accts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		for _, ln := range startLines {
			fmt.Println(ln)
		}

		time.Sleep(*startCheckDelay)
		infos, _ := sup.StatusInfos(accts)
		statusLines, _ := sup.Status(accts)
		for _, ln := range statusLines {
			fmt.Println(ln)
		}

		if *strictStart {
			failed := []string{}
			for _, it := range infos {
				if it.State == "stopped" {
					failed = append(failed, it.Account)
				}
			}
			if len(failed) > 0 {
				for _, a := range failed {
					fmt.Fprintf(os.Stderr, "[error] failed to start %s; recent log:\n", a)
					_ = sup.Logs(a, false, 80)
				}
				os.Exit(1)
			}
		}
		return
	}

	strictFailCh := make(chan []string, 1)

	// Print a quick status snapshot after start to match ctl ergonomics, optionally fail fast.
	go func() {
		time.Sleep(*startCheckDelay)
		infos, err := sup.StatusInfos(accts)
		if err != nil {
			return
		}
		statusLines, _ := sup.Status(accts)
		for _, ln := range statusLines {
			fmt.Println(ln)
		}
		if !*strictStart {
			return
		}
		failed := []string{}
		for _, it := range infos {
			if it.State == "stopped" {
				failed = append(failed, it.Account)
			}
		}
		if len(failed) == 0 {
			return
		}
		for _, a := range failed {
			fmt.Fprintf(os.Stderr, "[error] failed to start %s; recent log:\n", a)
			_ = sup.Logs(a, false, 80)
		}
		select {
		case strictFailCh <- failed:
		default:
		}
		cancel()
	}()

	if err := sup.StartAll(ctx, accts); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	select {
	case <-strictFailCh:
		os.Exit(1)
	default:
	}
}

func status(args []string) {
	args = normalizePositionalAccountArgs(args)
	fs, accounts, nodeBin, repoFlag := baseFlags("status")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl and use pidfile/manual detection only")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, DisableLaunchctl: *noLaunchctl})
	accts := normalizeAccountsOrAll(*accounts)
	lines, err := sup.Status(accts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, ln := range lines {
		fmt.Println(ln)
	}
}

func stop(args []string) {
	args = normalizePositionalAccountArgs(args)
	fs, accounts, nodeBin, repoFlag := baseFlags("stop")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl and stop only pidfile/manual processes")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, DisableLaunchctl: *noLaunchctl})
	accts := normalizeAccountsOrAll(*accounts)
	lines, err := sup.Stop(accts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, ln := range lines {
		fmt.Println(ln)
	}
}

func restart(args []string) {
	args = normalizePositionalAccountArgs(args)
	fs, accounts, nodeBin, repoFlag := baseFlags("restart")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl detached mode and run in foreground supervisor mode")
	strictStart := fs.Bool("strict-start", getenvBool("SUNCODEXCLAW_STRICT_START", false), "exit non-zero if any account fails to start")
	startCheckDelay := fs.Duration("start-check-delay", getenvDuration("SUNCODEXCLAW_START_CHECK_DELAY", 1*time.Second), "delay before checking status after restart")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)

	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, DisableLaunchctl: *noLaunchctl})
	accts := normalizeAccountsOrAll(*accounts)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	// Stop phase (print per-account results)
	stopLines, err := sup.Stop(accts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, ln := range stopLines {
		fmt.Println(ln)
	}

	// Start report phase
	startLines, err := sup.StartReport(accts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	hasError := false
	failedAccounts := []string{}
	for _, ln := range startLines {
		fmt.Println(ln)
		if strings.HasPrefix(ln, "[error]") {
			hasError = true
			if acct := parseAccountFromErrorLine(ln); acct != "" {
				failedAccounts = append(failedAccounts, acct)
			}
		}
	}
	if hasError {
		for _, a := range normalizeForLogTail(accts, failedAccounts) {
			_ = sup.Logs(a, false, 80)
		}
		os.Exit(1)
	}

	// macOS: detached launchctl jobs.
	if sup.UsingLaunchctl() {
		launchLines, err := sup.StartDetached(accts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		for _, ln := range launchLines {
			fmt.Println(ln)
		}

		time.Sleep(*startCheckDelay)
		infos, _ := sup.StatusInfos(accts)
		statusLines, _ := sup.Status(accts)
		for _, ln := range statusLines {
			fmt.Println(ln)
		}

		if *strictStart {
			failed := []string{}
			for _, it := range infos {
				if it.State == "stopped" {
					failed = append(failed, it.Account)
				}
			}
			if len(failed) > 0 {
				for _, a := range failed {
					fmt.Fprintf(os.Stderr, "[error] failed to start %s; recent log:\n", a)
					_ = sup.Logs(a, false, 80)
				}
				os.Exit(1)
			}
		}
		return
	}

	strictFailCh := make(chan []string, 1)

	go func() {
		time.Sleep(*startCheckDelay)
		infos, err := sup.StatusInfos(accts)
		if err != nil {
			return
		}
		statusLines, _ := sup.Status(accts)
		for _, ln := range statusLines {
			fmt.Println(ln)
		}
		if !*strictStart {
			return
		}
		failed := []string{}
		for _, it := range infos {
			if it.State == "stopped" {
				failed = append(failed, it.Account)
			}
		}
		if len(failed) == 0 {
			return
		}
		for _, a := range failed {
			fmt.Fprintf(os.Stderr, "[error] failed to start %s; recent log:\n", a)
			_ = sup.Logs(a, false, 80)
		}
		select {
		case strictFailCh <- failed:
		default:
		}
		cancel()
	}()

	if err := sup.StartAll(ctx, accts); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	select {
	case <-strictFailCh:
		os.Exit(1)
	default:
	}
}

func list(args []string) {
	fs, _, nodeBin, repoFlag := baseFlags("list")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin})
	accts, err := sup.DiscoverAccounts()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, a := range accts {
		fmt.Println(a)
	}
}

func logs(args []string) {
	args = normalizeLogsArgs(args)
	fs, accounts, nodeBin, repoFlag := baseFlags("logs")
	follow := fs.Bool("follow", false, "follow logs")
	lines := fs.Int("lines", 120, "lines to show before following")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl usage")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)

	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, DisableLaunchctl: *noLaunchctl})
	accts, all := parseAccounts(*accounts)
	if all {
		if err := sup.Logs("all", *follow, *lines); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if len(accts) == 0 {
		fmt.Fprintln(os.Stderr, "error: logs requires one account (or 'all')")
		os.Exit(2)
	}
	// Multiple accounts: support follow by multiplexing their log files.
	if len(accts) > 1 && !*follow {
		for _, a := range accts {
			fmt.Printf("===== %s =====\n", a)
			if err := sup.Logs(a, false, *lines); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		}
		return
	}
	if len(accts) > 1 && *follow {
		if err := sup.LogsSelected(accts, true, *lines); err != nil {
			var notFound *supervisor.LogFileNotFoundError
			if errors.As(err, &notFound) {
				fmt.Fprintf(os.Stderr, "[error] log file not found: %s\n", notFound.Path)
				os.Exit(1)
			}
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if err := sup.Logs(accts[0], *follow, *lines); err != nil {
		var notFound *supervisor.LogFileNotFoundError
		if errors.As(err, &notFound) {
			fmt.Fprintf(os.Stderr, "[error] log file not found: %s\n", notFound.Path)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func preflight(args []string) {
	args = normalizePositionalAccountArgs(args)
	fs, accounts, nodeBin, repoFlag := baseFlags("preflight")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl usage")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)

	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, DisableLaunchctl: *noLaunchctl})
	accts := normalizeAccountsOrAll(*accounts)
	if len(accts) == 0 {
		found, err := sup.DiscoverAccounts()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		accts = found
	}
	if len(accts) == 0 {
		fmt.Fprintln(os.Stderr, "error: no accounts found")
		os.Exit(1)
	}

	ok := true
	for _, a := range accts {
		// Node bot already has a robust dry-run that checks codex presence and config sources.
		// Run it as a preflight without starting the service.
		cmd := exec.Command(*nodeBin, filepath.Join(repo, "tools", "feishu_ws_bot.js"), "--account", a, "--dry-run")
		cmd.Dir = repo
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			ok = false
		}
	}
	if !ok {
		os.Exit(1)
	}
}

func configure(args []string) {
	if err := wizard.Configure(wizard.Options{Args: args}); err != nil {
		// Flag parsing errors already contain usage hints; keep it simple here.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func launchagents(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: launchagents requires action install|uninstall|status")
		os.Exit(2)
	}
	action := strings.TrimSpace(args[0])
	args = args[1:]
	args = normalizePositionalAccountArgs(args)

	fs, accounts, nodeBin, repoFlag := baseFlags("launchagents")
	runMode := fs.String("run-mode", getenvDefault("SUNCODEXCLAW_LAUNCHAGENT_RUN_MODE", "node"), "run mode: node|supervisor")
	keepAlive := fs.Bool("keepalive", getenvBool("SUNCODEXCLAW_LAUNCHAGENT_KEEPALIVE", true), "launchd keepalive (crash restart); supervisor mode still recommended for precise limits")
	throttle := fs.Int("throttle-interval", getenvInt("SUNCODEXCLAW_LAUNCHAGENT_THROTTLE_INTERVAL", 10), "launchd ThrottleInterval seconds (>=1)")
	prefix := fs.String("prefix", getenvDefault("SUNCODEXCLAW_LAUNCHCTL_PREFIX", "com.sunbelife.suncodexclaw.feishu"), "launchctl label prefix")
	daemonBin := fs.String("daemon-bin", getenvDefault("SUNCODEXCLAWD_BIN", ""), "supervisor mode: path to suncodexclawd binary (default: ./bin/suncodexclawd)")
	codexBin := fs.String("codex-bin", getenvDefault("CODEX_BIN", ""), "optional: codex binary path for FEISHU_CODEX_BIN in plist")
	codexHome := fs.String("codex-home", getenvDefault("CODEX_HOME", ""), "optional: CODEX_HOME in plist (default: ~/.codex)")
	pathValue := fs.String("path", getenvDefault("PATH", ""), "optional: PATH in plist")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)

	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, LaunchctlPrefix: *prefix})
	accts := normalizeAccountsOrAll(*accounts)

	opts := supervisor.LaunchAgentOptions{
		RunMode:          *runMode,
		KeepAlive:        *keepAlive,
		ThrottleInterval: *throttle,
		DaemonBin:        *daemonBin,
		CodexBin:         *codexBin,
		CodexHome:        *codexHome,
		PathValue:        *pathValue,
	}

	switch action {
	case "install":
		lines, err := sup.InstallLaunchAgents(accts, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		for _, ln := range lines {
			fmt.Println(ln)
		}
	case "uninstall":
		lines, err := sup.UninstallLaunchAgents(accts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		for _, ln := range lines {
			fmt.Println(ln)
		}
	case "status":
		lines, err := sup.StatusLaunchAgents(accts, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		for _, ln := range lines {
			fmt.Println(ln)
		}
	default:
		fmt.Fprintln(os.Stderr, "error: unknown launchagents action:", action)
		os.Exit(2)
	}
}

func serveHealth(addr string, sup *supervisor.Supervisor, accounts []string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		lines, err := sup.Status(accounts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		// Ready if at least one account is running, and none are "stopped" when explicitly specified.
		running := 0
		stopped := 0
		for _, ln := range lines {
			if strings.HasPrefix(ln, "[running]") {
				running++
			} else if strings.HasPrefix(ln, "[stopped]") {
				stopped++
			}
		}
		if running == 0 || (len(accounts) > 0 && stopped > 0) {
			http.Error(w, "not ready\n", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		lines, err := sup.Status(accounts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, ln := range lines {
			_, _ = w.Write([]byte(ln + "\n"))
		}
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	_ = srv.ListenAndServe()
}

func resolveRepoRoot(flagVal string) string {
	if strings.TrimSpace(flagVal) != "" {
		if filepath.IsAbs(flagVal) {
			return flagVal
		}
		cwd, _ := os.Getwd()
		return filepath.Join(cwd, flagVal)
	}
	// best-effort: walk up for package.json
	dir, _ := os.Getwd()
	for i := 0; i < 20; i++ {
		if exists(filepath.Join(dir, "package.json")) && exists(filepath.Join(dir, "tools")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	cwd, _ := os.Getwd()
	return cwd
}

func normalizeAccountsOrAll(in []string) []string {
	accts, all := parseAccounts(in)
	if all {
		return nil
	}
	return accts
}

func parseAccounts(in []string) ([]string, bool) {
	out := []string{}
	all := false
	for _, a := range in {
		v := strings.TrimSpace(a)
		if v == "" {
			continue
		}
		if v == "all" {
			all = true
			continue
		}
		out = append(out, v)
	}
	if all {
		return nil, true
	}
	return out, false
}

func normalizePositionalAccountArgs(args []string) []string {
	// Accept `start all` / `stop assistant` style.
	// If the first non-flag token exists, treat it as `--account <token>`.
	if len(args) == 0 {
		return args
	}
	if strings.HasPrefix(args[0], "-") {
		return args
	}
	token := strings.TrimSpace(args[0])
	if token == "" {
		return args[1:]
	}
	// Insert as --account, keep remaining args.
	out := []string{"--account", token}
	out = append(out, args[1:]...)
	return out
}

func normalizeLogsArgs(args []string) []string {
	// Accept `logs <account> -f` alias.
	out := []string{}
	for _, a := range args {
		if a == "-f" {
			out = append(out, "--follow")
			continue
		}
		out = append(out, a)
	}
	return normalizePositionalAccountArgs(out)
}

func normalizeForLogTail(selected []string, failed []string) []string {
	if len(failed) > 0 {
		return failed
	}
	if len(selected) == 0 {
		return []string{"all"}
	}
	return selected
}

func parseAccountFromErrorLine(line string) string {
	// Expected patterns:
	// - [error] missing config for <acct>:
	// - [error] <acct> preflight failed:
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "[error]") {
		return ""
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "[error]"))
	if strings.HasPrefix(s, "missing config for ") {
		s = strings.TrimPrefix(s, "missing config for ")
		if idx := strings.Index(s, ":"); idx >= 0 {
			return strings.TrimSpace(s[:idx])
		}
		return strings.Fields(s)[0]
	}
	// assume first token is account
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func getenvDefault(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	return v
}

func getenvBool(k string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

func getenvInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return def
	}
	return n
}

func getenvDuration(k string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
