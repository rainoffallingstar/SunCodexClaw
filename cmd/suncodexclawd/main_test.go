package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"suncodexclaw/internal/envstore"
	"suncodexclaw/internal/timer"
)

func TestParseAccountFromErrorLine(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"[error] missing config for assistant: /x", "assistant"},
		{"[error] assistant preflight failed: boom", "assistant"},
		{"[error]   assistant   preflight failed: boom", "assistant"},
	}
	for _, tt := range tests {
		if got := parseAccountFromErrorLine(tt.line); got != tt.want {
			t.Fatalf("line=%q got=%q want=%q", tt.line, got, tt.want)
		}
	}
}

func TestResolveUpdatedTimerScheduleKeepsExistingWhenNoChanges(t *testing.T) {
	existing := timer.Schedule{Kind: "daily", At: "09:00", Timezone: "Asia/Shanghai"}
	got, changed, err := resolveUpdatedTimerSchedule(existing, "", "", "", "", "")
	if err != nil {
		t.Fatalf("resolveUpdatedTimerSchedule() error = %v", err)
	}
	if changed {
		t.Fatalf("resolveUpdatedTimerSchedule() changed = true, want false")
	}
	if got.Kind != existing.Kind || got.At != existing.At || got.Timezone != existing.Timezone || got.Every != existing.Every {
		t.Fatalf("resolveUpdatedTimerSchedule() = %#v, want %#v", got, existing)
	}
}

func TestResolveUpdatedTimerScheduleUpdatesWeeklyTimeAndTimezone(t *testing.T) {
	existing := timer.Schedule{Kind: "weekly", Weekdays: []string{"mon", "fri"}, At: "09:00", Timezone: "Asia/Shanghai"}
	got, changed, err := resolveUpdatedTimerSchedule(existing, "", "", "", "10:30", "UTC")
	if err != nil {
		t.Fatalf("resolveUpdatedTimerSchedule() error = %v", err)
	}
	if !changed {
		t.Fatalf("resolveUpdatedTimerSchedule() changed = false, want true")
	}
	if got.At != "10:30" || got.Timezone != "UTC" {
		t.Fatalf("resolveUpdatedTimerSchedule() = %#v", got)
	}
}

func TestBuildUpdatedTimerTaskPartialUpdate(t *testing.T) {
	existing := timer.Task{
		ID:        "daily-report",
		Enabled:   true,
		Account:   "assistant",
		ChatID:    "oc_x",
		Prompt:    "old prompt",
		Cwd:       "/workspace",
		AddDirs:   []string{"/workspace/a"},
		Schedule:  timer.Schedule{Kind: "daily", At: "09:00", Timezone: "Asia/Shanghai"},
		CreatedAt: "2026-03-20T00:00:00Z",
		UpdatedAt: "2026-03-20T00:00:00Z",
	}
	got, err := buildUpdatedTimerTask(existing, timerUpdateInput{
		Prompt:       "new prompt",
		Daily:        "11:00",
		ClearAddDirs: true,
		Disable:      true,
		UpdatedBy:    "test",
	}, "2026-03-21T00:00:00Z")
	if err != nil {
		t.Fatalf("buildUpdatedTimerTask() error = %v", err)
	}
	if got.Prompt != "new prompt" {
		t.Fatalf("Prompt = %q, want new prompt", got.Prompt)
	}
	if got.Schedule.At != "11:00" {
		t.Fatalf("Schedule.At = %q, want 11:00", got.Schedule.At)
	}
	if got.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if len(got.AddDirs) != 0 {
		t.Fatalf("AddDirs = %#v, want empty", got.AddDirs)
	}
	if got.LastUpdatedBy != "test" {
		t.Fatalf("LastUpdatedBy = %q, want test", got.LastUpdatedBy)
	}
	if got.CreatedAt != existing.CreatedAt {
		t.Fatalf("CreatedAt changed: %q -> %q", existing.CreatedAt, got.CreatedAt)
	}
}

func TestDefaultSyncWorkspaceIDUsesAccount(t *testing.T) {
	if got := defaultSyncWorkspaceID("assistant"); got != "assistant" {
		t.Fatalf("defaultSyncWorkspaceID() = %q, want assistant", got)
	}
	if got := defaultSyncWorkspaceID("helper.bot"); got != "helper-bot" {
		t.Fatalf("defaultSyncWorkspaceID() = %q, want helper-bot", got)
	}
	if got := defaultSyncWorkspaceID(""); got != "default" {
		t.Fatalf("defaultSyncWorkspaceID() = %q, want default", got)
	}
}

