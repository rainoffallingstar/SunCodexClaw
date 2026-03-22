package feishunative

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type adminCommand struct {
	Kind     string
	Action   string
	Apply    bool
	Force    bool
	Limit    int
	MinScore int
	Scope    string
	Key      string
	Value    string
	Target   string
	Query    string
	Text     string
	Items    []string
	Snapshot string
}

var memoryAdminCommandRunner = runAdminCommand

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
	case regexp.MustCompile(`^/memory\s+stats(?:\s+(\d+))?$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+stats(?:\s+(\d+))?$`).FindStringSubmatch(raw)
		return &adminCommand{Kind: "memory", Action: "stats", Limit: parseOptionalCommandInt(match, 1)}
	case regexp.MustCompile(`^/memory\s+list$`).MatchString(raw):
		return &adminCommand{Kind: "memory", Action: "list"}
	case regexp.MustCompile(`^/memory\s+list\s+(archived|all)$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+list\s+(archived|all)$`).FindStringSubmatch(raw)
		return &adminCommand{Kind: "memory", Action: "list", Scope: strings.ToLower(strings.TrimSpace(match[1]))}
	case regexp.MustCompile(`^/memory\s+recall\s+(archived|all)\s+(.+)$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+recall\s+(archived|all)\s+(.+)$`).FindStringSubmatch(raw)
		return &adminCommand{
			Kind:   "memory",
			Action: "recall",
			Scope:  strings.ToLower(strings.TrimSpace(match[1])),
			Query:  strings.TrimSpace(match[2]),
		}
	case regexp.MustCompile(`^/memory\s+recall\s+(.+)$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+recall\s+(.+)$`).FindStringSubmatch(raw)
		return &adminCommand{Kind: "memory", Action: "recall", Query: strings.TrimSpace(match[1])}
	case regexp.MustCompile(`^/memory\s+review\s+(apply|--apply)(?:\s+(promote|stale|all))?(?:\s+(\d+))?$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+review\s+(apply|--apply)(?:\s+(promote|stale|all))?(?:\s+(\d+))?$`).FindStringSubmatch(raw)
		return &adminCommand{
			Kind:     "memory",
			Action:   "review",
			Apply:    true,
			Scope:    emptyFallback(strings.ToLower(strings.TrimSpace(match[2])), "all"),
			MinScore: parseOptionalCommandInt(match, 3),
		}
	case regexp.MustCompile(`^/memory\s+purge(?:\s+(apply|--apply))?(?:\s+(\d+))?$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+purge(?:\s+(apply|--apply))?(?:\s+(\d+))?$`).FindStringSubmatch(raw)
		return &adminCommand{
			Kind:     "memory",
			Action:   "purge",
			Apply:    len(match) >= 2 && strings.TrimSpace(match[1]) != "",
			MinScore: parseOptionalCommandInt(match, 2),
		}
	case regexp.MustCompile(`^/memory\s+review(?:\s+(\d+))?$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+review(?:\s+(\d+))?$`).FindStringSubmatch(raw)
		return &adminCommand{Kind: "memory", Action: "review", MinScore: parseOptionalCommandInt(match, 1)}
	case regexp.MustCompile(`^/memory\s+related\s+(\S+)(?:\s+(\d+))?$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+related\s+(\S+)(?:\s+(\d+))?$`).FindStringSubmatch(raw)
		return &adminCommand{
			Kind:     "memory",
			Action:   "related",
			Target:   strings.TrimSpace(match[1]),
			MinScore: parseOptionalCommandInt(match, 2),
		}
	case regexp.MustCompile(`^/memory\s+duplicates(?:\s+(\d+))?$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+duplicates(?:\s+(\d+))?$`).FindStringSubmatch(raw)
		return &adminCommand{Kind: "memory", Action: "duplicates", MinScore: parseOptionalCommandInt(match, 1)}
	case regexp.MustCompile(`^/memory\s+dedupe(?:\s+(apply|--apply))?(?:\s+(\d+))?$`).MatchString(raw):
		match := regexp.MustCompile(`^/memory\s+dedupe(?:\s+(apply|--apply))?(?:\s+(\d+))?$`).FindStringSubmatch(raw)
		return &adminCommand{
			Kind:     "memory",
			Action:   "dedupe",
			Apply:    len(match) >= 2 && strings.TrimSpace(match[1]) != "",
			MinScore: parseOptionalCommandInt(match, 2),
		}
	}
	if match := regexp.MustCompile(`^/memory\s+search\s+(archived|all)\s+(.+)$`).FindStringSubmatch(raw); len(match) == 3 {
		return &adminCommand{
			Kind:   "memory",
			Action: "search",
			Scope:  strings.ToLower(strings.TrimSpace(match[1])),
			Query:  strings.TrimSpace(match[2]),
		}
	}
	if match := regexp.MustCompile(`^/memory\s+search\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "search", Query: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+show\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "show", Target: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+archive\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "archive", Target: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+unarchive\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "unarchive", Target: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+pin\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "pin", Target: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+unpin\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "unpin", Target: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+merge\s+(\S+)\s+(.+)$`).FindStringSubmatch(raw); len(match) == 3 {
		return &adminCommand{
			Kind:   "memory",
			Action: "merge",
			Target: strings.TrimSpace(match[1]),
			Items:  strings.Fields(strings.TrimSpace(match[2])),
		}
	}
	if match := regexp.MustCompile(`^/memory\s+(?:delete|remove)\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "delete", Target: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+update\s+(\S+)\s+(.+)$`).FindStringSubmatch(raw); len(match) == 3 {
		return &adminCommand{Kind: "memory", Action: "update", Target: strings.TrimSpace(match[1]), Text: strings.TrimSpace(match[2])}
	}
	if match := regexp.MustCompile(`^/memory\s+add\s+--force-new\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "add", Force: true, Text: strings.TrimSpace(match[1])}
	}
	if match := regexp.MustCompile(`^/memory\s+force\s+(.+)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "memory", Action: "add", Force: true, Text: strings.TrimSpace(match[1])}
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

