package codexhome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"suncodexclaw/internal/codexenv"
)

func TestSyncWritesPerAccountManagedCodexHomeFiles(t *testing.T) {
	repo := t.TempDir()
	writeTestConfig(t, repo, `
[shared.codex]
base_url = "https://gateway.example/v1"

[bot.assistant]
enabled = true
`)
	writeTestSecrets(t, repo, `
[feishu.assistant]
app_id = "cli_xxx"
app_secret = "secret"

[feishu.assistant.codex]
api_key = "sk-test"
`)
	root := filepath.Join(t.TempDir(), ".codex-root")

	result, err := Sync(Options{RepoRoot: repo, CodexHome: root})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Status != "ok" {
		t.Fatalf("Sync() status = %q, want ok", result.Status)
	}
	if len(result.Results) != 1 {
		t.Fatalf("Sync() results = %d, want 1", len(result.Results))
	}

	paths, err := codexenv.ResolveAccountPaths(root, "assistant")
	if err != nil {
		t.Fatalf("ResolveAccountPaths() error = %v", err)
	}
	configBody, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	if !strings.Contains(string(configBody), `base_url = "https://gateway.example/v1"`) {
		t.Fatalf("config.toml missing base_url:\n%s", string(configBody))
	}

	authBody, err := os.ReadFile(paths.AuthPath)
	if err != nil {
		t.Fatalf("ReadFile(auth.json) error = %v", err)
	}
	if !strings.Contains(string(authBody), `"OPENAI_API_KEY": "sk-test"`) {
		t.Fatalf("auth.json missing api key:\n%s", string(authBody))
	}
}

func TestSyncWritesDifferentAccountsToDifferentHomes(t *testing.T) {
	repo := t.TempDir()
	writeTestConfig(t, repo, `
[bot.one]
enabled = true

[bot.two]
enabled = true

[bot.one.codex]
base_url = "https://one.example/v1"

[bot.two.codex]
base_url = "https://two.example/v1"
`)
	writeTestSecrets(t, repo, `
[feishu.one]
app_id = "cli_one"
app_secret = "secret"

[feishu.one.codex]
api_key = "sk-one"

[feishu.two]
app_id = "cli_two"
app_secret = "secret"

[feishu.two.codex]
api_key = "sk-two"
`)
	root := filepath.Join(t.TempDir(), ".codex-root")

	result, err := Sync(Options{RepoRoot: repo, CodexHome: root})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Status != "ok" {
		t.Fatalf("Sync() status = %q, want ok", result.Status)
	}

	onePaths, _ := codexenv.ResolveAccountPaths(root, "one")
	twoPaths, _ := codexenv.ResolveAccountPaths(root, "two")
	if onePaths.CodexHome == twoPaths.CodexHome {
		t.Fatalf("per-account CODEX_HOME should differ")
	}
	oneAuth, err := os.ReadFile(onePaths.AuthPath)
	if err != nil {
		t.Fatalf("ReadFile(one auth) error = %v", err)
	}
	twoAuth, err := os.ReadFile(twoPaths.AuthPath)
	if err != nil {
		t.Fatalf("ReadFile(two auth) error = %v", err)
	}
	if strings.Contains(string(oneAuth), "sk-two") || strings.Contains(string(twoAuth), "sk-one") {
		t.Fatalf("account auth.json contents leaked across accounts")
	}
}

func TestSyncDoesNotOverwriteUnmanagedFilesPerAccount(t *testing.T) {
	repo := t.TempDir()
	writeTestConfig(t, repo, `
[shared.codex]
base_url = "https://gateway.example/v1"

[bot.assistant]
enabled = true
`)
	writeTestSecrets(t, repo, `
[feishu.assistant]
app_id = "cli_xxx"
app_secret = "secret"

[feishu.assistant.codex]
api_key = "sk-test"
`)
	root := filepath.Join(t.TempDir(), ".codex-root")
	paths, err := codexenv.ResolveAccountPaths(root, "assistant")
	if err != nil {
		t.Fatalf("ResolveAccountPaths() error = %v", err)
	}
	if err := os.MkdirAll(paths.CodexHome, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(paths.ConfigPath, []byte("custom = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	result, err := Sync(Options{RepoRoot: repo, CodexHome: root})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Status != "skip" {
		t.Fatalf("Sync() status = %q, want skip", result.Status)
	}
	if len(result.Results) != 1 || result.Results[0].Message != "existing_unmanaged_codex_home_files" {
		t.Fatalf("Sync() result = %+v", result.Results)
	}
}

func writeTestConfig(t *testing.T, repo, body string) {
	t.Helper()
	path := filepath.Join(repo, "config", "feishu", "bots.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(config) error = %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
}

func writeTestSecrets(t *testing.T, repo, body string) {
	t.Helper()
	path := filepath.Join(repo, "config", "secrets", "local.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(secrets) error = %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(secrets) error = %v", err)
	}
}
