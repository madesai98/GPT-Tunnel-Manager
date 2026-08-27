package tunnelclient

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	proc "github.com/madesai98/GPT-Tunnel-Manager/internal/process"
)

const (
	latestURL   = "https://api.github.com/repos/openai/tunnel-client/releases/latest"
	releasesURL = "https://api.github.com/repos/openai/tunnel-client/releases?per_page=30"
)

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

type Release struct {
	TagName    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

type Active struct {
	Version         string `json:"version"`
	Path            string `json:"path"`
	Digest          string `json:"digest"`
	PreviousVersion string `json:"previous_version,omitempty"`
}

type Installer struct {
	Root   string
	Client *http.Client
}

func NewInstaller(root string) *Installer {
	return &Installer{Root: root, Client: &http.Client{Timeout: 2 * time.Minute}}
}

func normalizeChannel(channel string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		return "stable"
	}
	return channel
}

func (i *Installer) CheckLatest(ctx context.Context) (Release, error) {
	return i.CheckChannel(ctx, "stable")
}

func (i *Installer) CheckChannel(ctx context.Context, channel string) (Release, error) {
	switch normalizeChannel(channel) {
	case "stable":
		var release Release
		if err := i.getJSON(ctx, latestURL, &release); err != nil {
			return Release{}, err
		}
		if release.TagName == "" {
			return release, errors.New("latest tunnel-client release is missing a tag")
		}
		return release, nil
	case "prerelease":
		var releases []Release
		if err := i.getJSON(ctx, releasesURL, &releases); err != nil {
			return Release{}, err
		}
		for _, release := range releases {
			if !release.Draft && release.Prerelease && release.TagName != "" {
				return release, nil
			}
		}
		return Release{}, errors.New("no tunnel-client prerelease is currently published")
	default:
		return Release{}, fmt.Errorf("unsupported tunnel-client channel %q", channel)
	}
}

func (i *Installer) getJSON(ctx context.Context, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "GPT-Tunnel-Manager")
	resp, err := i.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release metadata HTTP %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(dst)
}

func (i *Installer) Ensure(ctx context.Context, override string) (Active, error) {
	return i.EnsureChannel(ctx, override, "stable")
}

func (i *Installer) EnsureChannel(ctx context.Context, override, channel string) (Active, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return Active{}, err
		}
		return Active{Version: "custom", Path: override}, nil
	}
	if a, err := i.readActive(); err == nil {
		if _, err := os.Stat(a.Path); err == nil {
			return a, nil
		}
	}
	return i.InstallChannel(ctx, channel)
}

func (i *Installer) InstallLatest(ctx context.Context) (Active, error) {
	return i.InstallChannel(ctx, "stable")
}

func (i *Installer) InstallChannel(ctx context.Context, channel string) (Active, error) {
	r, err := i.CheckChannel(ctx, channel)
	if err != nil {
		return Active{}, err
	}
	return i.install(ctx, r)
}

func (i *Installer) install(ctx context.Context, r Release) (Active, error) {
	asset, err := selectAsset(r)
	if err != nil {
		return Active{}, err
	}
	wantDigest := strings.TrimPrefix(strings.TrimSpace(asset.Digest), "sha256:")
	if len(wantDigest) != sha256.Size*2 {
		return Active{}, errors.New("release asset is missing a usable SHA-256 digest")
	}

	dataDir := filepath.Join(i.Root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Active{}, err
	}
	versionDir := filepath.Join(i.Root, "tools", "tunnel-client", r.TagName)
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		return Active{}, err
	}
	tmp, err := os.CreateTemp(dataDir, "tunnel-client-*.zip")
	if err != nil {
		return Active{}, err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return Active{}, err
	}
	defer os.Remove(tmpPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return Active{}, err
	}
	req.Header.Set("User-Agent", "GPT-Tunnel-Manager")
	resp, err := i.Client.Do(req)
	if err != nil {
		return Active{}, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return Active{}, fmt.Errorf("download HTTP %s", resp.Status)
	}
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		resp.Body.Close()
		return Active{}, err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 512<<20))
	resp.Body.Close()
	closeErr := f.Close()
	if copyErr != nil {
		return Active{}, copyErr
	}
	if closeErr != nil {
		return Active{}, closeErr
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, wantDigest) {
		return Active{}, errors.New("tunnel-client checksum mismatch")
	}

	bin, err := extractBinary(tmpPath, versionDir)
	if err != nil {
		return Active{}, err
	}
	if err := probeBinary(ctx, bin); err != nil {
		return Active{}, fmt.Errorf("tunnel-client compatibility probe failed: %w", err)
	}
	a := Active{Version: r.TagName, Path: bin, Digest: "sha256:" + got}
	if err := i.promote(a); err != nil {
		return Active{}, err
	}
	return i.readActive()
}

