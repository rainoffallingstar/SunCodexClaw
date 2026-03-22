package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"suncodexclaw/internal/envstore"
	"suncodexclaw/internal/memory"
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

func TestUsageListsExpandedMemoryCommands(t *testing.T) {
	output := captureStderr(t, func() {
		usage()
	})
	for _, want := range []string{
		"suncodexclawd memory <add|force|stats|list|show|search|recall|review|related|duplicates|dedupe|update|pin|unpin|archive|unarchive|purge|merge|delete>",
		"suncodexclawd env <set|get|list|delete|run>",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("usage missing %q\n%s", want, output)
		}
	}
}

func TestMemoryUsageMentionsForceShortcutAndWorkspaceInference(t *testing.T) {
	output := captureStderr(t, func() {
		memoryUsage()
	})
	for _, want := range []string{
		"memory force is a shortcut for memory add --force-new.",
		"In a bot workspace, memory commands can infer --account from .config.toml.",
		"memory show already prints raw JSON.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("memoryUsage missing %q\n%s", want, output)
		}
	}
}

func TestMemoryCmdHelpPrintsUsageWithoutError(t *testing.T) {
	for _, args := range [][]string{
		{"help"},
		{"--help"},
		{"-h"},
	} {
		exitCode, output := runMemoryCmdSubprocess(t, args...)
		if exitCode != 0 {
			t.Fatalf("memoryCmd(%v) exitCode = %d, want 0", args, exitCode)
		}
		for _, want := range []string{
			"Memory Usage:",
			"suncodexclawd memory add",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("memoryCmd(%v) missing %q\n%s", args, want, output)
			}
		}
	}
}

func TestMemoryCmdWithoutSubcommandExitsWithUsage(t *testing.T) {
	exitCode, output := runMemoryCmdSubprocess(t)
	if exitCode != 2 {
		t.Fatalf("memoryCmd() exitCode = %d, want 2", exitCode)
	}
	for _, want := range []string{
		"Memory Usage:",
		"suncodexclawd memory add",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("memoryCmd() missing %q\n%s", want, output)
		}
	}
}

func TestMemoryCmdUnknownSubcommandExitsWithUsage(t *testing.T) {
	exitCode, output := runMemoryCmdSubprocess(t, "wat")
	if exitCode != 2 {
		t.Fatalf("memoryCmd(wat) exitCode = %d, want 2", exitCode)
	}
	for _, want := range []string{
		"Memory Usage:",
		"suncodexclawd memory add",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("memoryCmd(wat) missing %q\n%s", want, output)
		}
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

func TestMemoryAddAndDeleteSupportJSONOutput(t *testing.T) {
	repo := t.TempDir()

	addOutput := captureStdout(t, func() {
		memoryAdd([]string{
			"以后默认用简体中文回复",
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--pinned",
			"--kind", "rule",
			"--priority", "80",
		})
	})

	var addPayload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(addOutput), &addPayload); err != nil {
		t.Fatalf("json.Unmarshal(addOutput) error = %v, body=%s", err, addOutput)
	}
	if addPayload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", addPayload.Library)
	}
	if addPayload.Action != "added" {
		t.Fatalf("action = %q, want added", addPayload.Action)
	}
	if addPayload.Entry.ID == "" {
		t.Fatalf("entry.ID is empty")
	}
	if !addPayload.Entry.Pinned {
		t.Fatalf("entry.Pinned = false, want true")
	}
	if addPayload.Entry.Kind != "rule" {
		t.Fatalf("entry.Kind = %q, want rule", addPayload.Entry.Kind)
	}
	if addPayload.Entry.Priority != 80 {
		t.Fatalf("entry.Priority = %d, want 80", addPayload.Entry.Priority)
	}
	if addPayload.Entry.Text != "以后默认用简体中文回复" {
		t.Fatalf("entry.Text = %q", addPayload.Entry.Text)
	}

	deleteOutput := captureStdout(t, func() {
		memoryDelete([]string{
			addPayload.Entry.ID,
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var deletePayload struct {
		Library   string        `json:"library"`
		Action    string        `json:"action"`
		DeletedID string        `json:"deleted_id"`
		Entry     *memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(deleteOutput), &deletePayload); err != nil {
		t.Fatalf("json.Unmarshal(deleteOutput) error = %v, body=%s", err, deleteOutput)
	}
	if deletePayload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", deletePayload.Library)
	}
	if deletePayload.Action != "deleted" {
		t.Fatalf("action = %q, want deleted", deletePayload.Action)
	}
	if deletePayload.DeletedID != addPayload.Entry.ID {
		t.Fatalf("deleted_id = %q, want %q", deletePayload.DeletedID, addPayload.Entry.ID)
	}
	if deletePayload.Entry == nil || deletePayload.Entry.ID != addPayload.Entry.ID {
		t.Fatalf("delete payload entry = %#v, want entry ID %q", deletePayload.Entry, addPayload.Entry.ID)
	}
}

func TestMemoryArchiveAndUnarchiveSupportJSONOutput(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	entry, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind: "preference",
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	archiveOutput := captureStdout(t, func() {
		memoryArchive([]string{
			entry.ID,
			"--account", "assistant",
			"--repo", repo,
			"--json",
		}, true)
	})

	var archivePayload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(archiveOutput), &archivePayload); err != nil {
		t.Fatalf("json.Unmarshal(archiveOutput) error = %v, body=%s", err, archiveOutput)
	}
	if archivePayload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", archivePayload.Library)
	}
	if archivePayload.Action != "archived" {
		t.Fatalf("action = %q, want archived", archivePayload.Action)
	}
	if !archivePayload.Entry.Archived {
		t.Fatalf("entry.Archived = false, want true")
	}
	if archivePayload.Entry.ArchivedAt == "" {
		t.Fatalf("entry.ArchivedAt is empty")
	}

	unarchiveOutput := captureStdout(t, func() {
		memoryArchive([]string{
			entry.ID,
			"--account", "assistant",
			"--repo", repo,
			"--json",
		}, false)
	})

	var unarchivePayload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(unarchiveOutput), &unarchivePayload); err != nil {
		t.Fatalf("json.Unmarshal(unarchiveOutput) error = %v, body=%s", err, unarchiveOutput)
	}
	if unarchivePayload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", unarchivePayload.Library)
	}
	if unarchivePayload.Action != "unarchived" {
		t.Fatalf("action = %q, want unarchived", unarchivePayload.Action)
	}
	if unarchivePayload.Entry.Archived {
		t.Fatalf("entry.Archived = true, want false")
	}
	if unarchivePayload.Entry.ArchivedAt != "" {
		t.Fatalf("entry.ArchivedAt = %q, want empty", unarchivePayload.Entry.ArchivedAt)
	}
}

func TestMemoryMergeSupportsJSONOutput(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	keep, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(keep) error = %v", err)
	}
	drop, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind: "note",
	})
	if err != nil {
		t.Fatalf("AddWithOptions(drop) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryMerge([]string{
			keep.ID,
			drop.ID,
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload struct {
		Library    string       `json:"library"`
		Action     string       `json:"action"`
		DeletedIDs []string     `json:"deleted_ids"`
		Entry      memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if payload.Action != "merged" {
		t.Fatalf("action = %q, want merged", payload.Action)
	}
	if len(payload.DeletedIDs) != 1 || payload.DeletedIDs[0] != drop.ID {
		t.Fatalf("deleted_ids = %#v, want [%s]", payload.DeletedIDs, drop.ID)
	}
	if payload.Entry.ID != keep.ID {
		t.Fatalf("entry.ID = %q, want %q", payload.Entry.ID, keep.ID)
	}
}

func TestMemoryMergeIgnoresDuplicateAndSelfDropIDs(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	keep, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(keep) error = %v", err)
	}
	drop, err := store.AddWithOptions("默认请用中文回复", memory.AddOptions{
		Kind: "note",
	})
	if err != nil {
		t.Fatalf("AddWithOptions(drop) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryMerge([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
			keep.ID,
			keep.ID,
			drop.ID,
			drop.ID,
		})
	})

	var payload struct {
		Library    string       `json:"library"`
		Action     string       `json:"action"`
		DeletedIDs []string     `json:"deleted_ids"`
		Entry      memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" || payload.Action != "merged" {
		t.Fatalf("payload header = %#v", payload)
	}
	if len(payload.DeletedIDs) != 1 || payload.DeletedIDs[0] != drop.ID {
		t.Fatalf("deleted_ids = %#v, want [%s]", payload.DeletedIDs, drop.ID)
	}
	if payload.Entry.ID != keep.ID {
		t.Fatalf("entry.ID = %q, want %q", payload.Entry.ID, keep.ID)
	}
	if _, err := store.ReadEntry(drop.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drop entry still exists, err = %v", err)
	}
}

