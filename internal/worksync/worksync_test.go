package worksync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPushUploadsDocsAndWritesState(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, name := range ImportantDocs {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	stored := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		case "PROPFIND":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><multistatus xmlns="DAV:"><response><href>/dav/suncodexclaw/backups/default/history/</href></response></multistatus>`))
		case http.MethodPut:
			body, _ := ioReadAll(r)
			stored[r.URL.Path] = body
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	mgr := NewManager(Options{
		RepoRoot:     repo,
		WorkspaceDir: workspace,
		WorkspaceID:  "default",
	})
	result, err := mgr.Push(context.Background(), Config{
		Provider:       "webdav",
		WebDAVURL:      server.URL + "/dav",
		WebDAVUsername: "user",
		WebDAVPassword: "pass",
		WebDAVBasePath: "/suncodexclaw/backups",
		WorkspaceID:    "default",
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Uploaded) != len(ImportantDocs) {
		t.Fatalf("Push() uploaded = %d, want %d", len(result.Uploaded), len(ImportantDocs))
	}
	for _, name := range ImportantDocs {
		if _, ok := stored["/dav/suncodexclaw/backups/default/latest/"+name]; !ok {
			t.Fatalf("latest upload missing for %s", name)
		}
	}
	if _, ok := stored["/dav/suncodexclaw/backups/default/latest/manifest.json"]; !ok {
		t.Fatalf("latest manifest missing")
	}
	stateBody, err := os.ReadFile(mgr.StatePath())
	if err != nil {
		t.Fatalf("ReadFile(state) error = %v", err)
	}
	var state State
	if err := json.Unmarshal(stateBody, &state); err != nil {
		t.Fatalf("Unmarshal(state) error = %v", err)
	}
	if state.WorkspaceID != "default" {
		t.Fatalf("state.WorkspaceID = %q, want default", state.WorkspaceID)
	}
}

func TestListRemoteAndPull(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "PROPFIND" && strings.HasSuffix(r.URL.Path, "/history"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><multistatus xmlns="DAV:"><response><href>/dav/base/default/history/</href></response><response><href>/dav/base/default/history/20260320T010203Z/</href></response></multistatus>`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/latest/manifest.json"):
			_, _ = w.Write([]byte(`{"files":[{"name":"agent.md"},{"name":"soul.md"}]}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/latest/agent.md"):
			_, _ = w.Write([]byte("# Agent\n"))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/latest/soul.md"):
			_, _ = w.Write([]byte("# Soul\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	repo := t.TempDir()
	targetDir := filepath.Join(repo, "restore")
	mgr := NewManager(Options{
		RepoRoot:     repo,
		WorkspaceDir: filepath.Join(repo, "workspace"),
		WorkspaceID:  "default",
	})
	listed, err := mgr.ListRemote(context.Background(), Config{
		Provider:       "webdav",
		WebDAVURL:      server.URL + "/dav",
		WebDAVUsername: "user",
		WebDAVPassword: "pass",
		WebDAVBasePath: "/base",
		WorkspaceID:    "default",
	})
	if err != nil {
		t.Fatalf("ListRemote() error = %v", err)
	}
	if len(listed.Snapshots) != 1 || listed.Snapshots[0].Name != "20260320T010203Z" {
		t.Fatalf("ListRemote() snapshots = %#v", listed.Snapshots)
	}
	pulled, err := mgr.Pull(context.Background(), Config{
		Provider:       "webdav",
		WebDAVURL:      server.URL + "/dav",
		WebDAVUsername: "user",
		WebDAVPassword: "pass",
		WebDAVBasePath: "/base",
		WorkspaceID:    "default",
	}, "latest", targetDir)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(pulled.Files) != 2 {
		t.Fatalf("Pull() files = %d, want 2", len(pulled.Files))
	}
	if _, err := os.Stat(filepath.Join(targetDir, "agent.md")); err != nil {
		t.Fatalf("agent.md not restored: %v", err)
	}
}

func TestRestoreCopiesFilesIntoWorkspace(t *testing.T) {
	repo := t.TempDir()
	sourceDir := filepath.Join(repo, "restore-source")
	workspaceDir := filepath.Join(repo, "workspace")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "agent.md"), []byte("agent body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(agent) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "soul.md"), []byte("soul body\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(soul) error = %v", err)
	}
	mgr := NewManager(Options{
		RepoRoot:     repo,
		WorkspaceDir: workspaceDir,
		WorkspaceID:  "default",
	})
	result, err := mgr.Restore(sourceDir, true)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("Restore() files = %d, want 2", len(result.Files))
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "agent.md")); err != nil {
		t.Fatalf("restored agent missing: %v", err)
	}
}

func TestRestoreSkipsExistingFilesWithoutForce(t *testing.T) {
	repo := t.TempDir()
	sourceDir := filepath.Join(repo, "restore-source")
	workspaceDir := filepath.Join(repo, "workspace")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error = %v", err)
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "agent.md"), []byte("remote agent\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source agent) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "soul.md"), []byte("remote soul\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(source soul) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "agent.md"), []byte("local agent\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(workspace agent) error = %v", err)
	}

	mgr := NewManager(Options{
		RepoRoot:     repo,
		WorkspaceDir: workspaceDir,
		WorkspaceID:  "default",
	})
	result, err := mgr.Restore(sourceDir, false)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(result.Files) != 1 || result.Files[0].Name != "soul.md" {
		t.Fatalf("Restore() files = %#v", result.Files)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Name != "agent.md" {
		t.Fatalf("Restore() skipped = %#v", result.Skipped)
	}
	agentBody, err := os.ReadFile(filepath.Join(workspaceDir, "agent.md"))
	if err != nil {
		t.Fatalf("ReadFile(agent) error = %v", err)
	}
	if string(agentBody) != "local agent\n" {
		t.Fatalf("agent.md = %q, want local file preserved", string(agentBody))
	}
}

func TestStatusReportsMissingFiles(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "agent.md"), []byte("agent\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	mgr := NewManager(Options{
		RepoRoot:     repo,
		WorkspaceDir: workspace,
		WorkspaceID:  "default",
	})
	status, err := mgr.Status(Config{
		Provider:       "webdav",
		WebDAVURL:      "https://dav.example.test/remote.php/dav/files/test",
		WebDAVBasePath: "/suncodexclaw/backups",
		WorkspaceID:    "default",
	})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Configured {
		t.Fatalf("Status().Configured = false, want true")
	}
	if len(status.Files) != len(ImportantDocs) {
		t.Fatalf("Status().Files len = %d, want %d", len(status.Files), len(ImportantDocs))
	}
	missing := 0
	for _, item := range status.Files {
		if !item.Exists {
			missing++
		}
	}
	if missing != 2 {
		t.Fatalf("missing files = %d, want 2", missing)
	}
	if !strings.Contains(status.RemoteBase, "/default") {
		t.Fatalf("Status().RemoteBase = %q, want workspace suffix", status.RemoteBase)
	}
}

func ioReadAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return os.ReadFile(writeBodyToTemp(r))
}

func writeBodyToTemp(r *http.Request) string {
	file, err := os.CreateTemp("", "worksync-body-*")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if _, err := file.ReadFrom(r.Body); err != nil {
		panic(err)
	}
	return file.Name()
}
