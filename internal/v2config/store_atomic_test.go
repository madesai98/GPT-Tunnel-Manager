package v2config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshInitializationFailureLeavesNoPartialConfig(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	store.AllocatePort = func() (int, error) { return 43130, nil }
	store.freshInitBeforeCommit = func(string) error { return errors.New("injected pre-commit failure") }

	if _, _, err := store.LoadOrCreate(); err == nil || !strings.Contains(err.Error(), "injected pre-commit failure") {
		t.Fatalf("expected injected initialization failure, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed fresh initialization must not expose partial config, stat err = %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".config-v2-init-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("fresh initialization staging directories leaked: %v", matches)
	}
}

func TestIncompleteV2ConfigFailsClosedWithoutCreatingPeer(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := os.MkdirAll(filepath.Dir(store.ManagerPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(store.ManagerPath(), DefaultManagerConfig(43131)); err != nil {
		t.Fatal(err)
	}

	_, _, err := store.LoadOrCreate()
	if err == nil || !strings.Contains(err.Error(), "incomplete v2 configuration") {
		t.Fatalf("expected incomplete-config failure, got %v", err)
	}
	if _, err := os.Stat(store.ServersPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadOrCreate must not synthesize the missing peer file, stat err = %v", err)
	}
}

func TestEmptyConfigDirectoryIsNotSilentlyReinterpreted(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)
	store.AllocatePort = func() (int, error) {
		t.Fatal("empty existing config directory must fail before allocating a v2 port")
		return 0, nil
	}
	_, _, err := store.LoadOrCreate()
	if err == nil || !strings.Contains(err.Error(), "incomplete v2 configuration") {
		t.Fatalf("expected empty config directory to fail closed, got %v", err)
	}
}