func TestMemoryUpdateSupportsJSONOutput(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	entry, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Source: "feishu/assistant/oc_xxx",
		Tags:   []string{"lang"},
		Kind:   "note",
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryUpdate([]string{
			entry.ID,
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--text", "以后默认先给结论再解释",
			"--kind", "preference",
			"--priority", "80",
			"--pinned",
			"--tag", "reply-style",
		})
	})

	var payload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if payload.Action != "updated" {
		t.Fatalf("action = %q, want updated", payload.Action)
	}
	if payload.Entry.ID != entry.ID {
		t.Fatalf("entry.ID = %q, want %q", payload.Entry.ID, entry.ID)
	}
	if payload.Entry.Text != "以后默认先给结论再解释" {
		t.Fatalf("entry.Text = %q", payload.Entry.Text)
	}
	if payload.Entry.Kind != "preference" || payload.Entry.Priority != 80 || !payload.Entry.Pinned {
		t.Fatalf("entry metadata = %#v", payload.Entry)
	}
	if len(payload.Entry.Tags) != 1 || payload.Entry.Tags[0] != "reply-style" {
		t.Fatalf("entry.Tags = %#v, want [reply-style]", payload.Entry.Tags)
	}
}

func TestMemoryPinAndUnpinSupportJSONOutput(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	entry, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind: "preference",
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	pinOutput := captureStdout(t, func() {
		memoryPin([]string{
			entry.ID,
			"--account", "assistant",
			"--repo", repo,
			"--json",
		}, true)
	})

	var pinPayload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(pinOutput), &pinPayload); err != nil {
		t.Fatalf("json.Unmarshal(pinOutput) error = %v, body=%s", err, pinOutput)
	}
	if pinPayload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", pinPayload.Library)
	}
	if pinPayload.Action != "pinned" {
		t.Fatalf("action = %q, want pinned", pinPayload.Action)
	}
	if !pinPayload.Entry.Pinned {
		t.Fatalf("entry.Pinned = false, want true")
	}

	unpinOutput := captureStdout(t, func() {
		memoryPin([]string{
			entry.ID,
			"--account", "assistant",
			"--repo", repo,
			"--json",
		}, false)
	})

	var unpinPayload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(unpinOutput), &unpinPayload); err != nil {
		t.Fatalf("json.Unmarshal(unpinOutput) error = %v, body=%s", err, unpinOutput)
	}
	if unpinPayload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", unpinPayload.Library)
	}
	if unpinPayload.Action != "unpinned" {
		t.Fatalf("action = %q, want unpinned", unpinPayload.Action)
	}
	if unpinPayload.Entry.Pinned {
		t.Fatalf("entry.Pinned = true, want false")
	}
}

func TestMemoryAddJSONReinforcesExistingHighConfidenceDuplicate(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	existing, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "note",
		Priority: 20,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(existing) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryAdd([]string{
			"以后默认用简体中文回复",
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--kind", "preference",
			"--priority", "80",
			"--pinned",
		})
	})

	var payload struct {
		Library     string       `json:"library"`
		Action      string       `json:"action"`
		MatchScore  int          `json:"match_score"`
		MatchReason string       `json:"match_reason"`
		Entry       memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if payload.Action != "reinforced" {
		t.Fatalf("action = %q, want reinforced", payload.Action)
	}
	if payload.Entry.ID != existing.ID {
		t.Fatalf("entry.ID = %q, want %q", payload.Entry.ID, existing.ID)
	}
	if payload.MatchScore < memory.DefaultRememberDuplicateMinScore {
		t.Fatalf("match_score = %d, want >= %d", payload.MatchScore, memory.DefaultRememberDuplicateMinScore)
	}
	if payload.MatchReason == "" {
		t.Fatalf("match_reason is empty")
	}
	if payload.Entry.Kind != "preference" || payload.Entry.Priority != 80 || !payload.Entry.Pinned {
		t.Fatalf("entry metadata = %#v", payload.Entry)
	}

	entries, err := store.ListEntriesAll()
	if err != nil {
		t.Fatalf("ListEntriesAll() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListEntriesAll() len = %d, want 1", len(entries))
	}
}

func TestMemoryAddPlainTextReinforceIncludesMatchDetails(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	existing, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "note",
		Priority: 20,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(existing) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryAdd([]string{
			"以后默认用简体中文回复",
			"--account", "assistant",
			"--repo", repo,
			"--kind", "preference",
			"--priority", "80",
			"--pinned",
		})
	})

	for _, want := range []string{
		"library=assistant",
		"reinforced=" + existing.ID,
		"force_new=false",
		"match_score=",
		"match_reason=",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryAddForceNewKeepsNearDuplicateEntry(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	first, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(first) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryAdd([]string{
			"以后默认用简体中文回复",
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--force-new",
		})
	})

	var payload struct {
		Library    string       `json:"library"`
		Action     string       `json:"action"`
		ForceNew   bool         `json:"force_new"`
		Entry      memory.Entry `json:"entry"`
		MatchScore int          `json:"match_score"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if payload.Action != "added" {
		t.Fatalf("action = %q, want added", payload.Action)
	}
	if !payload.ForceNew {
		t.Fatalf("force_new = false, want true")
	}
	if payload.Entry.ID == "" || payload.Entry.ID == first.ID {
		t.Fatalf("entry.ID = %q, want new ID distinct from %q", payload.Entry.ID, first.ID)
	}
	if payload.MatchScore != 0 {
		t.Fatalf("match_score = %d, want 0", payload.MatchScore)
	}

	entries, err := store.ListEntriesAll()
	if err != nil {
		t.Fatalf("ListEntriesAll() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListEntriesAll() len = %d, want 2", len(entries))
	}
}

func TestMemoryForceAliasKeepsNearDuplicateEntry(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	first, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(first) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryForce([]string{
			"以后默认用简体中文回复",
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload struct {
		Action   string       `json:"action"`
		ForceNew bool         `json:"force_new"`
		Entry    memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Action != "added" {
		t.Fatalf("action = %q, want added", payload.Action)
	}
	if !payload.ForceNew {
		t.Fatalf("force_new = false, want true")
	}
	if payload.Entry.ID == "" || payload.Entry.ID == first.ID {
		t.Fatalf("entry.ID = %q, want new ID distinct from %q", payload.Entry.ID, first.ID)
	}

	entries, err := store.ListEntriesAll()
	if err != nil {
		t.Fatalf("ListEntriesAll() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListEntriesAll() len = %d, want 2", len(entries))
	}
}

func TestMemoryForcePlainTextMentionsForceNew(t *testing.T) {
	repo := t.TempDir()
	output := captureStdout(t, func() {
		memoryForce([]string{
			"以后默认用简体中文回复",
			"--account", "assistant",
			"--repo", repo,
		})
	})
	if !strings.Contains(output, "force_new=true") {
		t.Fatalf("output missing force_new=true: %s", output)
	}
}

func TestMemoryShowPrintsRawJSONByDefault(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	entry, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Source:   "feishu/assistant/oc_xxx",
		Tags:     []string{"lang", "reply"},
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryShow([]string{
			entry.ID,
			"--account", "assistant",
			"--repo", repo,
		})
	})

	var payload memory.Entry
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.ID != entry.ID {
		t.Fatalf("id = %q, want %q", payload.ID, entry.ID)
	}
	if payload.Text != entry.Text {
		t.Fatalf("text = %q, want %q", payload.Text, entry.Text)
	}
	if payload.Source != entry.Source {
		t.Fatalf("source = %q, want %q", payload.Source, entry.Source)
	}
	if payload.Kind != "preference" || payload.Priority != 80 || !payload.Pinned {
		t.Fatalf("payload metadata = %#v", payload)
	}
	if len(payload.Tags) != 2 {
		t.Fatalf("tags len = %d, want 2", len(payload.Tags))
	}
}

func TestMemoryStatsJSONUsesSnakeCaseNestedKeys(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	}); err != nil {
		t.Fatalf("AddWithOptions(first) error = %v", err)
	}
	if _, err := store.AddWithOptions("代码修改后顺手跑测试", memory.AddOptions{
		Kind: "rule",
	}); err != nil {
		t.Fatalf("AddWithOptions(second) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryStats([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	stats, ok := payload["stats"].(map[string]any)
	if !ok {
		t.Fatalf("stats = %#v, want object", payload["stats"])
	}
	for _, key := range []string{
		"total_entries",
		"active_entries",
		"archived_entries",
		"pinned_entries",
		"kind_counts",
		"top_used",
		"top_reinforced",
		"top_priority",
	} {
		if _, ok := stats[key]; !ok {
			t.Fatalf("stats missing key %q: %#v", key, stats)
		}
	}
	if _, ok := stats["TotalEntries"]; ok {
		t.Fatalf("stats unexpectedly contains Go-style key TotalEntries: %#v", stats)
	}
}

func TestMemoryStatsJSONReportsExpectedCounts(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	pinned, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(pinned) error = %v", err)
	}
	archived, err := store.AddWithOptions("历史低价值说明", memory.AddOptions{
		Kind: "note",
	})
	if err != nil {
		t.Fatalf("AddWithOptions(archived) error = %v", err)
	}
	if _, err := store.UpdateEntry(archived.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-03-01T00:00:00Z"),
	}); err != nil {
		t.Fatalf("UpdateEntry(archived) error = %v", err)
	}
	pinnedEntry, err := store.ReadEntry(pinned.ID)
	if err != nil {
		t.Fatalf("ReadEntry(pinned) error = %v", err)
	}
	pinnedEntry.UseCount = 3
	pinnedEntry.ReinforceCount = 2
	pinnedEntry.LastUsedAt = "2026-03-20T00:00:00Z"
	pinnedEntry.LastReinforcedAt = "2026-03-21T00:00:00Z"
	pinnedEntry.UpdatedAt = "2026-03-21T00:00:00Z"
	if err := store.WriteEntry(pinnedEntry); err != nil {
		t.Fatalf("WriteEntry(pinned) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryStats([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--limit", "3",
		})
	})

	var payload struct {
		Library string `json:"library"`
		Limit   int    `json:"limit"`
		Stats   struct {
			TotalEntries    int            `json:"total_entries"`
			ActiveEntries   int            `json:"active_entries"`
			ArchivedEntries int            `json:"archived_entries"`
			PinnedEntries   int            `json:"pinned_entries"`
			KindCounts      map[string]int `json:"kind_counts"`
			TopUsed         []memory.Entry `json:"top_used"`
			TopReinforced   []memory.Entry `json:"top_reinforced"`
			TopPriority     []memory.Entry `json:"top_priority"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" || payload.Limit != 3 {
		t.Fatalf("payload header = %#v", payload)
	}
	if payload.Stats.TotalEntries != 2 || payload.Stats.ActiveEntries != 1 || payload.Stats.ArchivedEntries != 1 || payload.Stats.PinnedEntries != 1 {
		t.Fatalf("stats counts = %#v", payload.Stats)
	}
	if payload.Stats.KindCounts["preference"] != 1 || payload.Stats.KindCounts["note"] != 1 {
		t.Fatalf("kind_counts = %#v", payload.Stats.KindCounts)
	}
	if len(payload.Stats.TopUsed) == 0 || payload.Stats.TopUsed[0].ID != pinned.ID {
		t.Fatalf("top_used = %#v, want %s first", payload.Stats.TopUsed, pinned.ID)
	}
	if len(payload.Stats.TopReinforced) == 0 || payload.Stats.TopReinforced[0].ID != pinned.ID {
		t.Fatalf("top_reinforced = %#v, want %s first", payload.Stats.TopReinforced, pinned.ID)
	}
	if len(payload.Stats.TopPriority) == 0 || payload.Stats.TopPriority[0].ID != pinned.ID {
		t.Fatalf("top_priority = %#v, want %s first", payload.Stats.TopPriority, pinned.ID)
	}
}