func parseEnvCommand(text string) *adminCommand {
	raw := normalizeCommandText(text)
	if raw == "" {
		return nil
	}
	if regexp.MustCompile(`^/envs$`).MatchString(raw) {
		return &adminCommand{Kind: "env", Action: "list", Scope: "all"}
	}
	if !regexp.MustCompile(`^/env(?:\s|$)`).MatchString(raw) {
		return nil
	}
	switch {
	case regexp.MustCompile(`^/env(?:\s+help)?$`).MatchString(raw):
		return &adminCommand{Kind: "env", Action: "help"}
	case regexp.MustCompile(`^/env\s+list$`).MatchString(raw):
		return &adminCommand{Kind: "env", Action: "list", Scope: "all"}
	}
	if match := regexp.MustCompile(`^/env\s+list\s+(global|account|all)$`).FindStringSubmatch(raw); len(match) == 2 {
		return &adminCommand{Kind: "env", Action: "list", Scope: strings.ToLower(strings.TrimSpace(match[1]))}
	}
	if match := regexp.MustCompile(`^/env\s+get(?:\s+(global|account))?\s+([A-Za-z_][A-Za-z0-9_]*)$`).FindStringSubmatch(raw); len(match) == 3 {
		return &adminCommand{
			Kind:   "env",
			Action: "get",
			Scope:  emptyFallback(strings.ToLower(strings.TrimSpace(match[1])), "account"),
			Key:    strings.TrimSpace(match[2]),
		}
	}
	if match := regexp.MustCompile(`^/env\s+set(?:\s+(global|account))?\s+([A-Za-z_][A-Za-z0-9_]*)\s+(.+)$`).FindStringSubmatch(raw); len(match) == 4 {
		return &adminCommand{
			Kind:   "env",
			Action: "set",
			Scope:  emptyFallback(strings.ToLower(strings.TrimSpace(match[1])), "account"),
			Key:    strings.TrimSpace(match[2]),
			Value:  strings.TrimSpace(match[3]),
		}
	}
	if match := regexp.MustCompile(`^/env\s+(?:delete|remove)(?:\s+(global|account))?\s+([A-Za-z_][A-Za-z0-9_]*)$`).FindStringSubmatch(raw); len(match) == 3 {
		return &adminCommand{
			Kind:   "env",
			Action: "delete",
			Scope:  emptyFallback(strings.ToLower(strings.TrimSpace(match[1])), "account"),
			Key:    strings.TrimSpace(match[2]),
		}
	}
	return &adminCommand{Kind: "env", Action: "help"}
}

