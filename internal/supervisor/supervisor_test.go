package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"suncodexclaw/internal/feishunative"
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

func TestRuntimeCommandUsesGoBackend(t *testing.T) {
	s := New(Options{RepoRoot: "/repo", RuntimeBackend: "go"})
	cmd, err := s.runtimeCommand(context.Background(), "assistant", true, "/repo/config/timers/assistant/daily.json")
	if err != nil {
		t.Fatalf("runtimeCommand() error = %v", err)
	}
	if got := filepath.Base(cmd.Path); got != filepath.Base(os.Args[0]) {
		t.Fatalf("cmd.Path = %q, want current executable", cmd.Path)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "feishu-run --repo /repo --account assistant --dry-run --timer-task-file /repo/config/timers/assistant/daily.json") {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestRuntimeCommandUsesJSBackend(t *testing.T) {
	s := New(Options{RepoRoot: "/repo", NodeBin: "node", RuntimeBackend: "js"})
	cmd, err := s.runtimeCommand(context.Background(), "assistant", false, "")
	if err != nil {
		t.Fatalf("runtimeCommand() error = %v", err)
	}
	if got := filepath.Base(cmd.Path); got != "node" {
		t.Fatalf("cmd.Path = %q, want basename node", cmd.Path)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "/repo/tools/feishu_ws_bot.js --account assistant") {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestFormatSupervisorSpawnedLine(t *testing.T) {
	ts := time.Date(2026, 3, 20, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	line := formatSupervisorSpawnedLine(ts, "assistant", "go", 4321, []string{"suncodexclawd", "feishu-run", "--account", "assistant"})
	for _, want := range []string{
		"supervisor_spawned",
		"account=assistant",
		"runtime_backend=go",
		"pid=4321",
		"cmd=suncodexclawd feishu-run --account assistant",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}

func TestFormatSupervisorExitLine(t *testing.T) {
	ts := time.Date(2026, 3, 20, 12, 1, 0, 0, time.UTC)
	line := formatSupervisorExitLine(ts, "assistant", "js", 17, true, 2*time.Second)
	for _, want := range []string{
		"supervisor_exit",
		"account=assistant",
		"runtime_backend=js",
		"exit_code=17",
		"auto_restart=true",
		"retry_in=2s",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}

func TestSupervisorAccountNamespaceSanitizesRuntimePaths(t *testing.T) {
	s := New(Options{RepoRoot: "/repo"})
	if got := supervisorAccountNamespace("assistant bot"); got != "assistant-bot" {
		t.Fatalf("supervisorAccountNamespace() = %q, want assistant-bot", got)
	}
	if got := s.pidFile("assistant bot"); got != filepath.Join("/repo", ".runtime", "feishu", "pids", "assistant-bot.pid") {
		t.Fatalf("pidFile() = %q", got)
	}
	if got := s.logFile("assistant bot"); got != filepath.Join("/repo", ".runtime", "feishu", "logs", "assistant-bot.log") {
		t.Fatalf("logFile() = %q", got)
	}
	if got := s.errFile("assistant bot"); got != filepath.Join("/repo", ".runtime", "feishu", "errors", "assistant-bot.err") {
		t.Fatalf("errFile() = %q", got)
	}
}

func TestLaunchctlLabelUsesSanitizedAccountNamespace(t *testing.T) {
	s := New(Options{RepoRoot: "/repo"})
	if got := s.launchctlLabel("assistant bot"); got != "com.sunbelife.suncodexclaw.feishu.assistant-bot" {
		t.Fatalf("launchctlLabel() = %q, want sanitized suffix", got)
	}
}

func TestFormatSupervisorSpawnErrorLine(t *testing.T) {
	ts := time.Date(2026, 3, 20, 12, 2, 0, 0, time.UTC)
	line := formatSupervisorSpawnErrorLine(ts, "assistant", "go", errors.New("boom\nwith detail"), 1500*time.Millisecond)
	for _, want := range []string{
		"supervisor_spawn_error",
		"account=assistant",
		"runtime_backend=go",
		"error=boom with detail",
		"retry_in=1.5s",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}

func TestShouldIgnoreCodexBaseURLProbeErrorAllowsBadHandshake400(t *testing.T) {
	result := feishunative.CodexBaseURLProbeResult{
		Enabled: true,
		WSURL:   "ws://example.com/v1/responses",
		Message: "400 Bad Request body=Bad Request websocket: bad handshake",
	}
	if !shouldIgnoreCodexBaseURLProbeError(result, errors.New("codex responses websocket probe failed")) {
		t.Fatalf("shouldIgnoreCodexBaseURLProbeError() = false, want true")
	}
}

func TestShouldIgnoreCodexBaseURLProbeErrorKeepsOtherFailuresBlocking(t *testing.T) {
	result := feishunative.CodexBaseURLProbeResult{
		Enabled: true,
		WSURL:   "ws://example.com/v1/responses",
		Message: "dial tcp: lookup example.com: no such host",
	}
	if shouldIgnoreCodexBaseURLProbeError(result, errors.New("codex responses websocket probe failed")) {
		t.Fatalf("shouldIgnoreCodexBaseURLProbeError() = true, want false")
	}
}
