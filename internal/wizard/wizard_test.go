package wizard

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"suncodexclaw/internal/configstore"
)

func TestParseIntRejectsTrailingGarbage(t *testing.T) {
	if _, err := parseInt("12x"); err == nil {
		t.Fatalf("parseInt() error = nil, want invalid int")
	}
}

func TestParseListTrimsDedupesAndSkipsEmpty(t *testing.T) {
	got := parseList(" alpha, beta\nalpha , , gamma \n")
	want := []string{"alpha", "beta", "gamma"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("parseList() = %v, want %v", got, want)
	}
}

func TestAskForItemYesOptionalBlankSkips(t *testing.T) {
	value, ok, err := askForItem(bufio.NewReader(strings.NewReader("")), missingItem{
		Key:      "codex.model",
		Optional: true,
	}, true)
	if err != nil {
		t.Fatalf("askForItem() error = %v", err)
	}
	if ok {
		t.Fatalf("askForItem() ok = true, want false value=%v", value)
	}
}

func TestAskForItemYesRequiredWithoutDefaultFails(t *testing.T) {
	_, _, err := askForItem(bufio.NewReader(strings.NewReader("")), missingItem{
		Key:    "app_id",
		Prompt: "Feishu app_id",
	}, true)
	if err == nil || !strings.Contains(err.Error(), "missing required field") {
		t.Fatalf("askForItem() error = %v, want missing required field", err)
	}
}

