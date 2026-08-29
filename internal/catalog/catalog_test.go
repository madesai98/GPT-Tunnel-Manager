package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFreshCatalogSchemaRoutingStateAndStagingPersistAcrossReopen(t *testing.T) {
	ctx := context.Background(); root := t.TempDir()
	catalog, err := Open(ctx, root); if err != nil { t.Fatal(err) }
	if _, err := os.Stat(Path(root)); err != nil { t.Fatal(err) }
	var version int; if err := catalog.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil { t.Fatal(err) }
	if version != SchemaVersion { t.Fatalf("schema version = %d, want %d", version, SchemaVersion) }
	backend, err := routingstate.NewSQLiteBackend(catalog.DB()); if err != nil { t.Fatal(err) }
	tracker, err := routingstate.NewTracker(backend); if err != nil { t.Fatal(err) }
	state, changed, err := tracker.Reconcile(ctx, "sha256:routing-a"); if err != nil { t.Fatal(err) }
	if !changed || state.RoutingRevision != 1 { t.Fatalf("routing reconcile = %#v changed=%v", state, changed) }
	state, err = tracker.AdvancePreferenceRevision(ctx); if err != nil { t.Fatal(err) }
	if state.PreferenceRevision != 1 { t.Fatalf("preference revision = %d", state.PreferenceRevision) }
	if _, err := catalog.CreateStaging(ctx, GenerationSpec{ID: "gen_resume", RoutingStateHash: "sha256:routing-a"}); err != nil { t.Fatal(err) }
	if err := catalog.Close(); err != nil { t.Fatal(err) }
	catalog, err = Open(ctx, root); if err != nil { t.Fatal(err) }; defer catalog.Close()
	generation, err := catalog.Generation(ctx, "gen_resume"); if err != nil { t.Fatal(err) }
	if generation.Status != GenerationStaging || generation.RoutingStateHash != "sha256:routing-a" { t.Fatalf("reopened generation = %#v", generation) }
	backend, err = routingstate.NewSQLiteBackend(catalog.DB()); if err != nil { t.Fatal(err) }
	state, err = backend.Load(ctx); if err != nil { t.Fatal(err) }
	if state.RoutingRevision != 1 || state.RoutingStateHash != "sha256:routing-a" || state.PreferenceRevision != 1 { t.Fatalf("reopened routing state = %#v", state) }
}

func TestPromotionGuards(t *testing.T) {
	t.Run("routing hash", func(t *testing.T) {
		c := openTestCatalog(t); addCompleteSource(t, c, "gen", "sha256:a", tool("echo", "a"))
		if err := c.Promote(context.Background(), "gen", "sha256:b"); !errors.Is(err, ErrRoutingStateChanged) { t.Fatalf("Promote error = %v", err) }
	})
	t.Run("dirty partition", func(t *testing.T) {
		c := openTestCatalog(t); addCompleteSource(t, c, "gen", "sha256:a", tool("echo", "a"))
		if err := c.MarkDirty(context.Background(), "server:srv_1", "tool contract changed", "sha256:new"); err != nil { t.Fatal(err) }
		if err := c.Promote(context.Background(), "gen", "sha256:a"); !errors.Is(err, ErrDirtyPartitions) { t.Fatalf("Promote error = %v", err) }
	})
	t.Run("incomplete source", func(t *testing.T) {
		c := openTestCatalog(t); ctx := context.Background()
		if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "gen", RoutingStateHash: "sha256:a"}); err != nil { t.Fatal(err) }
		fp, _, err := toolcontract.FingerprintTool(tool("echo", "a")); if err != nil { t.Fatal(err) }
		if err := c.RequireSourceTool(ctx, "gen", "srv_1", "echo", fp); err != nil { t.Fatal(err) }
		if err := c.Promote(ctx, "gen", "sha256:a"); !errors.Is(err, ErrGenerationIncomplete) { t.Fatalf("Promote error = %v", err) }
	})
	t.Run("source fingerprint mismatch", func(t *testing.T) {
		c := openTestCatalog(t); ctx := context.Background()
		if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "gen", RoutingStateHash: "sha256:a"}); err != nil { t.Fatal(err) }
		expected, _, err := toolcontract.FingerprintTool(tool("echo", "old")); if err != nil { t.Fatal(err) }
		if err := c.RequireSourceTool(ctx, "gen", "srv_1", "echo", expected); err != nil { t.Fatal(err) }
		if _, err := c.PutSourceTool(ctx, "gen", "srv_1", tool("echo", "new"), true); err != nil { t.Fatal(err) }
		if err := c.Promote(ctx, "gen", "sha256:a"); !errors.Is(err, ErrSourceFingerprintMismatch) { t.Fatalf("Promote error = %v", err) }
	})
	t.Run("dependency mismatch", func(t *testing.T) {
		c := openTestCatalog(t); addCompleteSource(t, c, "gen", "sha256:a", tool("echo", "a")); ctx := context.Background()
		if err := c.RequireDependency(ctx, "gen", "embedding-model", "sha256:model-a"); err != nil { t.Fatal(err) }
		if err := c.SatisfyDependency(ctx, "gen", "embedding-model", "sha256:model-b", true); err != nil { t.Fatal(err) }
		if err := c.Promote(ctx, "gen", "sha256:a"); !errors.Is(err, ErrDependencyMismatch) { t.Fatalf("Promote error = %v", err) }
	})
}

