package enrichment

import (
	"context"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
)

func TestPrepareRepairsAcceptedToolBatchAfterRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cat, err := catalog.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatal("expected materialization failure after the batch was accepted")
	}
	stored, err := cat.EnrichmentBatch(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AcceptedFingerprint == "" {
		t.Fatal("valid first submission was not persisted before materialization failure")
	}
	if err := cat.Close(); err != nil {
		t.Fatal(err)
	}

	cat, err = catalog.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	coordinator, err = NewCoordinator(cat, &testProvider{identity: identity}, Options{NeighborhoodSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.PrepareToolEnrichment(ctx, "gen"); err != nil {
		t.Fatalf("restart did not repair accepted tool enrichment: %v", err)
	}
	if _, err := coordinator.PrepareCapabilityReconciliation(ctx, "gen"); err != nil {
		t.Fatalf("repaired tool enrichment did not satisfy reconciliation prerequisites: %v", err)
	}
}
