package app

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/logging"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

var phase11ServerIDPattern = regexp.MustCompile(`^srv_[a-f0-9]{32}$`)

func TestPhase11GeneratedServerIDs(t *testing.T) {
	first, err := NewServerID()
	if err != nil {
		t.Fatalf("generate first server id: %v", err)
	}
	second, err := NewServerID()
	if err != nil {
		t.Fatalf("generate second server id: %v", err)
	}
	if !phase11ServerIDPattern.MatchString(first) || !phase11ServerIDPattern.MatchString(second) {
		t.Fatalf("generated ids do not match v2 shape: %q %q", first, second)
	}
	if first == second {
		t.Fatalf("generated server ids unexpectedly collided: %s", first)
	}
}

func TestPhase11NativeFacadeManagesAllTransportShapesWithoutSecretRefs(t *testing.T) {
	setHeadlessInternalSecrets(t)
	application, err := NewV2App(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("create v2 app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	stdio, err := NewServerEntry("Stdio Test", v2config.ModeDisabled, v2config.TransportStdio)
	if err != nil {
		t.Fatal(err)
	}
	stdio.Transport.Stdio.Executable = "stdio-test"
	if err := application.SaveServer(t.Context(), stdio); err != nil {
		t.Fatalf("save stdio server: %v", err)
	}

	managed, err := NewServerEntry("Managed HTTP Test", v2config.ModeDisabled, v2config.TransportManagedHTTP)
	if err != nil {
		t.Fatal(err)
	}
	managed.Transport.ManagedHTTP.URL = "http://127.0.0.1:49001/mcp"
	managed.Transport.ManagedHTTP.Launch.Executable = "managed-http-test"
	if err := ConfigureOAuthAuth(&managed, []string{"tools.read", "tools.read", "tools.call"}); err != nil {
		t.Fatalf("configure managed OAuth: %v", err)
	}
	if err := application.SaveServer(t.Context(), managed); err != nil {
		t.Fatalf("save managed HTTP server: %v", err)
	}

	external, err := NewServerEntry("External HTTP Test", v2config.ModeDisabled, v2config.TransportExternalHTTP)
	if err != nil {
		t.Fatal(err)
	}
	external.Transport.ExternalHTTP.URL = "https://example.invalid/mcp"
	staticRef := StaticAuthSecretRef(external.ID)
	setHeadlessSecret(t, staticRef, "static-test-secret")
	if err := application.ConfigureStaticAuth(t.Context(), &external, "Authorization", "Bearer", nil); err != nil {
		t.Fatalf("configure static auth from native secret store: %v", err)
	}
	envRef := EnvironmentSecretRef(external.ID, "SERVICE_TOKEN")
	setHeadlessSecret(t, envRef, "environment-test-secret")
	if err := application.ConfigureEnvironmentSecret(t.Context(), &external, "SERVICE_TOKEN", nil); err != nil {
		t.Fatalf("configure secret environment from native secret store: %v", err)
	}
	if err := application.SaveServer(t.Context(), external); err != nil {
		t.Fatalf("save external HTTP server: %v", err)
	}

	entries := application.Entries()
	if len(entries) != 3 {
		t.Fatalf("saved entries = %d, want 3", len(entries))
	}
	for _, entry := range entries {
		if !phase11ServerIDPattern.MatchString(entry.ID) {
			t.Fatalf("saved invalid server id %q", entry.ID)
		}
	}
	if !application.StaticAuthCredentialConfigured(t.Context(), external.ID) {
		t.Fatalf("static credential should be discoverable without exposing its value")
	}
	names := EnvironmentSecretNames(external)
	if len(names) != 1 || names[0] != "SERVICE_TOKEN" {
		t.Fatalf("secret environment names = %v", names)
	}
	if strings.Contains(strings.Join(names, " "), "secret://") {
		t.Fatalf("native environment projection exposed a secret reference: %v", names)
	}
}

func TestPhase11ManagerConfigurationPersistsThroughFacade(t *testing.T) {
	setHeadlessInternalSecrets(t)
	application, err := NewV2App(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("create v2 app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	cfg := application.ManagerConfig()
	cfg.LocalManager.Port = 43123
	dimensions := 768
	cfg.Embedding.Dimensions = &dimensions
	cfg.Index.QueryEmbeddingCacheEntries = 512
	cfg.ManagedDefaults.IdleTimeoutSeconds = 42
	cfg.Logging.CaptureLevel = "debug"
	cfg.Logging.DisplayLevel = "all"
	cfg.Logging.MemoryLimitMB = 10
	cfg.Logging.WriteToDisk = false
	cfg.Logging.DiskMinimumLevel = "warn"
	cfg.Logging.MaximumFileSizeMB = 7
	cfg.Logging.KeepFiles = 3
	cfg.TunnelClient.AutoUpdate = false
	cfg.TunnelClient.Channel = "prerelease"
	cfg.TunnelClient.UpdateCheckIntervalHours = 12
	cfg.Appearance.Theme = "dark"
	if err := application.SaveManager(t.Context(), cfg); err != nil {
		t.Fatalf("save advanced Manager config: %v", err)
	}

	got := application.ManagerConfig()
	if got.LocalManager.Port != 43123 || got.Embedding.Dimensions == nil || *got.Embedding.Dimensions != 768 {
		t.Fatalf("manager local/embedding config not persisted: %+v", got)
	}
	if got.Index.QueryEmbeddingCacheEntries != 512 || got.ManagedDefaults.IdleTimeoutSeconds != 42 {
		t.Fatalf("index/lifecycle config not persisted: %+v", got)
	}
	if got.Logging.CaptureLevel != "debug" || got.Logging.MemoryLimitMB != 10 || got.Logging.DiskMinimumLevel != "warn" || got.Logging.MaximumFileSizeMB != 7 || got.Logging.KeepFiles != 3 {
		t.Fatalf("logging config not persisted: %+v", got.Logging)
	}
	if got.TunnelClient.AutoUpdate || got.TunnelClient.Channel != "prerelease" || got.TunnelClient.UpdateCheckIntervalHours != 12 {
		t.Fatalf("tunnel-client config not persisted: %+v", got.TunnelClient)
	}
	if got.Appearance.Theme != "dark" {
		t.Fatalf("appearance config not persisted: %+v", got.Appearance)
	}
}

func TestPhase11LogsRemainRedactedAndExportable(t *testing.T) {
	setHeadlessInternalSecrets(t)
	root := t.TempDir()
	application, err := NewV2App(context.Background(), root)
	if err != nil {
		t.Fatalf("create v2 app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	secret := []byte("phase11-super-secret")
	application.product.log.Redactor().Register(secret)
	application.product.log.Log(logging.Info, "Manager", "Phase11", "credential="+string(secret), map[string]any{"token": string(secret)})
	logs := application.Logs()
	if len(logs) == 0 {
		t.Fatalf("expected at least one log event")
	}
	last := logs[len(logs)-1]
	if strings.Contains(last.Message, string(secret)) {
		t.Fatalf("secret leaked into native log message: %q", last.Message)
	}
	if got := last.Fields["token"]; got != "[REDACTED]" {
		t.Fatalf("token field = %#v, want redacted", got)
	}

	textPath, err := application.ExportLogs("text")
	if err != nil {
		t.Fatalf("export text logs: %v", err)
	}
	jsonPath, err := application.ExportLogs("jsonl")
	if err != nil {
		t.Fatalf("export JSONL logs: %v", err)
	}
	for _, path := range []string{textPath, jsonPath} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read exported log %s: %v", path, err)
		}
		if strings.Contains(string(body), string(secret)) {
			t.Fatalf("secret leaked into exported log %s", path)
		}
	}
}
