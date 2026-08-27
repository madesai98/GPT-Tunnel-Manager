package selfupdate

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want int
	}{
		{"1.0.1", "v1.0.2", -1},
		{"v1.0.2", "1.0.2", 0},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.10.0", "v1.2.99", 1},
		{"v1.0.0-beta.2", "v1.0.0-beta.10", -1},
		{"v1.0.0-beta", "v1.0.0", -1},
	}
	for _, tt := range tests {
		a, err := parseVersion(tt.a)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.a, err)
		}
		b, err := parseVersion(tt.b)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.b, err)
		}
		got := compareVersions(a, b)
		if got < 0 {
			got = -1
		} else if got > 0 {
			got = 1
		}
		if got != tt.want {
			t.Fatalf("compare %q vs %q = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestExtractZipProtectsUserDataAndRenamesExecutable(t *testing.T) {
	temp := t.TempDir()
	archivePath := filepath.Join(temp, "release.zip")
	root := "GPT-Tunnel-Manager-v9.9.9-" + runtime.GOOS + "-" + runtime.GOARCH
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	executable := "tunnel-manager"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	entries := map[string]string{
		root + "/" + executable:       "new binary",
		root + "/README.md":           "readme",
		root + "/config/manager.json": "must not stage",
		root + "/data/state.json":     "must not stage",
		root + "/tools/client":        "must not stage",
	}
	for name, content := range entries {
		entry, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	stage := filepath.Join(temp, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	assetName := root + ".zip"
	targetName := "renamed-manager"
	if runtime.GOOS == "windows" {
		targetName += ".exe"
	}
	if err := extractReleaseArchive(archivePath, assetName, stage, targetName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, targetName)); err != nil {
		t.Fatalf("renamed executable not staged: %v", err)
	}
	for _, protected := range []string{"config", "data", "tools"} {
		if _, err := os.Stat(filepath.Join(stage, protected)); !os.IsNotExist(err) {
			t.Fatalf("protected path %s was staged", protected)
		}
	}
}

func TestReleaseRelativePathRejectsTraversal(t *testing.T) {
	if _, _, err := releaseRelativePath("root/../../evil", "root", "manager"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}
