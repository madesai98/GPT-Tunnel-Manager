package retrieval

import (
	"context"
	"errors"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCatalogStoreRequirementsBlockPromotionAndLoadDeterministically(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	tool := &mcp.Tool{Name: "weather", Description: "weather forecast", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}}
	prepareGeneration(t, cat, "gen", "sha256:routing", tool)
	store, err := NewCatalogStore(cat)
	if err != nil {
		t.Fatal(err)
	}
	projections, err := ProjectTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	dimensions := 3
	identity := embedding.Identity{Provider: "test", BaseURL: "https://embedding.invalid/v1", Model: "model-a", Dimensions: &dimensions, Protocol: embedding.IdentityVersion}
	member := "srv_1/weather"
	if err := store.RequireEmbedding(ctx, "gen", RoleSourceDescriptionVector, member, identity, projections.SourceDescription); err != nil {
		t.Fatal(err)
	}
	if err := store.RequireEmbedding(ctx, "gen", RoleInputSchemaVector, member, identity, projections.InputSchema); err != nil {
		t.Fatal(err)
	}
	if err := store.RequireLexical(ctx, "gen", member, projections.Lexical); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StoreEmbedding(ctx, "gen", RoleSourceDescriptionVector, member, identity, projections.SourceDescription, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StoreLexical(ctx, "gen", member, projections.Lexical); err != nil {
		t.Fatal(err)
	}
	if err := cat.Promote(ctx, "gen", "sha256:routing"); !errors.Is(err, catalog.ErrDependencyMismatch) {
		t.Fatalf("promotion with missing required input-schema embedding = %v", err)
	}
	if _, err := store.StoreEmbedding(ctx, "gen", RoleInputSchemaVector, member, identity, projections.InputSchema, []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := cat.Promote(ctx, "gen", "sha256:routing"); err != nil {
		t.Fatal(err)
	}

	vectorIndex, err := store.LoadVectorIndex(ctx, "gen", RoleSourceDescriptionVector)
	if err != nil {
		t.Fatal(err)
	}
	vectorResults, err := vectorIndex.Search([]float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectorResults) != 1 || vectorResults[0].Key != member {
		t.Fatalf("vector results = %#v", vectorResults)
	}
	lexicalIndex, err := store.LoadLexicalIndex(ctx, "gen")
	if err != nil {
		t.Fatal(err)
	}
	lexicalResults, err := lexicalIndex.Search("forecast city", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(lexicalResults) != 1 || lexicalResults[0].Key != member {
		t.Fatalf("lexical results = %#v", lexicalResults)
	}
}

func TestCatalogStoreEmbeddingReuseIsProviderBound(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	tool := &mcp.Tool{Name: "echo", Description: "echo text", InputSchema: map[string]any{"type": "object"}}
	prepareGeneration(t, cat, "gen1", "sha256:routing", tool)
	store, err := NewCatalogStore(cat)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := ProjectTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	dimensions := 2
	identityA := embedding.Identity{Provider: "test", BaseURL: "https://embedding.invalid/v1", Model: "model-a", Dimensions: &dimensions, Protocol: embedding.IdentityVersion}
	member := "srv_1/echo"
	if err := store.RequireEmbedding(ctx, "gen1", RoleSourceDescriptionVector, member, identityA, projection.SourceDescription); err != nil {
		t.Fatal(err)
	}
	firstKey, err := store.StoreEmbedding(ctx, "gen1", RoleSourceDescriptionVector, member, identityA, projection.SourceDescription, []float32{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	vector, reusedKey, ok, err := store.ReuseEmbedding(ctx, RoleSourceDescriptionVector, member, identityA, projection.SourceDescription)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || reusedKey != firstKey || len(vector) != 2 {
		t.Fatalf("reuse = ok:%v key:%s vector:%v", ok, reusedKey, vector)
	}
	identityB := identityA
	identityB.Model = "model-b"
	if _, _, ok, err := store.ReuseEmbedding(ctx, RoleSourceDescriptionVector, member, identityB, projection.SourceDescription); err != nil || ok {
		t.Fatalf("different model reuse = ok:%v err:%v", ok, err)
	}
}

func prepareGeneration(t *testing.T, cat *catalog.Catalog, generationID, routingHash string, tool *mcp.Tool) {
	t.Helper()
	ctx := context.Background()
	if _, err := cat.CreateStaging(ctx, catalog.GenerationSpec{ID: generationID, RoutingStateHash: routingHash}); err != nil {
		t.Fatal(err)
	}
	fingerprint, _, err := toolcontract.FingerprintTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.RequireSourceTool(ctx, generationID, "srv_1", tool.Name, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.PutSourceTool(ctx, generationID, "srv_1", tool, true); err != nil {
		t.Fatal(err)
	}
}