func TestMemoryStatsPlainTextIncludesKindsAndTopSections(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	pinned, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(pinned) error = %v", err)
	}
	entry, err := store.ReadEntry(pinned.ID)
	if err != nil {
		t.Fatalf("ReadEntry() error = %v", err)
	}
	entry.UseCount = 3
	entry.ReinforceCount = 2
	entry.UpdatedAt = "2026-03-21T00:00:00Z"
	if err := store.WriteEntry(entry); err != nil {
		t.Fatalf("WriteEntry() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryStats([]string{
			"--account", "assistant",
			"--repo", repo,
		})
	})

	for _, want := range []string{
		"library=assistant total=1 active=1 archived=0 pinned=1",
		"kinds=preference:1",
		"[top_used]",
		"[top_reinforced]",
		"[top_priority]",
		"use_count=3",
		"reinforce_count=2",
		"priority=80 pinned=true",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryListJSONSupportsArchivedFilters(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	active, err := store.AddWithOptions("活跃偏好", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(active) error = %v", err)
	}
	archived, err := store.AddWithOptions("归档偏好", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions(archived) error = %v", err)
	}
	if _, err := store.UpdateEntry(archived.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-03-01T00:00:00Z"),
	}); err != nil {
		t.Fatalf("UpdateEntry(archived) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryList([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--archived",
		})
	})

	var payload struct {
		Library         string         `json:"library"`
		ArchivedOnly    bool           `json:"archived_only"`
		IncludeArchived bool           `json:"include_archived"`
		Entries         []memory.Entry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" || !payload.ArchivedOnly || payload.IncludeArchived {
		t.Fatalf("payload header = %#v", payload)
	}
	if len(payload.Entries) != 1 || payload.Entries[0].ID != archived.ID {
		t.Fatalf("entries = %#v, want only archived %s", payload.Entries, archived.ID)
	}
	if payload.Entries[0].ID == active.ID {
		t.Fatalf("entries unexpectedly contains active id %s", active.ID)
	}
}

func TestMemoryListPlainTextDefaultsToActiveOnly(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	active, err := store.AddWithOptions("活跃偏好", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(active) error = %v", err)
	}
	archived, err := store.AddWithOptions("归档偏好", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions(archived) error = %v", err)
	}
	if _, err := store.UpdateEntry(archived.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-03-01T00:00:00Z"),
	}); err != nil {
		t.Fatalf("UpdateEntry(archived) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryList([]string{
			"--account", "assistant",
			"--repo", repo,
		})
	})

	if !strings.Contains(output, active.ID) {
		t.Fatalf("output missing active id %s: %s", active.ID, output)
	}
	if strings.Contains(output, archived.ID) {
		t.Fatalf("output unexpectedly contains archived id %s: %s", archived.ID, output)
	}
}

func TestMemoryListPlainTextShowsNoMemoriesWhenEmpty(t *testing.T) {
	repo := t.TempDir()
	output := captureStdout(t, func() {
		memoryList([]string{
			"--account", "assistant",
			"--repo", repo,
		})
	})
	if output != "(no memories)" {
		t.Fatalf("output = %q, want (no memories)", output)
	}
}

func TestMemorySearchJSONReturnsQueryAndMatches(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	match, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(match) error = %v", err)
	}
	if _, err := store.AddWithOptions("代码修改后顺手跑测试", memory.AddOptions{
		Kind: "rule",
	}); err != nil {
		t.Fatalf("AddWithOptions(other) error = %v", err)
	}

	output := captureStdout(t, func() {
		memorySearch([]string{
			"中文",
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--limit", "5",
		})
	})

	var payload struct {
		Library         string         `json:"library"`
		Query           string         `json:"query"`
		Limit           int            `json:"limit"`
		ArchivedOnly    bool           `json:"archived_only"`
		IncludeArchived bool           `json:"include_archived"`
		Entries         []memory.Entry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" || payload.Query != "中文" || payload.Limit != 5 {
		t.Fatalf("payload header = %#v", payload)
	}
	if payload.ArchivedOnly || payload.IncludeArchived {
		t.Fatalf("unexpected archive flags: %#v", payload)
	}
	if len(payload.Entries) == 0 || payload.Entries[0].ID != match.ID {
		t.Fatalf("entries = %#v, want %s first", payload.Entries, match.ID)
	}
}

func TestMemorySearchPlainTextReturnsMatchedSummaryLine(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	match, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(match) error = %v", err)
	}
	if _, err := store.AddWithOptions("代码修改后顺手跑测试", memory.AddOptions{
		Kind: "rule",
	}); err != nil {
		t.Fatalf("AddWithOptions(other) error = %v", err)
	}

	output := captureStdout(t, func() {
		memorySearch([]string{
			"中文",
			"--account", "assistant",
			"--repo", repo,
		})
	})

	if !strings.Contains(output, match.ID) {
		t.Fatalf("output missing matched id %s: %s", match.ID, output)
	}
	for _, want := range []string{
		"kind=preference",
		"priority=80",
		"text=以后默认用简体中文回复",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemorySearchPlainTextShowsNoMatchesWhenEmpty(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("代码修改后顺手跑测试", memory.AddOptions{Kind: "rule"}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memorySearch([]string{
			"中文",
			"--account", "assistant",
			"--repo", repo,
		})
	})
	if output != "(no matched memories)" {
		t.Fatalf("output = %q, want (no matched memories)", output)
	}
}

