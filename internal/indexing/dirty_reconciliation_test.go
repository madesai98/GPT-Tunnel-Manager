package indexing

import (
	"context"
	"strings"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDirtyStatusBlocksCommitAndIdentifiesPartition(t *testing.T) {
	ctx := context.Background()
	c := openDirtyIndexCatalog(t)
	if err := c.MarkDirty(ctx, "server:srv_1", "live downstream tool contract changed", ""); err != nil {
		t.Fatal(err)
	}
	s := &Service{catalog: c}
	status, err := s.applyDirtyStatus(ctx, Status{Ready: true, NextAction: "All required enrichment is complete. Call index_commit."})
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready {
		t.Fatal("dirty routing state was reported ready")
	}
	if len(status.PromotionBlockers) != 1 || !strings.Contains(status.PromotionBlockers[0], "server:srv_1") || !strings.Contains(status.PromotionBlockers[0], "live downstream tool contract changed") {
		t.Fatalf("promotion blockers = %#v", status.PromotionBlockers)
	}
	if !strings.Contains(status.NextAction, "index_refresh") {
		t.Fatalf("next action = %q, want index_refresh guidance", status.NextAction)
	}
}

func TestStagingSourceComparisonPreservesAcceptedWorkOnlyForUnchangedContracts(t *testing.T) {
	ctx := context.Background()
	c := openDirtyIndexCatalog(t)
	entry := dirtyTestServer()
	if _, err := c.CreateStaging(ctx, catalog.GenerationSpec{ID: "gen_existing", RoutingStateHash: "sha256:routing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceServer(ctx, "gen_existing", entry); err != nil {
		t.Fatal(err)
	}
	original := dirtyTestTool("original")
	fingerprint, _, err := toolcontract.FingerprintTool(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireSourceTool(ctx, "gen_existing", entry.ID, original.Name, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceTool(ctx, "gen_existing", entry.ID, original, true); err != nil {
		t.Fatal(err)
	}

	s := &Service{catalog: c, servers: v2config.ServersConfig{Servers: []v2config.ServerEntry{entry}}}
	same := map[string]downstream.ToolSnapshot{entry.ID: dirtySnapshot(t, original)}
	changed, err := s.stagingSourcesChanged(ctx, "gen_existing", same)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("identical authoritative source set incorrectly invalidated staging enrichment")
	}

	revised := dirtyTestTool("changed")
	different := map[string]downstream.ToolSnapshot{entry.ID: dirtySnapshot(t, revised)}
	changed, err = s.stagingSourcesChanged(ctx, "gen_existing", different)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("changed authoritative source contract did not invalidate immutable staging enrichment")
	}
}

func TestReconcileObservedDirtyPartitionClearsOnlyMarkerThatWasRebuilt(t *testing.T) {
	ctx := context.Background()
	c := openDirtyIndexCatalog(t)
	entry := dirtyTestServer()
	s := &Service{catalog: c, servers: v2config.ServersConfig{Servers: []v2config.ServerEntry{entry}}}
	snapshots := map[string]downstream.ToolSnapshot{entry.ID: dirtySnapshot(t, dirtyTestTool("current"))}

	if err := c.MarkDirty(ctx, "server:"+entry.ID, "first drift", "sha256:first"); err != nil {
		t.Fatal(err)
	}
	observed, err := c.DirtyPartitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileObservedDirtyPartitions(ctx, observed, snapshots); err != nil {
		t.Fatal(err)
	}
	remaining, err := c.DirtyPartitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("rebuilt dirty partition remained: %#v", remaining)
	}

	if err := c.MarkDirty(ctx, "server:"+entry.ID, "old drift", "sha256:old"); err != nil {
		t.Fatal(err)
	}
	observed, err = c.DirtyPartitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.MarkDirty(ctx, "server:"+entry.ID, "new drift during refresh", "sha256:new"); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileObservedDirtyPartitions(ctx, observed, snapshots); err != nil {
		t.Fatal(err)
	}
	remaining, err = c.DirtyPartitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Reason != "new drift during refresh" {
		t.Fatalf("concurrent dirty marker was lost: %#v", remaining)
	}
}

func openDirtyIndexCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func dirtyTestServer() v2config.ServerEntry {
	return v2config.ServerEntry{
		ID:   "srv_10000000000000000000000000000001",
		Name: "Dirty Test",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type:  v2config.TransportStdio,
			Stdio: &v2config.StdioTransport{Executable: "test"},
		},
	}
}

func dirtyTestTool(description string) *mcp.Tool {
	return &mcp.Tool{Name: "echo", Description: description, InputSchema: map[string]any{"type": "object"}}
}

func dirtySnapshot(t *testing.T, tools ...*mcp.Tool) downstream.ToolSnapshot {
	t.Helper()
	fingerprint, canonical, err := toolcontract.FingerprintTools(tools)
	if err != nil {
		t.Fatal(err)
	}
	return downstream.ToolSnapshot{Tools: canonical, Fingerprint: fingerprint}
}
