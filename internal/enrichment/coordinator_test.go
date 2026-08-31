package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testProvider struct {
	identity embedding.Identity
	calls    int
	failNext bool
}

func (p *testProvider) Identity() embedding.Identity { return p.identity }

func (p *testProvider) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	if p.failNext {
		p.failNext = false
		return nil, errors.New("test embedding failure")
	}
	p.calls += len(inputs)
	result := make([][]float32, len(inputs))
	for i := range inputs {
		result[i] = []float32{1, 1}
	}
	return result, nil
}

func TestCoordinatorRequiredPipelineIsIdempotentAndAmbiguityIsNonBlocking(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	identity := testIdentity()
	prepareSourceGeneration(t, cat, "gen", map[string]sourceVector{
		"a": {description: "alpha search", vector: []float32{1, 0}},
		"b": {description: "beta search", vector: []float32{0.8, 0.2}},
	}, identity)
	provider := &testProvider{identity: identity}
	coordinator, err := NewCoordinator(cat, provider, Options{NeighborhoodSize: 1, ToolBatchSize: 8})
	if err != nil {
		t.Fatal(err)
	}

	counts, err := coordinator.PrepareToolEnrichment(ctx, "gen")
	if err != nil {
		t.Fatal(err)
	}
	if counts.PendingRequired != 1 {
		t.Fatalf("pending required after prepare = %#v", counts)
	}
	first, ok, err := coordinator.GetBatch(ctx, "gen", catalog.BatchToolEnrichment)
	if err != nil || !ok {
		t.Fatalf("get tool batch = %#v ok=%v err=%v", first, ok, err)
	}
	second, ok, err := coordinator.GetBatch(ctx, "gen", catalog.BatchToolEnrichment)
	if err != nil || !ok || second.ID != first.ID {
		t.Fatalf("unclaimed batch was not repeatable: first=%s second=%s ok=%v err=%v", first.ID, second.ID, ok, err)
	}
	if err := cat.Promote(ctx, "gen", "sha256:routing"); !errors.Is(err, catalog.ErrDependencyMismatch) {
		t.Fatalf("promotion before enrichment = %v", err)
	}

	toolResponse := responseForToolBatch(t, first)
	submitted, err := coordinator.SubmitBatch(ctx, first.ID, toolResponse)
	if err != nil || submitted.Idempotent {
		t.Fatalf("first tool submission = %#v err=%v", submitted, err)
	}
	repeated, err := coordinator.SubmitBatch(ctx, first.ID, toolResponse)
	if err != nil || !repeated.Idempotent {
		t.Fatalf("identical tool submission = %#v err=%v", repeated, err)
	}
	var conflicting ToolBatchResponse
	if err := json.Unmarshal(toolResponse, &conflicting); err != nil {
		t.Fatal(err)
	}
	conflicting.Items[0].Guidance.Purpose = "different accepted answer"
	conflictingBody, _ := json.Marshal(conflicting)
	if _, err := coordinator.SubmitBatch(ctx, first.ID, conflictingBody); !errors.Is(err, catalog.ErrEnrichmentBatchConflict) {
		t.Fatalf("conflicting tool submission = %v", err)
	}

	counts, err = coordinator.PrepareCapabilityReconciliation(ctx, "gen")
	if err != nil {
		t.Fatal(err)
	}
	if counts.PendingRequired != 1 {
		t.Fatalf("pending required before reconciliation = %#v", counts)
	}
	capBatch, ok, err := coordinator.GetBatch(ctx, "gen", catalog.BatchCapabilityReconciliation)
	if err != nil || !ok {
		t.Fatalf("get capability batch = %#v ok=%v err=%v", capBatch, ok, err)
	}
	var capRequest CapabilityBatchRequest
	if err := json.Unmarshal(capBatch.RequestJSON, &capRequest); err != nil {
		t.Fatal(err)
	}
	members := make([]string, 0, len(capRequest.Items))
	for _, item := range capRequest.Items {
		members = append(members, item.Tool.MemberKey)
	}
	capResponse := CapabilityBatchResponse{
		Hierarchy: CapabilityHierarchy{Protocol: CapabilityProtocolVersion, Capabilities: []CapabilityNode{{ID: "tools", Name: "Tools", ToolMembers: members}}},
		Ambiguities: []AmbiguityProposal{{
			Summary: "Both tools overlap for search-like requests.",
			CompetingTools: []string{"srv/a", "srv/b"},
			ProsCons: map[string]ToolProsCons{
				"srv/a": {Pros: []string{"Source contract describes alpha search."}},
				"srv/b": {Cons: []string{"Source contract is narrower for this condition."}},
			},
			ConditionalUseCases: []string{"Prefer the tool whose source contract matches the requested search domain."},
			SuggestedOptions: []string{"Keep neutral", "Save an explicit conditional preference"},
		}},
	}
	capBody, _ := json.Marshal(capResponse)
	if _, err := coordinator.SubmitBatch(ctx, capBatch.ID, capBody); err != nil {
		t.Fatal(err)
	}
	counts, err = cat.EnrichmentBatchCounts(ctx, "gen")
	if err != nil {
		t.Fatal(err)
	}
	if counts.PendingRequired != 0 || counts.PendingOptional != 1 {
		t.Fatalf("counts after reconciliation = %#v", counts)
	}
	if err := cat.Promote(ctx, "gen", "sha256:routing"); err != nil {
		t.Fatalf("open ambiguity review blocked promotion: %v", err)
	}

	review, ok, err := coordinator.GetBatch(ctx, "gen", catalog.BatchAmbiguityReview)
	if err != nil || !ok {
		t.Fatalf("get active-generation ambiguity review = %#v ok=%v err=%v", review, ok, err)
	}
	neutral, _ := json.Marshal(AmbiguityReviewResponse{Resolution: AmbiguityNeutral})
	resolved, err := coordinator.SubmitBatch(ctx, review.ID, neutral)
	if err != nil || resolved.Idempotent {
		t.Fatalf("resolve ambiguity = %#v err=%v", resolved, err)
	}
	repeatReview, err := coordinator.SubmitBatch(ctx, review.ID, neutral)
	if err != nil || !repeatReview.Idempotent {
		t.Fatalf("repeat ambiguity resolution = %#v err=%v", repeatReview, err)
	}
}

