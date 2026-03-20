package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"suncodexclaw/internal/memory"
	"suncodexclaw/internal/supervisor"
	"suncodexclaw/internal/timer"
	"suncodexclaw/internal/updater"
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
	case "timer":
		timerCmd(os.Args[2:])
	case "memory":
		memoryCmd(os.Args[2:])
	case "update":
		updateCmd(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer <start|list|show|upsert|delete|run>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory <add|list|show|search|delete>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd update [--repo owner/repo] [--version vX.Y.Z] [--bin /path/to/suncodexclawd] [--check] [--dry-run]")
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
	timerMgr := timer.NewManager(timer.Options{
		RepoRoot: repo,
		NodeBin:  *nodeBin,
		Output:   os.Stdout,
	})
	go func() {
		if err := timerMgr.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "timer error:", err)
		}
	}()

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

func updateCmd(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	repo := fs.String("repo", "rainoffallingstar/SunCodexClaw", "github repo in owner/name form")
	version := fs.String("version", "", "optional release tag; default uses latest release")
	binPath := fs.String("bin", "", "target binary path; default is current executable")
	check := fs.Bool("check", false, "show the selected release asset without replacing the binary")
	dryRun := fs.Bool("dry-run", false, "download metadata only; do not replace the binary")
	_ = fs.Parse(args)

	result, err := updater.Run(context.Background(), updater.Options{
		Repo:       *repo,
		Version:    *version,
		BinaryPath: *binPath,
		CheckOnly:  *check,
		DryRun:     *dryRun,
		Output:     os.Stdout,
		Executable: executableNameForRuntime(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("repo=%s\n", result.Repo)
	fmt.Printf("version=%s\n", result.Version)
	fmt.Printf("asset=%s\n", result.AssetName)
	fmt.Printf("download=%s\n", result.DownloadURL)
	fmt.Printf("binary=%s\n", result.BinaryPath)
	if *check || *dryRun {
		fmt.Println("status=check_only")
		return
	}
	fmt.Printf("status=updated replaced=%s\n", result.ReplacedPath)
	fmt.Println("next_step=restart_required")
	fmt.Println("hint=更新已写入本地二进制；请重启当前服务或重新启动进程后生效。")
}

func executableNameForRuntime() string {
	name := "suncodexclawd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func timerCmd(args []string) {
	if len(args) == 0 {
		timerUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "start":
		timerStart(args[1:])
	case "list":
		timerList(args[1:])
	case "show":
		timerShow(args[1:])
	case "upsert":
		timerUpsert(args[1:])
	case "enable":
		timerEnableDisable(args[1:], true)
	case "disable":
		timerEnableDisable(args[1:], false)
	case "delete":
		timerDelete(args[1:])
	case "run":
		timerRun(args[1:])
	case "logs":
		timerLogs(args[1:])
	case "help", "--help", "-h":
		timerUsage()
	default:
		timerUsage()
		os.Exit(2)
	}
}

func memoryCmd(args []string) {
	if len(args) == 0 {
		memoryUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "add":
		memoryAdd(args[1:])
	case "list":
		memoryList(args[1:])
	case "show":
		memoryShow(args[1:])
	case "search":
		memorySearch(args[1:])
	case "delete":
		memoryDelete(args[1:])
	case "help", "--help", "-h":
		memoryUsage()
	default:
		memoryUsage()
		os.Exit(2)
	}
}

func timerUsage() {
	fmt.Fprintln(os.Stderr, "Timer Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer start [--node-bin node] [--repo .] [--poll-interval 30s]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer list [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer show <id> [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer run <id> [--node-bin node] [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer logs <id> [--repo .] [--lines 80]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer enable <id> [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer disable <id> [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer delete <id> [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer upsert --id <id> --account assistant --chat-id oc_xxx (--every 1h | --daily 09:00 | --weekly mon,tue --at 09:00) --prompt \"...\" [--cwd /workspace] [--add-dir /workspace/other] [--tz Asia/Shanghai] [--disable]")
}

func memoryUsage() {
	fmt.Fprintln(os.Stderr, "Memory Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory add --text \"...\" [--source feishu/assistant/oc_xxx] [--tag foo] [--tag bar] [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory list [--repo .] [--limit 20]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory show <id> [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory search <keyword> [--repo .] [--limit 20]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory delete <id> [--repo .]")
}

func timerStart(args []string) {
	fs := flag.NewFlagSet("timer start", flag.ExitOnError)
	nodeBin := fs.String("node-bin", getenvDefault("NODE_BIN", "node"), "node binary")
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	pollInterval := fs.Duration("poll-interval", 30*time.Second, "poll interval")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()
	mgr := timer.NewManager(timer.Options{
		RepoRoot:     repo,
		NodeBin:      *nodeBin,
		PollInterval: *pollInterval,
		Output:       os.Stdout,
	})
	if err := mgr.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func timerList(args []string) {
	fs := flag.NewFlagSet("timer list", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	store := timer.NewStore(repo)
	tasks, err := store.ListTasks()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(tasks) == 0 {
		fmt.Println("(no timers)")
		return
	}
	now := time.Now()
	for _, task := range tasks {
		st, _ := store.ReadState(task.ID)
		_, nextRun, _ := timer.NextDue(task, st, now)
		nextText := st.NextRunAt
		if nextText == "" && !nextRun.IsZero() {
			nextText = nextRun.UTC().Format(time.RFC3339)
		}
		status := "enabled"
		if !task.Enabled {
			status = "disabled"
		}
		fmt.Printf("%s status=%s account=%s schedule=%s next=%s chat_id=%s\n", task.ID, status, task.Account, timerScheduleSummary(task.Schedule), emptyFallback(nextText, "(none)"), task.ChatID)
	}
}

func timerShow(args []string) {
	fs := flag.NewFlagSet("timer show", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	store := timer.NewStore(repo)
	task, err := store.ReadTask(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	st, _ := store.ReadState(task.ID)
	out := map[string]any{
		"task":  task,
		"state": st,
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(string(body))
}

func timerDelete(args []string) {
	fs := flag.NewFlagSet("timer delete", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	store := timer.NewStore(repo)
	if err := store.DeleteTask(fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("deleted=%s\n", fs.Arg(0))
}

func timerEnableDisable(args []string, enabled bool) {
	name := "timer enable"
	if !enabled {
		name = "timer disable"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	store := timer.NewStore(repo)
	task, err := store.SetTaskEnabled(fs.Arg(0), enabled)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	action := "enabled"
	if !enabled {
		action = "disabled"
	}
	fmt.Printf("%s=%s schedule=%s\n", action, task.ID, timerScheduleSummary(task.Schedule))
}

func timerRun(args []string) {
	fs := flag.NewFlagSet("timer run", flag.ExitOnError)
	nodeBin := fs.String("node-bin", getenvDefault("NODE_BIN", "node"), "node binary")
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	mgr := timer.NewManager(timer.Options{
		RepoRoot: repo,
		NodeBin:  *nodeBin,
		Output:   os.Stdout,
	})
	if err := mgr.RunTaskNow(context.Background(), fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("run=%s status=ok\n", fs.Arg(0))
}

func timerLogs(args []string) {
	fs := flag.NewFlagSet("timer logs", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	lines := fs.Int("lines", 80, "lines to show")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	store := timer.NewStore(repo)
	text, err := store.ReadLogTail(fs.Arg(0), *lines)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if strings.TrimSpace(text) == "" {
		fmt.Println("(no logs)")
		return
	}
	fmt.Println(text)
}

func timerUpsert(args []string) {
	fs := flag.NewFlagSet("timer upsert", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	id := fs.String("id", "", "timer id")
	account := fs.String("account", "assistant", "feishu account name")
	chatID := fs.String("chat-id", "", "destination chat id")
	prompt := fs.String("prompt", "", "task prompt")
	promptFile := fs.String("prompt-file", "", "task prompt file")
	cwd := fs.String("cwd", "", "working directory")
	every := fs.String("every", "", "interval schedule, e.g. 1h")
	daily := fs.String("daily", "", "daily schedule time, e.g. 09:00")
	weekly := fs.String("weekly", "", "weekly schedule weekdays, e.g. mon,tue,fri")
	at := fs.String("at", "", "time for weekly schedule, e.g. 09:00")
	tz := fs.String("tz", "", "timezone, e.g. Asia/Shanghai")
	model := fs.String("model", "", "optional codex model override")
	reasoning := fs.String("reasoning-effort", "", "optional codex reasoning effort override")
	disable := fs.Bool("disable", false, "create/update as disabled")
	updatedBy := fs.String("updated-by", "", "free-form updater label")
	var addDirs multiFlag
	fs.Var(&addDirs, "add-dir", "additional directory (repeatable)")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	store := timer.NewStore(repo)
	taskPrompt := strings.TrimSpace(*prompt)
	if strings.TrimSpace(*promptFile) != "" {
		b, err := os.ReadFile(*promptFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		taskPrompt = strings.TrimSpace(string(b))
	}
	if strings.TrimSpace(*id) == "" {
		fmt.Fprintln(os.Stderr, "error: --id is required")
		os.Exit(2)
	}
	schedule, err := buildTimerSchedule(*every, *daily, *weekly, *at, *tz)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	task := timer.Task{
		ID:              strings.TrimSpace(*id),
		Enabled:         !*disable,
		Account:         strings.TrimSpace(*account),
		ChatID:          strings.TrimSpace(*chatID),
		Prompt:          taskPrompt,
		Cwd:             strings.TrimSpace(*cwd),
		AddDirs:         normalizeStringSlice(addDirs),
		Model:           strings.TrimSpace(*model),
		ReasoningEffort: strings.TrimSpace(*reasoning),
		Schedule:        schedule,
		UpdatedAt:       now,
		LastUpdatedBy:   strings.TrimSpace(*updatedBy),
	}
	if existing, err := store.ReadTask(task.ID); err == nil {
		task.CreatedAt = existing.CreatedAt
		if task.ChatID == "" {
			task.ChatID = existing.ChatID
		}
	} else {
		task.CreatedAt = now
	}
	if task.CreatedAt == "" {
		task.CreatedAt = now
	}
	if err := store.WriteTask(task); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("upserted=%s schedule=%s chat_id=%s enabled=%t\n", task.ID, timerScheduleSummary(task.Schedule), task.ChatID, task.Enabled)
}

func memoryAdd(args []string) {
	fs := flag.NewFlagSet("memory add", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	text := fs.String("text", "", "memory text")
	textFile := fs.String("text-file", "", "memory text file")
	source := fs.String("source", "", "memory source label")
	var tags multiFlag
	fs.Var(&tags, "tag", "memory tag (repeatable)")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	memoryText := strings.TrimSpace(*text)
	if strings.TrimSpace(*textFile) != "" {
		b, err := os.ReadFile(*textFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		memoryText = strings.TrimSpace(string(b))
	}
	if memoryText == "" && fs.NArg() > 0 {
		memoryText = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if memoryText == "" {
		fmt.Fprintln(os.Stderr, "error: memory text is required")
		os.Exit(2)
	}
	store := memory.NewStore(repo)
	entry, err := store.Add(memoryText, *source, tags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("added=%s source=%s tags=%s text=%s\n", entry.ID, emptyFallback(entry.Source, "(none)"), emptyFallback(strings.Join(entry.Tags, ","), "(none)"), compactSingleLine(entry.Text, 120))
}

func memoryList(args []string) {
	fs := flag.NewFlagSet("memory list", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	limit := fs.Int("limit", 20, "max memories to show")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	store := memory.NewStore(repo)
	entries, err := store.ListEntries()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("(no memories)")
		return
	}
	for i, entry := range entries {
		if i >= *limit {
			break
		}
		fmt.Println(memorySummaryLine(entry))
	}
}

func memoryShow(args []string) {
	fs := flag.NewFlagSet("memory show", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		memoryUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	store := memory.NewStore(repo)
	entry, err := store.ReadEntry(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	body, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(string(body))
}

func memorySearch(args []string) {
	fs := flag.NewFlagSet("memory search", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	limit := fs.Int("limit", 20, "max memories to show")
	_ = fs.Parse(args)
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "error: search keyword is required")
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	store := memory.NewStore(repo)
	entries, err := store.Search(query, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("(no matched memories)")
		return
	}
	for _, entry := range entries {
		fmt.Println(memorySummaryLine(entry))
	}
}

func memoryDelete(args []string) {
	fs := flag.NewFlagSet("memory delete", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		memoryUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	store := memory.NewStore(repo)
	if err := store.DeleteEntry(fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("deleted=%s\n", fs.Arg(0))
}

func buildTimerSchedule(every, daily, weekly, at, tz string) (timer.Schedule, error) {
	count := 0
	for _, raw := range []string{every, daily, weekly} {
		if strings.TrimSpace(raw) != "" {
			count++
		}
	}
	if count != 1 {
		return timer.Schedule{}, fmt.Errorf("choose exactly one schedule mode: --every | --daily | --weekly")
	}
	if strings.TrimSpace(every) != "" {
		return timer.Schedule{Kind: "interval", Every: strings.TrimSpace(every), Timezone: strings.TrimSpace(tz)}, nil
	}
	if strings.TrimSpace(daily) != "" {
		return timer.Schedule{Kind: "daily", At: strings.TrimSpace(daily), Timezone: strings.TrimSpace(tz)}, nil
	}
	parts := []string{}
	for _, item := range strings.Split(weekly, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return timer.Schedule{Kind: "weekly", Weekdays: parts, At: strings.TrimSpace(at), Timezone: strings.TrimSpace(tz)}, nil
}

func timerScheduleSummary(schedule timer.Schedule) string {
	switch schedule.Kind {
	case "interval":
		return "every " + strings.TrimSpace(schedule.Every)
	case "daily":
		if strings.TrimSpace(schedule.Timezone) != "" {
			return fmt.Sprintf("daily %s %s", strings.TrimSpace(schedule.At), strings.TrimSpace(schedule.Timezone))
		}
		return "daily " + strings.TrimSpace(schedule.At)
	case "weekly":
		base := fmt.Sprintf("weekly %s @ %s", strings.Join(schedule.Weekdays, ","), strings.TrimSpace(schedule.At))
		if strings.TrimSpace(schedule.Timezone) != "" {
			return base + " " + strings.TrimSpace(schedule.Timezone)
		}
		return base
	default:
		return schedule.Kind
	}
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func compactSingleLine(value string, max int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}

func memorySummaryLine(entry memory.Entry) string {
	return fmt.Sprintf(
		"%s updated=%s source=%s tags=%s text=%s",
		entry.ID,
		emptyFallback(strings.TrimSpace(entry.UpdatedAt), emptyFallback(strings.TrimSpace(entry.CreatedAt), "(unknown)")),
		emptyFallback(entry.Source, "(none)"),
		emptyFallback(strings.Join(entry.Tags, ","), "(none)"),
		compactSingleLine(entry.Text, 120),
	)
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