func TestMemorySearchJSONSupportsAllIncludingArchived(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	active, err := store.AddWithOptions("中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(active) error = %v", err)
	}
	archived, err := store.AddWithOptions("中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions(archived) error = %v", err)
	}
	if _, err := store.UpdateEntry(archived.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-03-01T00:00:00Z"),
	}); err != nil {
		t.Fatalf("UpdateEntry(archived) error = %v", err)
	}

	output := captureStdout(t, func() {
		memorySearch([]string{
			"中文",
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--all",
		})
	})

	var payload struct {
		IncludeArchived bool           `json:"include_archived"`
		ArchivedOnly    bool           `json:"archived_only"`
		Entries         []memory.Entry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if !payload.IncludeArchived || payload.ArchivedOnly {
		t.Fatalf("archive flags = %#v", payload)
	}
	if len(payload.Entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(payload.Entries))
	}
	ids := payload.Entries[0].ID + "," + payload.Entries[1].ID
	if !strings.Contains(ids, active.ID) || !strings.Contains(ids, archived.ID) {
		t.Fatalf("entries ids = %s, want active=%s archived=%s", ids, active.ID, archived.ID)
	}
}

func TestMemoryReviewJSONReportsDuplicatePromoteAndStaleSuggestions(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	now := time.Now().UTC()
	for _, entry := range []memory.Entry{
		{
			ID:        "mem-dup-keep",
			Text:      "以后默认用简体中文回复",
			Kind:      "preference",
			Priority:  70,
			Pinned:    true,
			CreatedAt: now.AddDate(0, 0, -5).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			ID:        "mem-dup-drop",
			Text:      "以后默认用简体中文回复",
			Kind:      "note",
			Priority:  10,
			CreatedAt: now.AddDate(0, 0, -4).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -2).Format(time.RFC3339),
		},
		{
			ID:               "mem-promote",
			Text:             "以后默认先给结论再解释",
			Kind:             "preference",
			Priority:         60,
			ReinforceCount:   2,
			LastReinforcedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
			CreatedAt:        now.AddDate(0, 0, -20).Format(time.RFC3339),
			UpdatedAt:        now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			ID:        "mem-stale",
			Text:      "临时记录：查过一次某个截图样式",
			Kind:      "note",
			Priority:  10,
			CreatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	output := captureStdout(t, func() {
		memoryReview([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--limit", "10",
			"--stale-days", "30",
		})
	})

	var payload struct {
		Library string `json:"library"`
		Report  struct {
			TotalEntries       int                     `json:"total_entries"`
			DuplicateGroups    []memory.DuplicateGroup `json:"duplicate_groups"`
			PromoteSuggestions []struct {
				Entry       memory.Entry `json:"entry"`
				Reason      string       `json:"reason"`
				TargetPin   bool         `json:"target_pin"`
				TargetScore int          `json:"target_score"`
			} `json:"promote_suggestions"`
			StaleSuggestions []struct {
				Entry    memory.Entry `json:"entry"`
				Reason   string       `json:"reason"`
				AgeDays  int          `json:"age_days"`
				LastSeen string       `json:"last_seen"`
			} `json:"stale_suggestions"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if payload.Report.TotalEntries != 4 {
		t.Fatalf("total_entries = %d, want 4", payload.Report.TotalEntries)
	}
	if len(payload.Report.DuplicateGroups) != 1 {
		t.Fatalf("duplicate_groups len = %d, want 1", len(payload.Report.DuplicateGroups))
	}
	if len(payload.Report.PromoteSuggestions) == 0 || payload.Report.PromoteSuggestions[0].Entry.ID != "mem-promote" {
		t.Fatalf("promote_suggestions = %#v", payload.Report.PromoteSuggestions)
	}
	if payload.Report.PromoteSuggestions[0].Reason == "" || !payload.Report.PromoteSuggestions[0].TargetPin {
		t.Fatalf("promote_suggestions[0] = %#v", payload.Report.PromoteSuggestions[0])
	}
	if len(payload.Report.StaleSuggestions) == 0 || payload.Report.StaleSuggestions[0].Entry.ID != "mem-stale" {
		t.Fatalf("stale_suggestions = %#v", payload.Report.StaleSuggestions)
	}
	if payload.Report.StaleSuggestions[0].AgeDays < 30 {
		t.Fatalf("stale_suggestions[0].age_days = %d, want >= 30", payload.Report.StaleSuggestions[0].AgeDays)
	}
}

func TestMemoryReviewApplyAllJSONReportsAppliedChanges(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	now := time.Now().UTC()
	for _, entry := range []memory.Entry{
		{
			ID:               "mem-promote",
			Text:             "以后默认先给结论再解释",
			Kind:             "preference",
			Priority:         60,
			ReinforceCount:   2,
			LastReinforcedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
			CreatedAt:        now.AddDate(0, 0, -20).Format(time.RFC3339),
			UpdatedAt:        now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			ID:        "mem-stale",
			Text:      "临时记录：查过一次某个截图样式",
			Kind:      "note",
			Priority:  10,
			CreatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	output := captureStdout(t, func() {
		memoryReview([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--apply-all",
			"--limit", "10",
			"--stale-days", "30",
		})
	})

	var payload struct {
		Library       string `json:"library"`
		ApplyPromote  bool   `json:"apply_promote"`
		ApplyStale    bool   `json:"apply_stale"`
		AppliedResult struct {
			Promoted []memory.Entry `json:"promoted"`
			Archived []memory.Entry `json:"archived"`
		} `json:"applied_result"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" || !payload.ApplyPromote || !payload.ApplyStale {
		t.Fatalf("payload header = %#v", payload)
	}
	if len(payload.AppliedResult.Promoted) != 1 || payload.AppliedResult.Promoted[0].ID != "mem-promote" {
		t.Fatalf("promoted = %#v", payload.AppliedResult.Promoted)
	}
	if !payload.AppliedResult.Promoted[0].Pinned || payload.AppliedResult.Promoted[0].Priority < 80 {
		t.Fatalf("promoted[0] = %#v", payload.AppliedResult.Promoted[0])
	}
	if len(payload.AppliedResult.Archived) != 1 || payload.AppliedResult.Archived[0].ID != "mem-stale" {
		t.Fatalf("archived = %#v", payload.AppliedResult.Archived)
	}
	if !payload.AppliedResult.Archived[0].Archived || payload.AppliedResult.Archived[0].ArchivedAt == "" {
		t.Fatalf("archived[0] = %#v", payload.AppliedResult.Archived[0])
	}
}

func TestMemoryReviewPlainTextIncludesSectionsAndSuggestions(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	now := time.Now().UTC()
	for _, entry := range []memory.Entry{
		{
			ID:               "mem-promote",
			Text:             "以后默认先给结论再解释",
			Kind:             "preference",
			Priority:         60,
			ReinforceCount:   2,
			LastReinforcedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
			CreatedAt:        now.AddDate(0, 0, -20).Format(time.RFC3339),
			UpdatedAt:        now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			ID:        "mem-stale",
			Text:      "临时记录：查过一次某个截图样式",
			Kind:      "note",
			Priority:  10,
			CreatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
			UpdatedAt: now.AddDate(0, 0, -90).Format(time.RFC3339),
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	output := captureStdout(t, func() {
		memoryReview([]string{
			"--account", "assistant",
			"--repo", repo,
			"--stale-days", "30",
		})
	})

	for _, want := range []string{
		"library=assistant",
		"[promote]",
		"[stale]",
		"reason=durable_preference",
		"reason=stale_unused_note",
		"suggest=suncodexclawd memory update --account \"assistant\" --priority 80 --pinned mem-promote",
		"suggest=suncodexclawd memory archive --account \"assistant\" mem-stale",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryReviewJSONUsesSnakeCaseNestedKeys(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "note",
		Priority: 20,
	}); err != nil {
		t.Fatalf("AddWithOptions(first) error = %v", err)
	}
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind: "note",
	}); err != nil {
		t.Fatalf("AddWithOptions(second) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryReview([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	report, ok := payload["report"].(map[string]any)
	if !ok {
		t.Fatalf("report = %#v, want object", payload["report"])
	}
	for _, key := range []string{
		"total_entries",
		"duplicate_groups",
		"promote_suggestions",
		"stale_suggestions",
	} {
		if _, ok := report[key]; !ok {
			t.Fatalf("report missing key %q: %#v", key, report)
		}
	}
	if _, ok := report["DuplicateGroups"]; ok {
		t.Fatalf("report unexpectedly contains Go-style key DuplicateGroups: %#v", report)
	}
}

func TestMemoryReviewApplyJSONUsesSnakeCaseNestedKeys(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "note",
		Priority: 20,
	}); err != nil {
		t.Fatalf("AddWithOptions(first) error = %v", err)
	}
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind: "note",
	}); err != nil {
		t.Fatalf("AddWithOptions(second) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryReview([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--apply-all",
		})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	applied, ok := payload["applied_result"].(map[string]any)
	if !ok {
		t.Fatalf("applied_result = %#v, want object", payload["applied_result"])
	}
	for _, key := range []string{"promoted", "archived"} {
		if _, ok := applied[key]; !ok {
			t.Fatalf("applied_result missing key %q: %#v", key, applied)
		}
	}
	if _, ok := applied["Promoted"]; ok {
		t.Fatalf("applied_result unexpectedly contains Go-style key Promoted: %#v", applied)
	}
}