func probeBinary(parent context.Context, path string) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := proc.ConfigureCommand(exec.CommandContext(ctx, path, "help", "quickstart"))
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func selectAsset(r Release) (Asset, error) {
	suffix := fmt.Sprintf("-%s-%s.zip", runtime.GOOS, runtime.GOARCH)
	for _, a := range r.Assets {
		if strings.HasPrefix(a.Name, "tunnel-client-") && strings.HasSuffix(a.Name, suffix) && !strings.Contains(a.Name, "runtime-cloudflared") && !strings.Contains(a.Name, "runtime-") {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("no tunnel-client asset for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func extractBinary(zipPath, dir string) (string, error) {
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer z.Close()
	want := "tunnel-client"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	for _, file := range z.File {
		if filepath.Base(file.Name) != want {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		dst := filepath.Join(dir, want)
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			rc.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, io.LimitReader(rc, 128<<20))
		rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		return dst, nil
	}
	return "", errors.New("tunnel-client executable not found in archive")
}

func (i *Installer) activePath() string {
	return filepath.Join(i.Root, "tools", "tunnel-client", "active.json")
}

func (i *Installer) readActive() (Active, error) {
	b, err := os.ReadFile(i.activePath())
	if err != nil {
		return Active{}, err
	}
	var a Active
	if err := json.Unmarshal(b, &a); err != nil {
		return Active{}, err
	}
	if a.Version == "" || a.Path == "" {
		return Active{}, errors.New("invalid active tunnel-client record")
	}
	return a, nil
}

func (i *Installer) promote(a Active) error {
	if cur, err := i.readActive(); err == nil {
		if cur.Version != a.Version {
			a.PreviousVersion = cur.Version
		} else {
			a.PreviousVersion = cur.PreviousVersion
		}
	}
	return i.writeActive(a)
}

func (i *Installer) writeActive(a Active) error {
	if err := os.MkdirAll(filepath.Dir(i.activePath()), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(i.activePath()), ".active-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, i.activePath()); err != nil {
		return err
	}
	ok = true
	return nil
}

func (i *Installer) Rollback() (Active, error) {
	cur, _ := i.readActive()
	root := filepath.Join(i.Root, "tools", "tunnel-client")
	want := "tunnel-client"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if cur.PreviousVersion != "" {
		p := filepath.Join(root, cur.PreviousVersion, want)
		if _, err := os.Stat(p); err == nil {
			a := Active{Version: cur.PreviousVersion, Path: p, PreviousVersion: cur.Version}
			if err := i.writeActive(a); err != nil {
				return Active{}, err
			}
			return a, nil
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return Active{}, err
	}
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != cur.Version {
			versions = append(versions, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	for _, version := range versions {
		p := filepath.Join(root, version, want)
		if _, err := os.Stat(p); err == nil {
			a := Active{Version: version, Path: p, PreviousVersion: cur.Version}
			if err := i.writeActive(a); err != nil {
				return Active{}, err
			}
			return a, nil
		}
	}
	return Active{}, errors.New("no previous tunnel-client version available")
}