func TestPromotionIsAtomicAndOldActiveSurvivesFailedReplacement(t *testing.T) {
	ctx := context.Background(); c := openTestCatalog(t)
	addCompleteSource(t, c, "gen1", "sha256:routing", tool("first", "v1")); if err := c.Promote(ctx, "gen1", "sha256:routing"); err != nil { t.Fatal(err) }
	active, err := c.ActiveGeneration(ctx); if err != nil { t.Fatal(err) }; if active.ID != "gen1" { t.Fatalf("active = %s", active.ID) }
	addCompleteSource(t, c, "gen2", "sha256:routing", tool("second", "v2"))
	if err := c.MarkDirty(ctx, "server:srv_1", "test", ""); err != nil { t.Fatal(err) }
	if err := c.Promote(ctx, "gen2", "sha256:routing"); !errors.Is(err, ErrDirtyPartitions) { t.Fatalf("Promote error = %v", err) }
	active, err = c.ActiveGeneration(ctx); if err != nil { t.Fatal(err) }; if active.ID != "gen1" { t.Fatalf("failed promotion changed active generation to %s", active.ID) }
	if err := c.ClearDirty(ctx, "server:srv_1"); err != nil { t.Fatal(err) }
	if err := c.Promote(ctx, "gen2", "sha256:routing"); err != nil { t.Fatal(err) }
	active, err = c.ActiveGeneration(ctx); if err != nil { t.Fatal(err) }; if active.ID != "gen2" { t.Fatalf("active = %s", active.ID) }
	old, err := c.Generation(ctx, "gen1"); if err != nil { t.Fatal(err) }; if old.Status != GenerationSuperseded { t.Fatalf("old generation status = %s", old.Status) }
}

