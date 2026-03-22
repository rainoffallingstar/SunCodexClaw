package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"suncodexclaw/internal/clawhub"
	"suncodexclaw/internal/codexenv"
	"suncodexclaw/internal/codexhome"
	"suncodexclaw/internal/configstore"
	"suncodexclaw/internal/envstore"
	"suncodexclaw/internal/feishunative"
	"suncodexclaw/internal/memory"
	"suncodexclaw/internal/supervisor"
	"suncodexclaw/internal/timer"
	"suncodexclaw/internal/updater"
	"suncodexclaw/internal/wizard"
	"suncodexclaw/internal/worksync"
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
	case "workspace-docs":
		workspaceDocsCmd(os.Args[2:])
	case "timer":
		timerCmd(os.Args[2:])
	case "memory":
		memoryCmd(os.Args[2:])
	case "env":
		envCmd(os.Args[2:])
	case "clawhub":
		clawhubCmd(os.Args[2:])
	case "sync":
		syncCmd(os.Args[2:])
	case "update":
		updateCmd(os.Args[2:])
	case "codex-home":
		codexHomeCmd(os.Args[2:])
	case "feishu-run":
		feishuRun(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd start [--account a] [--account b] [--repo .] [--node-bin node] [--no-launchctl] [--once] [--no-restart] [--max-restarts 20] [--restart-window 10m] [--strict-start] [--start-check-delay 1s]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd start --docker-compose [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd stop [--account a] [--account b] [--repo .] [--no-launchctl]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd stop --docker-compose [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd restart [--account a] [--account b] [--repo .] [--node-bin node] [--no-launchctl] [--strict-start] [--start-check-delay 1s]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd restart --docker-compose [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd status [--account a] [--account b] [--repo .] [--no-launchctl]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd status --docker-compose [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd list [--docker-compose] [--account a] [--account b]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd logs --account a [--account b|--account all] [--repo .] [--follow|-f] [--lines 120] [--no-launchctl]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd logs --docker-compose [--repo .] [--follow|-f] [--lines 120]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd preflight [--account a] [--account b] [--repo .] [--node-bin node] [--no-launchctl]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd preflight --docker-compose [--account a] [--account b] [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer <start|list|show|upsert|update|logs|run|enable|disable|delete>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory <add|list|show|search|delete>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd env <set|get|list|delete|run>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd clawhub <search|list|show|file>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd sync <status|list-remote|push|pull|restore>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd update [--repo owner/repo] [--version vX.Y.Z] [--bin /path/to/suncodexclawd] [--check] [--dry-run]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd update --docker-compose [--project-dir .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd codex-home sync [--repo .] [--account a] [--codex-home /home/node/.codex] [--force]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd launchagents <install|uninstall|status> ...   # macOS/local mode only")
	fmt.Fprintln(os.Stderr, "  suncodexclawd configure [--docker-compose] --account <account> [--yes]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd configure add [--docker-compose] --account <account> [--yes]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd configure edit [--docker-compose] --account <account> [--yes]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd workspace-docs refresh [--docker-compose] [--repo .] [--account <account>] [--workspace <dir>]")
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  - Default account selection depends on subcommand: start/preflight use enabled bots; status/stop use all configured bots; restart stops all configured bots then starts enabled bots.")
	fmt.Fprintln(os.Stderr, "  - Local mode is the default; passing --local is optional and only makes the mode explicit.")
	fmt.Fprintln(os.Stderr, "  - In a bot workspace, timer/memory/env/sync can infer --account from .config.toml.")
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func baseFlags(name string) (*flag.FlagSet, *multiFlag, *string, *string, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var accounts multiFlag
	fs.Var(&accounts, "account", "account name (repeatable); default set depends on subcommand")
	nodeBin := fs.String("node-bin", getenvDefault("NODE_BIN", "node"), "node binary")
	repo := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	runtimeBackend := fs.String("runtime-backend", normalizeRuntimeBackend(getenvDefault("SUNCODEXCLAW_FEISHU_RUNTIME", "go")), "deprecated; Go native runtime is always used")
	return fs, &accounts, nodeBin, repo, runtimeBackend
}

func start(args []string) {
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		if err := ensureComposeLifecycleFlags("start", composeArgs,
			"--account", "--node-bin", "--runtime-backend", "--no-launchctl",
			"--no-restart", "--once", "--strict-start", "--start-check-delay",
			"--max-restarts", "--restart-window", "--health-addr"); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeLifecycleCommand(repo, false); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	args = stripRuntimeModeFlags(args)
	fs, accounts, nodeBin, repoFlag, runtimeBackend := baseFlags("start")
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
		RuntimeBackend:   *runtimeBackend,
		DisableLaunchctl: *noLaunchctl,
		AutoRestart:      autoRestart,
		MaxRestarts:      *maxRestarts,
		RestartWindow:    *restartWindow,
	})
	accts, err := resolveEnabledAccounts(repo, *accounts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

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
		RepoRoot:       repo,
		NodeBin:        *nodeBin,
		RuntimeBackend: *runtimeBackend,
		Output:         os.Stdout,
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

	// macOS: prefer detached launchctl jobs for long-running local bots.
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
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		if err := ensureComposeLifecycleFlags("status", composeArgs,
			"--account", "--node-bin", "--runtime-backend", "--no-launchctl"); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerCompose(repo, "ps"); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	args = stripRuntimeModeFlags(args)
	fs, accounts, nodeBin, repoFlag, runtimeBackend := baseFlags("status")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl and use pidfile/manual detection only")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, RuntimeBackend: *runtimeBackend, DisableLaunchctl: *noLaunchctl})
	accts, err := resolveConfiguredAccounts(repo, *accounts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(accts) == 0 {
		fmt.Println("(no configured accounts)")
		return
	}
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
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		if err := ensureComposeLifecycleFlags("stop", composeArgs,
			"--account", "--node-bin", "--runtime-backend", "--no-launchctl"); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerCompose(repo, "down"); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	args = stripRuntimeModeFlags(args)
	fs, accounts, nodeBin, repoFlag, runtimeBackend := baseFlags("stop")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl and stop only pidfile/manual processes")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, RuntimeBackend: *runtimeBackend, DisableLaunchctl: *noLaunchctl})
	accts, err := resolveConfiguredAccounts(repo, *accounts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(accts) == 0 {
		fmt.Println("(no configured accounts)")
		return
	}
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
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		if err := ensureComposeLifecycleFlags("restart", composeArgs,
			"--account", "--node-bin", "--runtime-backend", "--no-launchctl",
			"--strict-start", "--start-check-delay"); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeLifecycleCommand(repo, true); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	args = stripRuntimeModeFlags(args)
	fs, accounts, nodeBin, repoFlag, runtimeBackend := baseFlags("restart")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl detached mode and run in foreground supervisor mode")
	strictStart := fs.Bool("strict-start", getenvBool("SUNCODEXCLAW_STRICT_START", false), "exit non-zero if any account fails to start")
	startCheckDelay := fs.Duration("start-check-delay", getenvDuration("SUNCODEXCLAW_START_CHECK_DELAY", 1*time.Second), "delay before checking status after restart")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)

	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, RuntimeBackend: *runtimeBackend, DisableLaunchctl: *noLaunchctl})
	stopAccounts, startAccounts, err := resolveRestartAccounts(repo, *accounts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(stopAccounts) == 0 && len(startAccounts) == 0 {
		fmt.Println("(no configured accounts)")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		cancel()
	}()

	// Stop phase (print per-account results)
	stopLines, err := sup.Stop(stopAccounts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, ln := range stopLines {
		fmt.Println(ln)
	}

	// Start report phase
	startLines, err := sup.StartReport(startAccounts)
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
		for _, a := range normalizeForLogTail(startAccounts, failedAccounts) {
			_ = sup.Logs(a, false, 80)
		}
		os.Exit(1)
	}

	// macOS: detached launchctl jobs.
	if sup.UsingLaunchctl() {
		launchLines, err := sup.StartDetached(startAccounts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		for _, ln := range launchLines {
			fmt.Println(ln)
		}

		time.Sleep(*startCheckDelay)
		infos, _ := sup.StatusInfos(startAccounts)
		statusLines, _ := sup.Status(stopAccounts)
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
		infos, err := sup.StatusInfos(startAccounts)
		if err != nil {
			return
		}
		statusLines, _ := sup.Status(stopAccounts)
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

	if err := sup.StartAll(ctx, startAccounts); err != nil && ctx.Err() == nil {
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
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "list", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	} else {
		args = composeArgs
	}
	fs, accounts, nodeBin, repoFlag, _ := baseFlags("list")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	_ = nodeBin
	store := configstore.NewStore(repo)
	accts := uniqueAccounts(*accounts)
	var err error
	if len(accts) == 0 {
		accts, err = store.ListConfiguredAccountNames()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, a := range accts {
		enabled, err := store.BotEnabled(a)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		fmt.Printf("%s\t%s\n", a, status)
	}
}

func logs(args []string) {
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		if err := ensureComposeLifecycleFlags("logs", composeArgs, "--account"); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		composeLogArgs, err := composeLogsCommandArgs(composeArgs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		if err := runDockerCompose(repo, composeLogArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	args = stripRuntimeModeFlags(args)
	fs, accounts, nodeBin, repoFlag, runtimeBackend := baseFlags("logs")
	follow := fs.Bool("follow", false, "follow logs")
	followShort := fs.Bool("f", false, "alias of --follow")
	lines := fs.Int("lines", 120, "lines to show before following")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl usage")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)

	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, RuntimeBackend: *runtimeBackend, DisableLaunchctl: *noLaunchctl})
	if *followShort {
		*follow = true
	}
	accts, all := parseAccounts(*accounts)
	if all {
		if err := sup.Logs("all", *follow, *lines); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if len(accts) == 0 {
		fmt.Fprintln(os.Stderr, "error: logs requires --account <account> (or --account all)")
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
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "preflight", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	args = stripRuntimeModeFlags(args)
	fs, accounts, nodeBin, repoFlag, runtimeBackend := baseFlags("preflight")
	noLaunchctl := fs.Bool("no-launchctl", getenvBool("SUNCODEXCLAW_DISABLE_LAUNCHCTL", false), "macOS: disable launchctl usage")
	_ = fs.Parse(args)
	_ = noLaunchctl
	repo := resolveRepoRoot(*repoFlag)

	accts, err := resolveEnabledAccounts(repo, *accounts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(accts) == 0 {
		fmt.Fprintln(os.Stderr, "error: no accounts found")
		os.Exit(1)
	}

	ok := true
	for _, a := range accts {
		// Node bot already has a robust dry-run that checks codex presence and config sources.
		// Run it as a preflight without starting the service.
		cmd := buildRuntimeCommand(repo, *nodeBin, *runtimeBackend, a, true, "")
		cmd.Dir = repo
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			ok = false
			continue
		}
		if err := runCodexBaseURLPreflight(context.Background(), os.Stdout, repo, a); err != nil {
			ok = false
		}
	}
	if !ok {
		os.Exit(1)
	}
}

func runCodexBaseURLPreflight(ctx context.Context, w io.Writer, repo, account string) error {
	cfg, err := feishunative.Load(repo, account)
	if err != nil {
		return err
	}
	result, err := feishunative.ProbeCodexBaseURL(ctx, cfg)
	if result.Skipped {
		if strings.TrimSpace(result.Message) != "" {
			_, _ = fmt.Fprintf(w, "codex_base_url_probe=skip account=%s reason=%s\n", account, result.Message)
		}
		return nil
	}
	if result.Enabled {
		status := "ok"
		if err != nil {
			status = "error"
		}
		message := result.Message
		if message == "" && err != nil {
			message = err.Error()
		}
		_, _ = fmt.Fprintf(w, "codex_base_url_probe=%s account=%s url=%s message=%s\n",
			status,
			account,
			emptyFallback(result.WSURL, "(none)"),
			emptyFallback(message, "(none)"),
		)
	}
	return err
}

func feishuRun(args []string) {
	fs := flag.NewFlagSet("feishu-run", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root")
	account := fs.String("account", "", "feishu account name")
	dryRun := fs.Bool("dry-run", false, "dry run")
	timerTaskFile := fs.String("timer-task-file", "", "timer task file")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	if strings.TrimSpace(*account) == "" {
		fmt.Fprintln(os.Stderr, "error: feishu-run requires --account <account>")
		os.Exit(2)
	}
	if _, err := codexenv.SetProcessAccountEnv(strings.TrimSpace(*account)); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := feishunative.Run(context.Background(), feishunative.RunOptions{
		RepoRoot:      repo,
		Account:       strings.TrimSpace(*account),
		DryRun:        *dryRun,
		TimerTaskFile: strings.TrimSpace(*timerTaskFile),
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func normalizeRuntimeBackend(raw string) string {
	return "go"
}

func buildRuntimeCommand(repo, nodeBin, runtimeBackend, account string, dryRun bool, timerTaskFile string) *exec.Cmd {
	_ = nodeBin
	_ = runtimeBackend
	exe, err := os.Executable()
	if err != nil {
		exe = filepath.Join(repo, "bin", executableNameForRuntime())
	}
	args := []string{"feishu-run", "--repo", repo, "--account", account}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if strings.TrimSpace(timerTaskFile) != "" {
		args = append(args, "--timer-task-file", strings.TrimSpace(timerTaskFile))
	}
	cmd := exec.Command(exe, args...)
	if env, _, err := codexenv.AppendAccountEnv(os.Environ(), account); err == nil {
		cmd.Env = env
	}
	return cmd
}

func configure(args []string) {
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "configure", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	} else {
		args = composeArgs
	}
	if err := wizard.Configure(wizard.Options{Args: args}); err != nil {
		// Flag parsing errors already contain usage hints; keep it simple here.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	account := fs.String("account", "", "feishu account name")
	_ = fs.Parse(normalizeConfigureArgs(args))
	if strings.TrimSpace(*account) == "" {
		fmt.Fprintln(os.Stderr, "error: configure requires --account <account>")
		os.Exit(2)
	}
	fmt.Printf("configured_account=%s\n", *account)
}

func workspaceDocsCmd(args []string) {
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "workspace-docs", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	} else {
		args = composeArgs
	}
	if len(args) == 0 {
		workspaceDocsUsage()
		os.Exit(2)
	}
	switch strings.TrimSpace(args[0]) {
	case "refresh":
		workspaceDocsRefresh(args[1:])
	case "help", "--help", "-h":
		workspaceDocsUsage()
	default:
		workspaceDocsUsage()
		os.Exit(2)
	}
}

func workspaceDocsUsage() {
	fmt.Fprintln(os.Stderr, "Workspace Docs Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd workspace-docs refresh [--docker-compose] [--repo .] [--account <account>] [--workspace <dir>]")
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  - refresh skips WebDAV restore and rewrites agent.md/soul.md/heartbeats.md using the latest built-in templates.")
	fmt.Fprintln(os.Stderr, "  - If --workspace is omitted, the command uses the bot's configured codex.cwd.")
	fmt.Fprintln(os.Stderr, "  - In a bot workspace, --account can be inferred from .config.toml.")
}

func workspaceDocsRefresh(args []string) {
	fs := flag.NewFlagSet("workspace-docs refresh", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "robot account name")
	workspace := fs.String("workspace", "", "workspace directory to rewrite")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveScopedAccount(strings.TrimSpace(*account), "workspace")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	cfg, err := feishunative.Load(repo, resolvedAccount)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if strings.TrimSpace(*workspace) != "" {
		cfg.Codex.Cwd = strings.TrimSpace(*workspace)
	}
	result, err := feishunative.EnsureRuntimeWorkspaceWithOptions(repo, cfg, feishunative.WorkspaceInitOptions{
		AttemptRestore: false,
		OverwriteDocs:  true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("workspace=%s\n", emptyFallback(strings.TrimSpace(cfg.Codex.Cwd), "(none)"))
	fmt.Printf("account=%s\n", resolvedAccount)
	fmt.Printf("config=%s\n", emptyFallback(result.ConfigPath, "(none)"))
	fmt.Printf("created_docs=%s\n", emptyFallback(strings.Join(result.CreatedDocs, " | "), "(none)"))
	fmt.Printf("overwritten_docs=%s\n", emptyFallback(strings.Join(result.OverwrittenDocs, " | "), "(none)"))
	fmt.Printf("restore_attempted=%t\n", result.RestoreAttempted)
	fmt.Println("status=ok")
}

func normalizeConfigureArgs(args []string) []string {
	if len(args) > 0 {
		action := strings.TrimSpace(args[0])
		if action == "add" || action == "edit" {
			return append([]string{}, args[1:]...)
		}
	}
	return append([]string{}, args...)
}

func updateCmd(args []string) {
	args = stripExplicitLocalFlag(args)
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	repo := fs.String("repo", "rainoffallingstar/SunCodexClaw", "github repo in owner/name form")
	version := fs.String("version", "", "optional release tag; default uses latest release")
	binPath := fs.String("bin", "", "target binary path; default is current executable")
	check := fs.Bool("check", false, "show the selected release asset without replacing the binary")
	dryRun := fs.Bool("dry-run", false, "download metadata only; do not replace the binary")
	dockerCompose := fs.Bool("docker-compose", false, "refresh container service through docker compose instead of replacing the local binary")
	projectDir := fs.String("project-dir", "", "docker compose project directory; defaults to repo root auto-detection")
	_ = fs.Parse(args)
	if *dockerCompose {
		if err := ensureComposeUpdateFlags(args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		repoRoot := resolveRepoRoot(*projectDir)
		if err := runDockerComposeLifecycleCommand(repoRoot, true); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("status=updated_docker_compose")
		fmt.Println("next_step=container_refreshed")
		return
	}

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

func maybeDockerComposeMode(args []string) (bool, []string) {
	for _, arg := range args {
		if arg == "--docker-compose" {
			return true, stripRuntimeModeFlags(args)
		}
	}
	return false, stripRuntimeModeFlags(args)
}

func stripRuntimeModeFlags(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--local" || arg == "--docker-compose" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func stripExplicitLocalFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--local" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func runDockerCompose(repo string, args ...string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("--docker-compose requires docker to be installed and available in PATH")
	}
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = repo
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func composeLifecycleUpArgs(forceRecreate bool, build bool) []string {
	args := []string{"up", "-d"}
	if build {
		args = append(args, "--build")
	}
	if forceRecreate {
		args = append(args, "--force-recreate")
	}
	return args
}

func composeServiceRunArgs(subcommand string, serviceArgs []string, build bool) []string {
	args := []string{"run", "--rm", "--workdir", "/app"}
	if build {
		args = append(args, "--build")
	}
	args = append(args, "suncodexclaw", subcommand)
	args = append(args, serviceArgs...)
	return args
}

func composeServiceExecArgs(subcommand string, serviceArgs []string, interactive bool) []string {
	args := []string{"exec"}
	if !interactive {
		args = append(args, "-T")
	}
	args = append(args, "--workdir", "/app", "suncodexclaw", "suncodexclawd", subcommand)
	args = append(args, serviceArgs...)
	return args
}

func runDockerComposeLifecycleCommand(repo string, forceRecreate bool) error {
	if err := runDockerCompose(repo, "pull", "suncodexclaw"); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] docker compose pull suncodexclaw failed, falling back to local build: %v\n", err)
		return runDockerCompose(repo, composeLifecycleUpArgs(forceRecreate, true)...)
	}
	return runDockerCompose(repo, composeLifecycleUpArgs(forceRecreate, false)...)
}

func runDockerComposeServiceCommand(repo string, subcommand string, args ...string) error {
	serviceArgs := normalizeComposeServiceArgs(subcommand, args)
	if running, err := dockerComposeServiceRunning(repo, "suncodexclaw"); err != nil {
		return err
	} else if running {
		return runDockerCompose(repo, composeServiceExecArgs(subcommand, serviceArgs, stdinLooksInteractive())...)
	}
	if err := runDockerCompose(repo, "pull", "suncodexclaw"); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] docker compose pull suncodexclaw failed, falling back to local build: %v\n", err)
		return runDockerCompose(repo, composeServiceRunArgs(subcommand, serviceArgs, true)...)
	}
	return runDockerCompose(repo, composeServiceRunArgs(subcommand, serviceArgs, false)...)
}

func extractRepoFlag(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "--repo" {
			if i+1 < len(args) {
				return strings.TrimSpace(args[i+1])
			}
			return ""
		}
		if strings.HasPrefix(arg, "--repo=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, "--repo="))
		}
	}
	return ""
}

func ensureComposeLifecycleFlags(command string, args []string, unsupported ...string) error {
	if flag := findPresentFlag(args, unsupported...); flag != "" {
		return fmt.Errorf("%s --docker-compose manages the whole compose service and does not support %s", command, flag)
	}
	return nil
}

func ensureComposeUpdateFlags(args []string) error {
	if flag := findPresentFlag(args, "--repo", "--version", "--bin", "--check", "--dry-run"); flag != "" {
		return fmt.Errorf("update --docker-compose refreshes the compose service and does not support %s; use --project-dir to choose the compose project directory", flag)
	}
	return nil
}

func findPresentFlag(args []string, names ...string) string {
	for _, name := range names {
		for _, arg := range args {
			trimmed := strings.TrimSpace(arg)
			if trimmed == name || strings.HasPrefix(trimmed, name+"=") {
				return name
			}
		}
	}
	return ""
}

func normalizeComposeServiceArgs(subcommand string, args []string) []string {
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "--repo" {
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "--repo=") {
			continue
		}
		out = append(out, args[i])
	}
	switch strings.TrimSpace(subcommand) {
	case "list", "preflight", "timer", "memory", "sync", "workspace-docs":
		out = append(out, "--repo", "/app")
	}
	return out
}

func composeLogsCommandArgs(args []string) ([]string, error) {
	fs := flag.NewFlagSet("logs --docker-compose", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "repo root")
	follow := fs.Bool("follow", false, "follow logs")
	followShort := fs.Bool("f", false, "alias of --follow")
	lines := fs.Int("lines", 120, "lines to show")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	_ = repo
	composeArgs := []string{"logs", "--tail", strconv.Itoa(*lines)}
	if *follow || *followShort {
		composeArgs = append(composeArgs, "-f")
	}
	composeArgs = append(composeArgs, "suncodexclaw")
	return composeArgs, nil
}

func reorderFlagsBeforePositionals(args []string, boolFlags map[string]bool) []string {
	if len(args) == 0 {
		return nil
	}
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		name := arg
		if boolFlags[name] {
			continue
		}
		if i+1 >= len(args) {
			continue
		}
		next := strings.TrimSpace(args[i+1])
		if next == "" || strings.HasPrefix(next, "-") {
			continue
		}
		flags = append(flags, next)
		i++
	}
	return append(flags, positionals...)
}

func dockerComposeServiceRunning(repo string, service string) (bool, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return false, fmt.Errorf("--docker-compose requires docker to be installed and available in PATH")
	}
	cmd := exec.Command("docker", composeRunningPSArgs(service)...)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg := strings.TrimSpace(string(exitErr.Stderr))
			if msg == "" {
				msg = strings.TrimSpace(err.Error())
			}
			return false, fmt.Errorf("docker compose ps failed: %s", msg)
		}
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func composeRunningPSArgs(service string) []string {
	return []string{"compose", "ps", "--status", "running", "-q", service}
}

func stdinLooksInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func timerCmd(args []string) {
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "timer", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	} else {
		args = composeArgs
	}
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
	case "update":
		timerUpdate(args[1:])
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
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "memory", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	} else {
		args = composeArgs
	}
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

func envCmd(args []string) {
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "env", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	} else {
		args = composeArgs
	}
	if len(args) == 0 {
		envUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "set":
		envSet(args[1:])
	case "get":
		envGet(args[1:])
	case "list":
		envList(args[1:])
	case "delete":
		envDelete(args[1:])
	case "run":
		envRun(args[1:])
	case "help", "--help", "-h":
		envUsage()
	default:
		envUsage()
		os.Exit(2)
	}
}

func clawhubCmd(args []string) {
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "clawhub", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	} else {
		args = composeArgs
	}
	if len(args) == 0 {
		clawhubUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "search":
		clawhubSearch(args[1:])
	case "list":
		clawhubList(args[1:])
	case "show":
		clawhubShow(args[1:])
	case "file":
		clawhubFile(args[1:])
	case "help", "--help", "-h":
		clawhubUsage()
	default:
		clawhubUsage()
		os.Exit(2)
	}
}

func syncCmd(args []string) {
	if dockerMode, composeArgs := maybeDockerComposeMode(args); dockerMode {
		repo := resolveRepoRoot(extractRepoFlag(composeArgs))
		if err := runDockerComposeServiceCommand(repo, "sync", composeArgs...); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	} else {
		args = composeArgs
	}
	if len(args) == 0 {
		syncUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "status":
		syncStatusCmd(args[1:])
	case "push":
		syncPushCmd(args[1:])
	case "list-remote":
		syncListRemoteCmd(args[1:])
	case "pull":
		syncPullCmd(args[1:])
	case "restore":
		syncRestoreCmd(args[1:])
	case "help", "--help", "-h":
		syncUsage()
	default:
		syncUsage()
		os.Exit(2)
	}
}

func codexHomeCmd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: codex-home requires a subcommand (supported: sync)")
		os.Exit(2)
	}
	switch strings.TrimSpace(args[0]) {
	case "sync":
		codexHomeSync(args[1:])
	case "help", "--help", "-h":
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  suncodexclawd codex-home sync [--repo .] [--account a] [--codex-home /home/node/.codex] [--force]")
	default:
		fmt.Fprintln(os.Stderr, "error: unsupported codex-home subcommand:", args[0])
		os.Exit(2)
	}
}

func codexHomeSync(args []string) {
	fs := flag.NewFlagSet("codex-home sync", flag.ExitOnError)
	var accounts multiFlag
	fs.Var(&accounts, "account", "account name (repeatable); defaults to enabled accounts")
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	codexHomeFlag := fs.String("codex-home", strings.TrimSpace(os.Getenv("CODEX_HOME")), "CODEX_HOME target directory")
	force := fs.Bool("force", false, "overwrite existing unmanaged config.toml/auth.json in CODEX_HOME")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	result, err := codexhome.Sync(codexhome.Options{
		RepoRoot:  repo,
		Accounts:  append([]string(nil), accounts...),
		CodexHome: *codexHomeFlag,
		Force:     *force,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "codex_home_sync=error root=%s message=%s\n", emptyFallback(result.Root, "(none)"), err.Error())
		os.Exit(1)
	}
	fmt.Printf("codex_home_sync=%s root=%s accounts=%s message=%s\n",
		emptyFallback(result.Status, "skip"),
		emptyFallback(result.Root, "(none)"),
		emptyFallback(strings.Join(result.Accounts, ","), "(none)"),
		emptyFallback(result.Message, "(none)"),
	)
	for _, item := range result.Results {
		fmt.Printf("codex_home_account=%s status=%s home=%s codex_home=%s message=%s\n",
			emptyFallback(item.Account, "(none)"),
			emptyFallback(item.Status, "skip"),
			emptyFallback(item.Paths.Home, "(none)"),
			emptyFallback(item.Paths.CodexHome, "(none)"),
			emptyFallback(item.Message, "(none)"),
		)
		if strings.TrimSpace(item.Paths.ConfigPath) != "" {
			fmt.Printf("codex_home_config account=%s path=%s\n", emptyFallback(item.Account, "(none)"), item.Paths.ConfigPath)
		}
		if strings.TrimSpace(item.Paths.AuthPath) != "" {
			fmt.Printf("codex_home_auth account=%s path=%s\n", emptyFallback(item.Account, "(none)"), item.Paths.AuthPath)
		}
	}
}

func timerUsage() {
	fmt.Fprintln(os.Stderr, "Timer Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer start [--docker-compose] [--node-bin node] [--repo .] [--poll-interval 30s]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer list [--docker-compose] [--repo .] --account <account>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer show <id> [--docker-compose] [--repo .] --account <account>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer run <id> [--docker-compose] [--node-bin node] [--repo .] --account <account>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer logs <id> [--docker-compose] [--repo .] --account <account> [--lines 80]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer enable <id> [--docker-compose] [--repo .] --account <account>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer disable <id> [--docker-compose] [--repo .] --account <account>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer delete <id> [--docker-compose] [--repo .] --account <account>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer upsert [--docker-compose] --id <id> --account <account> --chat-id oc_xxx (--every 1h | --daily 09:00 | --weekly mon,tue --at 09:00) --prompt \"...\" [--cwd workspace/<account-namespace>] [--add-dir workspace/shared] [--tz Asia/Shanghai] [--disable]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd timer update <id> [--docker-compose] --account <account> [--prompt \"...\"] [--every 1h | --daily 09:00 | --weekly mon,tue --at 09:00] [--cwd workspace/<account-namespace>] [--chat-id oc_xxx] [--add-dir workspace/shared] [--clear-add-dirs] [--enable|--disable]")
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  - In a bot workspace, timer commands can infer --account from .config.toml.")
	fmt.Fprintln(os.Stderr, "  - Outside a bot workspace, pass --account <account> explicitly.")
}

func memoryUsage() {
	fmt.Fprintln(os.Stderr, "Memory Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory add [--docker-compose] --account <account> --text \"...\" [--source feishu/<account>/oc_xxx] [--tag foo] [--tag bar] [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory list [--docker-compose] --account <account> [--repo .] [--limit 20]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory show <id> [--docker-compose] --account <account> [--repo .]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory search <keyword> [--docker-compose] --account <account> [--repo .] [--limit 20]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd memory delete <id> [--docker-compose] --account <account> [--repo .]")
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  - In a bot workspace, memory commands can infer --account from .config.toml.")
	fmt.Fprintln(os.Stderr, "  - Outside a bot workspace, pass --account <account> explicitly.")
}

func envUsage() {
	fmt.Fprintln(os.Stderr, "Env Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd env set [--docker-compose] [--repo .] [--scope global|account] [--account <account>] --key <KEY> (--value <VALUE> | --value-file <PATH>) [--updated-by <source>]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd env get [--docker-compose] [--repo .] [--scope auto|global|account] [--account <account>] [--raw] <KEY>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd env list [--docker-compose] [--repo .] [--scope global|account|all] [--account <account>]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd env delete [--docker-compose] [--repo .] [--scope global|account] [--account <account>] <KEY>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd env run [--docker-compose] [--repo .] [--scope auto|global|account] [--account <account>] --key <KEY> [--key <KEY> ...] -- <command> [args...]")
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  - env get/list/set default to masked output; use env get --raw only when you really need the plaintext.")
	fmt.Fprintln(os.Stderr, "  - Prefer env run when you need to pass stored secrets to another command without printing them.")
	fmt.Fprintln(os.Stderr, "  - In a bot workspace, account-scoped env commands can infer --account from .config.toml.")
}

func clawhubUsage() {
	fmt.Fprintln(os.Stderr, "ClawHub Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd clawhub search [--docker-compose] [--base-url <url>] [--timeout-sec 20] [--limit 10] [--json] <query>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd clawhub list [--docker-compose] [--base-url <url>] [--timeout-sec 20] [--sort updated|downloads|stars] [--limit 20] [--cursor <cursor>] [--json]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd clawhub show [--docker-compose] [--base-url <url>] [--timeout-sec 20] [--json] <skill-slug>")
	fmt.Fprintln(os.Stderr, "  suncodexclawd clawhub file [--docker-compose] [--base-url <url>] [--timeout-sec 20] [--version <ver>] --path <file> <skill-slug>")
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  - Default base URL is https://clawhub.ai; override with --base-url or CLAWHUB_BASE_URL.")
	fmt.Fprintln(os.Stderr, "  - For Codex skill instructions, prefer `clawhub search` first, then `clawhub file <slug> --path SKILL.md`.")
}

func syncUsage() {
	fmt.Fprintln(os.Stderr, "Sync Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd sync status [--docker-compose] [--repo .] --account <account> [--workspace workspace/<account-namespace>] [--workspace-id <account>]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd sync list-remote [--docker-compose] [--repo .] --account <account> [--workspace-id <account>]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd sync push [--docker-compose] [--repo .] --account <account> [--workspace workspace/<account-namespace>] [--workspace-id <account>] [--provider webdav] [--webdav-url https://dav.example.com/path] [--webdav-username user] [--webdav-password pass] [--webdav-base-path /SunCodexClaw/backups] [--skip-if-unconfigured]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd sync pull [--docker-compose] [--repo .] --account <account> [--workspace-id <account>] [--snapshot latest|20260320T010203Z] [--to .runtime/sync/restore/<account-namespace>/latest]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd sync restore [--docker-compose] [--repo .] --account <account> [--workspace workspace/<account-namespace>] [--workspace-id <account>] [--snapshot latest|20260320T010203Z | --from .runtime/sync/restore/<account-namespace>/latest] [--force]")
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  - In a bot workspace, sync commands can infer --account from .config.toml.")
	fmt.Fprintln(os.Stderr, "  - sync pull defaults to .runtime/sync/restore/<account-namespace>/<snapshot> when --to is omitted.")
}

func launchagentsUsage() {
	fmt.Fprintln(os.Stderr, "LaunchAgents Usage:")
	fmt.Fprintln(os.Stderr, "  suncodexclawd launchagents install [--account a] [--account b] [--repo .] [--node-bin node] [--run-mode node|supervisor] [--daemon-bin ./bin/suncodexclawd] [--prefix com.sunbelife.suncodexclaw.feishu] [--codex-bin <path>] [--codex-home <path>] [--path <PATH>] [--keepalive] [--throttle-interval 10]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd launchagents uninstall [--account a] [--account b] [--repo .] [--prefix com.sunbelife.suncodexclaw.feishu]")
	fmt.Fprintln(os.Stderr, "  suncodexclawd launchagents status [--account a] [--account b] [--repo .] [--node-bin node] [--run-mode node|supervisor] [--daemon-bin ./bin/suncodexclawd] [--prefix com.sunbelife.suncodexclaw.feishu] [--codex-bin <path>] [--codex-home <path>] [--path <PATH>] [--keepalive] [--throttle-interval 10]")
	fmt.Fprintln(os.Stderr, "Notes:")
	fmt.Fprintln(os.Stderr, "  - launchagents is a local/macOS deployment helper and does not support --docker-compose.")
	fmt.Fprintln(os.Stderr, "  - Local mode is the default; passing --local is optional and only makes the mode explicit.")
	fmt.Fprintln(os.Stderr, "  - Omitting --account defaults to enabled accounts for install, and all configured accounts for uninstall/status.")
}

func timerStart(args []string) {
	fs := flag.NewFlagSet("timer start", flag.ExitOnError)
	nodeBin := fs.String("node-bin", getenvDefault("NODE_BIN", "node"), "node binary")
	runtimeBackend := fs.String("runtime-backend", normalizeRuntimeBackend(getenvDefault("SUNCODEXCLAW_FEISHU_RUNTIME", "go")), "deprecated; Go native runtime is always used")
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
		RepoRoot:       repo,
		NodeBin:        *nodeBin,
		RuntimeBackend: *runtimeBackend,
		PollInterval:   *pollInterval,
		Output:         os.Stdout,
	})
	if err := mgr.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func timerList(args []string) {
	fs := flag.NewFlagSet("timer list", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "timer namespace / robot account name")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	store := timer.NewStore(repo)
	namespace, err := resolveScopedAccount(strings.TrimSpace(*account), "timer")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	tasks, err := store.ListTasks(namespace)
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
		st, _ := store.ReadState(task.ID, task.StorageAccount)
		_, nextRun, _ := timer.NextDue(task, st, now)
		nextText := st.NextRunAt
		if nextText == "" && !nextRun.IsZero() {
			nextText = nextRun.UTC().Format(time.RFC3339)
		}
		status := "enabled"
		if !task.Enabled {
			status = "disabled"
		}
		namespaceText := emptyFallback(strings.TrimSpace(task.StorageAccount), "global")
		fmt.Printf("%s namespace=%s status=%s action=%s account=%s schedule=%s next=%s chat_id=%s\n", task.ID, namespaceText, status, emptyFallback(strings.TrimSpace(task.Action), "feishu_codex"), task.Account, timerScheduleSummary(task.Schedule), emptyFallback(nextText, "(none)"), task.ChatID)
	}
}

func timerShow(args []string) {
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("timer show", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "timer namespace / robot account name")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	namespace, err := resolveScopedAccount(strings.TrimSpace(*account), "timer")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := timer.NewStore(repo)
	task, err := store.ReadTask(fs.Arg(0), namespace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	st, _ := store.ReadState(task.ID, task.StorageAccount)
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
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("timer delete", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "timer namespace / robot account name")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	namespace, err := resolveScopedAccount(strings.TrimSpace(*account), "timer")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := timer.NewStore(repo)
	if err := store.DeleteTask(fs.Arg(0), namespace); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("deleted=%s namespace=%s\n", fs.Arg(0), emptyFallback(namespace, "global"))
}

func timerEnableDisable(args []string, enabled bool) {
	args = reorderFlagsBeforePositionals(args, map[string]bool{
		"--enable":  true,
		"--disable": true,
	})
	name := "timer enable"
	if !enabled {
		name = "timer disable"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "timer namespace / robot account name")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	namespace, err := resolveScopedAccount(strings.TrimSpace(*account), "timer")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := timer.NewStore(repo)
	task, err := store.SetTaskEnabled(fs.Arg(0), namespace, enabled)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	action := "enabled"
	if !enabled {
		action = "disabled"
	}
	fmt.Printf("%s=%s namespace=%s schedule=%s\n", action, task.ID, emptyFallback(namespace, "global"), timerScheduleSummary(task.Schedule))
}

func timerRun(args []string) {
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("timer run", flag.ExitOnError)
	nodeBin := fs.String("node-bin", getenvDefault("NODE_BIN", "node"), "node binary")
	runtimeBackend := fs.String("runtime-backend", normalizeRuntimeBackend(getenvDefault("SUNCODEXCLAW_FEISHU_RUNTIME", "go")), "deprecated; Go native runtime is always used")
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "timer namespace / robot account name")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	namespace, err := resolveScopedAccount(strings.TrimSpace(*account), "timer")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	mgr := timer.NewManager(timer.Options{
		RepoRoot:       repo,
		NodeBin:        *nodeBin,
		RuntimeBackend: *runtimeBackend,
		Output:         os.Stdout,
	})
	if err := mgr.RunTaskNow(context.Background(), fs.Arg(0), namespace); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("run=%s namespace=%s status=ok\n", fs.Arg(0), emptyFallback(namespace, "global"))
}

func timerLogs(args []string) {
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("timer logs", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "timer namespace / robot account name")
	lines := fs.Int("lines", 80, "lines to show")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	namespace, err := resolveScopedAccount(strings.TrimSpace(*account), "timer")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := timer.NewStore(repo)
	text, err := store.ReadLogTail(fs.Arg(0), namespace, *lines)
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
	account := fs.String("account", "", "feishu account name / timer namespace")
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
	resolvedAccount, err := resolveScopedAccount(strings.TrimSpace(*account), "timer")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := timer.NewStore(repo)
	taskPrompt, _, err := resolveOptionalFileBackedText(*prompt, *promptFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
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
		Account:         resolvedAccount,
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
	namespaceAccount := resolvedAccount
	if existing, err := store.ReadTask(task.ID, namespaceAccount); err == nil {
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
	if err := store.WriteTask(task, namespaceAccount); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("upserted=%s schedule=%s chat_id=%s enabled=%t\n", task.ID, timerScheduleSummary(task.Schedule), task.ChatID, task.Enabled)
}

func timerUpdate(args []string) {
	args = reorderFlagsBeforePositionals(args, map[string]bool{
		"--enable":         true,
		"--disable":        true,
		"--clear-add-dirs": true,
	})
	fs := flag.NewFlagSet("timer update", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "timer namespace / robot account name")
	chatID := fs.String("chat-id", "", "destination chat id")
	prompt := fs.String("prompt", "", "task prompt")
	promptFile := fs.String("prompt-file", "", "task prompt file")
	cwd := fs.String("cwd", "", "working directory")
	every := fs.String("every", "", "interval schedule, e.g. 1h")
	daily := fs.String("daily", "", "daily schedule time, e.g. 09:00")
	weekly := fs.String("weekly", "", "weekly schedule weekdays, e.g. mon,tue,fri")
	at := fs.String("at", "", "time for weekly/daily schedule, e.g. 09:00")
	tz := fs.String("tz", "", "timezone, e.g. Asia/Shanghai")
	model := fs.String("model", "", "optional codex model override")
	reasoning := fs.String("reasoning-effort", "", "optional codex reasoning effort override")
	enable := fs.Bool("enable", false, "enable task after update")
	disable := fs.Bool("disable", false, "disable task after update")
	clearAddDirs := fs.Bool("clear-add-dirs", false, "clear existing add_dirs")
	updatedBy := fs.String("updated-by", "", "free-form updater label")
	var addDirs multiFlag
	fs.Var(&addDirs, "add-dir", "additional directory (repeatable)")
	_ = fs.Parse(args)

	if *enable && *disable {
		fmt.Fprintln(os.Stderr, "error: choose at most one of --enable or --disable")
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		timerUsage()
		os.Exit(2)
	}

	repo := resolveRepoRoot(*repoFlag)
	store := timer.NewStore(repo)
	namespaceAccount, err := resolveScopedAccount(strings.TrimSpace(*account), "timer")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	existing, err := store.ReadTask(fs.Arg(0), namespaceAccount)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	nextTask, err := buildUpdatedTimerTask(existing, timerUpdateInput{
		Account:      *account,
		ChatID:       *chatID,
		Prompt:       *prompt,
		PromptFile:   *promptFile,
		Cwd:          *cwd,
		Every:        *every,
		Daily:        *daily,
		Weekly:       *weekly,
		At:           *at,
		TZ:           *tz,
		Model:        *model,
		Reasoning:    *reasoning,
		Enable:       *enable,
		Disable:      *disable,
		AddDirs:      addDirs,
		ClearAddDirs: *clearAddDirs,
		UpdatedBy:    *updatedBy,
	}, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	if err := store.WriteTask(nextTask, namespaceAccount); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("updated=%s schedule=%s chat_id=%s enabled=%t\n", nextTask.ID, timerScheduleSummary(nextTask.Schedule), nextTask.ChatID, nextTask.Enabled)
}

type timerUpdateInput struct {
	Account      string
	ChatID       string
	Prompt       string
	PromptFile   string
	Cwd          string
	Every        string
	Daily        string
	Weekly       string
	At           string
	TZ           string
	Model        string
	Reasoning    string
	Enable       bool
	Disable      bool
	AddDirs      []string
	ClearAddDirs bool
	UpdatedBy    string
}

func buildUpdatedTimerTask(existing timer.Task, in timerUpdateInput, now string) (timer.Task, error) {
	task := existing
	task.UpdatedAt = now
	if strings.TrimSpace(in.UpdatedBy) != "" {
		task.LastUpdatedBy = strings.TrimSpace(in.UpdatedBy)
	}
	if strings.TrimSpace(in.Account) != "" {
		task.Account = strings.TrimSpace(in.Account)
	}
	if strings.TrimSpace(in.ChatID) != "" {
		task.ChatID = strings.TrimSpace(in.ChatID)
	}
	if strings.TrimSpace(in.Cwd) != "" {
		task.Cwd = strings.TrimSpace(in.Cwd)
	}
	if strings.TrimSpace(in.Model) != "" {
		task.Model = strings.TrimSpace(in.Model)
	}
	if strings.TrimSpace(in.Reasoning) != "" {
		task.ReasoningEffort = strings.TrimSpace(in.Reasoning)
	}
	if in.Enable {
		task.Enabled = true
	}
	if in.Disable {
		task.Enabled = false
	}
	if in.ClearAddDirs {
		task.AddDirs = nil
	}
	if len(normalizeStringSlice(in.AddDirs)) > 0 {
		task.AddDirs = normalizeStringSlice(in.AddDirs)
	}
	if promptValue, provided, err := resolveOptionalFileBackedText(in.Prompt, in.PromptFile); err != nil {
		return timer.Task{}, err
	} else if provided {
		task.Prompt = promptValue
	}
	if schedule, changed, err := resolveUpdatedTimerSchedule(existing.Schedule, in.Every, in.Daily, in.Weekly, in.At, in.TZ); err != nil {
		return timer.Task{}, err
	} else if changed {
		task.Schedule = schedule
	}
	return task, nil
}

func resolveOptionalFileBackedText(text, filePath string) (string, bool, error) {
	if strings.TrimSpace(filePath) != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return "", false, err
		}
		return strings.TrimSpace(string(b)), true, nil
	}
	if strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text), true, nil
	}
	return "", false, nil
}

func resolveUpdatedTimerSchedule(existing timer.Schedule, every, daily, weekly, at, tz string) (timer.Schedule, bool, error) {
	every = strings.TrimSpace(every)
	daily = strings.TrimSpace(daily)
	weekly = strings.TrimSpace(weekly)
	at = strings.TrimSpace(at)
	tz = strings.TrimSpace(tz)

	modeCount := 0
	for _, raw := range []string{every, daily, weekly} {
		if raw != "" {
			modeCount++
		}
	}
	if modeCount > 1 {
		return timer.Schedule{}, false, fmt.Errorf("choose at most one schedule mode for update: --every | --daily | --weekly")
	}
	if modeCount == 0 && at == "" && tz == "" {
		return existing, false, nil
	}

	if every != "" {
		return timer.Schedule{
			Kind:     "interval",
			Every:    every,
			Timezone: fallbackString(tz, existing.Timezone),
		}, true, nil
	}
	if daily != "" {
		return timer.Schedule{
			Kind:     "daily",
			At:       daily,
			Timezone: fallbackString(tz, existing.Timezone),
		}, true, nil
	}
	if weekly != "" {
		weekdays := splitCSVValues(weekly)
		if len(weekdays) == 0 {
			return timer.Schedule{}, false, fmt.Errorf("weekly schedule requires weekdays")
		}
		atValue := at
		if atValue == "" {
			atValue = existing.At
		}
		if strings.TrimSpace(atValue) == "" {
			return timer.Schedule{}, false, fmt.Errorf("weekly schedule requires --at HH:MM")
		}
		return timer.Schedule{
			Kind:     "weekly",
			Weekdays: weekdays,
			At:       atValue,
			Timezone: fallbackString(tz, existing.Timezone),
		}, true, nil
	}

	schedule := existing
	switch strings.TrimSpace(existing.Kind) {
	case "daily":
		if at != "" {
			schedule.At = at
		}
		if tz != "" {
			schedule.Timezone = tz
		}
		return schedule, true, nil
	case "weekly":
		if at != "" {
			schedule.At = at
		}
		if tz != "" {
			schedule.Timezone = tz
		}
		return schedule, true, nil
	default:
		return timer.Schedule{}, false, fmt.Errorf("timer %s schedule kind does not support partial update with --at/--tz only", existing.Kind)
	}
}

func memoryAdd(args []string) {
	fs := flag.NewFlagSet("memory add", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "memory library / robot account name")
	text := fs.String("text", "", "memory text")
	textFile := fs.String("text-file", "", "memory text file")
	source := fs.String("source", "", "memory source label")
	var tags multiFlag
	fs.Var(&tags, "tag", "memory tag (repeatable)")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveMemoryAccount(strings.TrimSpace(*account))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
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
	store := memory.NewLibraryStore(repo, resolvedAccount)
	entry, err := store.Add(memoryText, *source, tags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("library=%s added=%s source=%s tags=%s text=%s\n", sanitizePathSegment(resolvedAccount), entry.ID, emptyFallback(entry.Source, "(none)"), emptyFallback(strings.Join(entry.Tags, ","), "(none)"), compactSingleLine(entry.Text, 120))
}

func memoryList(args []string) {
	fs := flag.NewFlagSet("memory list", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "memory library / robot account name")
	limit := fs.Int("limit", 20, "max memories to show")
	_ = fs.Parse(args)
	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveMemoryAccount(strings.TrimSpace(*account))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := memory.NewLibraryStore(repo, resolvedAccount)
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
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("memory show", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "memory library / robot account name")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		memoryUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveMemoryAccount(strings.TrimSpace(*account))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := memory.NewLibraryStore(repo, resolvedAccount)
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
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("memory search", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "memory library / robot account name")
	limit := fs.Int("limit", 20, "max memories to show")
	_ = fs.Parse(args)
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "error: search keyword is required")
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveMemoryAccount(strings.TrimSpace(*account))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := memory.NewLibraryStore(repo, resolvedAccount)
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
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("memory delete", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "memory library / robot account name")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		memoryUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveMemoryAccount(strings.TrimSpace(*account))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := memory.NewLibraryStore(repo, resolvedAccount)
	if err := store.DeleteEntry(fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("deleted=%s\n", fs.Arg(0))
}

func envSet(args []string) {
	fs := flag.NewFlagSet("env set", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	scope := fs.String("scope", envstore.ScopeAccount, "env scope: global|account")
	account := fs.String("account", "", "robot account name for account scope")
	key := fs.String("key", "", "env key name")
	value := fs.String("value", "", "env value")
	valueFile := fs.String("value-file", "", "path to file containing env value")
	updatedBy := fs.String("updated-by", "", "audit source label")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	resolvedScope, resolvedAccount, err := resolveEnvScopeAndAccount(strings.TrimSpace(*scope), strings.TrimSpace(*account), true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	valueText := *value
	if strings.TrimSpace(*valueFile) != "" {
		body, err := os.ReadFile(strings.TrimSpace(*valueFile))
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		valueText = string(body)
	}
	if strings.TrimSpace(*key) == "" {
		fmt.Fprintln(os.Stderr, "error: --key is required")
		os.Exit(2)
	}
	store := envstore.NewStore(repo)
	entry, err := store.Set(resolvedScope, resolvedAccount, strings.TrimSpace(*key), valueText, strings.TrimSpace(*updatedBy))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("scope=%s account=%s key=%s value=%s updated_at=%s\n",
		entry.Scope,
		emptyFallback(entry.Account, "(none)"),
		entry.Key,
		envstore.MaskedValue(entry.Value),
		emptyFallback(entry.UpdatedAt, "(none)"),
	)
}

func envGet(args []string) {
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("env get", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	scope := fs.String("scope", envstore.ScopeAuto, "env scope: auto|global|account")
	account := fs.String("account", "", "robot account name for account/auto scope")
	raw := fs.Bool("raw", false, "print plaintext value")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		envUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	resolvedScope, resolvedAccount, err := resolveEnvScopeAndAccount(strings.TrimSpace(*scope), strings.TrimSpace(*account), false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := envstore.NewStore(repo)
	entry, err := getEnvEntry(store, resolvedScope, resolvedAccount, fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if *raw {
		fmt.Print(entry.Value)
		return
	}
	fmt.Printf("scope=%s account=%s key=%s value=%s updated_at=%s\n",
		entry.Scope,
		emptyFallback(entry.Account, "(none)"),
		entry.Key,
		envstore.MaskedValue(entry.Value),
		emptyFallback(entry.UpdatedAt, "(none)"),
	)
}

func envList(args []string) {
	fs := flag.NewFlagSet("env list", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	scope := fs.String("scope", envstore.ScopeAll, "env scope: global|account|all")
	account := fs.String("account", "", "robot account name for account/all scope")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	resolvedScope, resolvedAccount, err := resolveEnvScopeAndAccount(strings.TrimSpace(*scope), strings.TrimSpace(*account), false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := envstore.NewStore(repo)
	entries, err := store.List(resolvedScope, resolvedAccount)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("(no env entries)")
		return
	}
	for _, entry := range entries {
		fmt.Printf("scope=%s account=%s key=%s value=%s updated_at=%s\n",
			entry.Scope,
			emptyFallback(entry.Account, "(none)"),
			entry.Key,
			envstore.MaskedValue(entry.Value),
			emptyFallback(entry.UpdatedAt, "(none)"),
		)
	}
}

func envDelete(args []string) {
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("env delete", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	scope := fs.String("scope", envstore.ScopeAccount, "env scope: global|account")
	account := fs.String("account", "", "robot account name for account scope")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		envUsage()
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	resolvedScope, resolvedAccount, err := resolveEnvScopeAndAccount(strings.TrimSpace(*scope), strings.TrimSpace(*account), true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := envstore.NewStore(repo)
	if err := store.Delete(resolvedScope, resolvedAccount, fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("deleted=true scope=%s account=%s key=%s\n", resolvedScope, emptyFallback(resolvedAccount, "(none)"), fs.Arg(0))
}

func envRun(args []string) {
	fs := flag.NewFlagSet("env run", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	scope := fs.String("scope", envstore.ScopeAuto, "env scope: auto|global|account")
	account := fs.String("account", "", "robot account name for account/auto scope")
	var keys multiFlag
	fs.Var(&keys, "key", "env key to inject as same-named process env (repeatable)")
	_ = fs.Parse(args)
	if len(keys) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one --key is required")
		os.Exit(2)
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "error: missing command after env run")
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	resolvedScope, resolvedAccount, err := resolveEnvScopeAndAccount(strings.TrimSpace(*scope), strings.TrimSpace(*account), false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	store := envstore.NewStore(repo)
	childEnv := os.Environ()
	for _, key := range keys {
		entries, err := resolveEnvRunEntries(store, resolvedScope, resolvedAccount, key)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		for _, entry := range entries {
			childEnv = appendEnvOverride(childEnv, entry.Key, entry.Value)
		}
	}
	cmd := exec.Command(fs.Arg(0), fs.Args()[1:]...)
	cmd.Env = childEnv
	cmd.Dir = repo
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func clawhubCommon(fs *flag.FlagSet) (*string, *int) {
	baseURL := fs.String("base-url", getenvDefault("CLAWHUB_BASE_URL", clawhub.DefaultBaseURL), "ClawHub base URL")
	timeoutSec := fs.Int("timeout-sec", 20, "HTTP timeout in seconds")
	return baseURL, timeoutSec
}

func newClawHubClient(baseURL string, timeoutSec int) (*clawhub.Client, error) {
	timeout := 20 * time.Second
	if timeoutSec > 0 {
		timeout = time.Duration(timeoutSec) * time.Second
	}
	return clawhub.NewClient(baseURL, &http.Client{Timeout: timeout})
}

func clawhubSearch(args []string) {
	args = reorderFlagsBeforePositionals(args, map[string]bool{"--json": true})
	fs := flag.NewFlagSet("clawhub search", flag.ExitOnError)
	baseURL, timeoutSec := clawhubCommon(fs)
	limit := fs.Int("limit", 10, "max results")
	jsonOut := fs.Bool("json", false, "print raw JSON")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		clawhubUsage()
		os.Exit(2)
	}
	client, err := newClawHubClient(*baseURL, *timeoutSec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	payload, err := client.Search(context.Background(), strings.Join(fs.Args(), " "), *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	printClawHubPayload(payload, *jsonOut, formatClawHubSearchPayload(payload))
}

func clawhubList(args []string) {
	args = reorderFlagsBeforePositionals(args, map[string]bool{"--json": true})
	fs := flag.NewFlagSet("clawhub list", flag.ExitOnError)
	baseURL, timeoutSec := clawhubCommon(fs)
	sortBy := fs.String("sort", "updated", "sort order")
	limit := fs.Int("limit", 20, "max results")
	cursor := fs.String("cursor", "", "pagination cursor")
	jsonOut := fs.Bool("json", false, "print raw JSON")
	_ = fs.Parse(args)
	client, err := newClawHubClient(*baseURL, *timeoutSec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	payload, err := client.List(context.Background(), *sortBy, *limit, *cursor)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	printClawHubPayload(payload, *jsonOut, formatClawHubListPayload(payload))
}

func clawhubShow(args []string) {
	args = reorderFlagsBeforePositionals(args, map[string]bool{"--json": true})
	fs := flag.NewFlagSet("clawhub show", flag.ExitOnError)
	baseURL, timeoutSec := clawhubCommon(fs)
	jsonOut := fs.Bool("json", false, "print raw JSON")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		clawhubUsage()
		os.Exit(2)
	}
	client, err := newClawHubClient(*baseURL, *timeoutSec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	payload, err := client.Show(context.Background(), fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	printClawHubPayload(payload, *jsonOut, formatClawHubShowPayload(payload))
}

func clawhubFile(args []string) {
	args = reorderFlagsBeforePositionals(args, nil)
	fs := flag.NewFlagSet("clawhub file", flag.ExitOnError)
	baseURL, timeoutSec := clawhubCommon(fs)
	version := fs.String("version", "", "skill version")
	skillPath := fs.String("path", "", "file path inside skill package")
	_ = fs.Parse(args)
	if strings.TrimSpace(*skillPath) == "" || fs.NArg() != 1 {
		clawhubUsage()
		os.Exit(2)
	}
	client, err := newClawHubClient(*baseURL, *timeoutSec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	body, err := client.File(context.Background(), fs.Arg(0), *skillPath, *version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Print(string(body))
}

func syncStatusCmd(args []string) {
	fs := flag.NewFlagSet("sync status", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "feishu account name")
	workspace := fs.String("workspace", "", "workspace directory to back up")
	workspaceID := fs.String("workspace-id", "", "logical workspace id for remote paths")
	provider := fs.String("provider", "", "sync provider (default: webdav)")
	webdavURL := fs.String("webdav-url", "", "webdav base url")
	webdavBasePath := fs.String("webdav-base-path", "", "remote base path under webdav")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveScopedAccount(strings.TrimSpace(*account), "sync")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	cfg, workspaceDir, err := loadSyncConfig(repo, resolvedAccount, syncFlagConfig{
		Workspace:      *workspace,
		WorkspaceID:    *workspaceID,
		Provider:       *provider,
		WebDAVURL:      *webdavURL,
		WebDAVBasePath: *webdavBasePath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	mgr := worksync.NewManager(worksync.Options{
		RepoRoot:     repo,
		WorkspaceDir: workspaceDir,
		WorkspaceID:  cfg.WorkspaceID,
	})
	status, err := mgr.Status(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("provider=%s configured=%t\n", emptyFallback(status.Provider, "(none)"), status.Configured)
	fmt.Printf("workspace=%s\n", status.WorkspaceDir)
	fmt.Printf("workspace_id=%s\n", status.WorkspaceID)
	fmt.Printf("state=%s\n", status.StatePath)
	fmt.Printf("remote_base=%s\n", emptyFallback(status.RemoteBase, "(not configured)"))
	fmt.Printf("last_push_at=%s\n", emptyFallback(status.LastPushAt, "(never)"))
	for _, item := range status.Files {
		fmt.Printf("%s exists=%t size=%d sha256=%s path=%s\n", item.Name, item.Exists, item.Size, emptyFallback(item.SHA256, "(none)"), item.Path)
	}
}

func syncPushCmd(args []string) {
	fs := flag.NewFlagSet("sync push", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "feishu account name")
	workspace := fs.String("workspace", "", "workspace directory to back up")
	workspaceID := fs.String("workspace-id", "", "logical workspace id for remote paths")
	provider := fs.String("provider", "", "sync provider (default: webdav)")
	webdavURL := fs.String("webdav-url", "", "webdav base url")
	webdavUsername := fs.String("webdav-username", "", "webdav username")
	webdavPassword := fs.String("webdav-password", "", "webdav password")
	webdavBasePath := fs.String("webdav-base-path", "", "remote base path under webdav")
	timeoutSec := fs.Int("timeout-sec", 30, "http timeout in seconds")
	skipIfUnconfigured := fs.Bool("skip-if-unconfigured", false, "exit successfully when sync backend is not configured")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveScopedAccount(strings.TrimSpace(*account), "sync")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	cfg, workspaceDir, err := loadSyncConfig(repo, resolvedAccount, syncFlagConfig{
		Workspace:      *workspace,
		WorkspaceID:    *workspaceID,
		Provider:       *provider,
		WebDAVURL:      *webdavURL,
		WebDAVUsername: *webdavUsername,
		WebDAVPassword: *webdavPassword,
		WebDAVBasePath: *webdavBasePath,
		TimeoutSeconds: *timeoutSec,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if !syncConfigReady(cfg) {
		if *skipIfUnconfigured {
			fmt.Println("status=skipped reason=sync_not_configured")
			fmt.Printf("workspace=%s\n", workspaceDir)
			fmt.Printf("workspace_id=%s\n", cfg.WorkspaceID)
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "error: sync backend is not configured")
		os.Exit(1)
	}
	mgr := worksync.NewManager(worksync.Options{
		RepoRoot:     repo,
		WorkspaceDir: workspaceDir,
		WorkspaceID:  cfg.WorkspaceID,
	})
	result, err := mgr.Push(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("provider=%s\n", cfg.Provider)
	fmt.Printf("workspace=%s\n", result.WorkspaceDir)
	fmt.Printf("workspace_id=%s\n", result.WorkspaceID)
	fmt.Printf("remote_base=%s\n", result.RemoteBase)
	fmt.Printf("snapshot=%s\n", result.Snapshot)
	fmt.Printf("state=%s\n", result.StatePath)
	for _, item := range result.Uploaded {
		fmt.Printf("uploaded=%s size=%d sha256=%s latest=%s snapshot_path=%s\n", item.Name, item.Size, item.SHA256, item.LatestPath, item.SnapshotPath)
	}
	if len(result.Missing) > 0 {
		fmt.Printf("missing=%s\n", strings.Join(result.Missing, ","))
	}
	fmt.Println("status=ok")
}

func syncListRemoteCmd(args []string) {
	fs := flag.NewFlagSet("sync list-remote", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "feishu account name")
	workspaceID := fs.String("workspace-id", "", "logical workspace id for remote paths")
	provider := fs.String("provider", "", "sync provider (default: webdav)")
	webdavURL := fs.String("webdav-url", "", "webdav base url")
	webdavUsername := fs.String("webdav-username", "", "webdav username")
	webdavPassword := fs.String("webdav-password", "", "webdav password")
	webdavBasePath := fs.String("webdav-base-path", "", "remote base path under webdav")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveScopedAccount(strings.TrimSpace(*account), "sync")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	cfg, _, err := loadSyncConfig(repo, resolvedAccount, syncFlagConfig{
		WorkspaceID:    *workspaceID,
		Provider:       *provider,
		WebDAVURL:      *webdavURL,
		WebDAVUsername: *webdavUsername,
		WebDAVPassword: *webdavPassword,
		WebDAVBasePath: *webdavBasePath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if !syncConfigReady(cfg) {
		fmt.Fprintln(os.Stderr, "error: sync backend is not configured")
		os.Exit(1)
	}
	mgr := worksync.NewManager(worksync.Options{
		RepoRoot:     repo,
		WorkspaceDir: filepath.Join(repo, "workspace"),
		WorkspaceID:  cfg.WorkspaceID,
	})
	result, err := mgr.ListRemote(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("workspace_id=%s\n", result.WorkspaceID)
	fmt.Printf("remote_base=%s\n", result.RemoteBase)
	if len(result.Snapshots) == 0 {
		fmt.Println("(no remote snapshots)")
		return
	}
	for _, item := range result.Snapshots {
		fmt.Printf("snapshot=%s\n", item.Name)
	}
}

func syncPullCmd(args []string) {
	fs := flag.NewFlagSet("sync pull", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "feishu account name")
	workspaceID := fs.String("workspace-id", "", "logical workspace id for remote paths")
	snapshot := fs.String("snapshot", "latest", "remote snapshot name or latest")
	targetDir := fs.String("to", "", "local target dir for pulled files")
	provider := fs.String("provider", "", "sync provider (default: webdav)")
	webdavURL := fs.String("webdav-url", "", "webdav base url")
	webdavUsername := fs.String("webdav-username", "", "webdav username")
	webdavPassword := fs.String("webdav-password", "", "webdav password")
	webdavBasePath := fs.String("webdav-base-path", "", "remote base path under webdav")
	_ = fs.Parse(args)

	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveScopedAccount(strings.TrimSpace(*account), "sync")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	cfg, _, err := loadSyncConfig(repo, resolvedAccount, syncFlagConfig{
		WorkspaceID:    *workspaceID,
		Provider:       *provider,
		WebDAVURL:      *webdavURL,
		WebDAVUsername: *webdavUsername,
		WebDAVPassword: *webdavPassword,
		WebDAVBasePath: *webdavBasePath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if !syncConfigReady(cfg) {
		fmt.Fprintln(os.Stderr, "error: sync backend is not configured")
		os.Exit(1)
	}
	target := strings.TrimSpace(*targetDir)
	if target == "" {
		target = defaultSyncPullTarget(resolvedAccount, strings.TrimSpace(*snapshot))
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(repo, target)
	}
	mgr := worksync.NewManager(worksync.Options{
		RepoRoot:     repo,
		WorkspaceDir: filepath.Join(repo, "workspace"),
		WorkspaceID:  cfg.WorkspaceID,
	})
	result, err := mgr.Pull(context.Background(), cfg, strings.TrimSpace(*snapshot), target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("workspace_id=%s\n", result.WorkspaceID)
	fmt.Printf("remote_base=%s\n", result.RemoteBase)
	fmt.Printf("snapshot=%s\n", result.Snapshot)
	fmt.Printf("target=%s\n", result.TargetDir)
	for _, item := range result.Files {
		fmt.Printf("pulled=%s size=%d path=%s\n", item.Name, item.Size, item.Path)
	}
	fmt.Println("status=ok")
}

func syncRestoreCmd(args []string) {
	fs := flag.NewFlagSet("sync restore", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "repo root (default: auto-detect from cwd)")
	account := fs.String("account", "", "feishu account name")
	workspace := fs.String("workspace", "", "workspace directory to restore into")
	workspaceID := fs.String("workspace-id", "", "logical workspace id for remote paths")
	snapshot := fs.String("snapshot", "", "remote snapshot name or latest")
	fromDir := fs.String("from", "", "local pulled directory to restore from")
	force := fs.Bool("force", false, "overwrite existing workspace documents")
	provider := fs.String("provider", "", "sync provider (default: webdav)")
	webdavURL := fs.String("webdav-url", "", "webdav base url")
	webdavUsername := fs.String("webdav-username", "", "webdav username")
	webdavPassword := fs.String("webdav-password", "", "webdav password")
	webdavBasePath := fs.String("webdav-base-path", "", "remote base path under webdav")
	_ = fs.Parse(args)

	if strings.TrimSpace(*snapshot) != "" && strings.TrimSpace(*fromDir) != "" {
		fmt.Fprintln(os.Stderr, "error: choose one of --snapshot or --from")
		os.Exit(2)
	}
	repo := resolveRepoRoot(*repoFlag)
	resolvedAccount, err := resolveScopedAccount(strings.TrimSpace(*account), "sync")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	cfg, workspaceDir, err := loadSyncConfig(repo, resolvedAccount, syncFlagConfig{
		Workspace:      *workspace,
		WorkspaceID:    *workspaceID,
		Provider:       *provider,
		WebDAVURL:      *webdavURL,
		WebDAVUsername: *webdavUsername,
		WebDAVPassword: *webdavPassword,
		WebDAVBasePath: *webdavBasePath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	mgr := worksync.NewManager(worksync.Options{
		RepoRoot:     repo,
		WorkspaceDir: workspaceDir,
		WorkspaceID:  cfg.WorkspaceID,
	})

	sourceDir := strings.TrimSpace(*fromDir)
	cleanupDir := ""
	if sourceDir == "" {
		if !syncConfigReady(cfg) {
			fmt.Fprintln(os.Stderr, "error: sync backend is not configured")
			os.Exit(1)
		}
		restoreSnapshot := strings.TrimSpace(*snapshot)
		if restoreSnapshot == "" {
			restoreSnapshot = "latest"
		}
		stageName := restoreSnapshot
		if stageName == "" {
			stageName = "latest"
		}
		stageName = sanitizePathSegment(stageName)
		cleanupDir = filepath.Join(repo, ".runtime", "sync", cfg.WorkspaceID, "restore", ".staging", stageName+"-"+time.Now().UTC().Format("20060102T150405Z"))
		pullResult, err := mgr.Pull(context.Background(), cfg, restoreSnapshot, cleanupDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		sourceDir = pullResult.TargetDir
	}
	if !filepath.IsAbs(sourceDir) {
		sourceDir = filepath.Join(repo, sourceDir)
	}
	result, err := mgr.Restore(sourceDir, *force)
	if cleanupDir != "" {
		defer os.RemoveAll(cleanupDir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("workspace_id=%s\n", result.WorkspaceID)
	fmt.Printf("workspace=%s\n", result.WorkspaceDir)
	fmt.Printf("source=%s\n", result.SourceDir)
	for _, item := range result.Files {
		fmt.Printf("restored=%s size=%d path=%s\n", item.Name, item.Size, item.Path)
	}
	for _, item := range result.Skipped {
		fmt.Printf("skipped=%s path=%s reason=exists\n", item.Name, item.Path)
	}
	fmt.Println("status=ok")
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
	parts = splitCSVValues(weekly)
	return timer.Schedule{Kind: "weekly", Weekdays: parts, At: strings.TrimSpace(at), Timezone: strings.TrimSpace(tz)}, nil
}

func splitCSVValues(raw string) []string {
	parts := []string{}
	for _, item := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
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

func compactText(value string, max int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(value)
}

func sanitizePathSegment(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-", "..", "-")
	text = replacer.Replace(text)
	text = strings.Trim(text, "-.")
	if text == "" {
		return "default"
	}
	return text
}

func resolveScopedAccount(explicit string, scope string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err == nil {
		account, cfgPath, cfgErr := configstore.ResolveRuntimeAccountFromDir(cwd, scope)
		if cfgErr != nil {
			return "", fmt.Errorf("failed to read runtime account from %s: %w", cfgPath, cfgErr)
		}
		if strings.TrimSpace(account) != "" {
			return account, nil
		}
		if strings.TrimSpace(cfgPath) != "" {
			return "", fmt.Errorf("--account <account> is required; no %s account was found in %s. Run the command inside a bot workspace with .config.toml or pass --account explicitly", emptyFallback(strings.TrimSpace(scope), "runtime"), cfgPath)
		}
	}
	return "", fmt.Errorf("--account <account> is required; run the command inside a bot workspace with .config.toml or pass --account explicitly")
}

func resolveMemoryAccount(explicit string) (string, error) {
	return resolveScopedAccount(explicit, "memory")
}

func tryResolveScopedAccount(explicit string, scope string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err == nil {
		account, cfgPath, cfgErr := configstore.ResolveRuntimeAccountFromDir(cwd, scope)
		if cfgErr != nil {
			return "", fmt.Errorf("failed to read runtime account from %s: %w", cfgPath, cfgErr)
		}
		if strings.TrimSpace(account) != "" {
			return account, nil
		}
	}
	return "", nil
}

func resolveEnvScopeAndAccount(scope string, explicitAccount string, requireAccountForScoped bool) (string, string, error) {
	scope = strings.TrimSpace(strings.ToLower(scope))
	if scope == "" {
		scope = envstore.ScopeAuto
	}
	switch scope {
	case envstore.ScopeGlobal:
		return envstore.ScopeGlobal, "", nil
	case envstore.ScopeAccount:
		account, err := tryResolveScopedAccount(explicitAccount, "env")
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(account) == "" && requireAccountForScoped {
			return "", "", fmt.Errorf("--account <account> is required for account-scoped env commands; run the command inside a bot workspace with .config.toml or pass --account explicitly")
		}
		return envstore.ScopeAccount, strings.TrimSpace(account), nil
	case envstore.ScopeAll:
		account, err := tryResolveScopedAccount(explicitAccount, "env")
		if err != nil {
			return "", "", err
		}
		return envstore.ScopeAll, strings.TrimSpace(account), nil
	case envstore.ScopeAuto:
		account, err := tryResolveScopedAccount(explicitAccount, "env")
		if err != nil {
			return "", "", err
		}
		return envstore.ScopeAuto, strings.TrimSpace(account), nil
	default:
		return "", "", fmt.Errorf("invalid --scope %q, expected auto|global|account|all", scope)
	}
}

func getEnvEntry(store *envstore.Store, scope, account, key string) (envstore.Entry, error) {
	switch strings.TrimSpace(scope) {
	case envstore.ScopeGlobal:
		return store.Get(envstore.ScopeGlobal, "", key)
	case envstore.ScopeAccount:
		if strings.TrimSpace(account) == "" {
			return envstore.Entry{}, fmt.Errorf("account scope requires account")
		}
		return store.Get(envstore.ScopeAccount, account, key)
	case envstore.ScopeAuto:
		return store.Resolve(account, key)
	default:
		return envstore.Entry{}, fmt.Errorf("invalid env scope %q", scope)
	}
}

func resolveEnvRunEntries(store *envstore.Store, scope, account, key string) ([]envstore.Entry, error) {
	switch strings.TrimSpace(scope) {
	case envstore.ScopeGlobal:
		entry, err := store.Get(envstore.ScopeGlobal, "", key)
		if err != nil {
			return nil, err
		}
		return []envstore.Entry{entry}, nil
	case envstore.ScopeAccount:
		if strings.TrimSpace(account) == "" {
			return nil, fmt.Errorf("account scope requires account")
		}
		entry, err := store.Get(envstore.ScopeAccount, account, key)
		if err != nil {
			return nil, err
		}
		return []envstore.Entry{entry}, nil
	case envstore.ScopeAuto:
		return resolveAutoEnvRunEntries(store, account, key)
	default:
		return nil, fmt.Errorf("invalid env scope %q", scope)
	}
}

func resolveAutoEnvRunEntries(store *envstore.Store, account, key string) ([]envstore.Entry, error) {
	account = strings.TrimSpace(account)
	entries := []envstore.Entry{}
	var accountErr error
	if account != "" {
		globalEntry, err := store.Get(envstore.ScopeGlobal, "", key)
		if err == nil {
			entries = append(entries, globalEntry)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		accountEntry, err := store.Get(envstore.ScopeAccount, account, key)
		if err == nil {
			entries = append(entries, accountEntry)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		} else {
			accountErr = err
		}
		if len(entries) > 0 {
			return entries, nil
		}
		if accountErr != nil {
			return nil, accountErr
		}
		return nil, os.ErrNotExist
	}
	entry, err := store.Get(envstore.ScopeGlobal, "", key)
	if err != nil {
		return nil, err
	}
	return []envstore.Entry{entry}, nil
}

func appendEnvOverride(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func printClawHubPayload(payload map[string]any, jsonOut bool, fallbackText string) {
	if jsonOut {
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(string(body))
		return
	}
	if strings.TrimSpace(fallbackText) == "" {
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(string(body))
		return
	}
	fmt.Println(fallbackText)
}

func formatClawHubSearchPayload(payload map[string]any) string {
	results := clawhubResultList(payload)
	if len(results) == 0 {
		return "(no clawhub search results)"
	}
	lines := make([]string, 0, len(results))
	for _, item := range results {
		slug := clawhubString(item, "slug", "id")
		name := clawhubString(item, "name", "title")
		description := compactText(clawhubString(item, "description", "summary"), 180)
		version := clawhubString(item, "latestVersion", "version")
		updatedAt := clawhubString(item, "updatedAt", "updated_at")
		score := clawhubNumberString(item, "score")
		line := joinNonEmpty(" | ", []string{
			emptyFallback(slug, "(unknown-slug)"),
			name,
			version,
			score,
			updatedAt,
		})
		if description != "" {
			line += " | " + description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatClawHubListPayload(payload map[string]any) string {
	results := clawhubResultList(payload)
	if len(results) == 0 {
		return "(no clawhub skills)"
	}
	lines := make([]string, 0, len(results)+1)
	for _, item := range results {
		slug := clawhubString(item, "slug", "id")
		name := clawhubString(item, "name", "title")
		version := clawhubString(item, "latestVersion", "version")
		updatedAt := clawhubString(item, "updatedAt", "updated_at")
		lines = append(lines, joinNonEmpty(" | ", []string{
			emptyFallback(slug, "(unknown-slug)"),
			name,
			version,
			updatedAt,
		}))
	}
	if nextCursor := clawhubString(payload, "nextCursor", "cursor", "next_cursor"); nextCursor != "" {
		lines = append(lines, "next_cursor="+nextCursor)
	}
	return strings.Join(lines, "\n")
}

func formatClawHubShowPayload(payload map[string]any) string {
	skill := clawhubNestedMap(payload, "skill")
	if len(skill) == 0 {
		return ""
	}
	lines := []string{
		"slug=" + emptyFallback(clawhubString(skill, "slug", "id"), "(unknown)"),
		"name=" + emptyFallback(clawhubString(skill, "name", "title"), "(unnamed)"),
		"latest_version=" + emptyFallback(clawhubString(skill, "latestVersion", "version"), "(unknown)"),
	}
	if owner := clawhubString(skill, "owner", "author"); owner != "" {
		lines = append(lines, "owner="+owner)
	}
	if summary := compactText(clawhubString(skill, "description", "summary"), 400); summary != "" {
		lines = append(lines, "summary="+summary)
	}
	return strings.Join(lines, "\n")
}

func clawhubResultList(payload map[string]any) []map[string]any {
	for _, key := range []string{"results", "skills", "items", "data"} {
		if raw, ok := payload[key]; ok {
			if list := clawhubAnySliceToMaps(raw); len(list) > 0 {
				return list
			}
		}
	}
	return nil
}

func clawhubAnySliceToMaps(raw any) []map[string]any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			out = append(out, mapped)
		}
	}
	return out
}

func clawhubNestedMap(payload map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if raw, ok := payload[key]; ok {
			if mapped, ok := raw.(map[string]any); ok {
				return mapped
			}
		}
	}
	return payload
}

func clawhubString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := payload[key]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		}
	}
	return ""
}

func clawhubNumberString(payload map[string]any, key string) string {
	raw, ok := payload[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case float64:
		return "score=" + strconv.FormatFloat(v, 'f', 3, 64)
	case int:
		return "score=" + strconv.Itoa(v)
	default:
		return ""
	}
}

func joinNonEmpty(sep string, parts []string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return strings.Join(filtered, sep)
}

func defaultSyncPullTarget(account string, snapshot string) string {
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		snapshot = "latest"
	}
	return filepath.Join(".runtime", "sync", "restore", sanitizePathSegment(account), sanitizePathSegment(snapshot))
}

type syncFlagConfig struct {
	Workspace      string
	WorkspaceID    string
	Provider       string
	WebDAVURL      string
	WebDAVUsername string
	WebDAVPassword string
	WebDAVBasePath string
	TimeoutSeconds int
}

func loadSyncConfig(repo, account string, flags syncFlagConfig) (worksync.Config, string, error) {
	return feishunative.ResolveSyncConfig(repo, account, feishunative.SyncConfigOptions{
		Workspace:      flags.Workspace,
		WorkspaceID:    flags.WorkspaceID,
		Provider:       flags.Provider,
		WebDAVURL:      flags.WebDAVURL,
		WebDAVUsername: flags.WebDAVUsername,
		WebDAVPassword: flags.WebDAVPassword,
		WebDAVBasePath: flags.WebDAVBasePath,
		TimeoutSeconds: maxInt(flags.TimeoutSeconds, getenvInt("SUNCODEXCLAW_SYNC_TIMEOUT_SEC", 30)),
	})
}

func defaultSyncWorkspaceID(account string) string {
	return feishunative.DefaultSyncWorkspaceID(account)
}

func accountEnvKey(account, suffix string) string {
	raw := strings.TrimSpace(account)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("FEISHU_")
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - ('a' - 'A'))
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	b.WriteByte('_')
	b.WriteString(suffix)
	return b.String()
}

func getNestedString(root map[string]any, parts ...string) string {
	var cur any = root
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[part]
		if !ok {
			return ""
		}
	}
	switch v := cur.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func syncConfigReady(cfg worksync.Config) bool {
	if strings.TrimSpace(cfg.Provider) != "webdav" {
		return false
	}
	return strings.TrimSpace(cfg.WebDAVURL) != "" && strings.TrimSpace(cfg.WebDAVUsername) != "" && strings.TrimSpace(cfg.WebDAVPassword) != ""
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
	args = stripExplicitLocalFlag(args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: launchagents requires action install|uninstall|status")
		launchagentsUsage()
		os.Exit(2)
	}
	action := strings.TrimSpace(args[0])
	if action == "help" || action == "--help" || action == "-h" {
		launchagentsUsage()
		return
	}
	if action == "--docker-compose" || findPresentFlag(args, "--docker-compose") != "" {
		fmt.Fprintln(os.Stderr, "error: launchagents is a local/macOS helper and does not support --docker-compose")
		launchagentsUsage()
		os.Exit(2)
	}
	args = args[1:]

	fs, accounts, nodeBin, repoFlag, runtimeBackend := baseFlags("launchagents")
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

	sup := supervisor.New(supervisor.Options{RepoRoot: repo, NodeBin: *nodeBin, RuntimeBackend: *runtimeBackend, LaunchctlPrefix: *prefix})
	accts, err := resolveLaunchAgentAccounts(repo, action, *accounts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(accts) == 0 {
		fmt.Println("(no configured accounts)")
		return
	}

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
		launchagentsUsage()
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

func resolveEnabledAccounts(repo string, requested []string) ([]string, error) {
	return resolveAccountsWithDefault(repo, requested, true)
}

func resolveConfiguredAccounts(repo string, requested []string) ([]string, error) {
	return resolveAccountsWithDefault(repo, requested, false)
}

func resolveRestartAccounts(repo string, requested []string) ([]string, []string, error) {
	accts := uniqueAccounts(requested)
	if len(accts) > 0 {
		return accts, accts, nil
	}
	stopAccounts, err := resolveConfiguredAccounts(repo, requested)
	if err != nil {
		return nil, nil, err
	}
	startAccounts, err := resolveEnabledAccounts(repo, requested)
	if err != nil {
		return nil, nil, err
	}
	return stopAccounts, startAccounts, nil
}

func resolveLaunchAgentAccounts(repo string, action string, requested []string) ([]string, error) {
	switch strings.TrimSpace(action) {
	case "install":
		return resolveEnabledAccounts(repo, requested)
	case "uninstall", "status":
		return resolveConfiguredAccounts(repo, requested)
	default:
		return uniqueAccounts(requested), nil
	}
}

func resolveAccountsWithDefault(repo string, requested []string, enabledOnly bool) ([]string, error) {
	accts := uniqueAccounts(requested)
	if len(accts) > 0 {
		return accts, nil
	}
	store := configstore.NewStore(repo)
	if enabledOnly {
		return store.ListEnabledAccountNames()
	}
	return store.ListConfiguredAccountNames()
}

func uniqueAccounts(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || trimmed == "all" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
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