func TestLoadSyncConfigDefaultsWorkspaceIDToAccount(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "workspace")
	cfg, workspaceDir, err := loadSyncConfig(repo, "assistant", syncFlagConfig{Workspace: workspace})
	if err != nil {
		t.Fatalf("loadSyncConfig() error = %v", err)
	}
	if workspaceDir != workspace {
		t.Fatalf("workspaceDir = %q, want %q", workspaceDir, workspace)
	}
	if cfg.WorkspaceID != "assistant" {
		t.Fatalf("WorkspaceID = %q, want assistant", cfg.WorkspaceID)
	}
}

func TestLoadSyncConfigUsesAccountSpecificWorkspaceID(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := "" +
		"[sync.default]\n" +
		"workspace_id = \"shared-default\"\n\n" +
		"[sync.assistant]\n" +
		"workspace_id = \"assistant-private\"\n"
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, _, err := loadSyncConfig(repo, "assistant", syncFlagConfig{Workspace: filepath.Join(repo, "workspace", "assistant")})
	if err != nil {
		t.Fatalf("loadSyncConfig() error = %v", err)
	}
	if cfg.WorkspaceID != "assistant-private" {
		t.Fatalf("WorkspaceID = %q, want assistant-private", cfg.WorkspaceID)
	}
}

func TestLoadSyncConfigIgnoresSharedWorkspaceIDFallback(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := "" +
		"[sync.default]\n" +
		"workspace_id = \"shared-default\"\n"
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, _, err := loadSyncConfig(repo, "assistant", syncFlagConfig{Workspace: filepath.Join(repo, "workspace", "assistant")})
	if err != nil {
		t.Fatalf("loadSyncConfig() error = %v", err)
	}
	if cfg.WorkspaceID != "assistant" {
		t.Fatalf("WorkspaceID = %q, want assistant", cfg.WorkspaceID)
	}
}

func TestLoadSyncConfigUsesFeishuSecretWorkspaceFallback(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := "" +
		"[feishu.assistant.codex]\n" +
		"cwd = \"workspace/private\"\n"
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, workspaceDir, err := loadSyncConfig(repo, "assistant", syncFlagConfig{})
	if err != nil {
		t.Fatalf("loadSyncConfig() error = %v", err)
	}
	if workspaceDir != filepath.Join(repo, "workspace", "private") {
		t.Fatalf("workspaceDir = %q, want %q", workspaceDir, filepath.Join(repo, "workspace", "private"))
	}
	if cfg.WorkspaceID != "assistant" {
		t.Fatalf("WorkspaceID = %q, want assistant", cfg.WorkspaceID)
	}
}

