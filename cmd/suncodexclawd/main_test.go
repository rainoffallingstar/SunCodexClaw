package main

import (
	"os"
	"path/filepath"
	"testing"

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

func TestResolveScopedAccountRequiresExplicitAccount(t *testing.T) {
	_, err := resolveScopedAccount("", "timer")
	if err == nil {
		t.Fatalf("resolveScopedAccount() error = nil, want error")
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