func TestMemoryDedupeSupportsJSONPreviewAndApply(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	first, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(first) error = %v", err)
	}
	second, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(second) error = %v", err)
	}

	previewOutput := captureStdout(t, func() {
		memoryDedupe([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var previewPayload struct {
		Library          string                  `json:"library"`
		DryRun           bool                    `json:"dry_run"`
		DeleteCandidates int                     `json:"delete_candidates"`
		DuplicateGroups  []memory.DuplicateGroup `json:"duplicate_groups"`
	}
	if err := json.Unmarshal([]byte(previewOutput), &previewPayload); err != nil {
		t.Fatalf("json.Unmarshal(previewOutput) error = %v, body=%s", err, previewOutput)
	}
	if previewPayload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", previewPayload.Library)
	}
	if !previewPayload.DryRun {
		t.Fatalf("dry_run = false, want true")
	}
	if previewPayload.DeleteCandidates < 1 {
		t.Fatalf("delete_candidates = %d, want >= 1", previewPayload.DeleteCandidates)
	}
	if len(previewPayload.DuplicateGroups) != 1 {
		t.Fatalf("duplicate_groups len = %d, want 1", len(previewPayload.DuplicateGroups))
	}

	applyOutput := captureStdout(t, func() {
		memoryDedupe([]string{
			"--apply",
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var applyPayload struct {
		Library      string `json:"library"`
		DryRun       bool   `json:"dry_run"`
		MergedGroups int    `json:"merged_groups"`
		Deleted      int    `json:"deleted"`
		Results      []struct {
			Group      int          `json:"group"`
			SourceKeep string       `json:"source_keep"`
			DeletedIDs []string     `json:"deleted_ids"`
			Entry      memory.Entry `json:"entry"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(applyOutput), &applyPayload); err != nil {
		t.Fatalf("json.Unmarshal(applyOutput) error = %v, body=%s", err, applyOutput)
	}
	if applyPayload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", applyPayload.Library)
	}
	if applyPayload.DryRun {
		t.Fatalf("dry_run = true, want false")
	}
	if applyPayload.MergedGroups != 1 {
		t.Fatalf("merged_groups = %d, want 1", applyPayload.MergedGroups)
	}
	if applyPayload.Deleted != 1 {
		t.Fatalf("deleted = %d, want 1", applyPayload.Deleted)
	}
	if len(applyPayload.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(applyPayload.Results))
	}
	if applyPayload.Results[0].Group != 1 {
		t.Fatalf("results[0].group = %d, want 1", applyPayload.Results[0].Group)
	}
	if applyPayload.Results[0].SourceKeep == "" {
		t.Fatalf("results[0].source_keep is empty")
	}
	if len(applyPayload.Results[0].DeletedIDs) != 1 {
		t.Fatalf("results[0].deleted_ids len = %d, want 1", len(applyPayload.Results[0].DeletedIDs))
	}
	if applyPayload.Results[0].Entry.ID != first.ID && applyPayload.Results[0].Entry.ID != second.ID {
		t.Fatalf("results[0].entry.id = %q, want one of [%s %s]", applyPayload.Results[0].Entry.ID, first.ID, second.ID)
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("remaining entries = %d, want 1", len(entries))
	}
}

func TestMemoryDedupePlainTextPreviewIncludesMergeSuggestion(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	keep, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(keep) error = %v", err)
	}
	drop, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind: "note",
	})
	if err != nil {
		t.Fatalf("AddWithOptions(drop) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryDedupe([]string{
			"--account", "assistant",
			"--repo", repo,
		})
	})

	for _, want := range []string{
		"dry_run=true",
		"groups=1",
		"delete_candidates=1",
		"group=1",
		"keep=" + keep.ID,
		"drops=" + drop.ID,
		"suggest=suncodexclawd memory merge --account \"assistant\" " + keep.ID + " " + drop.ID,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryDedupePlainTextApplyReportsMergedSummary(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	keep, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(keep) error = %v", err)
	}
	drop, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind: "note",
	})
	if err != nil {
		t.Fatalf("AddWithOptions(drop) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryDedupe([]string{
			"--apply",
			"--account", "assistant",
			"--repo", repo,
		})
	})

	for _, want := range []string{
		"merged_group=1",
		"keep=" + keep.ID,
		"deleted=" + drop.ID,
		"dedupe=ok library=assistant groups=1 deleted=1",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryDedupeApplyJSONReturnsEmptyPayloadWhenNoDuplicates(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryDedupe([]string{
			"--apply",
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload struct {
		Library      string           `json:"library"`
		DryRun       bool             `json:"dry_run"`
		MergedGroups int              `json:"merged_groups"`
		Deleted      int              `json:"deleted"`
		Results      []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if payload.DryRun {
		t.Fatalf("dry_run = true, want false")
	}
	if payload.MergedGroups != 0 {
		t.Fatalf("merged_groups = %d, want 0", payload.MergedGroups)
	}
	if payload.Deleted != 0 {
		t.Fatalf("deleted = %d, want 0", payload.Deleted)
	}
	if len(payload.Results) != 0 {
		t.Fatalf("results len = %d, want 0", len(payload.Results))
	}
}

func TestMemoryDuplicatesJSONReturnsEmptyPayloadWhenNoDuplicates(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryDuplicates([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload struct {
		Library           string                  `json:"library"`
		Limit             int                     `json:"limit"`
		MinScore          int                     `json:"min_score"`
		DuplicateGroups   []memory.DuplicateGroup `json:"duplicate_groups"`
		DuplicateMemories int                     `json:"duplicate_memories"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if payload.Limit != 20 {
		t.Fatalf("limit = %d, want 20", payload.Limit)
	}
	if payload.MinScore != memory.DefaultDuplicateMinScore {
		t.Fatalf("min_score = %d, want %d", payload.MinScore, memory.DefaultDuplicateMinScore)
	}
	if payload.DuplicateMemories != 0 {
		t.Fatalf("duplicate_memories = %d, want 0", payload.DuplicateMemories)
	}
	if len(payload.DuplicateGroups) != 0 {
		t.Fatalf("duplicate_groups len = %d, want 0", len(payload.DuplicateGroups))
	}
}

func TestMemoryDuplicatesJSONUsesSnakeCaseNestedKeys(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"}); err != nil {
		t.Fatalf("AddWithOptions(first) error = %v", err)
	}
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"}); err != nil {
		t.Fatalf("AddWithOptions(second) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryDuplicates([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	groups, ok := payload["duplicate_groups"].([]any)
	if !ok || len(groups) == 0 {
		t.Fatalf("duplicate_groups = %#v, want non-empty array", payload["duplicate_groups"])
	}
	group, ok := groups[0].(map[string]any)
	if !ok {
		t.Fatalf("group = %#v, want object", groups[0])
	}
	for _, key := range []string{"keep", "score", "drops"} {
		if _, ok := group[key]; !ok {
			t.Fatalf("group missing key %q: %#v", key, group)
		}
	}
	if _, ok := group["Keep"]; ok {
		t.Fatalf("group unexpectedly contains Go-style key Keep: %#v", group)
	}
	drops, ok := group["drops"].([]any)
	if !ok || len(drops) == 0 {
		t.Fatalf("drops = %#v, want non-empty array", group["drops"])
	}
	drop, ok := drops[0].(map[string]any)
	if !ok {
		t.Fatalf("drop = %#v, want object", drops[0])
	}
	for _, key := range []string{"entry", "score", "reason"} {
		if _, ok := drop[key]; !ok {
			t.Fatalf("drop missing key %q: %#v", key, drop)
		}
	}
	if _, ok := drop["Entry"]; ok {
		t.Fatalf("drop unexpectedly contains Go-style key Entry: %#v", drop)
	}
}

func TestMemoryPurgeJSONReturnsEmptyPayloadWhenNoCandidates(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryPurge([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload struct {
		Library         string                 `json:"library"`
		DryRun          bool                   `json:"dry_run"`
		Days            int                    `json:"days"`
		Limit           int                    `json:"limit"`
		PurgeCandidates []memory.PurgeCandidate `json:"purge_candidates"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if !payload.DryRun {
		t.Fatalf("dry_run = false, want true")
	}
	if payload.Days != 30 {
		t.Fatalf("days = %d, want 30", payload.Days)
	}
	if payload.Limit != 20 {
		t.Fatalf("limit = %d, want 20", payload.Limit)
	}
	if len(payload.PurgeCandidates) != 0 {
		t.Fatalf("purge_candidates len = %d, want 0", len(payload.PurgeCandidates))
	}
}

func TestMemoryPurgeApplyJSONUsesStablePayload(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	entry, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	archived, err := store.UpdateEntry(entry.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-01-01T00:00:00Z"),
	})
	if err != nil {
		t.Fatalf("UpdateEntry() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryPurge([]string{
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--apply",
			"--days", "1",
		})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if got := payload["library"]; got != "assistant" {
		t.Fatalf("library = %#v, want assistant", got)
	}
	if got, ok := payload["dry_run"].(bool); !ok || got {
		t.Fatalf("dry_run = %#v, want false", payload["dry_run"])
	}
	deleted, ok := payload["deleted"].([]any)
	if !ok || len(deleted) != 1 {
		t.Fatalf("deleted = %#v, want single deleted id", payload["deleted"])
	}
	if deleted[0] != archived.ID {
		t.Fatalf("deleted[0] = %#v, want %s", deleted[0], archived.ID)
	}
}

func TestMemoryPurgePlainTextPreviewIncludesSuggestCommand(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	entry, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	if _, err := store.UpdateEntry(entry.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-01-01T00:00:00Z"),
	}); err != nil {
		t.Fatalf("UpdateEntry() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryPurge([]string{
			"--account", "assistant",
			"--repo", repo,
			"--days", "1",
		})
	})

	for _, want := range []string{
		"dry_run=true",
		"library=assistant",
		"purge_candidates=1",
		"purge=1",
		"archived_at=",
		"suggest=suncodexclawd memory purge --account \"assistant\" --days 1 --apply",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryPurgePlainTextShowsNoCandidatesWhenEmpty(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryPurge([]string{
			"--account", "assistant",
			"--repo", repo,
		})
	})

	for _, want := range []string{
		"dry_run=true library=assistant purge_candidates=0 days=30",
		"(no archived purge candidates)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryRecallJSONUsesSnakeCaseNestedKeys(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryRecall([]string{
			"中文回复",
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	matches, ok := payload["matches"].([]any)
	if !ok || len(matches) == 0 {
		t.Fatalf("matches = %#v, want non-empty array", payload["matches"])
	}
	match, ok := matches[0].(map[string]any)
	if !ok {
		t.Fatalf("match = %#v, want object", matches[0])
	}
	for _, key := range []string{"entry", "score", "reasons"} {
		if _, ok := match[key]; !ok {
			t.Fatalf("match missing key %q: %#v", key, match)
		}
	}
	if _, ok := match["Entry"]; ok {
		t.Fatalf("match unexpectedly contains Go-style key Entry: %#v", match)
	}
}

func TestMemoryRecallJSONSupportsArchivedOnly(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	active, err := store.AddWithOptions("中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(active) error = %v", err)
	}
	archived, err := store.AddWithOptions("中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions(archived) error = %v", err)
	}
	if _, err := store.UpdateEntry(archived.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-03-01T00:00:00Z"),
	}); err != nil {
		t.Fatalf("UpdateEntry(archived) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryRecall([]string{
			"中文",
			"--account", "assistant",
			"--repo", repo,
			"--json",
			"--archived",
		})
	})

	var payload struct {
		IncludeArchived bool `json:"include_archived"`
		ArchivedOnly    bool `json:"archived_only"`
		Matches         []struct {
			Entry memory.Entry `json:"entry"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.IncludeArchived || !payload.ArchivedOnly {
		t.Fatalf("archive flags = %#v", payload)
	}
	if len(payload.Matches) != 1 || payload.Matches[0].Entry.ID != archived.ID {
		t.Fatalf("matches = %#v, want only archived %s", payload.Matches, archived.ID)
	}
	if len(payload.Matches) == 1 && payload.Matches[0].Entry.ID == active.ID {
		t.Fatalf("matches unexpectedly returned active id %s", active.ID)
	}
}

func TestMemoryRecallJSONIncludesCollapsedSimilarReason(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	for _, entry := range []memory.Entry{
		{
			ID:        "mem-primary",
			Text:      "以后默认用简体中文回复",
			Priority:  80,
			Pinned:    true,
			CreatedAt: "2026-03-21T00:00:00Z",
			UpdatedAt: "2026-03-21T00:00:00Z",
		},
		{
			ID:        "mem-duplicate",
			Text:      "以后默认用简体中文回复",
			Priority:  20,
			CreatedAt: "2026-03-20T00:00:00Z",
			UpdatedAt: "2026-03-20T00:00:00Z",
		},
		{
			ID:        "mem-other",
			Text:      "回答前先给结论",
			Priority:  30,
			CreatedAt: "2026-03-19T00:00:00Z",
			UpdatedAt: "2026-03-19T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	output := captureStdout(t, func() {
		memoryRecall([]string{
			"请以后默认用简体中文回复，并回答前先给结论",
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload struct {
		Library string `json:"library"`
		Query   string `json:"query"`
		Matches []struct {
			Entry   memory.Entry `json:"entry"`
			Score   int          `json:"score"`
			Reasons []string     `json:"reasons"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant" {
		t.Fatalf("library = %q, want assistant", payload.Library)
	}
	if len(payload.Matches) != 2 {
		t.Fatalf("matches len = %d, want 2", len(payload.Matches))
	}
	if payload.Matches[0].Entry.ID != "mem-primary" {
		t.Fatalf("matches[0].entry.id = %q, want mem-primary", payload.Matches[0].Entry.ID)
	}
	if !strings.Contains(strings.Join(payload.Matches[0].Reasons, ","), "collapsed_similar:1") {
		t.Fatalf("matches[0].reasons = %#v, want collapsed_similar:1", payload.Matches[0].Reasons)
	}
	if payload.Matches[1].Entry.ID != "mem-other" {
		t.Fatalf("matches[1].entry.id = %q, want mem-other", payload.Matches[1].Entry.ID)
	}
}

func TestMemoryRecallPlainTextIncludesCollapsedSimilarReason(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	for _, entry := range []memory.Entry{
		{
			ID:        "mem-primary",
			Text:      "以后默认用简体中文回复",
			Priority:  80,
			Pinned:    true,
			CreatedAt: "2026-03-21T00:00:00Z",
			UpdatedAt: "2026-03-21T00:00:00Z",
		},
		{
			ID:        "mem-duplicate",
			Text:      "以后默认用简体中文回复",
			Priority:  20,
			CreatedAt: "2026-03-20T00:00:00Z",
			UpdatedAt: "2026-03-20T00:00:00Z",
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}

	output := captureStdout(t, func() {
		memoryRecall([]string{
			"请以后默认用简体中文回复",
			"--account", "assistant",
			"--repo", repo,
		})
	})

	for _, want := range []string{
		"query=请以后默认用简体中文回复",
		"recalled=1",
		"match_score=",
		"collapsed_similar:1",
		"mem-primary",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryRecallPlainTextShowsNoMatchesWhenEmpty(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("代码修改后顺手跑测试", memory.AddOptions{Kind: "rule"}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryRecall([]string{
			"中文",
			"--account", "assistant",
			"--repo", repo,
		})
	})

	for _, want := range []string{
		"query=中文 recalled=0",
		"(no recalled memories)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryRelatedJSONUsesSnakeCaseNestedKeys(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	target, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(target) error = %v", err)
	}
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"}); err != nil {
		t.Fatalf("AddWithOptions(related) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryRelated([]string{
			target.ID,
			"--account", "assistant",
			"--repo", repo,
			"--json",
		})
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	targetPayload, ok := payload["target"].(map[string]any)
	if !ok {
		t.Fatalf("target = %#v, want object", payload["target"])
	}
	if _, ok := targetPayload["id"]; !ok {
		t.Fatalf("target missing key id: %#v", targetPayload)
	}
	matches, ok := payload["matches"].([]any)
	if !ok || len(matches) == 0 {
		t.Fatalf("matches = %#v, want non-empty array", payload["matches"])
	}
	match, ok := matches[0].(map[string]any)
	if !ok {
		t.Fatalf("match = %#v, want object", matches[0])
	}
	for _, key := range []string{"entry", "score", "reason"} {
		if _, ok := match[key]; !ok {
			t.Fatalf("match missing key %q: %#v", key, match)
		}
	}
	if _, ok := match["Entry"]; ok {
		t.Fatalf("match unexpectedly contains Go-style key Entry: %#v", match)
	}
}

func TestMemoryRelatedPlainTextIncludesMergeSuggestion(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	target, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(target) error = %v", err)
	}
	related, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions(related) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryRelated([]string{
			target.ID,
			"--account", "assistant",
			"--repo", repo,
		})
	})

	for _, want := range []string{
		"target=" + target.ID,
		"related=1",
		"match_score=",
		"reason=",
		"suggest=suncodexclawd memory merge --account \"assistant\" " + target.ID + " " + related.ID,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryDuplicatesPlainTextIncludesMergeSuggestion(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	keep, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(keep) error = %v", err)
	}
	drop, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind: "note",
	})
	if err != nil {
		t.Fatalf("AddWithOptions(drop) error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryDuplicates([]string{
			"--account", "assistant",
			"--repo", repo,
		})
	})

	for _, want := range []string{
		"duplicate_groups=1",
		"duplicate_memories=1",
		"group=1",
		"keep=" + keep.ID,
		"drops=" + drop.ID,
		"match_score=",
		"reason=",
		"suggest=suncodexclawd memory merge --account \"assistant\" " + keep.ID + " " + drop.ID,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryDuplicatesPlainTextShowsNoDuplicatesWhenEmpty(t *testing.T) {
	repo := t.TempDir()
	store := memory.NewLibraryStore(repo, "assistant")
	if _, err := store.AddWithOptions("代码修改后顺手跑测试", memory.AddOptions{Kind: "rule"}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}

	output := captureStdout(t, func() {
		memoryDuplicates([]string{
			"--account", "assistant",
			"--repo", repo,
		})
	})
	if output != "(no duplicate memories)" {
		t.Fatalf("output = %q, want (no duplicate memories)", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return strings.TrimSpace(string(body))
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = oldStderr
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return strings.TrimSpace(string(body))
}

func runMemoryCmdSubprocess(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmdArgs := []string{"-test.run=TestMemoryCmdSubprocessHelper", "--"}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(), "GO_WANT_MEMORY_CMD_HELPER=1")
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err == nil {
		return 0, trimmed
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("subprocess error = %v\n%s", err, trimmed)
	}
	return exitErr.ExitCode(), trimmed
}

func TestMemoryCmdSubprocessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MEMORY_CMD_HELPER") != "1" {
		return
	}
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i
			break
		}
	}
	if index == -1 {
		os.Exit(0)
	}
	memoryCmd(os.Args[index+1:])
	os.Exit(0)
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	fn()
}

func setupWorkspaceConfig(t *testing.T, body string) (repo string, workspace string, nested string, cfgPath string) {
	t.Helper()
	repo = t.TempDir()
	workspace = filepath.Join(repo, "workspace", "assistant")
	nested = filepath.Join(workspace, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	cfgPath = filepath.Join(workspace, ".config.toml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return repo, workspace, nested, cfgPath
}

func setupMemoryWorkspaceConfig(t *testing.T) (repo string, nested string, store *memory.Store) {
	t.Helper()
	repo, _, nested, _ = setupWorkspaceConfig(t, ""+
		"[bot]\n" +
		"account = \"assistant\"\n\n" +
		"[runtime]\n" +
		"memory_account = \"assistant-memory\"\n")
	return repo, nested, memory.NewLibraryStore(repo, "assistant-memory")
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
	_, _, nested, _ := setupWorkspaceConfig(t, ""+
		"[bot]\n" +
		"account = \"assistant\"\n\n" +
		"[runtime]\n" +
		"memory_account = \"assistant-memory\"\n" +
		"timer_account = \"assistant-timer\"\n")
	withWorkingDir(t, nested, func() {
		got, err := resolveScopedAccount("", "timer")
		if err != nil {
			t.Fatalf("resolveScopedAccount() error = %v", err)
		}
		if got != "assistant-timer" {
			t.Fatalf("resolveScopedAccount() = %q, want assistant-timer", got)
		}
	})
}

func TestResolveScopedAccountReportsConfigPathWhenWorkspaceConfigLacksScope(t *testing.T) {
	_, _, nested, cfgPath := setupWorkspaceConfig(t, ""+
		"[runtime]\n" +
		"memory_account = \"assistant-memory\"\n")
	withWorkingDir(t, nested, func() {
		_, err := resolveScopedAccount("", "timer")
		if err == nil {
			t.Fatalf("resolveScopedAccount() error = nil, want error")
		}
		if !strings.Contains(err.Error(), cfgPath) {
			t.Fatalf("resolveScopedAccount() error = %v, want cfg path %s", err, cfgPath)
		}
	})
}

func TestResolveMemoryAccountUsesMemoryScopeNotSyncScope(t *testing.T) {
	_, _, nested, _ := setupWorkspaceConfig(t, ""+
		"[bot]\n" +
		"account = \"assistant\"\n\n" +
		"[runtime]\n" +
		"memory_account = \"assistant-memory\"\n" +
		"sync_account = \"assistant-sync\"\n")
	withWorkingDir(t, nested, func() {
		got, err := resolveMemoryAccount("")
		if err != nil {
			t.Fatalf("resolveMemoryAccount() error = %v", err)
		}
		if got != "assistant-memory" {
			t.Fatalf("resolveMemoryAccount() = %q, want assistant-memory", got)
		}
	})
}

func TestMemoryShowInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryShow([]string{
				entry.ID,
				"--repo", tmp,
			})
		})
	})

	var payload memory.Entry
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.ID != entry.ID {
		t.Fatalf("id = %q, want %q", payload.ID, entry.ID)
	}
}

func TestMemoryStatsInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	if _, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	}); err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryStats([]string{
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library string `json:"library"`
		Stats   struct {
			TotalEntries  int `json:"total_entries"`
			PinnedEntries int `json:"pinned_entries"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if payload.Stats.TotalEntries != 1 || payload.Stats.PinnedEntries != 1 {
		t.Fatalf("stats = %#v", payload.Stats)
	}
}

func TestMemoryAddInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, _ := setupMemoryWorkspaceConfig(t)
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryAdd([]string{
				"以后默认用简体中文回复",
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if payload.Action != "added" {
		t.Fatalf("action = %q, want added", payload.Action)
	}

	store := memory.NewLibraryStore(tmp, "assistant-memory")
	entries, err := store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != payload.Entry.ID {
		t.Fatalf("entries = %#v, want added id %s", entries, payload.Entry.ID)
	}
}

func TestMemoryListInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryList([]string{
				"--repo", tmp,
			})
		})
	})

	for _, want := range []string{
		entry.ID,
		"kind=preference",
		"priority=80",
		"text=以后默认用简体中文回复",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
}

func TestMemoryForceInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	first, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryForce([]string{
				"以后默认用简体中文回复",
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library  string       `json:"library"`
		Action   string       `json:"action"`
		ForceNew bool         `json:"force_new"`
		Entry    memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if payload.Action != "added" || !payload.ForceNew {
		t.Fatalf("payload = %#v, want added + force_new", payload)
	}
	if payload.Entry.ID == "" || payload.Entry.ID == first.ID {
		t.Fatalf("entry.ID = %q, want new id distinct from %q", payload.Entry.ID, first.ID)
	}

	entries, err := store.ListEntriesAll()
	if err != nil {
		t.Fatalf("ListEntriesAll() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
}

func TestMemoryRecallInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryRecall([]string{
				"中文回复",
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library string `json:"library"`
		Matches []struct {
			Entry memory.Entry `json:"entry"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if len(payload.Matches) != 1 || payload.Matches[0].Entry.ID != entry.ID {
		t.Fatalf("matches = %#v, want %s", payload.Matches, entry.ID)
	}
}

func TestMemorySearchInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认用简体中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
	})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memorySearch([]string{
				"中文",
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library string         `json:"library"`
		Query   string         `json:"query"`
		Entries []memory.Entry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" || payload.Query != "中文" {
		t.Fatalf("payload header = %#v", payload)
	}
	if len(payload.Entries) != 1 || payload.Entries[0].ID != entry.ID {
		t.Fatalf("entries = %#v, want %s", payload.Entries, entry.ID)
	}
}

func TestMemoryReviewInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	now := time.Now().UTC()
	for _, entry := range []memory.Entry{
		{
			ID:               "mem-promote",
			Text:             "以后默认先给结论再解释",
			Kind:             "preference",
			Priority:         60,
			ReinforceCount:   2,
			LastReinforcedAt: now.AddDate(0, 0, -1).Format(time.RFC3339),
			CreatedAt:        now.AddDate(0, 0, -20).Format(time.RFC3339),
			UpdatedAt:        now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
	} {
		if err := store.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry(%s) error = %v", entry.ID, err)
		}
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryReview([]string{
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library string `json:"library"`
		Report  struct {
			PromoteSuggestions []struct {
				Entry memory.Entry `json:"entry"`
			} `json:"promote_suggestions"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if len(payload.Report.PromoteSuggestions) != 1 || payload.Report.PromoteSuggestions[0].Entry.ID != "mem-promote" {
		t.Fatalf("promote_suggestions = %#v", payload.Report.PromoteSuggestions)
	}
}

func TestMemoryRelatedInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	target, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions(target) error = %v", err)
	}
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"}); err != nil {
		t.Fatalf("AddWithOptions(related) error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryRelated([]string{
				target.ID,
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library string `json:"library"`
		Target  memory.Entry `json:"target"`
		Matches []struct {
			Entry memory.Entry `json:"entry"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if payload.Target.ID != target.ID {
		t.Fatalf("target.id = %q, want %q", payload.Target.ID, target.ID)
	}
	if len(payload.Matches) != 1 {
		t.Fatalf("matches = %#v, want 1 related match", payload.Matches)
	}
}

func TestMemoryUpdateInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryUpdate([]string{
				entry.ID,
				"--repo", tmp,
				"--json",
				"--text", "以后默认先给结论再解释",
				"--kind", "preference",
				"--priority", "80",
				"--pinned",
			})
		})
	})

	var payload struct {
		Library string `json:"library"`
		Action  string `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if payload.Action != "updated" {
		t.Fatalf("action = %q, want updated", payload.Action)
	}
	if payload.Entry.ID != entry.ID || payload.Entry.Kind != "preference" || payload.Entry.Priority != 80 || !payload.Entry.Pinned {
		t.Fatalf("entry = %#v", payload.Entry)
	}
}

func TestMemoryPinInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "preference"})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryPin([]string{
				entry.ID,
				"--repo", tmp,
				"--json",
			}, true)
		})
	})

	var payload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if payload.Action != "pinned" || !payload.Entry.Pinned {
		t.Fatalf("payload = %#v, want pinned entry", payload)
	}
}

func TestMemoryArchiveInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryArchive([]string{
				entry.ID,
				"--repo", tmp,
				"--json",
			}, true)
		})
	})

	var payload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if payload.Action != "archived" || !payload.Entry.Archived || payload.Entry.ArchivedAt == "" {
		t.Fatalf("payload = %#v, want archived entry", payload)
	}
}

func TestMemoryDuplicatesInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	keep, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(keep) error = %v", err)
	}
	if _, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"}); err != nil {
		t.Fatalf("AddWithOptions(drop) error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryDuplicates([]string{
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library          string                  `json:"library"`
		DuplicateGroups  []memory.DuplicateGroup `json:"duplicate_groups"`
		DuplicateMemories int                    `json:"duplicate_memories"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" {
		t.Fatalf("library = %q, want assistant-memory", payload.Library)
	}
	if payload.DuplicateMemories != 1 || len(payload.DuplicateGroups) != 1 || payload.DuplicateGroups[0].Keep.ID != keep.ID {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestMemoryDeleteInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryDelete([]string{
				entry.ID,
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library   string        `json:"library"`
		Action    string        `json:"action"`
		DeletedID string        `json:"deleted_id"`
		Entry     *memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" || payload.Action != "deleted" || payload.DeletedID != entry.ID {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Entry == nil || payload.Entry.ID != entry.ID {
		t.Fatalf("entry = %#v, want %s", payload.Entry, entry.ID)
	}
}

func TestMemoryPurgeInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	if _, err := store.UpdateEntry(entry.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-01-01T00:00:00Z"),
	}); err != nil {
		t.Fatalf("UpdateEntry() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryPurge([]string{
				"--repo", tmp,
				"--json",
				"--apply",
				"--days", "1",
			})
		})
	})

	var payload struct {
		Library string   `json:"library"`
		DryRun  bool     `json:"dry_run"`
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" || payload.DryRun {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.Deleted) != 1 || payload.Deleted[0] != entry.ID {
		t.Fatalf("deleted = %#v, want [%s]", payload.Deleted, entry.ID)
	}
}

func TestMemoryMergeInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	keep, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(keep) error = %v", err)
	}
	drop, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions(drop) error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryMerge([]string{
				keep.ID,
				drop.ID,
				"--repo", tmp,
				"--json",
			})
		})
	})

	var payload struct {
		Library    string       `json:"library"`
		Action     string       `json:"action"`
		DeletedIDs []string     `json:"deleted_ids"`
		Entry      memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" || payload.Action != "merged" {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.DeletedIDs) != 1 || payload.DeletedIDs[0] != drop.ID || payload.Entry.ID != keep.ID {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestMemoryDedupeInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	keep, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{
		Kind:     "preference",
		Priority: 80,
		Pinned:   true,
	})
	if err != nil {
		t.Fatalf("AddWithOptions(keep) error = %v", err)
	}
	drop, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions(drop) error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryDedupe([]string{
				"--repo", tmp,
				"--json",
				"--apply",
			})
		})
	})

	var payload struct {
		Library      string `json:"library"`
		DryRun       bool   `json:"dry_run"`
		MergedGroups int    `json:"merged_groups"`
		Deleted      int    `json:"deleted"`
		Results      []struct {
			SourceKeep string `json:"source_keep"`
			DeletedIDs []string `json:"deleted_ids"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" || payload.DryRun || payload.MergedGroups != 1 || payload.Deleted != 1 {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.Results) != 1 || payload.Results[0].SourceKeep != keep.ID || len(payload.Results[0].DeletedIDs) != 1 || payload.Results[0].DeletedIDs[0] != drop.ID {
		t.Fatalf("payload results = %#v", payload.Results)
	}
}

func TestMemoryUnarchiveInfersAccountFromWorkspaceConfig(t *testing.T) {
	tmp, nested, store := setupMemoryWorkspaceConfig(t)
	entry, err := store.AddWithOptions("以后默认中文回复", memory.AddOptions{Kind: "note"})
	if err != nil {
		t.Fatalf("AddWithOptions() error = %v", err)
	}
	if _, err := store.UpdateEntry(entry.ID, memory.UpdateOptions{
		Archived:   boolPtr(true),
		ArchivedAt: stringPtr("2026-01-01T00:00:00Z"),
	}); err != nil {
		t.Fatalf("UpdateEntry() error = %v", err)
	}
	output := ""
	withWorkingDir(t, nested, func() {
		output = captureStdout(t, func() {
			memoryArchive([]string{
				entry.ID,
				"--repo", tmp,
				"--json",
			}, false)
		})
	})

	var payload struct {
		Library string       `json:"library"`
		Action  string       `json:"action"`
		Entry   memory.Entry `json:"entry"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json.Unmarshal(output) error = %v, body=%s", err, output)
	}
	if payload.Library != "assistant-memory" || payload.Action != "unarchived" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Entry.ID != entry.ID || payload.Entry.Archived || payload.Entry.ArchivedAt != "" {
		t.Fatalf("entry = %#v", payload.Entry)
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

func TestReorderFlagsBeforePositionalsSupportsMultipleBoolFlags(t *testing.T) {
	got := reorderFlagsBeforePositionals([]string{"mem-123", "--json", "--account", "assistant", "--pinned"}, map[string]bool{
		"--json":   true,
		"--pinned": true,
	})
	want := []string{"--json", "--account", "assistant", "--pinned", "mem-123"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reorderFlagsBeforePositionals() = %v, want %v", got, want)
	}
}

func TestReorderFlagsBeforePositionalsPreservesMultiplePositionalsForMergeStyleCommands(t *testing.T) {
	got := reorderFlagsBeforePositionals([]string{"keep-id", "drop-a", "drop-b", "--account", "assistant", "--json"}, map[string]bool{
		"--json": true,
	})
	want := []string{"--account", "assistant", "--json", "keep-id", "drop-a", "drop-b"}
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
