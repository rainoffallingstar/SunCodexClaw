package codexhome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncWritesManagedCodexHomeFiles(t *testing.T) {
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
	codexHome := filepath.Join(t.TempDir(), ".codex")

	result, err := Sync(Options{RepoRoot: repo, CodexHome: codexHome})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Status != "ok" {
		t.Fatalf("Sync() status = %q, want ok", result.Status)
	}

	configBody, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	if !strings.Contains(string(configBody), `base_url = "https://gateway.example/v1"`) {
		t.Fatalf("config.toml missing base_url:\n%s", string(configBody))
	}

	authBody, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile(auth.json) error = %v", err)
	}
	if !strings.Contains(string(authBody), `"OPENAI_API_KEY": "sk-test"`) {
		t.Fatalf("auth.json missing api key:\n%s", string(authBody))
	}
}

func TestSyncSkipsWhenAccountsConflict(t *testing.T) {
	repo := t.TempDir()
	writeTestConfig(t, repo, `
[shared.codex]
base_url = "https://gateway.example/v1"

[bot.one]
enabled = true

[bot.two]
enabled = true
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
	codexHome := filepath.Join(t.TempDir(), ".codex")

	result, err := Sync(Options{RepoRoot: repo, CodexHome: codexHome})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Status != "skip" {
		t.Fatalf("Sync() status = %q, want skip", result.Status)
	}
	if result.Message != "account_codex_config_conflict" {
		t.Fatalf("Sync() message = %q", result.Message)
	}
	if fileExists(filepath.Join(codexHome, "config.toml")) || fileExists(filepath.Join(codexHome, "auth.json")) {
		t.Fatalf("Sync() wrote files despite conflict")
	}
}

func TestSyncDoesNotOverwriteUnmanagedFiles(t *testing.T) {
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
	codexHome := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("custom = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	result, err := Sync(Options{RepoRoot: repo, CodexHome: codexHome})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Status != "skip" {
		t.Fatalf("Sync() status = %q, want skip", result.Status)
	}
	if result.Message != "existing_unmanaged_codex_home_files" {
		t.Fatalf("Sync() message = %q", result.Message)
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
