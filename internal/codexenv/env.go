package codexenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"suncodexclaw/internal/configstore"
)

const (
	RootEnvVar      = "SUNCODEXCLAW_CODEX_HOME_ROOT"
	botHomesDirName = "bot-homes"
)

type AccountPaths struct {
	Account      string
	Namespace    string
	Root         string
	Home         string
	CodexHome    string
	ConfigPath   string
	AuthPath     string
	MetadataPath string
}

func ResolveAccountPaths(baseRoot, account string) (AccountPaths, error) {
	resolvedAccount := strings.TrimSpace(account)
	if resolvedAccount == "" {
		return AccountPaths{}, fmt.Errorf("missing account")
	}
	root, err := resolveCodexHomeRoot(baseRoot)
	if err != nil {
		return AccountPaths{}, err
	}
	namespace := configstore.AccountDirName(resolvedAccount)
	if namespace == "" {
		return AccountPaths{}, fmt.Errorf("invalid account namespace")
	}
	home := filepath.Join(root, botHomesDirName, namespace)
	codexHome := filepath.Join(home, ".codex")
	return AccountPaths{
		Account:      resolvedAccount,
		Namespace:    namespace,
		Root:         root,
		Home:         home,
		CodexHome:    codexHome,
		ConfigPath:   filepath.Join(codexHome, "config.toml"),
		AuthPath:     filepath.Join(codexHome, "auth.json"),
		MetadataPath: filepath.Join(codexHome, "suncodexclaw-managed.json"),
	}, nil
}

func AppendAccountEnv(baseEnv []string, account string) ([]string, AccountPaths, error) {
	paths, err := ResolveAccountPaths("", account)
	if err != nil {
		return append([]string(nil), baseEnv...), AccountPaths{}, err
	}
	env := append([]string(nil), baseEnv...)
	env = setEnvValue(env, "HOME", paths.Home)
	env = setEnvValue(env, "CODEX_HOME", paths.CodexHome)
	env = setEnvValue(env, RootEnvVar, paths.Root)
	return env, paths, nil
}

func SetProcessAccountEnv(account string) (AccountPaths, error) {
	paths, err := ResolveAccountPaths("", account)
	if err != nil {
		return AccountPaths{}, err
	}
	if err := os.MkdirAll(paths.CodexHome, 0o755); err != nil {
		return AccountPaths{}, err
	}
	if err := os.Setenv("HOME", paths.Home); err != nil {
		return AccountPaths{}, err
	}
	if err := os.Setenv("CODEX_HOME", paths.CodexHome); err != nil {
		return AccountPaths{}, err
	}
	if err := os.Setenv(RootEnvVar, paths.Root); err != nil {
		return AccountPaths{}, err
	}
	return paths, nil
}

func resolveCodexHomeRoot(raw string) (string, error) {
	candidates := []string{
		strings.TrimSpace(raw),
		strings.TrimSpace(os.Getenv(RootEnvVar)),
		strings.TrimSpace(os.Getenv("CODEX_HOME")),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		inferred := inferRootFromCodexHome(candidate)
		if inferred == "" {
			continue
		}
		return filepath.Abs(inferred)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func inferRootFromCodexHome(raw string) string {
	path := filepath.Clean(strings.TrimSpace(raw))
	if path == "" {
		return ""
	}
	marker := string(filepath.Separator) + botHomesDirName + string(filepath.Separator)
	suffix := string(filepath.Separator) + ".codex"
	if strings.Contains(path, marker) && strings.HasSuffix(path, suffix) {
		parts := strings.SplitN(path, marker, 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			return parts[0]
		}
	}
	return path
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	replaced := false
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}
