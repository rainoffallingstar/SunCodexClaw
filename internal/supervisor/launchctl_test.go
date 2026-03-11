package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLaunchctlPID(t *testing.T) {
	raw := `
{
	"Label" = "com.sunbelife.suncodexclaw.feishu.assistant";
	"LastExitStatus" = 0;
	"PID" = 12345;
}
`
	pid, ok := parseLaunchctlPID(raw)
	if !ok || pid != 12345 {
		t.Fatalf("pid parse failed: ok=%v pid=%d", ok, pid)
	}
}

func TestParseLaunchctlLastExit(t *testing.T) {
	raw := `"LastExitStatus" = -9;`
	n, ok := parseLaunchctlLastExit(raw)
	if !ok || n != -9 {
		t.Fatalf("last exit parse failed: ok=%v n=%d", ok, n)
	}
}

func TestLaunchAgentPlistPath(t *testing.T) {
	sup := New(Options{RepoRoot: "/tmp/repo"})
	got := sup.launchAgentPlistPathForTest("assistant", "/Users/me")
	want := filepath.Join("/Users/me", "Library", "LaunchAgents", sup.launchctlLabel("assistant")+".plist")
	if got != want {
		t.Fatalf("plist path mismatch: got=%q want=%q", got, want)
	}
}

func TestStatusInfosLaunchAgentFileOnly(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".runtime", "feishu", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "assistant.json"), []byte("{\"bot_name\":\"x\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake a LaunchAgent plist existing.
	sup := New(Options{RepoRoot: repo})
	plistPath := sup.launchAgentPlistPathForTest("assistant", home)
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Assert plist detection works via injected home function.
	sup.userHomeDir = func() (string, error) { return home, nil }
	if !sup.launchAgentPlistExists("assistant") {
		t.Fatalf("expected launch agent plist to exist")
	}
}
