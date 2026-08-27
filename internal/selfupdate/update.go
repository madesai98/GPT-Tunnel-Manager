package selfupdate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repositoryOwner = "madesai98"
	repositoryName  = "GPT-Tunnel-Manager"
	latestRelease   = "https://api.github.com/repos/" + repositoryOwner + "/" + repositoryName + "/releases/latest"
	maxArchiveSize  = int64(512 << 20)
	maxChecksumSize = int64(2 << 20)
)

var protectedTopLevel = map[string]struct{}{
	"config": {},
	"data":   {},
	"tools":  {},
}

type Plan struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
	TempDir         string
	StageDir        string
	TargetDir       string
	Executable      string
}

type release struct {
	TagName string         `json:"tag_name"`
	Draft   bool           `json:"draft"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

type updater struct {
	client *http.Client
}

func CheckAndStage(ctx context.Context, currentVersion, executable string) (Plan, error) {
	executable, err := filepath.Abs(executable)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve executable path: %w", err)
	}
	current, err := parseVersion(currentVersion)
	if err != nil {
		return Plan{}, fmt.Errorf("parse current version %q: %w", currentVersion, err)
	}

	u := updater{client: &http.Client{Timeout: 2 * time.Minute}}
	rel, err := u.latest(ctx)
	if err != nil {
		return Plan{}, err
	}
	latest, err := parseVersion(rel.TagName)
	if err != nil {
		return Plan{}, fmt.Errorf("parse latest release version %q: %w", rel.TagName, err)
	}

	plan := Plan{
		CurrentVersion: current.String(),
		LatestVersion:  latest.String(),
		TargetDir:      filepath.Dir(executable),
		Executable:     executable,
	}
	if compareVersions(current, latest) >= 0 {
		return plan, nil
	}
	plan.UpdateAvailable = true

	assetName, err := platformAssetName(rel.TagName)
	if err != nil {
		return Plan{}, err
	}
	asset, ok := findAsset(rel.Assets, assetName)
	if !ok {
		return Plan{}, fmt.Errorf("release %s does not contain %s", rel.TagName, assetName)
	}
	if asset.Size <= 0 || asset.Size > maxArchiveSize {
		return Plan{}, fmt.Errorf("release asset %s has invalid size %d", asset.Name, asset.Size)
	}

	tempDir, err := os.MkdirTemp("", "gpt-tunnel-manager-update-*")
	if err != nil {
		return Plan{}, fmt.Errorf("create update temp directory: %w", err)
	}
	plan.TempDir = tempDir
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()

	archivePath := filepath.Join(tempDir, asset.Name)
	if err := u.download(ctx, asset.BrowserDownloadURL, archivePath, maxArchiveSize); err != nil {
		return Plan{}, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if err := u.verify(ctx, archivePath, asset, rel.Assets); err != nil {
		return Plan{}, err
	}

	stageDir := filepath.Join(tempDir, "stage")
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return Plan{}, fmt.Errorf("create update stage: %w", err)
	}
	if err := extractReleaseArchive(archivePath, asset.Name, stageDir, filepath.Base(executable)); err != nil {
		return Plan{}, err
	}
	if err := os.Remove(archivePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Plan{}, fmt.Errorf("remove downloaded update archive: %w", err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, filepath.Base(executable))); err != nil {
		return Plan{}, fmt.Errorf("staged update is missing executable %s", filepath.Base(executable))
	}

	plan.StageDir = stageDir
	cleanup = false
	return plan, nil
}

func (p Plan) Cleanup() {
	if p.TempDir != "" {
		_ = os.RemoveAll(p.TempDir)
	}
}

func (u updater) latest(ctx context.Context) (release, error) {
	var rel release
	if err := u.getJSON(ctx, latestRelease, &rel); err != nil {
		return rel, fmt.Errorf("check latest GPT Tunnel Manager release: %w", err)
	}
	if rel.Draft || strings.TrimSpace(rel.TagName) == "" {
		return rel, errors.New("latest GPT Tunnel Manager release is invalid")
	}
	return rel, nil
}

func (u updater) getJSON(ctx context.Context, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GPT-Tunnel-Manager-self-updater")
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GitHub returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(dst)
}

func (u updater) download(ctx context.Context, rawURL, destination string, limit int64) error {
	if err := validateReleaseURL(rawURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "GPT-Tunnel-Manager-self-updater")
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub returned %s", resp.Status)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	written, err := io.Copy(file, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if written > limit {
		return fmt.Errorf("download exceeds %d bytes", limit)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

func (u updater) verify(ctx context.Context, archivePath string, asset releaseAsset, assets []releaseAsset) error {
	expected := strings.TrimSpace(asset.Digest)
	if strings.HasPrefix(strings.ToLower(expected), "sha256:") {
		return verifySHA256(archivePath, strings.TrimSpace(expected[len("sha256:"):]))
	}

	checksums, ok := findAsset(assets, "SHA256SUMS.txt")
	if !ok {
		return fmt.Errorf("release asset %s has no SHA-256 digest and release has no SHA256SUMS.txt", asset.Name)
	}
	checksumPath := filepath.Join(filepath.Dir(archivePath), "SHA256SUMS.txt")
	if err := u.download(ctx, checksums.BrowserDownloadURL, checksumPath, maxChecksumSize); err != nil {
		return fmt.Errorf("download release checksums: %w", err)
	}
	defer os.Remove(checksumPath)
	checksum, err := checksumFor(checksumPath, asset.Name)
	if err != nil {
		return err
	}
	return verifySHA256(archivePath, checksum)
}

func verifySHA256(filePath, expected string) error {
	expectedBytes, err := hex.DecodeString(strings.TrimSpace(expected))
	if err != nil || len(expectedBytes) != sha256.Size {
		return errors.New("release asset has an invalid SHA-256 digest")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if !equalBytes(hash.Sum(nil), expectedBytes) {
		return errors.New("downloaded update failed SHA-256 verification")
	}
	return nil
}

func checksumFor(filePath, assetName string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("SHA256SUMS.txt does not contain %s", assetName)
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func validateReleaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid release URL: %w", err)
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return errors.New("release asset URL is not an HTTPS github.com URL")
	}
	prefix := "/" + repositoryOwner + "/" + repositoryName + "/releases/download/"
	if !strings.HasPrefix(parsed.EscapedPath(), prefix) && !strings.HasPrefix(parsed.Path, prefix) {
		return errors.New("release asset URL does not belong to the GPT Tunnel Manager repository")
	}
	return nil
}

func platformAssetName(tag string) (string, error) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "", fmt.Errorf("self-update is not supported on %s", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", fmt.Errorf("self-update is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("GPT-Tunnel-Manager-%s-%s-%s%s", tag, runtime.GOOS, runtime.GOARCH, ext), nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
