package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatusInfosShowsLastErrorWhenStopped(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	runtime := filepath.Join(repo, ".runtime", "feishu")
	configDir := filepath.Join(repo, "config", "feishu")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "bots.toml"), []byte("[bot.assistant]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(Options{RepoRoot: repo, RuntimeDir: runtime, ConfigDir: configDir})
	if err := os.MkdirAll(s.errDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.errFile("assistant"), []byte("boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	infos, err := s.StatusInfos([]string{"assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 info, got %d", len(infos))
	}
	if infos[0].State != "stopped" {
		t.Fatalf("expected stopped, got %q", infos[0].State)
	}
	if infos[0].LastError == "" {
		t.Fatalf("expected last_error, got empty")
	}
}

func TestStatusInfosShowsStalePIDAndLastError(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	runtime := filepath.Join(repo, ".runtime", "feishu")
	configDir := filepath.Join(repo, "config", "feishu")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "bots.toml"), []byte("[bot.assistant]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(Options{RepoRoot: repo, RuntimeDir: runtime, ConfigDir: configDir})
	if err := os.MkdirAll(s.pidDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.errDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.pidFile("assistant"), []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.errFile("assistant"), []byte("crash loop\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	infos, err := s.StatusInfos([]string{"assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 info, got %d", len(infos))
	}
	if infos[0].State != "stopped" {
		t.Fatalf("expected stopped, got %q", infos[0].State)
	}
	if infos[0].StalePID == 0 {
		t.Fatalf("expected stale_pid > 0")
	}
	if infos[0].LastError == "" {
		t.Fatalf("expected last_error, got empty")
	}
}

func TestDiscoverAccountsReturnsEnabledBotsOnly(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(""+
		"[bot.assistant]\n"+
		"enabled = true\n\n"+
		"[bot.reviewer]\n"+
		"enabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(""+
		"[feishu.assistant]\napp_id = \"cli_a\"\n\n"+
		"[feishu.reviewer]\napp_id = \"cli_b\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(Options{RepoRoot: repo})
	got, err := s.DiscoverAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "assistant" {
		t.Fatalf("DiscoverAccounts() = %v, want [assistant]", got)
	}

	all, err := s.DiscoverAllAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("DiscoverAllAccounts() = %v, want two accounts", all)
	}
}
