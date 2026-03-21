package configstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTOMLMiniRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		"[shared]",
		"bot_name = \"SunCodexClaw\"",
		"require_mention = true",
		"mention_aliases = [\"a\", \"b\"]",
		"",
		"[shared.progress]",
		"mode = \"doc\"",
		"",
		"[bot.assistant]",
		"bot_name = \"Assistant\"",
		"",
		"[bot.assistant.codex]",
		"cwd = \"/workspace/assistant\"",
		"history_turns = 6",
		"",
	}, "\n")
	doc, err := parseTOML([]byte(src))
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	got := normalizeText(doc.stringify())
	if got != normalizeText(src) {
		t.Fatalf("round-trip mismatch\n--- expected ---\n%s\n--- got ---\n%s", normalizeText(src), got)
	}
}

func TestReadOverlayMergesSharedAndAccountFromTOML(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		"[shared]",
		"reply_prefix = \"AI 助手：\"",
		"",
		"[shared.progress]",
		"mode = \"doc\"",
		"",
		"[bot.assistant]",
		"bot_name = \"Assistant\"",
		"",
		"[bot.assistant.codex]",
		"cwd = \"/workspace/assistant\"",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(tmp, "config", "feishu", "bots.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store := NewStore(tmp)
	got, err := store.ReadOverlay("assistant")
	if err != nil {
		t.Fatalf("ReadOverlay: %v", err)
	}
	if got["reply_prefix"] != "AI 助手：" {
		t.Fatalf("reply_prefix mismatch: %#v", got["reply_prefix"])
	}
	if got["bot_name"] != "Assistant" {
		t.Fatalf("bot_name mismatch: %#v", got["bot_name"])
	}
	if getNestedString(got, "progress", "mode") != "doc" {
		t.Fatalf("progress.mode mismatch: %#v", got["progress"])
	}
	if getNestedString(got, "codex", "cwd") != "/workspace/assistant" {
		t.Fatalf("codex.cwd mismatch: %#v", got["codex"])
	}
}

func TestWriteOverlayCreatesBotTableInTOML(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store := NewStore(tmp)
	if err := store.WriteOverlay("assistant", map[string]any{
		"bot_name": "Assistant",
		"codex": map[string]any{
			"cwd": "/workspace/assistant",
		},
	}); err != nil {
		t.Fatalf("WriteOverlay: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(tmp, "config", "feishu", "bots.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(b)
	if !strings.Contains(text, "[bot.assistant]") {
		t.Fatalf("missing bot section:\n%s", text)
	}
	if !strings.Contains(text, "[bot.assistant.codex]") {
		t.Fatalf("missing codex subsection:\n%s", text)
	}
}

func TestOverlayTargetLabelQuotesSpecialAccountNames(t *testing.T) {
	store := NewStore("/repo")
	got := store.OverlayTargetLabel("assistant.bot")
	want := filepath.Join("/repo", "config", "feishu", "bots.toml") + ` [bot."assistant.bot"]`
	if got != want {
		t.Fatalf("OverlayTargetLabel() = %q, want %q", got, want)
	}
	if got := store.SecretsEntryLabel("feishu", "assistant.bot"); got != `[feishu."assistant.bot"]` {
		t.Fatalf("SecretsEntryLabel() = %q, want quoted account", got)
	}
}

func TestResolveRuntimeAccountFromDirUsesScopedRuntimeAccount(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace", "assistant", "nested")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		"[bot]",
		"account = \"assistant\"",
		"",
		"[runtime]",
		"memory_account = \"assistant-memory\"",
		"timer_account = \"assistant-timer\"",
		"sync_account = \"assistant-sync\"",
		"",
	}, "\n")
	cfgPath := filepath.Join(tmp, "workspace", "assistant", ".config.toml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, usedPath, err := ResolveRuntimeAccountFromDir(workspace, "timer")
	if err != nil {
		t.Fatalf("ResolveRuntimeAccountFromDir: %v", err)
	}
	if got != "assistant-timer" {
		t.Fatalf("account = %q, want assistant-timer", got)
	}
	if usedPath != cfgPath {
		t.Fatalf("path = %q, want %q", usedPath, cfgPath)
	}
}

func TestResolveRuntimeAccountFromDirFallsBackToBotAccount(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		"[bot]",
		"account = \"assistant\"",
		"",
	}, "\n")
	cfgPath := filepath.Join(tmp, "workspace", ".config.toml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, usedPath, err := ResolveRuntimeAccountFromDir(filepath.Join(tmp, "workspace"), "memory")
	if err != nil {
		t.Fatalf("ResolveRuntimeAccountFromDir: %v", err)
	}
	if got != "assistant" {
		t.Fatalf("account = %q, want assistant", got)
	}
	if usedPath != cfgPath {
		t.Fatalf("path = %q, want %q", usedPath, cfgPath)
	}
}

func TestListEnabledAccountNamesFiltersDisabledBots(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("mkdir feishu: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("mkdir secrets: %v", err)
	}
	bots := strings.Join([]string{
		"[bot.assistant]",
		"enabled = true",
		"",
		"[bot.reviewer]",
		"enabled = false",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(tmp, "config", "feishu", "bots.toml"), []byte(bots), 0o644); err != nil {
		t.Fatalf("write bots.toml: %v", err)
	}
	secrets := strings.Join([]string{
		"[feishu.assistant]",
		"app_id = \"cli_a\"",
		"",
		"[feishu.reviewer]",
		"app_id = \"cli_b\"",
		"",
		"[feishu.helper]",
		"app_id = \"cli_c\"",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(tmp, "config", "secrets", "local.toml"), []byte(secrets), 0o644); err != nil {
		t.Fatalf("write local.toml: %v", err)
	}
	store := NewStore(tmp)
	got, err := store.ListEnabledAccountNames()
	if err != nil {
		t.Fatalf("ListEnabledAccountNames: %v", err)
	}
	want := []string{"assistant", "helper"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("enabled accounts = %v, want %v", got, want)
	}
}

func TestReadOverlayDerivesCwdFromSanitizedAccountNamespace(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("mkdir feishu: %v", err)
	}
	body := strings.Join([]string{
		"[shared.codex]",
		"cwd_root = \"workspace\"",
		"",
		"[bot.\"assistant/bot\"]",
		"bot_name = \"Assistant\"",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(tmp, "config", "feishu", "bots.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write bots.toml: %v", err)
	}
	store := NewStore(tmp)
	got, err := store.ReadOverlay("assistant/bot")
	if err != nil {
		t.Fatalf("ReadOverlay: %v", err)
	}
	if got["bot_name"] != "Assistant" {
		t.Fatalf("bot_name mismatch: %#v", got["bot_name"])
	}
	if getNestedString(got, "codex", "cwd") != "workspace/assistant-bot" {
		t.Fatalf("codex.cwd = %q, want workspace/assistant-bot", getNestedString(got, "codex", "cwd"))
	}
}

func getNestedString(m map[string]any, parts ...string) string {
	cur := any(m)
	for _, part := range parts {
		next, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = next[part]
	}
	v, _ := cur.(string)
	return v
}

func normalizeText(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}
