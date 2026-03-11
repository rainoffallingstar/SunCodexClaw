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
	if err := os.WriteFile(filepath.Join(configDir, "assistant.json"), []byte("{}\n"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(configDir, "assistant.json"), []byte("{}\n"), 0o644); err != nil {
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