func parseWorkspaceDocsCommand(text string) *adminCommand {
	raw := normalizeCommandText(text)
	if raw == "" {
		return nil
	}
	if regexp.MustCompile(`^/(?:workspace-docs|docs)(?:\s|$)`).MatchString(raw) {
		switch {
		case regexp.MustCompile(`^/(?:workspace-docs|docs)(?:\s+help)?$`).MatchString(raw):
			return &adminCommand{Kind: "workspace-docs", Action: "help"}
		case regexp.MustCompile(`^/(?:workspace-docs|docs)\s+refresh$`).MatchString(raw):
			return &adminCommand{Kind: "workspace-docs", Action: "refresh"}
		}
		return &adminCommand{Kind: "workspace-docs", Action: "help"}
	}
	return nil
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
	case "env":
		reply, err := handleEnvCommand(ctx, cfg, chatID, command)
		return true, reply, err
	case "sync":
		reply, err := handleSyncCommand(ctx, cfg, command)
		return true, reply, err
	case "workspace-docs":
		reply, err := handleWorkspaceDocsCommand(ctx, cfg, command)
		return true, reply, err
	default:
		return false, "", nil
	}
}

func handleTimerCommand(ctx context.Context, cfg Config, command *adminCommand) (string, error) {
	if command == nil {
		return "", nil
	}
	if command.Action == "help" {
		return formatTimerHelp(), nil
	}
	account, err := requireAdminCommandAccount(cfg)
	if err != nil {
		return "", err
	}
	switch command.Action {
	case "list":
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, "timer", "list", "--account", account)
	}
	target := strings.TrimSpace(command.Target)
	argsByAction := map[string]func(string) []string{
		"show":    func(id string) []string { return []string{"timer", "show", "--account", account, id} },
		"run":     func(id string) []string { return []string{"timer", "run", "--account", account, id} },
		"logs":    func(id string) []string { return []string{"timer", "logs", "--account", account, id} },
		"enable":  func(id string) []string { return []string{"timer", "enable", "--account", account, id} },
		"disable": func(id string) []string { return []string{"timer", "disable", "--account", account, id} },
		"delete":  func(id string) []string { return []string{"timer", "delete", "--account", account, id} },
	}
	buildArgs, ok := argsByAction[command.Action]
	if !ok {
		return formatTimerHelp(), nil
	}
	if target == "" {
		return "缺少任务 ID。请用 /timer help 查看命令格式。", nil
	}
	args := buildArgs(target)
	return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, args...)
}

