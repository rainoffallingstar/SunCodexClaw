package worksync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

var ImportantDocs = []string{"agent.md", "soul.md", "heartbeats.md"}

type Config struct {
	Provider       string
	WebDAVURL      string
	WebDAVUsername string
	WebDAVPassword string
	WebDAVBasePath string
	WorkspaceID    string
	Timeout        time.Duration
}

type Options struct {
	RepoRoot     string
	WorkspaceDir string
	WorkspaceID  string
}

type Manager struct {
	opts Options
}

type FileStatus struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Size     int64  `json:"size,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Modified string `json:"modified,omitempty"`
}

type State struct {
	Provider     string               `json:"provider"`
	WorkspaceID  string               `json:"workspace_id"`
	WorkspaceDir string               `json:"workspace_dir"`
	LastPushAt   string               `json:"last_push_at,omitempty"`
	UpdatedAt    string               `json:"updated_at,omitempty"`
	Files        map[string]StateFile `json:"files,omitempty"`
}

type StateFile struct {
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	LatestPath   string `json:"latest_path,omitempty"`
	SnapshotPath string `json:"snapshot_path,omitempty"`
}

type StatusResult struct {
	Provider     string
	Configured   bool
	WorkspaceID  string
	WorkspaceDir string
	StatePath    string
	LastPushAt   string
	RemoteBase   string
	Files        []FileStatus
}

type UploadedFile struct {
	Name         string
	LatestPath   string
	SnapshotPath string
	SHA256       string
	Size         int64
}

type PushResult struct {
	WorkspaceID  string
	WorkspaceDir string
	StatePath    string
	Snapshot     string
	RemoteBase   string
	Uploaded     []UploadedFile
	Missing      []string
}

type RemoteSnapshot struct {
	Name string
}

type ListedFile struct {
	Name string
	Size int64
}

type ListRemoteResult struct {
	WorkspaceID string
	RemoteBase  string
	Snapshots   []RemoteSnapshot
}

type PulledFile struct {
	Name string
	Path string
	Size int64
}

type PullResult struct {
	WorkspaceID string
	RemoteBase  string
	Snapshot    string
	TargetDir   string
	Files       []PulledFile
}

type RestoredFile struct {
	Name string
	Path string
	Size int64
}

type RestoreResult struct {
	WorkspaceID  string
	WorkspaceDir string
	SourceDir    string
	Files        []RestoredFile
	Skipped      []RestoredFile
}

func NewManager(opts Options) *Manager {
	return &Manager{opts: opts}
}

func (m *Manager) StatePath() string {
	workspaceID := sanitizeSegment(m.opts.WorkspaceID)
	if workspaceID == "" {
		workspaceID = "default"
	}
	return filepath.Join(m.opts.RepoRoot, ".runtime", "sync", workspaceID, "state.json")
}

func (m *Manager) ReadState() (State, error) {
	var st State
	body, err := os.ReadFile(m.StatePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return st, err
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return st, err
	}
	return st, nil
}

func (m *Manager) WriteState(st State) error {
	if err := os.MkdirAll(filepath.Dir(m.StatePath()), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.StatePath(), append(body, '\n'), 0o644)
}

func (m *Manager) CollectFiles() ([]FileStatus, error) {
	out := make([]FileStatus, 0, len(ImportantDocs))
	for _, name := range ImportantDocs {
		filePath := filepath.Join(m.opts.WorkspaceDir, name)
		st := FileStatus{
			Name: name,
			Path: filePath,
		}
		info, err := os.Stat(filePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				out = append(out, st)
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			out = append(out, st)
			continue
		}
		body, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		st.Exists = true
		st.Size = info.Size()
		st.SHA256 = hex.EncodeToString(sum[:])
		st.Modified = info.ModTime().UTC().Format(time.RFC3339)
		out = append(out, st)
	}
	return out, nil
}

func (m *Manager) Status(cfg Config) (StatusResult, error) {
	files, err := m.CollectFiles()
	if err != nil {
		return StatusResult{}, err
	}
	state, err := m.ReadState()
	if err != nil {
		return StatusResult{}, err
	}
	provider := strings.TrimSpace(cfg.Provider)
	configured := false
	remoteBase := ""
	if provider == "webdav" && strings.TrimSpace(cfg.WebDAVURL) != "" {
		configured = true
		remoteBase = remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID)
	}
	return StatusResult{
		Provider:     provider,
		Configured:   configured,
		WorkspaceID:  m.opts.WorkspaceID,
		WorkspaceDir: m.opts.WorkspaceDir,
		StatePath:    m.StatePath(),
		LastPushAt:   state.LastPushAt,
		RemoteBase:   remoteBase,
		Files:        files,
	}, nil
}

func (m *Manager) Push(ctx context.Context, cfg Config) (PushResult, error) {
	if strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = "webdav"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Provider != "webdav" {
		return PushResult{}, fmt.Errorf("unsupported sync provider %q", cfg.Provider)
	}
	client, err := newWebDAVClient(cfg)
	if err != nil {
		return PushResult{}, err
	}
	files, err := m.CollectFiles()
	if err != nil {
		return PushResult{}, err
	}
	snapshot := time.Now().UTC().Format("20060102T150405Z")
	latestDir := remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID, "latest")
	snapshotDir := remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID, "history", snapshot)
	if err := client.EnsureCollection(ctx, latestDir); err != nil {
		return PushResult{}, err
	}
	if err := client.EnsureCollection(ctx, snapshotDir); err != nil {
		return PushResult{}, err
	}

	result := PushResult{
		WorkspaceID:  m.opts.WorkspaceID,
		WorkspaceDir: m.opts.WorkspaceDir,
		StatePath:    m.StatePath(),
		Snapshot:     snapshot,
		RemoteBase:   remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID),
	}
	state := State{
		Provider:     cfg.Provider,
		WorkspaceID:  m.opts.WorkspaceID,
		WorkspaceDir: m.opts.WorkspaceDir,
		LastPushAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
		Files:        map[string]StateFile{},
	}

	manifest := map[string]any{
		"provider":      cfg.Provider,
		"workspace_id":  m.opts.WorkspaceID,
		"workspace_dir": m.opts.WorkspaceDir,
		"snapshot":      snapshot,
		"pushed_at":     state.LastPushAt,
		"files":         []map[string]any{},
	}

	for _, item := range files {
		if !item.Exists {
			result.Missing = append(result.Missing, item.Name)
			continue
		}
		body, err := os.ReadFile(item.Path)
		if err != nil {
			return PushResult{}, err
		}
		latestPath := remotePath(latestDir, item.Name)
		snapshotPath := remotePath(snapshotDir, item.Name)
		if err := client.Put(ctx, latestPath, body, "text/markdown; charset=utf-8"); err != nil {
			return PushResult{}, err
		}
		if err := client.Put(ctx, snapshotPath, body, "text/markdown; charset=utf-8"); err != nil {
			return PushResult{}, err
		}
		result.Uploaded = append(result.Uploaded, UploadedFile{
			Name:         item.Name,
			LatestPath:   latestPath,
			SnapshotPath: snapshotPath,
			SHA256:       item.SHA256,
			Size:         item.Size,
		})
		state.Files[item.Name] = StateFile{
			SHA256:       item.SHA256,
			Size:         item.Size,
			LatestPath:   latestPath,
			SnapshotPath: snapshotPath,
		}
		manifest["files"] = append(manifest["files"].([]map[string]any), map[string]any{
			"name":          item.Name,
			"sha256":        item.SHA256,
			"size":          item.Size,
			"latest_path":   latestPath,
			"snapshot_path": snapshotPath,
		})
	}
	if len(result.Uploaded) == 0 {
		return PushResult{}, fmt.Errorf("no existing documents found in workspace %s", m.opts.WorkspaceDir)
	}

	manifestBody, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return PushResult{}, err
	}
	if err := client.Put(ctx, remotePath(latestDir, "manifest.json"), append(manifestBody, '\n'), "application/json"); err != nil {
		return PushResult{}, err
	}
	if err := client.Put(ctx, remotePath(snapshotDir, "manifest.json"), append(manifestBody, '\n'), "application/json"); err != nil {
		return PushResult{}, err
	}
	if err := m.WriteState(state); err != nil {
		return PushResult{}, err
	}
	return result, nil
}

func (m *Manager) ListRemote(ctx context.Context, cfg Config) (ListRemoteResult, error) {
	if strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = "webdav"
	}
	client, err := newWebDAVClient(cfg)
	if err != nil {
		return ListRemoteResult{}, err
	}
	historyDir := remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID, "history")
	entries, err := client.List(ctx, historyDir)
	if err != nil {
		return ListRemoteResult{}, err
	}
	out := ListRemoteResult{
		WorkspaceID: cfg.WorkspaceID,
		RemoteBase:  remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID),
	}
	for _, entry := range entries {
		name := path.Base(strings.TrimSuffix(entry, "/"))
		if name == "" || name == "history" {
			continue
		}
		out.Snapshots = append(out.Snapshots, RemoteSnapshot{Name: name})
	}
	return out, nil
}

func (m *Manager) Pull(ctx context.Context, cfg Config, snapshot, targetDir string) (PullResult, error) {
	if strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = "webdav"
	}
	if strings.TrimSpace(targetDir) == "" {
		return PullResult{}, fmt.Errorf("target dir is required")
	}
	client, err := newWebDAVClient(cfg)
	if err != nil {
		return PullResult{}, err
	}
	mode := "latest"
	baseDir := remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID, "latest")
	if strings.TrimSpace(snapshot) != "" && strings.TrimSpace(snapshot) != "latest" {
		mode = strings.TrimSpace(snapshot)
		baseDir = remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID, "history", strings.TrimSpace(snapshot))
	}
	manifestBody, err := client.Get(ctx, remotePath(baseDir, "manifest.json"))
	if err != nil {
		return PullResult{}, err
	}
	var manifest struct {
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifestBody, &manifest); err != nil {
		return PullResult{}, err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return PullResult{}, err
	}
	result := PullResult{
		WorkspaceID: cfg.WorkspaceID,
		RemoteBase:  remotePath(cfg.WebDAVBasePath, cfg.WorkspaceID),
		Snapshot:    mode,
		TargetDir:   targetDir,
	}
	for _, item := range manifest.Files {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		body, err := client.Get(ctx, remotePath(baseDir, item.Name))
		if err != nil {
			return PullResult{}, err
		}
		dst := filepath.Join(targetDir, item.Name)
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return PullResult{}, err
		}
		result.Files = append(result.Files, PulledFile{
			Name: item.Name,
			Path: dst,
			Size: int64(len(body)),
		})
	}
	return result, nil
}

func (m *Manager) Restore(sourceDir string, force bool) (RestoreResult, error) {
	if strings.TrimSpace(sourceDir) == "" {
		return RestoreResult{}, fmt.Errorf("source dir is required")
	}
	info, err := os.Stat(sourceDir)
	if err != nil {
		return RestoreResult{}, err
	}
	if !info.IsDir() {
		return RestoreResult{}, fmt.Errorf("source path is not a directory: %s", sourceDir)
	}
	if err := os.MkdirAll(m.opts.WorkspaceDir, 0o755); err != nil {
		return RestoreResult{}, err
	}
	result := RestoreResult{
		WorkspaceID:  m.opts.WorkspaceID,
		WorkspaceDir: m.opts.WorkspaceDir,
		SourceDir:    sourceDir,
	}
	for _, name := range ImportantDocs {
		src := filepath.Join(sourceDir, name)
		info, err := os.Stat(src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return RestoreResult{}, err
		}
		if info.IsDir() {
			continue
		}
		dst := filepath.Join(m.opts.WorkspaceDir, name)
		if _, err := os.Stat(dst); err == nil && !force {
			result.Skipped = append(result.Skipped, RestoredFile{
				Name: name,
				Path: dst,
				Size: info.Size(),
			})
			continue
		}
		body, err := os.ReadFile(src)
		if err != nil {
			return RestoreResult{}, err
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return RestoreResult{}, err
		}
		result.Files = append(result.Files, RestoredFile{
			Name: name,
			Path: dst,
			Size: int64(len(body)),
		})
	}
	if len(result.Files) == 0 {
		if len(result.Skipped) > 0 {
			return result, nil
		}
		return RestoreResult{}, fmt.Errorf("no restorable documents found in %s", sourceDir)
	}
	return result, nil
}

func sanitizeSegment(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\\", "-")
	text = strings.ReplaceAll(text, "/", "-")
	text = strings.ReplaceAll(text, " ", "-")
	text = strings.Trim(text, "-.")
	if text == "" {
		return ""
	}
	return text
}

func remotePath(parts ...string) string {
	items := []string{"/"}
	for _, item := range parts {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		items = append(items, trimmed)
	}
	return path.Clean(path.Join(items...))
}

type webDAVClient struct {
	baseURL    *url.URL
	username   string
	password   string
	httpClient *http.Client
}

type propfindMultistatus struct {
	Responses []propfindResponse `xml:"response"`
}

type propfindResponse struct {
	Href string `xml:"href"`
}

func newWebDAVClient(cfg Config) (*webDAVClient, error) {
	if strings.TrimSpace(cfg.WebDAVURL) == "" {
		return nil, fmt.Errorf("webdav url is required")
	}
	if strings.TrimSpace(cfg.WebDAVUsername) == "" {
		return nil, fmt.Errorf("webdav username is required")
	}
	if strings.TrimSpace(cfg.WebDAVPassword) == "" {
		return nil, fmt.Errorf("webdav password is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.WebDAVURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid webdav url %q", cfg.WebDAVURL)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &webDAVClient{
		baseURL:  parsed,
		username: cfg.WebDAVUsername,
		password: cfg.WebDAVPassword,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *webDAVClient) EnsureCollection(ctx context.Context, remoteDir string) error {
	trimmed := strings.Trim(strings.TrimSpace(remoteDir), "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	cur := ""
	for _, item := range parts {
		cur = remotePath(cur, item)
		if err := c.mkcol(ctx, cur); err != nil {
			return err
		}
	}
	return nil
}

func (c *webDAVClient) Put(ctx context.Context, remoteFile string, body []byte, contentType string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.urlFor(remoteFile), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	if strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return fmt.Errorf("webdav PUT %s failed: %s", remoteFile, responseSummary(resp))
}

func (c *webDAVClient) Get(ctx context.Context, remoteFile string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.urlFor(remoteFile), nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webdav GET %s failed: %s", remoteFile, responseSummary(resp))
	}
	return io.ReadAll(resp.Body)
}

func (c *webDAVClient) List(ctx context.Context, remoteDir string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", c.urlFor(remoteDir), strings.NewReader(`<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("webdav PROPFIND %s failed: %s", remoteDir, responseSummary(resp))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var data propfindMultistatus
	if err := xml.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	out := []string{}
	for _, item := range data.Responses {
		href := strings.TrimSpace(item.Href)
		if href == "" {
			continue
		}
		out = append(out, href)
	}
	return out, nil
}

func (c *webDAVClient) mkcol(ctx context.Context, remoteDir string) error {
	req, err := http.NewRequestWithContext(ctx, "MKCOL", c.urlFor(remoteDir), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusMethodNotAllowed:
		io.Copy(io.Discard, resp.Body)
		return nil
	default:
		return fmt.Errorf("webdav MKCOL %s failed: %s", remoteDir, responseSummary(resp))
	}
}

func (c *webDAVClient) urlFor(remote string) string {
	next := *c.baseURL
	next.Path = path.Join(c.baseURL.Path, strings.TrimPrefix(remotePath(remote), "/"))
	return next.String()
}

func responseSummary(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	text := strings.TrimSpace(string(body))
	if text == "" {
		return resp.Status
	}
	return fmt.Sprintf("%s body=%s", resp.Status, text)
}
