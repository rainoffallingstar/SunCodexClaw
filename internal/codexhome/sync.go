package codexhome

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"suncodexclaw/internal/codexenv"
	"suncodexclaw/internal/configstore"
	"suncodexclaw/internal/feishunative"
)

type Options struct {
	RepoRoot  string
	Accounts  []string
	CodexHome string
	Force     bool
}

type Result struct {
	Status   string
	Message  string
	Root     string
	Accounts []string
	Results  []AccountResult
}

type AccountResult struct {
	Account string
	Status  string
	Message string
	Paths   AccountPaths
	Config  codexConnectionConfig
}

type AccountPaths = codexenv.AccountPaths

type codexConnectionConfig struct {
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"-"`
}

type managedMetadata struct {
	ManagedBy    string `json:"managed_by"`
	Version      int    `json:"version"`
	Account      string `json:"account,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	APIKeySHA256 string `json:"api_key_sha256,omitempty"`
}

func Sync(opts Options) (Result, error) {
	repoRoot := strings.TrimSpace(opts.RepoRoot)
	if repoRoot == "" {
		repoRoot = "."
	}
	root, err := resolveCodexHomeRoot(opts.CodexHome)
	if err != nil {
		return Result{}, err
	}
	accounts, err := resolveAccounts(repoRoot, opts.Accounts)
	if err != nil {
		return Result{Status: "error", Root: root}, err
	}
	if len(accounts) == 0 {
		return Result{
			Status:  "skip",
			Message: "no_enabled_accounts",
			Root:    root,
		}, nil
	}

	results := make([]AccountResult, 0, len(accounts))
	okCount := 0
	skipCount := 0
	for _, account := range accounts {
		item, syncErr := syncAccount(repoRoot, root, account, opts.Force)
		if syncErr != nil {
			return Result{
				Status:   "error",
				Message:  syncErr.Error(),
				Root:     root,
				Accounts: append([]string(nil), accounts...),
				Results:  append(results, item),
			}, syncErr
		}
		results = append(results, item)
		switch item.Status {
		case "ok":
			okCount++
		case "skip":
			skipCount++
		}
	}

	status := "skip"
	message := "all_skipped"
	if okCount > 0 {
		status = "ok"
		if skipCount > 0 {
			message = "partial_sync"
		} else {
			message = "synced"
		}
	}
	return Result{
		Status:   status,
		Message:  message,
		Root:     root,
		Accounts: append([]string(nil), accounts...),
		Results:  results,
	}, nil
}

func syncAccount(repoRoot, root, account string, force bool) (AccountResult, error) {
	paths, err := codexenv.ResolveAccountPaths(root, account)
	if err != nil {
		return AccountResult{Account: strings.TrimSpace(account), Status: "error"}, err
	}
	cfg, err := feishunative.Load(repoRoot, account)
	if err != nil {
		return AccountResult{Account: account, Status: "error", Paths: paths}, err
	}
	connection := codexConnectionConfig{
		BaseURL: strings.TrimSpace(cfg.Codex.BaseURL),
		APIKey:  strings.TrimSpace(cfg.Codex.APIKey),
	}
	if strings.TrimSpace(connection.BaseURL) == "" && strings.TrimSpace(connection.APIKey) == "" {
		return AccountResult{
			Account: account,
			Status:  "skip",
			Message: "no_codex_base_url_or_api_key",
			Paths:   paths,
			Config:  connection,
		}, nil
	}

	if err := os.MkdirAll(paths.CodexHome, 0o755); err != nil {
		return AccountResult{Account: account, Status: "error", Paths: paths}, err
	}
	if !force && shouldSkipManagedWrite(paths.ConfigPath, paths.AuthPath, paths.MetadataPath) {
		return AccountResult{
			Account: account,
			Status:  "skip",
			Message: "existing_unmanaged_codex_home_files",
			Paths:   paths,
			Config:  connection,
		}, nil
	}

	if strings.TrimSpace(connection.BaseURL) != "" {
		if err := writeFileAtomically(paths.ConfigPath, []byte(renderConfigTOML(connection.BaseURL)), 0o600); err != nil {
			return AccountResult{Account: account, Status: "error", Paths: paths, Config: connection}, err
		}
	} else if err := removeIfExists(paths.ConfigPath); err != nil {
		return AccountResult{Account: account, Status: "error", Paths: paths, Config: connection}, err
	}

	if strings.TrimSpace(connection.APIKey) != "" {
		authBody, err := json.MarshalIndent(map[string]string{
			"OPENAI_API_KEY": connection.APIKey,
		}, "", "  ")
		if err != nil {
			return AccountResult{Account: account, Status: "error", Paths: paths, Config: connection}, err
		}
		authBody = append(authBody, '\n')
		if err := writeFileAtomically(paths.AuthPath, authBody, 0o600); err != nil {
			return AccountResult{Account: account, Status: "error", Paths: paths, Config: connection}, err
		}
	} else if err := removeIfExists(paths.AuthPath); err != nil {
		return AccountResult{Account: account, Status: "error", Paths: paths, Config: connection}, err
	}

	metadataBody, err := json.MarshalIndent(managedMetadata{
		ManagedBy:    "suncodexclaw",
		Version:      1,
		Account:      account,
		BaseURL:      connection.BaseURL,
		APIKeySHA256: hashAPIKey(connection.APIKey),
	}, "", "  ")
	if err != nil {
		return AccountResult{Account: account, Status: "error", Paths: paths, Config: connection}, err
	}
	metadataBody = append(metadataBody, '\n')
	if err := writeFileAtomically(paths.MetadataPath, metadataBody, 0o600); err != nil {
		return AccountResult{Account: account, Status: "error", Paths: paths, Config: connection}, err
	}

	return AccountResult{
		Account: account,
		Status:  "ok",
		Message: "synced",
		Paths:   paths,
		Config:  connection,
	}, nil
}

func resolveCodexHomeRoot(raw string) (string, error) {
	paths, err := codexenv.ResolveAccountPaths(raw, "default")
	if err != nil {
		return "", err
	}
	return paths.Root, nil
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
