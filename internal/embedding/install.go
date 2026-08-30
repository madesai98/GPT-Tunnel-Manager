package embedding

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

const maxEmbeddingDownloadBytes int64 = 1 << 30

type runtimeAsset struct{ FileName, SHA256 string }

var llamaCppB10621Assets = map[string]runtimeAsset{
	"windows/amd64": {"llama-b10621-bin-win-cpu-x64.zip", "0e8b65e650e369f70f8307d890508886f171ef4fb00facccddd4a1b7ffdaca51"},
	"windows/arm64": {"llama-b10621-bin-win-cpu-arm64.zip", "c072e8bb057751587243c1e0ed28d82e23c7e0544a426e0d476f1e77792bf3ce"},
	"linux/amd64":   {"llama-b10621-bin-ubuntu-x64.tar.gz", "91d7b03ddae498a39f28fdb85d84d2b4a0fd3838d10b4f897e0ef8975bb9b583"},
	"linux/arm64":   {"llama-b10621-bin-ubuntu-arm64.tar.gz", "95940151be63492f70f659da420b268244cc83a6ee70e310d2600ccdb7ea4deb"},
	"darwin/amd64":  {"llama-b10621-bin-macos-x64.tar.gz", "33c44e036e0e223f71a29fc74a0ab3e130ca9eadeb032ecc1c7af25985b8b91b"},
	"darwin/arm64":  {"llama-b10621-bin-macos-arm64.tar.gz", "429c8270608600188035e5e92f7d78dffb7900904fe7dd7e6a84f48068cd13cf"},
}

func StorageRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cache) == "" {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			if err != nil {
				return "", err
			}
			return "", executableErr
		}
		return filepath.Join(filepath.Dir(executable), ".gtm-cache"), nil
	}
	return filepath.Join(cache, "GPT-Tunnel-Manager"), nil
}

func ModelPath(root string, model v2config.EmbeddingModel) string {
	return filepath.Join(root, "data", "embedding", "models", model.ID, model.FileName)
}

func RuntimeDir(root string, config v2config.EmbeddingConfig) string {
	return filepath.Join(root, "data", "embedding", "runtime", config.Runtime.Release, runtime.GOOS+"-"+runtime.GOARCH)
}

func RuntimeBinaryPath(root string, config v2config.EmbeddingConfig) (string, error) {
	config = v2config.EffectiveEmbeddingConfig(config)
	if config.Runtime.BinaryPath != "" {
		path := config.Runtime.BinaryPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("%w: %s", ErrRuntimeNotInstalled, path)
			}
			return "", err
		}
		return path, nil
	}
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	rootDir := RuntimeDir(root, config)
	var found string
	err := filepath.WalkDir(rootDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(entry.Name(), name) {
			found = path
			return io.EOF
		}
		return nil
	})
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("%w: llama.cpp %s for %s/%s", ErrRuntimeNotInstalled, config.Runtime.Release, runtime.GOOS, runtime.GOARCH)
	}
	return found, nil
}

func ModelInstalled(root string, model v2config.EmbeddingModel) bool {
	info, err := os.Stat(ModelPath(root, model))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func Install(ctx context.Context, root string, config v2config.EmbeddingConfig, modelID string, client *http.Client) error {
	config = v2config.EffectiveEmbeddingConfig(config)
	var model v2config.EmbeddingModel
	found := false
	for _, candidate := range config.Models {
		if candidate.ID == modelID {
			model = candidate
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("embedding model %q is not configured", modelID)
	}
	if client == nil {
		client = http.DefaultClient
	}
	if config.Runtime.BinaryPath == "" {
		if _, err := RuntimeBinaryPath(root, config); errors.Is(err, ErrRuntimeNotInstalled) {
			if err := installRuntime(ctx, root, config, client); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	if ModelInstalled(root, model) {
		return nil
	}
	destination := ModelPath(root, model)
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	return downloadVerified(ctx, client, model.DownloadURL, destination, model.SHA256)
}

func installRuntime(ctx context.Context, root string, config v2config.EmbeddingConfig, client *http.Client) error {
	if config.Runtime.Release != v2config.DefaultLlamaCppRelease {
		return fmt.Errorf("automatic llama.cpp download is unavailable for release %q; set embedding.runtime.binary_path to a local llama-server binary", config.Runtime.Release)
	}
	asset, ok := llamaCppB10621Assets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return fmt.Errorf("automatic llama.cpp download is unavailable for %s/%s; set embedding.runtime.binary_path", runtime.GOOS, runtime.GOARCH)
	}
	parent := filepath.Dir(RuntimeDir(root, config))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	archive, err := os.CreateTemp(parent, ".llama-runtime-*")
	if err != nil {
		return err
	}
	archivePath := archive.Name()
	archive.Close()
	defer os.Remove(archivePath)
	url := "https://github.com/ggml-org/llama.cpp/releases/download/" + config.Runtime.Release + "/" + asset.FileName
	if err := downloadVerified(ctx, client, url, archivePath, asset.SHA256); err != nil {
		return fmt.Errorf("download llama.cpp runtime: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".llama-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if strings.HasSuffix(asset.FileName, ".zip") {
		err = extractZip(archivePath, stage)
	} else {
		err = extractTarGz(archivePath, stage)
	}
	if err != nil {
		return fmt.Errorf("extract llama.cpp runtime: %w", err)
	}
	target := RuntimeDir(root, config)
	_ = os.RemoveAll(target)
	if err := os.Rename(stage, target); err != nil {
		return fmt.Errorf("install llama.cpp runtime: %w", err)
	}
	if _, err := RuntimeBinaryPath(root, config); err != nil {
		return err
	}
	return nil
}

func downloadVerified(ctx context.Context, client *http.Client, url, destination, expectedSHA256 string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return fmt.Errorf("download returned HTTP %s", response.Status)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".download-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(tempPath)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, maxEmbeddingDownloadBytes+1))
	if err != nil {
		return err
	}
	if written > maxEmbeddingDownloadBytes {
		return errors.New("download exceeds 1 GiB limit")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if expectedSHA256 != "" && actual != strings.ToLower(expectedSHA256) {
		return fmt.Errorf("SHA-256 mismatch: got %s, want %s", actual, expectedSHA256)
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return err
	}
	ok = true
	return nil
}

func secureArchivePath(root, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	path := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return path, nil
}

func extractZip(path, target string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		destination, err := secureArchivePath(target, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode()&0o777)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, io.LimitReader(src, maxEmbeddingDownloadBytes))
		closeErr := errors.Join(src.Close(), dst.Close())
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractTarGz(path, target string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		destination, err := secureArchivePath(target, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			dst, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(dst, io.LimitReader(tr, maxEmbeddingDownloadBytes))
			closeErr := dst.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			continue
		}
	}
	return nil
}
