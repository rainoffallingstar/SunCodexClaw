package feishunative

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type adminCommand struct {
	Kind     string
	Action   string
	Target   string
	Query    string
	Text     string
	Snapshot string
}

func parseTimerCommand(text string) *adminCommand {
	raw := normalizeCommandText(text)
	if raw == "" {
		return nil
	}
	if regexp.MustCompile(`^/timers$`).MatchString(raw) {
		return &adminCommand{Kind: "timer", Action: "list"}
	}
	if !regexp.MustCompile(`^/timer(?:\s|$)`).MatchString(raw) {
		return nil
	}
	switch {
	case regexp.MustCompile(`^/timer(?:\s+help)?$`).MatchString(raw):
		return &adminCommand{Kind: "timer", Action: "help"}
	case regexp.MustCompile(`^/timer\s+list$`).MatchString(raw):
		return &adminCommand{Kind: "timer", Action: "list"}
	}
	patterns := []struct {
		action string
		re     *regexp.Regexp
	}{
		{"show", regexp.MustCompile(`^/timer\s+show\s+(.+)$`)},
		{"run", regexp.MustCompile(`^/timer\s+run\s+(.+)$`)},
		{"logs", regexp.MustCompile(`^/timer\s+logs\s+(.+)$`)},
		{"enable", regexp.MustCompile(`^/timer\s+enable\s+(.+)$`)},
		{"disable", regexp.MustCompile(`^/timer\s+disable\s+(.+)$`)},
		{"delete", regexp.MustCompile(`^/timer\s+delete\s+(.+)$`)},
	}
	for _, item := range patterns {
		if match := item.re.FindStringSubmatch(raw); len(match) == 2 {
			return &adminCommand{Kind: "timer", Action: item.action, Target: strings.TrimSpace(match[1])}
		}
	}
	return nil
}