func TestInspectConfigMergesOverlaySecretsAndSync(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(strings.Join([]string{
		"[shared]",
		"reply_prefix = \"Shared：\"",
		"",
		"[shared.codex]",
		"cwd_root = \"workspace\"",
		"",
		"[bot.assistant]",
		"bot_name = \"Assistant\"",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(bots.toml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(strings.Join([]string{
		"[feishu.default]",
		"reply_prefix = \"Default：\"",
		"",
		"[feishu.assistant]",
		"app_id = \"cli_xxx\"",
		"reply_prefix = \"Secret：\"",
		"",
		"[sync.default]",
		"provider = \"webdav\"",
		"",
		"[sync.default.webdav]",
		"base_path = \"/shared\"",
		"",
		"[sync.assistant]",
		"workspace_id = \"assistant-private\"",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(local.toml) error = %v", err)
	}

	store := configstore.NewStore(repo)
	inspect, effective, err := inspectConfig(store, "assistant")
	if err != nil {
		t.Fatalf("inspectConfig() error = %v", err)
	}
	if inspect.Paths.AccountConfig != filepath.Join(repo, "config", "feishu", "bots.toml")+" [bot.assistant]" {
		t.Fatalf("AccountConfig = %q", inspect.Paths.AccountConfig)
	}
	if got, _ := getDottedValue(effective, "app_id"); got != "cli_xxx" {
		t.Fatalf("effective app_id = %v, want cli_xxx", got)
	}
	if got, _ := getDottedValue(effective, "bot_name"); got != "Assistant" {
		t.Fatalf("effective bot_name = %v, want Assistant", got)
	}
	if got, _ := getDottedValue(effective, "reply_prefix"); got != "Secret：" {
		t.Fatalf("effective reply_prefix = %v, want Secret：", got)
	}
	if got, _ := getDottedValue(effective, "codex.cwd"); got != filepath.ToSlash(filepath.Join("workspace", "assistant")) {
		t.Fatalf("effective codex.cwd = %v, want workspace/assistant", got)
	}
	if got, _ := getDottedValue(effective, "sync.provider"); got != "webdav" {
		t.Fatalf("effective sync.provider = %v, want webdav", got)
	}
	if got, _ := getDottedValue(effective, "sync.workspace_id"); got != "assistant-private" {
		t.Fatalf("effective sync.workspace_id = %v, want assistant-private", got)
	}
	if got, _ := getDottedValue(effective, "sync.webdav.base_path"); got != "/shared" {
		t.Fatalf("effective sync.webdav.base_path = %v, want /shared", got)
	}
}

func TestInspectConfigPrefersAccountSecretsAndIgnoresSharedSyncWorkspaceID(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(strings.Join([]string{
		"[shared]",
		"reply_prefix = \"Overlay：\"",
		"",
		"[bot.assistant]",
		"bot_name = \"Assistant\"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(bots.toml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(strings.Join([]string{
		"[feishu.default]",
		"reply_prefix = \"Default：\"",
		"",
		"[feishu.assistant]",
		"app_id = \"cli_xxx\"",
		"reply_prefix = \"Secret：\"",
		"",
		"[sync.default]",
		"workspace_id = \"shared-default\"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(local.toml) error = %v", err)
	}

	store := configstore.NewStore(repo)
	_, effective, err := inspectConfig(store, "assistant")
	if err != nil {
		t.Fatalf("inspectConfig() error = %v", err)
	}
	if got, _ := getDottedValue(effective, "reply_prefix"); got != "Secret：" {
		t.Fatalf("effective reply_prefix = %v, want Secret：", got)
	}
	if got, _ := getDottedValue(effective, "sync.workspace_id"); got != "assistant" {
		t.Fatalf("effective sync.workspace_id = %v, want assistant", got)
	}
}

func TestApplyPatchesWritesOverlayAndSecrets(t *testing.T) {
	repo := t.TempDir()
	store := configstore.NewStore(repo)
	err := applyPatches(store, "assistant", applyRequest{
		Secrets: map[string]any{
			"app_id": "cli_xxx",
			"sync": map[string]any{
				"provider": "webdav",
				"webdav": map[string]any{
					"url": "https://dav.example.com",
				},
			},
		},
		SharedOverlay: map[string]any{
			"codex": map[string]any{
				"cwd_root": "workspace",
			},
		},
		Overlay: map[string]any{
			"bot_name": "Assistant",
			"progress": map[string]any{
				"mode": "doc",
			},
		},
	})
	if err != nil {
		t.Fatalf("applyPatches() error = %v", err)
	}

	overlay, err := store.ReadOverlay("assistant")
	if err != nil {
		t.Fatalf("ReadOverlay() error = %v", err)
	}
	if got, _ := getDottedValue(overlay, "bot_name"); got != "Assistant" {
		t.Fatalf("overlay bot_name = %v, want Assistant", got)
	}
	if got, _ := getDottedValue(overlay, "progress.mode"); got != "doc" {
		t.Fatalf("overlay progress.mode = %v, want doc", got)
	}
	if got, _ := getDottedValue(overlay, "codex.cwd"); got != filepath.ToSlash(filepath.Join("workspace", "assistant")) {
		t.Fatalf("overlay codex.cwd = %v, want workspace/assistant", got)
	}

	secrets, err := store.ReadSecretsEntry("feishu", "assistant")
	if err != nil {
		t.Fatalf("ReadSecretsEntry(feishu) error = %v", err)
	}
	if got, _ := getDottedValue(secrets, "app_id"); got != "cli_xxx" {
		t.Fatalf("secrets app_id = %v, want cli_xxx", got)
	}

	syncCfg, err := store.ReadSecretsEntry("sync", "assistant")
	if err != nil {
		t.Fatalf("ReadSecretsEntry(sync) error = %v", err)
	}
	if got, _ := getDottedValue(syncCfg, "provider"); got != "webdav" {
		t.Fatalf("sync provider = %v, want webdav", got)
	}
	if got, _ := getDottedValue(syncCfg, "webdav.url"); got != "https://dav.example.com" {
		t.Fatalf("sync webdav.url = %v, want https://dav.example.com", got)
	}
}

func TestBuildBootstrapRequestUsesActualAccount(t *testing.T) {
	repo := t.TempDir()
	store := configstore.NewStore(repo)

	req, err := buildBootstrapRequest(store, "openclaw")
	if err != nil {
		t.Fatalf("buildBootstrapRequest() error = %v", err)
	}
	if got, _ := getDottedValue(req.Overlay, "bot_name"); got != "飞书 Codex 助手 openclaw" {
		t.Fatalf("overlay bot_name = %v, want account-specific default", got)
	}
	if got, _ := getDottedValue(req.Secrets, "app_id"); got != "cli_xxx" {
		t.Fatalf("secrets app_id = %v, want cli_xxx", got)
	}
	if got, _ := getDottedValue(req.Secrets, "codex.base_url"); got != "https://api.openai.com/v1" {
		t.Fatalf("secrets codex.base_url = %v, want official default", got)
	}
}

func TestBuildBootstrapRequestKeepsExistingValues(t *testing.T) {
	repo := t.TempDir()
	store := configstore.NewStore(repo)
	if err := applyPatches(store, "openclaw", applyRequest{
		Overlay: map[string]any{
			"bot_name": "Existing Bot",
		},
		Secrets: map[string]any{
			"app_id": "cli_real",
			"codex": map[string]any{
				"base_url": "http://gateway.local/v1",
			},
		},
	}); err != nil {
		t.Fatalf("applyPatches() error = %v", err)
	}

	req, err := buildBootstrapRequest(store, "openclaw")
	if err != nil {
		t.Fatalf("buildBootstrapRequest() error = %v", err)
	}
	if got, ok := getDottedValue(req.Overlay, "bot_name"); ok {
		t.Fatalf("overlay bot_name bootstrap should not override existing value: %v", got)
	}
	if got, ok := getDottedValue(req.Secrets, "app_id"); ok {
		t.Fatalf("secrets app_id bootstrap should not override existing value: %v", got)
	}
	if got, ok := getDottedValue(req.Secrets, "codex.base_url"); ok {
		t.Fatalf("secrets codex.base_url bootstrap should not override existing value: %v", got)
	}
}

func TestBootstrapAndApplyPatchesWriteQuotedAccountTables(t *testing.T) {
	repo := t.TempDir()
	store := configstore.NewStore(repo)

	bootstrapReq, err := buildBootstrapRequest(store, "share.claw")
	if err != nil {
		t.Fatalf("buildBootstrapRequest() error = %v", err)
	}
	if err := applyPatches(store, "share.claw", bootstrapReq); err != nil {
		t.Fatalf("applyPatches() error = %v", err)
	}

	botsBody, err := os.ReadFile(filepath.Join(repo, "config", "feishu", "bots.toml"))
	if err != nil {
		t.Fatalf("ReadFile(bots.toml) error = %v", err)
	}
	if !strings.Contains(string(botsBody), `[bot."share.claw"]`) {
		t.Fatalf("bots.toml missing quoted account table:\n%s", string(botsBody))
	}

	secretsBody, err := os.ReadFile(filepath.Join(repo, "config", "secrets", "local.toml"))
	if err != nil {
		t.Fatalf("ReadFile(local.toml) error = %v", err)
	}
	for _, want := range []string{`[feishu."share.claw"]`, `[feishu."share.claw".codex]`} {
		if !strings.Contains(string(secretsBody), want) {
			t.Fatalf("local.toml missing %s:\n%s", want, string(secretsBody))
		}
	}
}