func TestContentAddressedArtifactReuseAndDependencyValidation(t *testing.T) {
	ctx := context.Background(); c := openTestCatalog(t)
	spec := ArtifactSpec{Kind: "lexical", Payload: []byte("stable payload"), Dependencies: []ArtifactDependency{{Key: "source", Fingerprint: "sha256:source"}}, ContextFingerprint: "sha256:context"}
	first, err := c.PutArtifact(ctx, spec); if err != nil { t.Fatal(err) }; second, err := c.PutArtifact(ctx, spec); if err != nil { t.Fatal(err) }
	if first.Key != second.Key { t.Fatalf("artifact keys differ: %s != %s", first.Key, second.Key) }
	var count int; if err := c.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts WHERE artifact_key = ?`, first.Key).Scan(&count); err != nil { t.Fatal(err) }; if count != 1 { t.Fatalf("artifact row count = %d", count) }
	reused, err := c.ReusableArtifact(ctx, first.Key, spec.Dependencies, spec.ContextFingerprint); if err != nil { t.Fatal(err) }; if string(reused.Payload) != string(spec.Payload) { t.Fatalf("payload = %q", reused.Payload) }
	_, err = c.ReusableArtifact(ctx, first.Key, []ArtifactDependency{{Key: "source", Fingerprint: "sha256:changed"}}, spec.ContextFingerprint); if !errors.Is(err, ErrDependencyMismatch) { t.Fatalf("dependency mismatch error = %v", err) }
}

func TestCorruptCatalogIsQuarantinedAndNotTrusted(t *testing.T) {
	ctx := context.Background(); root := t.TempDir(); c, err := Open(ctx, root); if err != nil { t.Fatal(err) }
	backend, err := routingstate.NewSQLiteBackend(c.DB()); if err != nil { t.Fatal(err) }
	if err := backend.Store(ctx, routingstate.Snapshot{RoutingRevision: 99, RoutingStateHash: "sha256:must-not-survive", PreferenceRevision: 7}); err != nil { t.Fatal(err) }
	if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "gen_untrusted", RoutingStateHash: "sha256:must-not-survive"}); err != nil { t.Fatal(err) }
	path := c.Path(); if err := c.Close(); err != nil { t.Fatal(err) }; if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil { t.Fatal(err) }
	c, err = Open(ctx, root); if err != nil { t.Fatal(err) }; defer c.Close()
	if c.QuarantinePath() == "" { t.Fatal("corrupt catalog was not quarantined") }; if _, err := os.Stat(c.QuarantinePath()); err != nil { t.Fatal(err) }
	backend, err = routingstate.NewSQLiteBackend(c.DB()); if err != nil { t.Fatal(err) }; state, err := backend.Load(ctx); if err != nil { t.Fatal(err) }
	if state != (routingstate.Snapshot{}) { t.Fatalf("fresh routing state trusted corrupt values: %#v", state) }
	if _, err := c.Generation(ctx, "gen_untrusted"); !errors.Is(err, ErrGenerationNotFound) { t.Fatalf("corrupt generation survived quarantine: %v", err) }
	if err := c.CheckIntegrity(ctx); err != nil { t.Fatal(err) }
}

func TestPreferenceRevisionDoesNotStaleActiveGeneration(t *testing.T) {
	ctx := context.Background(); c := openTestCatalog(t); addCompleteSource(t, c, "gen", "sha256:routing", tool("echo", "v1")); if err := c.Promote(ctx, "gen", "sha256:routing"); err != nil { t.Fatal(err) }
	backend, err := routingstate.NewSQLiteBackend(c.DB()); if err != nil { t.Fatal(err) }; tracker, err := routingstate.NewTracker(backend); if err != nil { t.Fatal(err) }
	if _, _, err := tracker.Reconcile(ctx, "sha256:routing"); err != nil { t.Fatal(err) }; if _, err := tracker.AdvancePreferenceRevision(ctx); err != nil { t.Fatal(err) }
	active, current, err := c.ActiveCurrent(ctx, "sha256:routing"); if err != nil { t.Fatal(err) }; if !current || active.ID != "gen" { t.Fatalf("preference change staled active generation: active=%#v current=%v", active, current) }
}

func TestStagingReconciliationPreservesMatchingBuildAndSupersedesStaleBuild(t *testing.T) {
	ctx := context.Background(); c := openTestCatalog(t); if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "same", RoutingStateHash: "sha256:a"}); err != nil { t.Fatal(err) }
	if changed, err := c.ReconcileStaging(ctx, "sha256:a"); err != nil || changed != 0 { t.Fatalf("matching reconciliation = %d, %v", changed, err) }
	if changed, err := c.ReconcileStaging(ctx, "sha256:b"); err != nil || changed != 1 { t.Fatalf("stale reconciliation = %d, %v", changed, err) }
	generation, err := c.Generation(ctx, "same"); if err != nil { t.Fatal(err) }; if generation.Status != GenerationSuperseded { t.Fatalf("status = %s", generation.Status) }
}

func openTestCatalog(t *testing.T) *Catalog { t.Helper(); c, err := Open(context.Background(), t.TempDir()); if err != nil { t.Fatal(err) }; t.Cleanup(func() { _ = c.Close() }); return c }

func addCompleteSource(t *testing.T, c *Catalog, generationID, routingHash string, source *mcp.Tool) {
	t.Helper(); ctx := context.Background(); if _, err := c.CreateStaging(ctx, GenerationSpec{ID: generationID, RoutingStateHash: routingHash}); err != nil { t.Fatal(err) }
	fingerprint, _, err := toolcontract.FingerprintTool(source); if err != nil { t.Fatal(err) }; if err := c.RequireSourceTool(ctx, generationID, "srv_1", source.Name, fingerprint); err != nil { t.Fatal(err) }; if _, err := c.PutSourceTool(ctx, generationID, "srv_1", source, true); err != nil { t.Fatal(err) }
}

func tool(name, description string) *mcp.Tool { return &mcp.Tool{Name: name, Description: description, InputSchema: map[string]any{"type": "object"}} }

func TestCatalogPathIsPortableRootLocal(t *testing.T) { root := t.TempDir(); want := filepath.Join(root, "data", DatabaseFileName); if got := Path(root); got != want { t.Fatalf("Path = %q, want %q", got, want) } }