func handleMemoryCommand(ctx context.Context, cfg Config, chatID string, command *adminCommand) (string, error) {
	if command == nil {
		return "", nil
	}
	if command.Action == "help" {
		return formatMemoryHelp(), nil
	}
	account, err := requireAdminCommandAccount(cfg)
	if err != nil {
		return "", err
	}
	switch command.Action {
	case "stats":
		args := []string{"memory", "stats", "--account", account}
		if command.Limit > 0 {
			args = append(args, "--limit", strconv.Itoa(command.Limit))
		}
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "list":
		args := []string{"memory", "list", "--account", account}
		switch strings.TrimSpace(command.Scope) {
		case "archived":
			args = append(args, "--archived")
		case "all":
			args = append(args, "--all")
		}
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "review":
		args := []string{"memory", "review", "--account", account}
		if command.MinScore > 0 {
			args = append(args, "--min-score", strconv.Itoa(command.MinScore))
		}
		if command.Apply {
			switch emptyFallback(strings.TrimSpace(command.Scope), "all") {
			case "promote":
				args = append(args, "--apply-promote")
			case "stale":
				args = append(args, "--apply-stale")
			default:
				args = append(args, "--apply-all")
			}
		}
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "recall":
		if strings.TrimSpace(command.Query) == "" {
			return "缺少召回关键词。请用 /memory help 查看命令格式。", nil
		}
		args := []string{"memory", "recall", "--account", account}
		switch strings.TrimSpace(command.Scope) {
		case "archived":
			args = append(args, "--archived")
		case "all":
			args = append(args, "--all")
		}
		args = append(args, command.Query)
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "purge":
		args := []string{"memory", "purge", "--account", account}
		if command.MinScore > 0 {
			args = append(args, "--days", strconv.Itoa(command.MinScore))
		}
		if command.Apply {
			args = append(args, "--apply")
		}
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "related":
		if strings.TrimSpace(command.Target) == "" {
			return "缺少记忆 ID。请用 /memory help 查看命令格式。", nil
		}
		args := []string{"memory", "related", "--account", account}
		if command.MinScore > 0 {
			args = append(args, "--min-score", strconv.Itoa(command.MinScore))
		}
		args = append(args, command.Target)
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "duplicates":
		args := []string{"memory", "duplicates", "--account", account}
		if command.MinScore > 0 {
			args = append(args, "--min-score", strconv.Itoa(command.MinScore))
		}
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "dedupe":
		args := []string{"memory", "dedupe", "--account", account}
		if command.Apply {
			args = append(args, "--apply")
		}
		if command.MinScore > 0 {
			args = append(args, "--min-score", strconv.Itoa(command.MinScore))
		}
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "search":
		if strings.TrimSpace(command.Query) == "" {
			return "缺少搜索关键词。请用 /memory help 查看命令格式。", nil
		}
		args := []string{"memory", "search", "--account", account}
		switch strings.TrimSpace(command.Scope) {
		case "archived":
			args = append(args, "--archived")
		case "all":
			args = append(args, "--all")
		}
		args = append(args, command.Query)
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
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
		args := []string{"memory", "add", "--account", account, "--text", command.Text, "--source", strings.Join(sourceParts, "/")}
		if command.Force {
			args = append(args, "--force-new")
		}
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "update":
		if strings.TrimSpace(command.Text) == "" {
			return "缺少更新后的记忆内容。请用 /memory help 查看命令格式。", nil
		}
		if strings.TrimSpace(command.Target) == "" {
			return "缺少记忆 ID。请用 /memory help 查看命令格式。", nil
		}
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, "memory", "update", "--account", account, "--text", command.Text, command.Target)
	case "merge":
		if strings.TrimSpace(command.Target) == "" || len(command.Items) == 0 {
			return "缺少要保留的记忆 ID 或待合并的记忆 ID。请用 /memory help 查看命令格式。", nil
		}
		args := []string{"memory", "merge", "--account", account, command.Target}
		args = append(args, command.Items...)
		return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
	}
	target := strings.TrimSpace(command.Target)
	argsByAction := map[string]func(string) []string{
		"show":      func(id string) []string { return []string{"memory", "show", "--account", account, id} },
		"archive":   func(id string) []string { return []string{"memory", "archive", "--account", account, id} },
		"unarchive": func(id string) []string { return []string{"memory", "unarchive", "--account", account, id} },
		"pin":       func(id string) []string { return []string{"memory", "pin", "--account", account, id} },
		"unpin":     func(id string) []string { return []string{"memory", "unpin", "--account", account, id} },
		"delete":    func(id string) []string { return []string{"memory", "delete", "--account", account, id} },
	}
	buildArgs, ok := argsByAction[command.Action]
	if !ok {
		return formatMemoryHelp(), nil
	}
	if target == "" {
		return "缺少记忆 ID。请用 /memory help 查看命令格式。", nil
	}
	args := buildArgs(target)
	return memoryAdminCommandRunner(ctx, cfg.RepoRoot, 30*time.Second, args...)
}

func handleSyncCommand(ctx context.Context, cfg Config, command *adminCommand) (string, error) {
	if command == nil {
		return "", nil
	}
	if command.Action == "help" {
		return formatSyncHelp(), nil
	}
	account, err := requireAdminCommandAccount(cfg)
	if err != nil {
		return "", err
	}
	args := buildSyncAdminArgs(command, account)
	if len(args) == 0 {
		return formatSyncHelp(), nil
	}
	return runAdminCommand(ctx, cfg.RepoRoot, 60*time.Second, args...)
}

