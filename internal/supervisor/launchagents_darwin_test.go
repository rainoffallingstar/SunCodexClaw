//go:build darwin

package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePlistNodeMode(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".runtime", "feishu", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := New(Options{RepoRoot: repo, BotScriptRel: filepath.Join("tools", "feishu_ws_bot.js"), RuntimeDir: filepath.Join(repo, ".runtime", "feishu")})
	sup.userHomeDir = func() (string, error) { return filepath.Join(tmp, "home"), nil }

	plistPath, err := sup.writePlist("assistant", LaunchAgentOptions{RunMode: "node", KeepAlive: true, ThrottleInterval: 10})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(b)
	if !strings.Contains(txt, "feishu_ws_bot.js") {
		t.Fatalf("expected node bot script in plist: %s", plistPath)
	}
	if !strings.Contains(txt, "<key>ThrottleInterval</key>") {
		t.Fatalf("expected throttle interval in plist")
	}
}

func TestWritePlistSupervisorModeRequiresExecutable(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".runtime", "feishu", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := New(Options{RepoRoot: repo, RuntimeDir: filepath.Join(repo, ".runtime", "feishu")})
	sup.userHomeDir = func() (string, error) { return filepath.Join(tmp, "home"), nil }

	_, err := sup.writePlist("assistant", LaunchAgentOptions{RunMode: "supervisor", DaemonBin: filepath.Join(repo, "bin", "suncodexclawd"), KeepAlive: true, ThrottleInterval: 10})
	if err == nil {
		t.Fatalf("expected error when daemon is not executable")
	}
}
