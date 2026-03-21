package codexenv

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAccountEnvUsesPerBotHome(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), ".codex-root"))
	env, paths, err := AppendAccountEnv([]string{"PATH=/usr/bin"}, "assistant")
	if err != nil {
		t.Fatalf("AppendAccountEnv() error = %v", err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"HOME=" + paths.Home,
		"CODEX_HOME=" + paths.CodexHome,
		RootEnvVar + "=" + paths.Root,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q:\n%s", want, joined)
		}
	}
}
