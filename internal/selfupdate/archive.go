package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

func extractReleaseArchive(archivePath, assetName, stageDir, targetExecutableName string) error {
	rootName := assetName
	switch {
	case strings.HasSuffix(rootName, ".tar.gz"):
		rootName = strings.TrimSuffix(rootName, ".tar.gz")
		return extractTarGzip(archivePath, rootName, stageDir, targetExecutableName)
	case strings.HasSuffix(rootName, ".zip"):
		rootName = strings.TrimSuffix(rootName, ".zip")
		return extractZip(archivePath, rootName, stageDir, targetExecutableName)
	default:
		return fmt.Errorf("unsupported release archive %s", assetName)
	}
}

func extractZip(archivePath, rootName, stageDir, targetExecutableName string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open update zip: %w", err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		rel, skip, err := releaseRelativePath(entry.Name, rootName, targetExecutableName)
		if err != nil {
			return err
		}
		if skip || rel == "" {
			continue
		}
		mode := entry.Mode()
		if mode&os.ModeType != 0 && !mode.IsDir() {
			return fmt.Errorf("update archive contains unsupported entry %s", entry.Name)
		}
		destination, err := stagePath(stageDir, rel)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		src, err := entry.Open()
		if err != nil {
			return err
		}
		perm := mode.Perm()
		if perm == 0 {
			perm = 0o600
		}
		if filepath.Base(rel) == targetExecutableName {
			perm |= 0o700
		}
		err = writeExtractedFile(destination, src, perm)
		_ = src.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGzip(archivePath, rootName, stageDir, targetExecutableName string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open update gzip: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read update tar: %w", err)
		}
		rel, skip, err := releaseRelativePath(header.Name, rootName, targetExecutableName)
		if err != nil {
			return err
		}
		if skip || rel == "" {
			continue
		}
		destination, err := stagePath(stageDir, rel)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			perm := os.FileMode(header.Mode).Perm()
			if perm == 0 {
				perm = 0o600
			}
			if filepath.Base(rel) == targetExecutableName {
				perm |= 0o700
			}
			if err := writeExtractedFile(destination, io.LimitReader(reader, header.Size), perm); err != nil {
				return err
			}
		default:
			return fmt.Errorf("update archive contains unsupported entry %s", header.Name)
		}
	}
	return nil
}

func releaseRelativePath(name, rootName, targetExecutableName string) (string, bool, error) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || clean == rootName {
		return "", true, nil
	}
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false, fmt.Errorf("update archive contains unsafe path %s", name)
	}
	prefix := rootName + "/"
	if !strings.HasPrefix(clean, prefix) {
		return "", false, fmt.Errorf("update archive entry %s is outside expected root %s", name, rootName)
	}
	rel := strings.TrimPrefix(clean, prefix)
	if rel == "" {
		return "", true, nil
	}
	parts := strings.Split(rel, "/")
	if _, protected := protectedTopLevel[parts[0]]; protected {
		return "", true, nil
	}
	expectedExecutable := "tunnel-manager"
	if runtime.GOOS == "windows" {
		expectedExecutable += ".exe"
	}
	if rel == expectedExecutable {
		rel = targetExecutableName
	}
	return filepath.FromSlash(rel), false, nil
}

func stagePath(stageDir, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe staged update path %s", rel)
	}
	destination := filepath.Join(stageDir, clean)
	relToStage, err := filepath.Rel(stageDir, destination)
	if err != nil || relToStage == ".." || strings.HasPrefix(relToStage, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe staged update path %s", rel)
	}
	return destination, nil
}

func writeExtractedFile(destination string, src io.Reader, perm os.FileMode) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, src)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(destination, perm)
}
