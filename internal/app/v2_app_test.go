package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func TestV2AppFreshNativeFacade(t *testing.T) {
	root := t.TempDir()
	application, err := NewV2App(t.Context(), root)
	if err != nil {
		t.Fatalf("create v2 app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	if got := application.ManagerConfig().SchemaVersion; got != v2config.SchemaVersion {
		t.Fatalf("manager schema version = %d, want %d", got, v2config.SchemaVersion)
	}
	if len(application.Entries()) != 0 {
		t.Fatalf("fresh v2 app unexpectedly has downstream servers")
	}
	if !application.ManagerSnapshot().AccessProtectionEnabled {
		t.Fatalf("local Manager access protection must default on")
	}
	if err := application.Start(t.Context()); err != nil {
		t.Fatalf("start v2 app: %v", err)
	}
	if snapshot := application.ManagerSnapshot(); !snapshot.Running || snapshot.MCPURL == "" {
		t.Fatalf("Manager snapshot after start = %+v", snapshot)
	}
	if err := application.SetLocalManagerProtection(t.Context(), false); err != nil {
		t.Fatalf("disable local Manager protection: %v", err)
	}
	if application.ManagerSnapshot().AccessProtectionEnabled {
		t.Fatalf("local Manager protection did not update")
	}

	entry := v2config.ServerEntry{
		ID: "native-test", Name: "Native Test", Mode: v2config.ModeDisabled,
		Transport: v2config.TransportConfig{Type: v2config.TransportStdio, Stdio: &v2config.StdioTransport{Executable: "native-test"}},
	}
	if err := application.SaveServer(t.Context(), entry); err != nil {
		t.Fatalf("save downstream server: %v", err)
	}
	if got := application.Entries(); len(got) != 1 || got[0].ID != entry.ID {
		t.Fatalf("entries after save = %+v", got)
	}
	if err := application.DeleteServer(t.Context(), entry.ID); err != nil {
		t.Fatalf("delete downstream server: %v", err)
	}
	if len(application.Entries()) != 0 {
		t.Fatalf("downstream server was not deleted")
	}

	prefs, err := application.RoutingPreferences(t.Context())
	if err != nil {
		t.Fatalf("load routing preferences: %v", err)
	}
	write, err := application.PutRoutingProfile(t.Context(), prefs.PreferenceRevision, routingprefs.Profile{ID: "focused", Name: "Focused"})
	if err != nil {
		t.Fatalf("create routing profile: %v", err)
	}
	if !write.Changed {
		t.Fatalf("profile write should change preference state")
	}
	prefs, err = application.RoutingPreferences(t.Context())
	if err != nil {
		t.Fatalf("reload routing preferences: %v", err)
	}
	if len(prefs.Profiles) != 1 || prefs.Profiles[0].ID != "focused" {
		t.Fatalf("profiles after create = %+v", prefs.Profiles)
	}
}

func TestV2AppOpaqueLegacyCutover(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacyBody := []byte(`{"schema_version":1,"do_not_parse":"legacy"}`)
	if err := os.WriteFile(filepath.Join(root, "config", "config.json"), legacyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "legacy.bin"), []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}

	application, err := NewV2App(context.Background(), root)
	if err != nil {
		t.Fatalf("create v2 app after opaque cutover: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	if _, err := os.Stat(filepath.Join(root, "config", "manager.json")); err != nil {
		t.Fatalf("fresh v2 manager config missing: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(root, "legacy-v1-*", "config", "config.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("opaque legacy config was not moved aside: matches=%v err=%v", matches, err)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(legacyBody) {
		t.Fatalf("legacy config content was modified during cutover")
	}
}