func TestAcceptedToolBatchCanRepairMaterializationAfterEmbeddingFailure(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	identity := testIdentity()
	prepareSourceGeneration(t, cat, "gen", map[string]sourceVector{"a": {description: "alpha", vector: []float32{1, 0}}}, identity)
	provider := &testProvider{identity: identity, failNext: true}
	coordinator, err := NewCoordinator(cat, provider, Options{NeighborhoodSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.PrepareToolEnrichment(ctx, "gen"); err != nil {
		t.Fatal(err)
	}
	batch, ok, err := coordinator.GetBatch(ctx, "gen", catalog.BatchToolEnrichment)
	if err != nil || !ok {
		t.Fatalf("get batch = %#v ok=%v err=%v", batch, ok, err)
	}
	response := responseForToolBatch(t, batch)
	if _, err := coordinator.SubmitBatch(ctx, batch.ID, response); err == nil {
		t.Fatal("expected first materialization to fail")
	}
	stored, err := cat.EnrichmentBatch(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AcceptedFingerprint == "" {
		t.Fatal("valid submission was not accepted before downstream materialization failure")
	}
	repaired, err := coordinator.SubmitBatch(ctx, batch.ID, response)
	if err != nil || !repaired.Idempotent {
		t.Fatalf("idempotent repair = %#v err=%v", repaired, err)
	}
	if _, err := coordinator.PrepareCapabilityReconciliation(ctx, "gen"); err != nil {
		t.Fatalf("repaired batch did not satisfy enrichment prerequisites: %v", err)
	}
}

func TestSemanticNeighborhoodMembershipChangeReusesUnchangedToolEnrichment(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	identity := testIdentity()
	provider := &testProvider{identity: identity}
	coordinator, err := NewCoordinator(cat, provider, Options{NeighborhoodSize: 1, ToolBatchSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	prepareSourceGeneration(t, cat, "gen1", map[string]sourceVector{
		"a": {description: "alpha", vector: []float32{1, 0}},
		"b": {description: "beta", vector: []float32{0.8, 0.2}},
	}, identity)
	if _, err := coordinator.PrepareToolEnrichment(ctx, "gen1"); err != nil {
		t.Fatal(err)
	}
	batch, ok, err := coordinator.GetBatch(ctx, "gen1", catalog.BatchToolEnrichment)
	if err != nil || !ok {
		t.Fatalf("get gen1 batch = %#v ok=%v err=%v", batch, ok, err)
	}
	if _, err := coordinator.SubmitBatch(ctx, batch.ID, responseForToolBatch(t, batch)); err != nil {
		t.Fatal(err)
	}
	oldContext, err := cat.NeighborhoodContext(ctx, "gen1", "srv/a")
	if err != nil {
		t.Fatal(err)
	}

	prepareSourceGeneration(t, cat, "gen2", map[string]sourceVector{
		"a": {description: "alpha", vector: []float32{1, 0}},
		"b": {description: "beta", vector: []float32{0.8, 0.2}},
		"c": {description: "alpha specialist", vector: []float32{0.99, 0.01}},
	}, identity)
	if _, err := coordinator.PrepareToolEnrichment(ctx, "gen2"); err != nil {
		t.Fatal(err)
	}
	newContext, err := cat.NeighborhoodContext(ctx, "gen2", "srv/a")
	if err != nil {
		t.Fatal(err)
	}
	if oldContext == newContext {
		t.Fatal("new tool entering top-K neighborhood did not change context fingerprint")
	}
	pending, ok, err := coordinator.GetBatch(ctx, "gen2", catalog.BatchToolEnrichment)
	if err != nil || !ok {
		t.Fatalf("new tool did not produce required enrichment batch: batch=%#v ok=%v err=%v", pending, ok, err)
	}
	var request ToolBatchRequest
	if err := json.Unmarshal(pending.RequestJSON, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Items) != 1 || request.Items[0].Tool.MemberKey != "srv/c" {
		t.Fatalf("pending enrichment after neighborhood churn = %#v, want only new tool srv/c", request.Items)
	}
}

func TestAmbiguityValidationDoesNotRequireInventedSymmetry(t *testing.T) {
	known := map[string]struct{}{"srv/a": {}, "srv/b": {}}
	proposal := AmbiguityProposal{
		Summary: "The tools overlap but their source contracts document different strengths.",
		CompetingTools: []string{"srv/a", "srv/b"},
		ProsCons: map[string]ToolProsCons{
			"srv/a": {Pros: []string{"Documented strength"}},
			"srv/b": {Cons: []string{"Documented limitation"}},
		},
		ConditionalUseCases: []string{"Choose based on the documented condition."},
		SuggestedOptions: []string{"Neutral"},
	}
	if err := validateAmbiguity(proposal, known); err != nil {
		t.Fatalf("source-grounded one-sided comparative detail was rejected: %v", err)
	}
}

type sourceVector struct {
	description string
	vector      []float32
}

func testIdentity() embedding.Identity {
	dimensions := 2
	return embedding.Identity{Provider: "test", BaseURL: "https://embedding.invalid/v1", Model: "phase6", Dimensions: &dimensions, Protocol: embedding.IdentityVersion}
}

func prepareSourceGeneration(t *testing.T, cat *catalog.Catalog, generationID string, sources map[string]sourceVector, identity embedding.Identity) {
	t.Helper()
	ctx := context.Background()
	if _, err := cat.CreateStaging(ctx, catalog.GenerationSpec{ID: generationID, RoutingStateHash: "sha256:routing"}); err != nil {
		t.Fatal(err)
	}
	store, err := retrieval.NewCatalogStore(cat)
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range sources {
		tool := &mcp.Tool{Name: name, Description: source.description, InputSchema: map[string]any{"type": "object"}}
		fingerprint, _, err := toolcontract.FingerprintTool(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := cat.RequireSourceTool(ctx, generationID, "srv", name, fingerprint); err != nil {
			t.Fatal(err)
		}
		if _, err := cat.PutSourceTool(ctx, generationID, "srv", tool, true); err != nil {
			t.Fatal(err)
		}
		projections, err := retrieval.ProjectTool(tool)
		if err != nil {
			t.Fatal(err)
		}
		member := MemberKey("srv", name)
		if err := store.RequireEmbedding(ctx, generationID, retrieval.RoleSourceDescriptionVector, member, identity, projections.SourceDescription); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StoreEmbedding(ctx, generationID, retrieval.RoleSourceDescriptionVector, member, identity, projections.SourceDescription, source.vector); err != nil {
			t.Fatal(err)
		}
	}
}

func responseForToolBatch(t *testing.T, batch catalog.EnrichmentBatch) []byte {
	t.Helper()
	var request ToolBatchRequest
	if err := json.Unmarshal(batch.RequestJSON, &request); err != nil {
		t.Fatal(err)
	}
	response := ToolBatchResponse{Items: make([]ToolEnrichmentResult, 0, len(request.Items))}
	for _, item := range request.Items {
		response.Items = append(response.Items, ToolEnrichmentResult{MemberKey: item.Tool.MemberKey, Guidance: ToolGuidance{Purpose: "Route requests to " + item.Tool.MemberKey}})
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
