package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func TestSchemaV1CatalogMigratesWithoutQuarantine(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	catalog, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.DB().ExecContext(ctx, `DROP TABLE source_servers`); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.DB().ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	catalog, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	if catalog.QuarantinePath() != "" {
		t.Fatalf("supported schema migration unexpectedly quarantined %q", catalog.QuarantinePath())
	}
	var version int
	if err := catalog.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", version, SchemaVersion)
	}
	var count int
	if err := catalog.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='source_servers'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("source_servers table count = %d", count)
	}
}

func TestSourceServerContractExcludesOperationalAndCredentialMaterial(t *testing.T) {
	ctx := context.Background()
	catalog := openTestCatalog(t)
	if _, err := catalog.CreateStaging(ctx, GenerationSpec{ID: "gen", RoutingStateHash: "sha256:routing"}); err != nil {
		t.Fatal(err)
	}
	entry := v2config.ServerEntry{
		ID:   "srv_1",
		Name: "test server",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type: v2config.TransportExternalHTTP,
			ExternalHTTP: &v2config.ExternalHTTPTransport{
				URL: "https://private.example.test/mcp",
				Auth: v2config.HTTPAuthConfig{
					Mode: v2config.HTTPAuthStatic,
					Static: &v2config.StaticAuthConfig{
						HeaderName: "Authorization",
						Scheme:     "Bearer",
						SecretRef:  "secret://servers/srv_1/api-key",
					},
				},
			},
		},
		Environment: v2config.EnvironmentConfig{
			Values:     map[string]string{"PRIVATE_VALUE": "must-not-enter-catalog"},
			SecretRefs: map[string]string{"API_KEY": "secret://servers/srv_1/env"},
		},
	}
	stored, err := catalog.PutSourceServer(ctx, "gen", entry)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(stored.Contract)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{"private.example.test", "must-not-enter-catalog", "secret://", "Authorization"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("source server contract leaked forbidden material %q: %s", forbidden, text)
		}
	}
	loaded, err := catalog.SourceServer(ctx, "gen", "srv_1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded != stored {
		t.Fatalf("loaded source server = %#v, want %#v", loaded, stored)
	}
}
