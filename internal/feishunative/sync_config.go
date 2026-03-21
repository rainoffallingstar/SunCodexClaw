package feishunative

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"suncodexclaw/internal/configstore"
	"suncodexclaw/internal/worksync"
)

type SyncConfigOptions struct {
	Workspace      string
	WorkspaceID    string
	Provider       string
	WebDAVURL      string
	WebDAVUsername string
	WebDAVPassword string
	WebDAVBasePath string
	TimeoutSeconds int
}

func ResolveSyncConfig(repoRoot, account string, opts SyncConfigOptions) (worksync.Config, string, error) {
	store := configstore.NewStore(repoRoot)
	overlay, _ := store.ReadOverlay(account)
	feishuDefault, _ := store.ReadSecretsEntry("feishu", "default")
	feishuAccount, _ := store.ReadSecretsEntry("feishu", account)
	secretDefault, _ := store.ReadSecretsEntry("sync", "default")
	secretAccount, _ := store.ReadSecretsEntry("sync", account)

	workspaceDir := resolveSyncWorkspaceDir(repoRoot, strings.TrimSpace(opts.Workspace))
	if workspaceDir == "" {
		workspaceDir = resolveSyncWorkspaceDir(repoRoot, firstNonEmpty(
			accountEnv(account, "CODEX_CWD"),
			os.Getenv(accountEnvKey(account, "CODEX_CD")),
			os.Getenv("FEISHU_CODEX_CWD"),
			os.Getenv("FEISHU_CODEX_CD"),
			getNestedString(overlay, "codex", "cwd"),
			getNestedString(overlay, "codex", "cd"),
			getNestedString(feishuAccount, "codex", "cwd"),
			getNestedString(feishuAccount, "codex", "cd"),
			getNestedString(feishuDefault, "codex", "cwd"),
			getNestedString(feishuDefault, "codex", "cd"),
			getNestedString(secretAccount, "codex", "cwd"),
			getNestedString(secretAccount, "codex", "cd"),
			getNestedString(secretDefault, "codex", "cwd"),
			getNestedString(secretDefault, "codex", "cd"),
		))
	}
	if workspaceDir == "" {
		candidate := filepath.Join(repoRoot, "workspace")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			workspaceDir = candidate
		}
	}
	if workspaceDir == "" {
		return worksync.Config{}, "", fmt.Errorf("workspace dir is not set; use --workspace or FEISHU_CODEX_CWD")
	}

	timeoutSeconds := opts.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = resolveInt(nil, nil, os.Getenv("SUNCODEXCLAW_SYNC_TIMEOUT_SEC"), 30)
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	cfg := worksync.Config{
		Provider: firstNonEmpty(
			strings.TrimSpace(opts.Provider),
			strings.TrimSpace(os.Getenv("SUNCODEXCLAW_SYNC_PROVIDER")),
			strings.TrimSpace(getNestedString(secretAccount, "provider")),
			strings.TrimSpace(getNestedString(secretDefault, "provider")),
			"webdav",
		),
		WebDAVURL: firstNonEmpty(
			strings.TrimSpace(opts.WebDAVURL),
			strings.TrimSpace(os.Getenv("SUNCODEXCLAW_SYNC_WEBDAV_URL")),
			strings.TrimSpace(getNestedString(secretAccount, "webdav", "url")),
			strings.TrimSpace(getNestedString(secretDefault, "webdav", "url")),
		),
		WebDAVUsername: firstNonEmpty(
			strings.TrimSpace(opts.WebDAVUsername),
			strings.TrimSpace(os.Getenv("SUNCODEXCLAW_SYNC_WEBDAV_USERNAME")),
			strings.TrimSpace(getNestedString(secretAccount, "webdav", "username")),
			strings.TrimSpace(getNestedString(secretDefault, "webdav", "username")),
		),
		WebDAVPassword: firstNonEmpty(
			strings.TrimSpace(opts.WebDAVPassword),
			strings.TrimSpace(os.Getenv("SUNCODEXCLAW_SYNC_WEBDAV_PASSWORD")),
			strings.TrimSpace(getNestedString(secretAccount, "webdav", "password")),
			strings.TrimSpace(getNestedString(secretDefault, "webdav", "password")),
		),
		WebDAVBasePath: firstNonEmpty(
			strings.TrimSpace(opts.WebDAVBasePath),
			strings.TrimSpace(os.Getenv("SUNCODEXCLAW_SYNC_WEBDAV_BASE_PATH")),
			strings.TrimSpace(getNestedString(secretAccount, "webdav", "base_path")),
			strings.TrimSpace(getNestedString(secretDefault, "webdav", "base_path")),
			"/SunCodexClaw/backups",
		),
		WorkspaceID: firstNonEmpty(
			strings.TrimSpace(opts.WorkspaceID),
			strings.TrimSpace(getNestedString(secretAccount, "workspace_id")),
			DefaultSyncWorkspaceID(account),
		),
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	}
	return cfg, workspaceDir, nil
}

func DefaultSyncWorkspaceID(account string) string {
	account = strings.TrimSpace(account)
	if account == "" {
		return "default"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-", ".", "-")
	account = replacer.Replace(account)
	account = strings.Trim(account, "-.")
	if account == "" {
		return "default"
	}
	return account
}

func resolveSyncWorkspaceDir(repoRoot, raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if filepath.IsAbs(text) {
		return filepath.Clean(text)
	}
	return filepath.Clean(filepath.Join(repoRoot, text))
}
