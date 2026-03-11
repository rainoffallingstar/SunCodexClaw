//go:build darwin

package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatusInfosShowsLaunchAgentFileOnlyAsLaunchctlStopped(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repo := filepath.Join(tmp, "repo")

	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".runtime", "feishu", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "assistant.json"), []byte("{\"bot_name\":\"x\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sup := New(Options{RepoRoot: repo})
	sup.userHomeDir = func() (string, error) { return home, nil }
	sup.launchctlPath = "/bin/false" // force UsingLaunchctl() true but avoid executing real launchctl

	plistPath := sup.launchAgentPlistPath("assistant")
	if plistPath == "" {
		t.Fatalf("empty plist path")
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	infos, err := sup.StatusInfos([]string{"assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 info, got %d", len(infos))
	}
	if infos[0].Manager != "launchctl" || infos[0].State != "stopped" {
		t.Fatalf("unexpected status: state=%s manager=%s", infos[0].State, infos[0].Manager)
	}
	if infos[0].LaunchAgent != "file-only" {
		t.Fatalf("expected launchagent=file-only, got %q", infos[0].LaunchAgent)
	}
}