func parseMemoryCommand(text string) *adminCommand {
	raw := normalizeCommandText(text)
	if raw == "" {
		return nil
	}
	if regexp.MustCompile(`^/memories$`).MatchString(raw) {
		return &adminCommand{Kind: "memory", Action: "list"}
	}
	if !regexp.MustCompile(`^/memory(?:\s|$)`).MatchString(raw) {
		return nil
	}
	switch {
	case regexp.MustCompile(`^/memory(?:\s+help)?$`).MatchString(raw):
		return &adminCommand{Kind: "memory", Action: "help"}
	case regexp.MustCompile(`^/memory\s+list$`).MatchString(raw):
		return &adminCommand{Kind: "memory", Action: "list"}
	}
	if match := regexp.MustCompile(`^/memory\s+search\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "search", Query: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+show\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "show", Target: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+(?:delete|remove)\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "delete", Target: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+add\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "add", Text: strings.TrimSpace(match[1])}
	}
	bareText := strings.TrimSpace(regexp.MustCompile(`^/memory\s+`).ReplaceAllString(raw, ""))
	if bareText != "" {
		return &adminCommand{Kind: "memory", Action: "add", Text: bareText}
	}
	return &adminCommand{Kind: "memory", Action: "help"}
}

func parseSyncCommand(text string) *adminCommand {
	raw := normalizeCommandText(text)
	if raw == "" {
		return nil
	}
	if regexp.MustCompile(`^/syncs$`).MatchString(raw) {
		return &adminCommand{Kind: "sync", Action: "list"}
	}
	if !regexp.MustCompile(`^/sync(?:\s|$)`).MatchString(raw) {
		return nil
	}
	switch {
	case regexp.MustCompile(`^/sync(?:\s+help)?$`).MatchString(raw):
		return &adminCommand{Kind: "sync", Action: "help"}
	case regexp.MustCompile(`^/sync\s+status$`).MatchString(raw):
		return &adminCommand{Kind: "sync", Action: "status"}
	case regexp.MustCompile(`^/sync\s+(?:list|list-remote)$`).MatchString(raw):
		return &adminCommand{Kind: "sync", Action: "list"}
	case regexp.MustCompile(`^/sync\s+push$`).MatchString(raw):
		return &adminCommand{Kind: "sync", Action: "push"}
	}
	if match := regexp.MustCompile(`^/sync\s+pull(?:\s+(.+))?$`).FindStringSubmatch(raw); len(match) == 2 {
		snapshot := strings.TrimSpace(match[1])
		if snapshot == "" {
			snapshot = "latest"
		}
		return &adminCommand{Kind: "sync", Action: "pull", Snapshot: snapshot}
	}
	if match := regexp.MustCompile(`^/sync\s+restore(?:\s+(.+))?$`).FindStringSubmatch(raw); len(match) == 2 {
		snapshot := strings.TrimSpace(match[1])
		if snapshot == "" {
			snapshot = "latest"
		}
		return &adminCommand{Kind: "sync", Action: "restore", Snapshot: snapshot}
	}
	return &adminCommand{Kind: "sync", Action: "help"}
}

func normalizeCommandText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\u00a0", " "))
}

func handleAdminCommand(ctx context.Context, cfg Config, chatID string, command *adminCommand) (bool, string, error) {
	if command == nil {
		return false, "", nil
	}
	switch command.Kind {
	case "timer":
		reply, err := handleTimerCommand(ctx, cfg, command)
		return true, reply, err
	case "memory":
		reply, err := handleMemoryCommand(ctx, cfg, chatID, command)
		return true, reply, err
	case "sync":
		reply, err := handleSyncCommand(ctx, cfg, command)
		return true, reply, err
	default:
		return false, "", nil
	}
}

func handleTimerCommand(ctx context.Context, cfg Config, command *adminCommand) (string, error) {
	account, err := requireAdminCommandAccount(cfg)
	if err != nil {
		return "", err
	}
	switch command.Action {
	case "help":
		return formatTimerHelp(), nil
	case "list":
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, "timer", "list", "--account", account)
	}
	target := strings.TrimSpace(command.Target)
	if target == "" {
		return "缺少任务 ID。请用 /timer help 查看命令格式。", nil
	}
	argsByAction := map[string][]string{
		"show":    {"timer", "show", "--account", account, target},
		"run":     {"timer", "run", "--account", account, target},
		"logs":    {"timer", "logs", "--account", account, target},
		"enable":  {"timer", "enable", "--account", account, target},
		"disable": {"timer", "disable", "--account", account, target},
		"delete":  {"timer", "delete", "--account", account, target},
	}
	args := argsByAction[command.Action]
	if len(args) == 0 {
		return "", nil
	}
	return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, args...)
}

func handleMemoryCommand(ctx context.Context, cfg Config, chatID string, command *adminCommand) (string, error) {
	account, err := requireAdminCommandAccount(cfg)
	if err != nil {
		return "", err
	}
	switch command.Action {
	case "help":
		return formatMemoryHelp(), nil
	case "list":
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, "memory", "list", "--account", account)
	case "search":
		if strings.TrimSpace(command.Query) == "" {
			return "缺少搜索关键词。请用 /memory help 查看命令格式。", nil
		}
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, "memory", "search", "--account", account, command.Query)
	case "add":
		if strings.TrimSpace(command.Text) == "" {
			return "缺少记忆内容。请用 /memory help 查看命令格式。", nil
		}
		sourceParts := []string{"feishu"}
		if strings.TrimSpace(account) != "" {
			sourceParts = append(sourceParts, account)
		}
		if strings.TrimSpace(chatID) != "" {
			sourceParts = append(sourceParts, chatID)
		}
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, "memory", "add", "--account", account, "--text", command.Text, "--source", strings.Join(sourceParts, "/"))
	}
	target := strings.TrimSpace(command.Target)
	if target == "" {
		return "缺少记忆 ID。请用 /memory help 查看命令格式。", nil
	}
	argsByAction := map[string][]string{
		"show":   {"memory", "show", "--account", account, target},
		"delete": {"memory", "delete", "--account", account, target},
	}
	args := argsByAction[command.Action]
	if len(args) == 0 {
		return "", nil
	}
	return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, args...)
}

func handleSyncCommand(ctx context.Context, cfg Config, command *adminCommand) (string, error) {
	account, err := requireAdminCommandAccount(cfg)
	if err != nil {
		return "", err
	}
	if command.Action == "help" {
		return formatSyncHelp(), nil
	}
	args := buildSyncAdminArgs(command, account)
	if len(args) == 0 {
		return "", nil
	}
	return runAdminCommand(ctx, cfg.RepoRoot, 60*time.Second, args...)
}

func buildSyncAdminArgs(command *adminCommand, account string) []string {
	if command == nil {
		return nil
	}
	account = strings.TrimSpace(account)
	if account == "" {
		return nil
	}
	switch command.Action {
	case "status":
		return []string{"sync", "status", "--account", account}
	case "list":
		return []string{"sync", "list-remote", "--account", account}
	case "push":
		return []string{"sync", "push", "--account", account}
	case "pull":
		snapshot := emptyFallback(command.Snapshot, "latest")
		targetDir := filepath.Join(".runtime", "sync", "restore", sanitizeRestorePathSegment(account), sanitizeRestorePathSegment(snapshot))
		return []string{"sync", "pull", "--account", account, "--snapshot", snapshot, "--to", targetDir}
	case "restore":
		snapshot := emptyFallback(command.Snapshot, "latest")
		return []string{"sync", "restore", "--account", account, "--snapshot", snapshot}
	default:
		return nil
	}
}

func requireAdminCommandAccount(cfg Config) (string, error) {
	account := strings.TrimSpace(cfg.AccountName)
	if account == "" {
		return "", fmt.Errorf("missing account name in runtime config")
	}
	return account, nil
}

func runAdminCommand(ctx context.Context, repoRoot string, timeout time.Duration, args ...string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmdCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	fullArgs := append([]string{}, args...)
	fullArgs = append(fullArgs, "--repo", repoRoot)
	cmd := exec.CommandContext(cmdCtx, exe, fullArgs...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	text := compactCommandOutput(string(out), 4000)
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "", fmt.Errorf("%s", text)
	}
	if text == "" {
		text = "ok"
	}
	return text, nil
}

func compactCommandOutput(raw string, max int) string {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	text = strings.TrimSpace(strings.Join(out, "\n"))
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 8 {
		return text[:max]
	}
	return text[:max-8] + "\n...(已截断)"
}

func sanitizeRestorePathSegment(raw string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "..", "-")
	out := strings.Trim(replacer.Replace(strings.TrimSpace(raw)), "-.")
	if out == "" {
		return "latest"
	}
	return out
}

func formatTimerHelp() string {
	return strings.Join([]string{
		"定时任务命令：",
		"/timers",
		"/timer help",
		"/timer list",
		"/timer show <任务ID>",
		"/timer run <任务ID>",
		"/timer logs <任务ID>",
		"/timer enable <任务ID>",
		"/timer disable <任务ID>",
		"/timer delete <任务ID>",
		"",
		"复杂创建或修改也可以直接发自然语言，例如：",
		"/timer 把 daily-report 改成每天 10:30 执行，并把任务内容改成检查当前工作目录后发回当前会话",
		"/timer 创建一个每天 09:00 执行的日报任务，目录 workspace/<account-namespace>，结果发回当前会话",
		"",
		"说明：",
		"/timer 默认操作当前机器人账号的定时任务。",
		"/timer 自然语言创建任务时，默认把结果发回当前会话。",
	}, "\n")
}

func formatMemoryHelp() string {
	return strings.Join([]string{
		"记忆命令：",
		"/memory <要记住的内容>",
		"/memory add <要记住的内容>",
		"/memory list",
		"/memory search <关键词>",
		"/memory show <记忆ID>",
		"/memory delete <记忆ID>",
		"",
		"示例：",
		"/memory 以后默认用简体中文回复",
		"/memory search 中文",
		"",
		"说明：",
		"/memory 默认操作当前机器人账号自己的独立记忆库。",
	}, "\n")
}

func formatSyncHelp() string {
	return strings.Join([]string{
		"同步命令：",
		"/sync help",
		"/sync status",
		"/sync list",
		"/sync push",
		"/sync pull [latest|快照ID]",
		"/sync restore [latest|快照ID]",
		"",
		"说明：",
		"/sync 默认操作当前机器人账号的同步配置。",
		"/sync pull 会把远端文档拉到本地恢复目录，不直接覆盖工作区。",
		"/sync pull 默认落到 .runtime/sync/restore/<account-namespace>/<snapshot>。",
		"/sync restore 默认只补缺失文档；显式覆盖时才会使用 force 模式。",
	}, "\n")
}
