package codexhome

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"suncodexclaw/internal/configstore"
	"suncodexclaw/internal/feishunative"
)

const managedMetadataFilename = "suncodexclaw-managed.json"

type Options struct {
	RepoRoot  string
	Accounts  []string
	CodexHome string
	Force     bool
}

type Result struct {
	Status     string
	Message    string
	CodexHome  string
	Accounts   []string
	Config     codexConnectionConfig
	ConfigPath string
	AuthPath   string
}

type codexConnectionConfig struct {
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"-"`
}

type managedMetadata struct {
	ManagedBy    string   `json:"managed_by"`
	Version      int      `json:"version"`
	Accounts     []string `json:"accounts,omitempty"`
	BaseURL      string   `json:"base_url,omitempty"`
	APIKeySHA256 string   `json:"api_key_sha256,omitempty"`
}

func Sync(opts Options) (Result, error) {
	repoRoot := strings.TrimSpace(opts.RepoRoot)
	if repoRoot == "" {
		repoRoot = "."
	}
	codexHome, err := resolveCodexHome(opts.CodexHome)
	if err != nil {
		return Result{}, err
	}
	accounts, err := resolveAccounts(repoRoot, opts.Accounts)
	if err != nil {
		return Result{Status: "error", CodexHome: codexHome}, err
	}
	if len(accounts) == 0 {
		return Result{
			Status:    "skip",
			Message:   "no_enabled_accounts",
			CodexHome: codexHome,
		}, nil
	}
	cfg, err := resolveSharedCodexConnection(repoRoot, accounts)
	if err != nil {
		return Result{
			Status:    "skip",
			Message:   err.Error(),
			CodexHome: codexHome,
			Accounts:  append([]string(nil), accounts...),
		}, nil
	}
	if strings.TrimSpace(cfg.BaseURL) == "" && strings.TrimSpace(cfg.APIKey) == "" {
		return Result{
			Status:    "skip",
			Message:   "no_codex_base_url_or_api_key",
			CodexHome: codexHome,
			Accounts:  append([]string(nil), accounts...),
		}, nil
	}

	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		return Result{}, err
	}
	configPath := filepath.Join(codexHome, "config.toml")
	authPath := filepath.Join(codexHome, "auth.json")
	metadataPath := filepath.Join(codexHome, managedMetadataFilename)
	if !opts.Force {
		if shouldSkipManagedWrite(configPath, authPath, metadataPath) {
			return Result{
				Status:     "skip",
				Message:    "existing_unmanaged_codex_home_files",
				CodexHome:  codexHome,
				Accounts:   append([]string(nil), accounts...),
				Config:     cfg,
				ConfigPath: configPath,
				AuthPath:   authPath,
			}, nil
		}
	}

	if strings.TrimSpace(cfg.BaseURL) != "" {
		if err := writeFileAtomically(configPath, []byte(renderConfigTOML(cfg.BaseURL)), 0o600); err != nil {
			return Result{}, err
		}
	} else if err := removeIfExists(configPath); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(cfg.APIKey) != "" {
		authBody, err := json.MarshalIndent(map[string]string{
			"OPENAI_API_KEY": cfg.APIKey,
		}, "", "  ")
		if err != nil {
			return Result{}, err
		}
		authBody = append(authBody, '\n')
		if err := writeFileAtomically(authPath, authBody, 0o600); err != nil {
			return Result{}, err
		}
	} else if err := removeIfExists(authPath); err != nil {
		return Result{}, err
	}
	metadataBody, err := json.MarshalIndent(managedMetadata{
		ManagedBy:    "suncodexclaw",
		Version:      1,
		Accounts:     append([]string(nil), accounts...),
		BaseURL:      strings.TrimSpace(cfg.BaseURL),
		APIKeySHA256: hashAPIKey(cfg.APIKey),
	}, "", "  ")
	if err != nil {
		return Result{}, err
	}
	metadataBody = append(metadataBody, '\n')
	if err := writeFileAtomically(metadataPath, metadataBody, 0o600); err != nil {
		return Result{}, err
	}

	return Result{
		Status:     "ok",
		Message:    "synced",
		CodexHome:  codexHome,
		Accounts:   append([]string(nil), accounts...),
		Config:     cfg,
		ConfigPath: configPath,
		AuthPath:   authPath,
	}, nil
}

func resolveCodexHome(raw string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		return filepath.Abs(strings.TrimSpace(raw))
	}
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		return filepath.Abs(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func resolveAccounts(repoRoot string, requested []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, account := range requested {
		account = strings.TrimSpace(account)
		if account == "" || seen[account] {
			continue
		}
		seen[account] = true
		out = append(out, account)
	}
	if len(out) > 0 {
		sort.Strings(out)
		return out, nil
	}
	store := configstore.NewStore(repoRoot)
	return store.ListEnabledAccountNames()
}

func resolveSharedCodexConnection(repoRoot string, accounts []string) (codexConnectionConfig, error) {
	var resolved *codexConnectionConfig
	for _, account := range accounts {
		cfg, err := feishunative.Load(repoRoot, account)
		if err != nil {
			return codexConnectionConfig{}, err
		}
		current := codexConnectionConfig{
			BaseURL: strings.TrimSpace(cfg.Codex.BaseURL),
			APIKey:  strings.TrimSpace(cfg.Codex.APIKey),
		}
		if resolved == nil {
			copyValue := current
			resolved = &copyValue
			continue
		}
		if resolved.BaseURL != current.BaseURL || resolved.APIKey != current.APIKey {
			return codexConnectionConfig{}, fmt.Errorf("account_codex_config_conflict")
		}
	}
	if resolved == nil {
		return codexConnectionConfig{}, nil
	}
	return *resolved, nil
}

func shouldSkipManagedWrite(configPath, authPath, metadataPath string) bool {
	metadata, ok := readManagedMetadata(metadataPath)
	if ok && metadata.ManagedBy == "suncodexclaw" {
		return false
	}
	if fileExists(configPath) || fileExists(authPath) {
		return true
	}
	return false
}

func readManagedMetadata(path string) (managedMetadata, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return managedMetadata{}, false
	}
	var metadata managedMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return managedMetadata{}, false
	}
	return metadata, true
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func renderConfigTOML(baseURL string) string {
	lines := []string{
		"# Managed by SunCodexClaw. Regenerate via `suncodexclawd codex-home sync`.",
		`model_provider = "custom"`,
		"",
		"[model_providers.custom]",
		`name = "custom"`,
		`wire_api = "responses"`,
		`requires_openai_auth = true`,
		fmt.Sprintf("base_url = %q", strings.TrimSpace(baseURL)),
		"",
	}
	return strings.Join(lines, "\n")
}

func hashAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