func TestResolveConfiguredAccountsIncludesDisabledBots(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(""+
		"[bot.assistant]\n"+
		"enabled = true\n\n"+
		"[bot.reviewer]\n"+
		"enabled = false\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(""+
		"[feishu.assistant]\napp_id = \"cli_a\"\n\n"+
		"[feishu.reviewer]\napp_id = \"cli_b\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := resolveConfiguredAccounts(repo, nil)
	if err != nil {
		t.Fatalf("resolveConfiguredAccounts() error = %v", err)
	}
	if strings.Join(got, ",") != "assistant,reviewer" {
		t.Fatalf("resolveConfiguredAccounts() = %v, want [assistant reviewer]", got)
	}
}

func TestResolveRestartAccountsStopsConfiguredStartsEnabled(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(""+
		"[bot.assistant]\n"+
		"enabled = true\n\n"+
		"[bot.reviewer]\n"+
		"enabled = false\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(""+
		"[feishu.assistant]\napp_id = \"cli_a\"\n\n"+
		"[feishu.reviewer]\napp_id = \"cli_b\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stopAccounts, startAccounts, err := resolveRestartAccounts(repo, nil)
	if err != nil {
		t.Fatalf("resolveRestartAccounts() error = %v", err)
	}
	if strings.Join(stopAccounts, ",") != "assistant,reviewer" {
		t.Fatalf("stopAccounts = %v, want [assistant reviewer]", stopAccounts)
	}
	if strings.Join(startAccounts, ",") != "assistant" {
		t.Fatalf("startAccounts = %v, want [assistant]", startAccounts)
	}
}

func TestResolveLaunchAgentAccountsInstallUsesEnabledBots(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(""+
		"[bot.assistant]\n"+
		"enabled = true\n\n"+
		"[bot.reviewer]\n"+
		"enabled = false\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(""+
		"[feishu.assistant]\napp_id = \"cli_a\"\n\n"+
		"[feishu.reviewer]\napp_id = \"cli_b\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := resolveLaunchAgentAccounts(repo, "install", nil)
	if err != nil {
		t.Fatalf("resolveLaunchAgentAccounts() error = %v", err)
	}
	if strings.Join(got, ",") != "assistant" {
		t.Fatalf("resolveLaunchAgentAccounts() = %v, want [assistant]", got)
	}
}

func TestResolveEnvRunEntriesAutoWithAccountMergesGlobalThenAccount(t *testing.T) {
	repo := t.TempDir()
	store := envstore.NewStore(repo)
	if _, err := store.Set(envstore.ScopeGlobal, "", "OPENAI_API_KEY", "global-value", "test"); err != nil {
		t.Fatalf("store.Set(global) error = %v", err)
	}
	if _, err := store.Set(envstore.ScopeAccount, "assistant", "OPENAI_API_KEY", "account-value", "test"); err != nil {
		t.Fatalf("store.Set(account) error = %v", err)
	}

	entries, err := resolveEnvRunEntries(store, envstore.ScopeAuto, "assistant", "OPENAI_API_KEY")
	if err != nil {
		t.Fatalf("resolveEnvRunEntries() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Scope != envstore.ScopeGlobal || entries[0].Value != "global-value" {
		t.Fatalf("entries[0] = %#v, want global entry first", entries[0])
	}
	if entries[1].Scope != envstore.ScopeAccount || entries[1].Account != "assistant" || entries[1].Value != "account-value" {
		t.Fatalf("entries[1] = %#v, want account entry second", entries[1])
	}

	childEnv := []string{"OPENAI_API_KEY=original"}
	for _, entry := range entries {
		childEnv = appendEnvOverride(childEnv, entry.Key, entry.Value)
	}
	if got := findEnvValue(childEnv, "OPENAI_API_KEY"); got != "account-value" {
		t.Fatalf("findEnvValue() = %q, want account-value", got)
	}
}

func TestResolveEnvRunEntriesAutoFallsBackToGlobal(t *testing.T) {
	repo := t.TempDir()
	store := envstore.NewStore(repo)
	if _, err := store.Set(envstore.ScopeGlobal, "", "OPENAI_BASE_URL", "https://global.example/v1", "test"); err != nil {
		t.Fatalf("store.Set(global) error = %v", err)
	}

	entries, err := resolveEnvRunEntries(store, envstore.ScopeAuto, "assistant", "OPENAI_BASE_URL")
	if err != nil {
		t.Fatalf("resolveEnvRunEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Scope != envstore.ScopeGlobal || entries[0].Value != "https://global.example/v1" {
		t.Fatalf("entries[0] = %#v, want global fallback", entries[0])
	}
}

func findEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func TestResolveLaunchAgentAccountsStatusIncludesDisabledBots(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(""+
		"[bot.assistant]\n"+
		"enabled = true\n\n"+
		"[bot.reviewer]\n"+
		"enabled = false\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(""+
		"[feishu.assistant]\napp_id = \"cli_a\"\n\n"+
		"[feishu.reviewer]\napp_id = \"cli_b\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := resolveLaunchAgentAccounts(repo, "status", nil)
	if err != nil {
		t.Fatalf("resolveLaunchAgentAccounts() error = %v", err)
	}
	if strings.Join(got, ",") != "assistant,reviewer" {
		t.Fatalf("resolveLaunchAgentAccounts() = %v, want [assistant reviewer]", got)
	}
}

func TestResolveScopedAccountRequiresExplicitAccount(t *testing.T) {
	_, err := resolveScopedAccount("", "timer")
	if err == nil {
		t.Fatalf("resolveScopedAccount() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "run the command inside a bot workspace with .config.toml") {
		t.Fatalf("resolveScopedAccount() error = %v, want workspace hint", err)
	}
}

func TestResolveScopedAccountAcceptsExplicitAccount(t *testing.T) {
	got, err := resolveScopedAccount("assistant", "timer")
	if err != nil {
		t.Fatalf("resolveScopedAccount() error = %v", err)
	}
	if got != "assistant" {
		t.Fatalf("resolveScopedAccount() = %q, want assistant", got)
	}
}

func TestResolveScopedAccountUsesRuntimeConfigScope(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace", "assistant")
	if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := "" +
		"[bot]\n" +
		"account = \"assistant\"\n\n" +
		"[runtime]\n" +
		"memory_account = \"assistant-memory\"\n" +
		"timer_account = \"assistant-timer\"\n"
	if err := os.WriteFile(filepath.Join(workspace, ".config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()
	if err := os.Chdir(filepath.Join(workspace, "nested")); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	got, err := resolveScopedAccount("", "timer")
	if err != nil {
		t.Fatalf("resolveScopedAccount() error = %v", err)
	}
	if got != "assistant-timer" {
		t.Fatalf("resolveScopedAccount() = %q, want assistant-timer", got)
	}
}

func TestResolveScopedAccountReportsConfigPathWhenWorkspaceConfigLacksScope(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace", "assistant")
	if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := "" +
		"[runtime]\n" +
		"memory_account = \"assistant-memory\"\n"
	cfgPath := filepath.Join(workspace, ".config.toml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()
	if err := os.Chdir(filepath.Join(workspace, "nested")); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	_, err = resolveScopedAccount("", "timer")
	if err == nil {
		t.Fatalf("resolveScopedAccount() error = nil, want error")
	}
	if !strings.Contains(err.Error(), cfgPath) {
		t.Fatalf("resolveScopedAccount() error = %v, want cfg path %s", err, cfgPath)
	}
}

func TestResolveMemoryAccountUsesMemoryScopeNotSyncScope(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace", "assistant")
	if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := "" +
		"[bot]\n" +
		"account = \"assistant\"\n\n" +
		"[runtime]\n" +
		"memory_account = \"assistant-memory\"\n" +
		"sync_account = \"assistant-sync\"\n"
	if err := os.WriteFile(filepath.Join(workspace, ".config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()
	if err := os.Chdir(filepath.Join(workspace, "nested")); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	got, err := resolveMemoryAccount("")
	if err != nil {
		t.Fatalf("resolveMemoryAccount() error = %v", err)
	}
	if got != "assistant-memory" {
		t.Fatalf("resolveMemoryAccount() = %q, want assistant-memory", got)
	}
}

func TestBuildRuntimeCommandUsesGoBackend(t *testing.T) {
	cmd := buildRuntimeCommand("/repo", "node", "go", "assistant", true, "/repo/config/timers/assistant/daily.json")
	if got := filepath.Base(cmd.Path); got != filepath.Base(os.Args[0]) {
		t.Fatalf("cmd.Path = %q, want current executable", cmd.Path)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "feishu-run --repo /repo --account assistant --dry-run --timer-task-file /repo/config/timers/assistant/daily.json") {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestBuildRuntimeCommandIgnoresLegacyJSBackendFlag(t *testing.T) {
	cmd := buildRuntimeCommand("/repo", "node", "js", "assistant", false, "")
	if got := filepath.Base(cmd.Path); got != filepath.Base(os.Args[0]) {
		t.Fatalf("cmd.Path = %q, want current executable", cmd.Path)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "feishu-run --repo /repo --account assistant") {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestReorderFlagsBeforePositionalsMovesTrailingFlagsAheadOfID(t *testing.T) {
	got := reorderFlagsBeforePositionals([]string{"daily-report", "--account", "assistant", "--repo", "/repo"}, nil)
	want := []string{"--account", "assistant", "--repo", "/repo", "daily-report"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reorderFlagsBeforePositionals() = %v, want %v", got, want)
	}
}

func TestReorderFlagsBeforePositionalsPreservesSearchQueryWords(t *testing.T) {
	got := reorderFlagsBeforePositionals([]string{"中文", "回复", "--account", "assistant", "--limit", "5"}, nil)
	want := []string{"--account", "assistant", "--limit", "5", "中文", "回复"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reorderFlagsBeforePositionals() = %v, want %v", got, want)
	}
}

func TestReorderFlagsBeforePositionalsSupportsBoolFlags(t *testing.T) {
	got := reorderFlagsBeforePositionals([]string{"daily-report", "--enable", "--account", "assistant"}, map[string]bool{
		"--enable": true,
	})
	want := []string{"--enable", "--account", "assistant", "daily-report"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reorderFlagsBeforePositionals() = %v, want %v", got, want)
	}
}

func TestNormalizeComposeServiceArgsRewritesRepoForContainer(t *testing.T) {
	got := normalizeComposeServiceArgs("timer", []string{"list", "--account", "assistant", "--repo", "/host/repo"})
	want := []string{"list", "--account", "assistant", "--repo", "/app"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalizeComposeServiceArgs() = %v, want %v", got, want)
	}
}

func TestNormalizeComposeServiceArgsStripsHostRepoForConfigure(t *testing.T) {
	got := normalizeComposeServiceArgs("configure", []string{"--account", "assistant", "--repo=/host/repo"})
	want := []string{"--account", "assistant"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalizeComposeServiceArgs() = %v, want %v", got, want)
	}
}

func TestNormalizeComposeServiceArgsRewritesRepoForPreflight(t *testing.T) {
	got := normalizeComposeServiceArgs("preflight", []string{"--account", "assistant", "--repo=/host/repo"})
	want := []string{"--account", "assistant", "--repo", "/app"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalizeComposeServiceArgs() = %v, want %v", got, want)
	}
}

func TestMaybeDockerComposeModeStripsLocalFlagForLocalExecution(t *testing.T) {
	dockerMode, got := maybeDockerComposeMode([]string{"--local", "list", "--account", "assistant"})
	if dockerMode {
		t.Fatalf("maybeDockerComposeMode() dockerMode = true, want false")
	}
	want := []string{"list", "--account", "assistant"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("maybeDockerComposeMode() args = %v, want %v", got, want)
	}
}

func TestMaybeDockerComposeModeStripsComposeFlagForDockerExecution(t *testing.T) {
	dockerMode, got := maybeDockerComposeMode([]string{"list", "--docker-compose", "--account", "assistant"})
	if !dockerMode {
		t.Fatalf("maybeDockerComposeMode() dockerMode = false, want true")
	}
	want := []string{"list", "--account", "assistant"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("maybeDockerComposeMode() args = %v, want %v", got, want)
	}
}

func TestStripExplicitLocalFlagPreservesDockerCompose(t *testing.T) {
	got := stripExplicitLocalFlag([]string{"--local", "--docker-compose", "--project-dir", "/repo"})
	want := []string{"--docker-compose", "--project-dir", "/repo"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stripExplicitLocalFlag() = %v, want %v", got, want)
	}
}

func TestFindPresentFlagMatchesLaunchAgentsComposeMode(t *testing.T) {
	if got := findPresentFlag([]string{"install", "--docker-compose", "--account", "assistant"}, "--docker-compose"); got != "--docker-compose" {
		t.Fatalf("findPresentFlag() = %q, want --docker-compose", got)
	}
}

func TestExtractRepoFlagSupportsSeparateAndInlineForms(t *testing.T) {
	if got := extractRepoFlag([]string{"--account", "assistant", "--repo", "/repo/a"}); got != "/repo/a" {
		t.Fatalf("extractRepoFlag() = %q, want /repo/a", got)
	}
	if got := extractRepoFlag([]string{"--repo=/repo/b", "--account", "assistant"}); got != "/repo/b" {
		t.Fatalf("extractRepoFlag() = %q, want /repo/b", got)
	}
}

func TestFindPresentFlagMatchesExactAndInlineForms(t *testing.T) {
	if got := findPresentFlag([]string{"--account", "assistant"}, "--account"); got != "--account" {
		t.Fatalf("findPresentFlag() = %q, want --account", got)
	}
	if got := findPresentFlag([]string{"--runtime-backend=go"}, "--runtime-backend"); got != "--runtime-backend" {
		t.Fatalf("findPresentFlag() = %q, want --runtime-backend", got)
	}
}

func TestEnsureComposeLifecycleFlagsRejectsUnsupportedArgs(t *testing.T) {
	err := ensureComposeLifecycleFlags("start", []string{"--account", "assistant"}, "--account")
	if err == nil || !strings.Contains(err.Error(), "does not support --account") {
		t.Fatalf("ensureComposeLifecycleFlags() error = %v, want unsupported --account", err)
	}
}

func TestEnsureComposeUpdateFlagsRejectsBinaryUpdateFlags(t *testing.T) {
	err := ensureComposeUpdateFlags([]string{"--docker-compose", "--check"})
	if err == nil || !strings.Contains(err.Error(), "does not support --check") {
		t.Fatalf("ensureComposeUpdateFlags() error = %v, want unsupported --check", err)
	}
}

func TestEnsureComposeUpdateFlagsAllowsProjectDir(t *testing.T) {
	if err := ensureComposeUpdateFlags([]string{"--docker-compose", "--project-dir", "/repo"}); err != nil {
		t.Fatalf("ensureComposeUpdateFlags() error = %v, want nil", err)
	}
}

func TestComposeLogsCommandArgsSupportsInlineLinesAndShortFollow(t *testing.T) {
	got, err := composeLogsCommandArgs([]string{"--repo=/repo", "--lines=50", "-f"})
	if err != nil {
		t.Fatalf("composeLogsCommandArgs() error = %v", err)
	}
	want := []string{"logs", "--tail", "50", "-f", "suncodexclaw"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("composeLogsCommandArgs() = %v, want %v", got, want)
	}
}

func TestComposeLifecycleUpArgsPreferPullPathWithoutBuild(t *testing.T) {
	got := composeLifecycleUpArgs(false, false)
	want := []string{"up", "-d"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("composeLifecycleUpArgs() = %v, want %v", got, want)
	}
}

func TestComposeLifecycleUpArgsFallbackBuildForRestart(t *testing.T) {
	got := composeLifecycleUpArgs(true, true)
	want := []string{"up", "-d", "--build", "--force-recreate"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("composeLifecycleUpArgs() = %v, want %v", got, want)
	}
}

func TestComposeServiceRunArgsPreferImageWithoutBuild(t *testing.T) {
	got := composeServiceRunArgs("timer", []string{"list", "--account", "assistant", "--repo", "/app"}, false)
	want := []string{"run", "--rm", "--workdir", "/app", "suncodexclaw", "timer", "list", "--account", "assistant", "--repo", "/app"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("composeServiceRunArgs() = %v, want %v", got, want)
	}
}

func TestComposeServiceExecArgsUsesContainerWorkdir(t *testing.T) {
	got := composeServiceExecArgs("configure", []string{"--account", "assistant"}, true)
	want := []string{"exec", "--workdir", "/app", "suncodexclaw", "suncodexclawd", "configure", "--account", "assistant"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("composeServiceExecArgs() = %v, want %v", got, want)
	}
}

func TestComposeServiceExecArgsDisablesTTYWhenNonInteractive(t *testing.T) {
	got := composeServiceExecArgs("timer", []string{"list", "--account", "assistant", "--repo", "/app"}, false)
	want := []string{"exec", "-T", "--workdir", "/app", "suncodexclaw", "suncodexclawd", "timer", "list", "--account", "assistant", "--repo", "/app"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("composeServiceExecArgs() = %v, want %v", got, want)
	}
}

func TestComposeServiceRunArgsFallbackBuild(t *testing.T) {
	got := composeServiceRunArgs("sync", []string{"status", "--account", "assistant", "--repo", "/app"}, true)
	want := []string{"run", "--rm", "--workdir", "/app", "--build", "suncodexclaw", "sync", "status", "--account", "assistant", "--repo", "/app"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("composeServiceRunArgs() = %v, want %v", got, want)
	}
}

func TestComposeRunningPSArgsOnlyChecksRunningContainers(t *testing.T) {
	got := composeRunningPSArgs("suncodexclaw")
	want := []string{"compose", "ps", "--status", "running", "-q", "suncodexclaw"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("composeRunningPSArgs() = %v, want %v", got, want)
	}
}

func TestDefaultSyncPullTargetUsesAccountAndSnapshot(t *testing.T) {
	got := defaultSyncPullTarget("assistant.bot", "latest")
	want := filepath.Join(".runtime", "sync", "restore", "assistant.bot", "latest")
	if got != want {
		t.Fatalf("defaultSyncPullTarget() = %q, want %q", got, want)
	}
}

func TestDefaultSyncPullTargetDefaultsLatestSnapshot(t *testing.T) {
	got := defaultSyncPullTarget("assistant", "")
	want := filepath.Join(".runtime", "sync", "restore", "assistant", "latest")
	if got != want {
		t.Fatalf("defaultSyncPullTarget() = %q, want %q", got, want)
	}
}

func TestNormalizeConfigureArgsDropsAddAction(t *testing.T) {
	got := normalizeConfigureArgs([]string{"add", "--account", "assistant", "--yes"})
	want := []string{"--account", "assistant", "--yes"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalizeConfigureArgs() = %v, want %v", got, want)
	}
}

func TestNormalizeConfigureArgsDropsEditAction(t *testing.T) {
	got := normalizeConfigureArgs([]string{"edit", "--account", "assistant", "--yes"})
	want := []string{"--account", "assistant", "--yes"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalizeConfigureArgs() = %v, want %v", got, want)
	}
}