func handleEnvCommand(ctx context.Context, cfg Config, chatID string, command *adminCommand) (string, error) {
	if command == nil {
		return "", nil
	}
	if command.Action == "help" {
		return formatEnvHelp(), nil
	}
	account, err := requireAdminCommandAccount(cfg)
	if err != nil {
		return "", err
	}
	scope := strings.TrimSpace(command.Scope)
	if scope == "" {
		scope = "account"
	}
	switch command.Action {
	case "list":
		args := []string{"env", "list", "--scope", emptyFallback(scope, "all")}
		if scope != "global" {
			args = append(args, "--account", account)
		}
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "get":
		if strings.TrimSpace(command.Key) == "" {
			return "缺少环境变量 key。请用 /env help 查看命令格式。", nil
		}
		args := []string{"env", "get", "--scope", scope}
		if scope != "global" {
			args = append(args, "--account", account)
		}
		args = append(args, command.Key)
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "set":
		if strings.TrimSpace(command.Key) == "" || command.Value == "" {
			return "缺少环境变量 key 或 value。请用 /env help 查看命令格式。", nil
		}
		args := []string{"env", "set", "--scope", scope}
		if scope != "global" {
			args = append(args, "--account", account)
		}
		sourceParts := []string{"feishu", account}
		if strings.TrimSpace(chatID) != "" {
			sourceParts = append(sourceParts, chatID)
		}
		args = append(args, "--key", command.Key, "--value", command.Value, "--updated-by", strings.Join(sourceParts, "/"))
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, args...)
	case "delete":
		if strings.TrimSpace(command.Key) == "" {
			return "缺少环境变量 key。请用 /env help 查看命令格式。", nil
		}
		args := []string{"env", "delete", "--scope", scope}
		if scope != "global" {
			args = append(args, "--account", account)
		}
		args = append(args, command.Key)
		return runAdminCommand(ctx, cfg.RepoRoot, 30*time.Second, args...)
	default:
		return formatEnvHelp(), nil
	}
}

