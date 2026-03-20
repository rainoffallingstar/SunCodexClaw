package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Options struct {
	Repo       string
	Version    string
	BinaryPath string
	HTTPClient *http.Client
	Output     io.Writer
	CheckOnly  bool
	DryRun     bool
	Executable string
}

type Result struct {
	Repo         string
	Version      string
	AssetName    string
	DownloadURL  string
	BinaryPath   string
	ReplacedPath string
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseResponse struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func Run(ctx context.Context, opts Options) (Result, error) {
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = "rainoffallingstar/SunCodexClaw"
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	out := opts.Output
	if out == nil {
		out = io.Discard
	}

	binaryPath, err := resolveBinaryPath(opts.BinaryPath)
	if err != nil {
		return Result{}, err
	}
	exeName := opts.Executable
	if strings.TrimSpace(exeName) == "" {
		exeName = "suncodexclawd"
		if runtime.GOOS == "windows" {
			exeName += ".exe"
		}
	}

	release, err := fetchRelease(ctx, client, repo, opts.Version)
	if err != nil {
		return Result{}, err
	}
	asset, err := findReleaseAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		Repo:        repo,
		Version:     release.TagName,
		AssetName:   asset.Name,
		DownloadURL: asset.BrowserDownloadURL,
		BinaryPath:  binaryPath,
	}

	if opts.CheckOnly || opts.DryRun {
		return result, nil
	}

	if runtime.GOOS == "windows" {
		return result, fmt.Errorf("self-update is not supported on windows yet")
	}

	tmpDir, err := os.MkdirTemp("", "suncodexclaw-update-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, asset.Name)
	if err := downloadFile(ctx, client, asset.BrowserDownloadURL, archivePath); err != nil {
		return result, err
	}
	if err := maybeVerifySHA256(ctx, client, asset, archivePath); err != nil {
		return result, err
	}

	extractedPath := filepath.Join(tmpDir, exeName)
	if err := extractBinaryFromTarGz(archivePath, exeName, extractedPath); err != nil {
		return result, err
	}

	if _, err := fmt.Fprintf(out, "update repo=%s version=%s asset=%s target=%s\n", repo, release.TagName, asset.Name, binaryPath); err != nil {
		return result, err
	}
	if err := replaceBinary(binaryPath, extractedPath); err != nil {
		return result, err
	}
	result.ReplacedPath = binaryPath
	return result, nil
}

func resolveBinaryPath(raw string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		return filepath.Abs(strings.TrimSpace(raw))
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return filepath.Abs(exe)
}

func fetchRelease(ctx context.Context, client *http.Client, repo, version string) (*releaseResponse, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	if strings.TrimSpace(version) != "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, strings.TrimSpace(version))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("github release lookup failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.TagName) == "" {
		return nil, fmt.Errorf("github release response missing tag_name")
	}
	return &out, nil
}

func findReleaseAsset(assets []releaseAsset, goos, goarch string) (*releaseAsset, error) {
	wantSuffix := fmt.Sprintf("-%s-%s.tar.gz", goos, goarch)
	for _, asset := range assets {
		if strings.HasSuffix(asset.Name, wantSuffix) {
			return &asset, nil
		}
	}
	return nil, fmt.Errorf("no release asset found for %s/%s", goos, goarch)
}

func downloadFile(ctx context.Context, client *http.Client, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("download failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	file, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}

func maybeVerifySHA256(ctx context.Context, client *http.Client, asset *releaseAsset, archivePath string) error {
	shaURL := asset.BrowserDownloadURL + ".sha256"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, shaURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return err
	}
	want := parseSHA256File(string(body))
	if want == "" {
		return nil
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 mismatch for %s", asset.Name)
	}
	return nil
}

func parseSHA256File(raw string) string {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return ""
	}
	sum := strings.TrimSpace(fields[0])
	if len(sum) != 64 {
		return ""
	}
	return strings.ToLower(sum)
}

func extractBinaryFromTarGz(archivePath, binaryName, dst string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(filepath.Clean(hdr.Name), "./")
		if filepath.Base(name) != binaryName {
			continue
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chmod(dst, 0o755)
	}
	return fmt.Errorf("binary %s not found in archive", binaryName)
}

func replaceBinary(targetPath, extractedPath string) error {
	targetInfo, err := os.Stat(targetPath)
	if err == nil {
		if err := os.Chmod(extractedPath, targetInfo.Mode()); err != nil {
			return err
		}
	}
	tmpPath := filepath.Join(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".new")
	if err := os.RemoveAll(tmpPath); err != nil {
		return err
	}
	if err := os.Rename(extractedPath, tmpPath); err != nil {
		return err
	}
	return os.Rename(tmpPath, targetPath)
}
