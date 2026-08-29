package discovery

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func buildDiscoveryFixture(t *testing.T) (*Service, *catalog.Catalog, *routingprefs.Store, *executionhandle.Manager, *semanticProvider) {
	t.Helper()
	ctx := context.Background()
	cat, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	provider := newSemanticProvider()
	if _, err := cat.CreateStaging(ctx, catalog.GenerationSpec{ID: "gen_phase7", RoutingStateHash: testRoutingHash}); err != nil {
		t.Fatal(err)
	}
	store, err := retrieval.NewCatalogStore(cat)
	if err != nil {
		t.Fatal(err)
	}
	servers := map[string]string{}
	for _, tool := range phase7Tools() {
		servers[tool.serverID] = tool.serverName
	}
	for serverID, serverName := range servers {
		if _, err := cat.PutSourceServer(ctx, "gen_phase7", v2config.ServerEntry{
			ID: serverID, Name: serverName, Mode: v2config.ModeAlwaysOn,
			Transport: v2config.TransportConfig{Type: v2config.TransportExternalHTTP},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range phase7Tools() {
		closed := false
		nondestructive := false
		tool := &mcp.Tool{
			Name: item.name, Title: item.title, Description: item.description,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &nondestructive, IdempotentHint: true, OpenWorldHint: &closed},
		}
		fingerprint, _, err := toolcontract.FingerprintTool(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := cat.RequireSourceTool(ctx, "gen_phase7", item.serverID, item.name, fingerprint); err != nil {
			t.Fatal(err)
		}
		if _, err := cat.PutSourceTool(ctx, "gen_phase7", item.serverID, tool, true); err != nil {
			t.Fatal(err)
		}
		projections, err := retrieval.ProjectTool(tool)
		if err != nil {
			t.Fatal(err)
		}
		member := enrichment.MemberKey(item.serverID, item.name)
		for _, roleProjection := range []struct {
			role       string
			projection retrieval.Projection
		}{
			{retrieval.RoleSourceDescriptionVector, projections.SourceDescription},
			{retrieval.RoleInputSchemaVector, projections.InputSchema},
		} {
			if err := store.RequireEmbedding(ctx, "gen_phase7", roleProjection.role, member, provider.Identity(), roleProjection.projection); err != nil {
				t.Fatal(err)
			}
			if _, err := store.StoreEmbedding(ctx, "gen_phase7", roleProjection.role, member, provider.Identity(), roleProjection.projection, basisVector(8, item.dimension)); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.RequireLexical(ctx, "gen_phase7", member, projections.Lexical); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StoreLexical(ctx, "gen_phase7", member, projections.Lexical); err != nil {
			t.Fatal(err)
		}
		guidanceBody, _ := json.Marshal(item.guidance)
		artifact, err := cat.PutArtifact(ctx, catalog.ArtifactSpec{Kind: enrichment.ToolEnrichmentArtifactKind, Payload: guidanceBody})
		if err != nil {
			t.Fatal(err)
		}
		if err := cat.AttachArtifact(ctx, "gen_phase7", enrichment.RoleToolEnrichment, member, artifact.Key, false, true); err != nil {
			t.Fatal(err)
		}
		enrichedProjection := retrieval.Projection{Version: "phase7-test-enriched/v1", Text: string(guidanceBody), Fingerprint: "sha256:phase7-enriched-" + member}
		if err := store.RequireEmbedding(ctx, "gen_phase7", enrichment.RoleEnrichedEmbedding, member, provider.Identity(), enrichedProjection); err != nil {
			t.Fatal(err)
		}
		enrichedVectors, err := provider.Embed(ctx, []string{enrichedProjection.Text})
		if err != nil || len(enrichedVectors) != 1 {
			t.Fatalf("embed fixture enrichment %s: vectors=%d err=%v", member, len(enrichedVectors), err)
		}
		if _, err := store.StoreEmbedding(ctx, "gen_phase7", enrichment.RoleEnrichedEmbedding, member, provider.Identity(), enrichedProjection, enrichedVectors[0]); err != nil {
			t.Fatal(err)
		}
	}
	hierarchy := enrichment.CapabilityHierarchy{Protocol: enrichment.CapabilityProtocolVersion, Capabilities: []enrichment.CapabilityNode{
		{ID: "repository", Name: "Repository", ToolMembers: []string{"repo/search_code", "repo/search_symbols", "repo/create_issue"}},
		{ID: "code", Name: "Code Search", ParentID: "repository", ToolMembers: []string{"repo/search_code", "repo/search_symbols"}},
		{ID: "issues", Name: "Issue Tracking", ParentID: "repository", ToolMembers: []string{"repo/create_issue"}},
		{ID: "calendar", Name: "Calendar", ToolMembers: []string{"calendar/create_event"}},
		{ID: "mail", Name: "Email", ToolMembers: []string{"mail/search_messages"}},
		{ID: "weather", Name: "Weather", ToolMembers: []string{"weather/forecast"}},
		{ID: "files", Name: "Files", ToolMembers: []string{"files/read_file"}},
	}}
	hierarchyBody, _ := json.Marshal(hierarchy)
	hierarchyArtifact, err := cat.PutArtifact(ctx, catalog.ArtifactSpec{Kind: enrichment.CapabilityHierarchyArtifactKind, Payload: hierarchyBody})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachArtifact(ctx, "gen_phase7", enrichment.RoleCapabilityHierarchy, "global", hierarchyArtifact.Key, false, true); err != nil {
		t.Fatal(err)
	}
	if err := cat.Promote(ctx, "gen_phase7", testRoutingHash); err != nil {
		t.Fatal(err)
	}
	prefs, err := routingprefs.NewStore(cat)
	if err != nil {
		t.Fatal(err)
	}
	handles, err := executionhandle.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := embedding.NewQueryCache(16)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(cat, provider, prefs, staticState{routingstate.Snapshot{RoutingStateHash: testRoutingHash}}, handles, Options{QueryCache: cache})
	if err != nil {
		t.Fatal(err)
	}
	return service, cat, prefs, handles, provider
}
