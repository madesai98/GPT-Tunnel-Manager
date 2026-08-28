package v2config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFreshStoreCreatesStrictV2DefaultsAndStablePort(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	allocations := 0
	store.AllocatePort = func() (int, error) {
		allocations++
		return 43127, nil
	}

	manager, servers, err := store.LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if manager.SchemaVersion != SchemaVersion || servers.SchemaVersion != SchemaVersion {
		t.Fatalf("schema versions = manager %d servers %d", manager.SchemaVersion, servers.SchemaVersion)
	}
	if manager.LocalManager.Port != 43127 {
		t.Fatalf("manager port = %d, want 43127", manager.LocalManager.Port)
	}
	if !manager.LocalManager.AccessProtectionEnabled {
		t.Fatal("local Manager access protection should default enabled")
	}
	if manager.Embedding.CredentialRef == manager.ManagerTunnel.RuntimeCredentialRef {
		t.Fatal("embedding credential must be separate from Manager Tunnel runtime credential")
	}
	if allocations != 1 {
		t.Fatalf("port allocations = %d, want 1", allocations)
	}

	store.AllocatePort = func() (int, error) {
		t.Fatal("persisted Manager port should not be reallocated")
		return 0, nil
	}
	reloaded, _, err := store.LoadOrCreate()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.LocalManager.Port != manager.LocalManager.Port {
		t.Fatalf("reloaded port = %d, want %d", reloaded.LocalManager.Port, manager.LocalManager.Port)
	}
}

func TestV1ConfigIsRejectedRatherThanConverted(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	v1 := `{"schema_version":1,"manager_tunnel":{"tunnel_id":""},"general":{},"managed_defaults":{},"logging":{},"tunnel_client":{},"appearance":{}}`
	if err := os.WriteFile(filepath.Join(root, "config", "manager.json"), []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "servers.json"), []byte(`{"schema_version":1,"servers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := NewStore(root).LoadOrCreate()
	if err == nil {
		t.Fatal("expected v1 configuration to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown field") && !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("unexpected strict-v2 error: %v", err)
	}
}

func TestCutoverOpaqueLegacyMovesBytesWithoutParsingAndPreservesTools(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"config", "data", "tools"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configBytes := []byte("this is deliberately not JSON\x00legacy")
	dataBytes := []byte{0x00, 0xff, 0x7f, 0x01}
	toolBytes := []byte("keep-manager-tunnel-tooling")
	if err := os.WriteFile(filepath.Join(root, "config", "manager.json"), configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "runtime.bin"), dataBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tools", "tunnel-client"), toolBytes, 0o700); err != nil {
		t.Fatal(err)
	}

	store := NewStore(root)
	store.Now = func() time.Time { return time.Date(2026, 8, 28, 20, 0, 0, 0, time.UTC) }
	legacyRoot, err := store.CutoverOpaqueLegacy()
	if err != nil {
		t.Fatalf("CutoverOpaqueLegacy: %v", err)
	}
	if filepath.Base(legacyRoot) != "legacy-v1-20260828T200000Z" {
		t.Fatalf("legacy root = %q", legacyRoot)
	}
	assertFileBytes(t, filepath.Join(legacyRoot, "config", "manager.json"), configBytes)
	assertFileBytes(t, filepath.Join(legacyRoot, "data", "runtime.bin"), dataBytes)
	assertFileBytes(t, filepath.Join(root, "tools", "tunnel-client"), toolBytes)
	if _, err := os.Stat(filepath.Join(root, "config")); !os.IsNotExist(err) {
		t.Fatalf("old config should be moved aside, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Fatalf("old data should be moved aside, stat err = %v", err)
	}

	store.AllocatePort = func() (int, error) { return 43128, nil }
	manager, servers, err := store.LoadOrCreate()
	if err != nil {
		t.Fatalf("fresh v2 initialization after cutover: %v", err)
	}
	if manager.SchemaVersion != 2 || servers.SchemaVersion != 2 {
		t.Fatalf("fresh schema versions = %d/%d", manager.SchemaVersion, servers.SchemaVersion)
	}
}

func TestStrictV2UnknownFieldsAreRejected(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	store.AllocatePort = func() (int, error) { return 43129, nil }
	if _, _, err := store.LoadOrCreate(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(store.ManagerPath())
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `"schema_version": 2,`, `"schema_version": 2, "legacy_plugin_field": true,`, 1))
	if err := os.WriteFile(store.ManagerPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadOrCreate(); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown-field rejection, got %v", err)
	}
}

func TestServerJSONHasNoV1TopologyFields(t *testing.T) {
	entry := ServerEntry{
		ID:   "srv_0123456789abcdef0123456789abcdef",
		Name: "example",
		Mode: ModeManaged,
		Transport: TransportConfig{
			Type:  TransportStdio,
			Stdio: &StdioTransport{Executable: "example-mcp"},
		},
	}
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"chatgpt_plugin_name", `"tunnel"`, `"enabled"`} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("v2 server JSON contains forbidden v1 topology field %q: %s", forbidden, body)
		}
	}
}

func TestCredentialBearingRemoteHTTPRequiresHTTPSOrExplicitOverride(t *testing.T) {
	entry := testExternalHTTPServer("http://example.com/mcp")
	entry.Transport.ExternalHTTP.Auth = HTTPAuthConfig{
		Mode: HTTPAuthStatic,
		Static: &StaticAuthConfig{
			HeaderName: "Authorization",
			Scheme:     "Bearer",
			SecretRef:  "secret://servers/example/api-key",
		},
	}
	if err := ValidateServer(entry); err == nil || !strings.Contains(err.Error(), "requires https") {
		t.Fatalf("expected plaintext credential rejection, got %v", err)
	}

	entry.Transport.ExternalHTTP.AllowInsecureCredentialTransport = true
	if err := ValidateServer(entry); err != nil {
		t.Fatalf("explicit insecure override should validate: %v", err)
	}

	entry.Transport.ExternalHTTP.URL = "http://127.0.0.1:8765/mcp"
	entry.Transport.ExternalHTTP.AllowInsecureCredentialTransport = false
	if err := ValidateServer(entry); err != nil {
		t.Fatalf("loopback plaintext credentials should validate: %v", err)
	}
}

func TestStaticAuthRejectsTransportControlledHeaders(t *testing.T) {
	entry := testExternalHTTPServer("https://example.com/mcp")
	entry.Transport.ExternalHTTP.Auth = HTTPAuthConfig{
		Mode: HTTPAuthStatic,
		Static: &StaticAuthConfig{
			HeaderName: "Host",
			SecretRef:  "secret://servers/example/host",
		},
	}
	if err := ValidateServer(entry); err == nil || !strings.Contains(err.Error(), "transport-controlled") {
		t.Fatalf("expected transport-controlled header rejection, got %v", err)
	}
}

func testExternalHTTPServer(rawURL string) ServerEntry {
	return ServerEntry{
		ID:   "srv_0123456789abcdef0123456789abcdef",
		Name: "external",
		Mode: ModeManaged,
		Transport: TransportConfig{
			Type: TransportExternalHTTP,
			ExternalHTTP: &ExternalHTTPTransport{
				URL:  rawURL,
				Auth: HTTPAuthConfig{Mode: HTTPAuthNone},
			},
		},
	}
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s contents changed during opaque cutover: got %q want %q", path, got, want)
	}
}