func handleWorkspaceDocsCommand(ctx context.Context, cfg Config, command *adminCommand) (string, error) {
	if command == nil {
		return "", nil
	}
	if command.Action == "help" {
		return formatWorkspaceDocsHelp(), nil
	}
	account, err := requireAdminCommandAccount(cfg)
	if err != nil {
		return "", err
	}
	switch command.Action {
	case "refresh":
		return runAdminCommand(ctx, cfg.RepoRoot, 60*time.Second, "workspace-docs", "refresh", "--account", account)
	default:
		return formatWorkspaceDocsHelp(), nil
	}
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
		"/memory help",
		"/memory <要记住的内容>",
		"/memory add <要记住的内容>",
		"/memory add --force-new <要记住的内容>",
		"/memory force <要记住的内容>",
		"/memory stats [数量]",
		"/memory list",
		"/memory list archived|all",
		"/memory recall <关键词>",
		"/memory recall archived|all <关键词>",
		"/memory purge [apply] [天数]",
		"/memory review [最小分数]",
		"/memory review apply [promote|stale|all] [最小分数]",
		"/memory related <记忆ID> [最小分数]",
		"/memory duplicates [最小分数]",
		"/memory dedupe [最小分数]",
		"/memory dedupe apply [最小分数]",
		"/memory search <关键词>",
		"/memory search archived|all <关键词>",
		"/memory show <记忆ID>",
		"/memory archive <记忆ID>",
		"/memory unarchive <记忆ID>",
		"/memory pin <记忆ID>",
		"/memory unpin <记忆ID>",
		"/memory merge <保留ID> <合并ID> [合并ID...]",
		"/memory update <记忆ID> <新内容>",
		"/memory delete <记忆ID>",
		"/memory remove <记忆ID>",
		"",
		"示例：",
		"/memory 以后默认用简体中文回复",
		"/memory add --force-new 以后默认用简体中文回复",
		"/memory force 以后默认用简体中文回复",
		"/memory stats",
		"/memory stats 10",
		"/memory review",
		"/memory review 130",
		"/memory purge",
		"/memory purge 45",
		"/memory purge apply 45",
		"/memory recall 中文回复",
		"/memory recall archived 中文回复",
		"/memory review apply",
		"/memory review apply stale 130",
		"/memory review apply promote",
		"/memory related mem-20260322-101500-000",
		"/memory related mem-20260322-101500-000 130",
		"/memory duplicates 130",
		"/memory dedupe 130",
		"/memory dedupe apply 130",
		"/memory search 中文",
		"/memory list archived",
		"/memory search archived 中文",
		"/memory archive mem-20260322-101500-000",
		"/memory unarchive mem-20260322-101500-000",
		"/memory pin mem-20260322-101500-000",
		"/memory merge mem-20260322-101500-000 mem-20260321-090000-000 mem-20260320-080000-000",
		"/memory update mem-20260322-101500-000 以后默认先给结论再解释",
		"/memory delete mem-20260322-101500-000",
		"/memory remove mem-20260322-101500-000",
		"",
		"说明：",
		"/memory 默认操作当前机器人账号自己的独立记忆库。",
		"/memory add 如果命中高置信度重复，会优先强化已有记忆，而不是再创建一条近似重复项。",
		"/memory force 是 /memory add --force-new 的简写。",
		"/memory add --force-new 或 /memory force 可显式保留新的近似重复记忆。",
		"如果你明确需要保留新的近似重复记忆，可改用命令行 `suncodexclawd memory add --force-new ...`。",
		"/memory stats 会输出当前记忆库的总览，包括 active/archived、kind 分布，以及 top used/reinforced/priority。",
		"/memory review 会主动汇总重复候选、值得晋升的高价值记忆，以及长期闲置的低价值 note。",
		"/memory review apply 默认不会处理 duplicate 分组，只会批量执行 promote 和/或 stale->archive。",
		"/memory purge 默认只预览超出保留期的已归档记忆；显式 apply 后才会物理删除。",
		"/memory recall 会直接预览当前自动召回逻辑会命中的记忆排序，便于调试 active memory 行为。",
		"/memory recall 如果遇到高度相似的重复记忆，会优先保留排序更高的一条，并在原因里附带 collapsed_similar 提示。",
		"/memory list archived|all 与 /memory search archived|all 可用于排查已归档记忆。",
		"/memory related 会围绕指定记忆查看附近的近似/重复候选，适合在 merge 前先做点状检查。",
		"/memory duplicates 会列出疑似重复或近似的记忆分组，便于后续人工 merge；数字越大越保守。",
		"/memory dedupe 默认只预览会合并哪些分组；显式使用 `/memory dedupe apply` 时才会在飞书里直接执行批量合并。",
		"/memory archive 会把记忆从默认召回和搜索结果里隐藏，但仍可通过 ID show 或 unarchive 恢复。",
		"/memory remove 是 /memory delete 的别名，适合顺手删除低价值错误条目。",
		"/memory pin 可以把高价值记忆固定到更高优先级，便于自动召回。",
		"/memory merge 会保留一条主记忆，并吸收其他重复或近似条目的元数据后删除它们。",
	}, "\n")
}

func parseOptionalCommandInt(match []string, index int) int {
	if index < 0 || index >= len(match) {
		return 0
	}
	value := strings.TrimSpace(match[index])
	if value == "" {
		return 0
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
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

func formatEnvHelp() string {
	return strings.Join([]string{
		"环境变量库命令：",
		"/env help",
		"/env list",
		"/env list global",
		"/env list account",
		"/env get <KEY>",
		"/env get global <KEY>",
		"/env set <KEY> <VALUE>",
		"/env set global <KEY> <VALUE>",
		"/env delete <KEY>",
		"/env delete global <KEY>",
		"",
		"说明：",
		"/env 默认操作当前机器人账号自己的账号作用域。",
		"/env list 默认会同时显示当前账号作用域和 global 作用域。",
		"/env get / list / set 的返回值默认都是脱敏的，不会直接回显明文。",
	}, "\n")
}

func formatWorkspaceDocsHelp() string {
	return strings.Join([]string{
		"工作区文档命令：",
		"/docs help",
		"/docs refresh",
		"/workspace-docs help",
		"/workspace-docs refresh",
		"",
		"说明：",
		"/docs refresh 会跳过 WebDAV restore，直接用当前代码内置的最新模板覆盖 agent.md、soul.md、heartbeats.md，并刷新 .config.toml。",
		"/docs refresh 默认操作当前机器人账号绑定的工作目录。",
	}, "\n")
}
