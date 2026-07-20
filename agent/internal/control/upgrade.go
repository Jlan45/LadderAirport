package control

import (
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

const (
	// DefaultUpgradeDir is where the unprivileged agent stages a new binary.
	// A root-owned systemd path unit watches this directory and applies the upgrade.
	DefaultUpgradeDir = "/var/lib/ladder-agent/upgrade"

	defaultUpgradeRepo = "Jlan45/LadderAirport"
	githubAPIBase      = "https://api.github.com"
	githubReleaseBase  = "https://github.com"
)

// UpgradeRequest is the agent-side upgrade plan.
type UpgradeRequest struct {
	Version     string // tag or "latest"/empty
	Repo        string // owner/repo
	DownloadURL string // optional direct URL
	SHA256      string // optional hex digest
	// UpgradeDir overrides DefaultUpgradeDir (tests).
	UpgradeDir string
	// HTTPClient optional.
	HTTPClient *http.Client
}

// UpgradeResult is returned after the binary is staged for the helper.
type UpgradeResult struct {
	Version     string
	StagedPath  string
	DownloadURL string
	Message     string
}

// StageAgentUpgrade downloads the target binary into the upgrade staging dir and
// writes a .ready marker so the root helper applies it. This process never
// replaces its own executable (no root / NoNewPrivileges).
func StageAgentUpgrade(ctx context.Context, req UpgradeRequest) (*UpgradeResult, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("remote upgrade is only supported on linux (got %s)", runtime.GOOS)
	}
	dir := strings.TrimSpace(req.UpgradeDir)
	if dir == "" {
		dir = DefaultUpgradeDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create upgrade dir: %w", err)
	}

	client := req.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}

	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = "latest"
	}
	repo := strings.TrimSpace(req.Repo)
	if repo == "" {
		repo = defaultUpgradeRepo
	}

	url := strings.TrimSpace(req.DownloadURL)
	resolvedVersion := version
	if url == "" {
		u, tag, err := resolveReleaseURL(ctx, client, repo, version)
		if err != nil {
			return nil, err
		}
		url = u
		if tag != "" {
			resolvedVersion = tag
		}
	}

	arch := runtime.GOARCH
	asset := fmt.Sprintf("ladder-agent-linux-%s", arch)
	tmpPath := filepath.Join(dir, asset+".partial")
	finalPath := filepath.Join(dir, asset)
	readyPath := finalPath + ".ready"
	metaPath := finalPath + ".json"

	// Clean previous staging artifacts for this asset.
	_ = os.Remove(tmpPath)
	_ = os.Remove(readyPath)

	if err := downloadFile(ctx, client, url, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}

	sum, err := fileSHA256(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	if want := strings.TrimSpace(strings.ToLower(req.SHA256)); want != "" {
		if sum != want {
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("sha256 mismatch: got %s want %s", sum, want)
		}
	} else if resolvedVersion != "" && resolvedVersion != "latest" {
		// Best-effort: verify against SHA256SUMS.txt when present.
		if err := verifyAgainstReleaseSums(ctx, client, repo, resolvedVersion, asset, sum); err != nil {
			// Soft-fail only when sums file missing; hard-fail on mismatch.
			if !strings.Contains(err.Error(), "no checksum file") {
				_ = os.Remove(tmpPath)
				return nil, err
			}
		}
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("chmod staged binary: %w", err)
	}
	// Atomic-ish rename into place.
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("stage binary: %w", err)
	}

	meta := map[string]any{
		"version":      resolvedVersion,
		"download_url": url,
		"sha256":       sum,
		"staged_at":    time.Now().UTC().Format(time.RFC3339),
		"asset":        asset,
	}
	if b, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = os.WriteFile(metaPath, b, 0o644)
	}

	// Marker file triggers the systemd path unit (PathExists).
	marker := fmt.Sprintf("version=%s\nsha256=%s\nsource=%s\n", resolvedVersion, sum, url)
	if err := os.WriteFile(readyPath, []byte(marker), 0o644); err != nil {
		return nil, fmt.Errorf("write ready marker: %w", err)
	}

	return &UpgradeResult{
		Version:     resolvedVersion,
		StagedPath:  finalPath,
		DownloadURL: url,
		Message:     "staged; waiting for upgrade helper to apply and restart",
	}, nil
}

func resolveReleaseURL(ctx context.Context, client *http.Client, repo, version string) (url, tag string, err error) {
	arch := runtime.GOARCH
	asset := fmt.Sprintf("ladder-agent-linux-%s", arch)
	version = strings.TrimSpace(version)
	if version == "" || version == "latest" {
		apiURL := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBase, repo)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return "", "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "LadderAirport-Agent")
		resp, err := client.Do(req)
		if err != nil {
			return "", "", fmt.Errorf("fetch latest release: %w", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if err != nil {
			return "", "", err
		}
		if resp.StatusCode != http.StatusOK {
			return "", "", fmt.Errorf("github latest release: HTTP %d", resp.StatusCode)
		}
		var parsed struct {
			TagName string `json:"tag_name"`
			Assets  []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			} `json:"assets"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return "", "", err
		}
		tag = strings.TrimSpace(parsed.TagName)
		for _, a := range parsed.Assets {
			if a.Name == asset && a.BrowserDownloadURL != "" {
				return a.BrowserDownloadURL, tag, nil
			}
		}
		if tag == "" {
			return "", "", fmt.Errorf("latest release has no tag_name")
		}
		return fmt.Sprintf("%s/%s/releases/download/%s/%s", githubReleaseBase, repo, tag, asset), tag, nil
	}

	tag = version
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", githubReleaseBase, repo, tag, asset), tag, nil
}

func downloadFile(ctx context.Context, client *http.Client, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "LadderAirport-Agent")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 256<<20)); err != nil {
		return fmt.Errorf("write download: %w", err)
	}
	return f.Close()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyAgainstReleaseSums(ctx context.Context, client *http.Client, repo, tag, asset, got string) error {
	sumsURL := fmt.Sprintf("%s/%s/releases/download/%s/SHA256SUMS.txt", githubReleaseBase, repo, tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "LadderAirport-Agent")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("no checksum file: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no checksum file")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksum file HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// formats: "<hex>  <name>" or "<hex> *<name>"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == asset {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum file entry for %s", asset)
	}
	if want != got {
		return fmt.Errorf("sha256 mismatch vs release sums: got %s want %s", got, want)
	}
	return nil
}
