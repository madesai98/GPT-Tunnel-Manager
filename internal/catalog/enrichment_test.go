package catalog

import (
	"context"
	"errors"
	"testing"
)

func TestEnrichmentBatchFirstValidWinsAndAmbiguityCanResolveAfterPromotion(t *testing.T) {
	ctx := context.Background()
	c := openTestCatalog(t)
	if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "gen", RoutingStateHash: "sha256:routing"}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutEnrichmentBatches(ctx, []EnrichmentBatchSpec{
		{ID: "required", GenerationID: "gen", Kind: BatchToolEnrichment, BatchKey: "tool:000000", Required: true, RequestJSON: []byte(`{"items":[1]}`)},
		{ID: "review", GenerationID: "gen", Kind: BatchAmbiguityReview, BatchKey: "review:000000", Required: false, RequestJSON: []byte(`{"question":"choose"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	first, err := c.AcceptEnrichmentBatch(ctx, "required", []byte(`{"answer":1}`))
	if err != nil || first.Idempotent {
		t.Fatalf("first submission = %#v, %v", first, err)
	}
	repeat, err := c.AcceptEnrichmentBatch(ctx, "required", []byte("{\n  \"answer\": 1\n}"))
	if err != nil || !repeat.Idempotent {
		t.Fatalf("identical repeat = %#v, %v", repeat, err)
	}
	if _, err := c.AcceptEnrichmentBatch(ctx, "required", []byte(`{"answer":2}`)); !errors.Is(err, ErrEnrichmentBatchConflict) {
		t.Fatalf("conflicting repeat = %v", err)
	}
	if err := c.Promote(ctx, "gen", "sha256:routing"); err != nil {
		t.Fatal(err)
	}
	postPromotion, err := c.AcceptEnrichmentBatch(ctx, "required", []byte(`{"answer":1}`))
	if err != nil || !postPromotion.Idempotent {
		t.Fatalf("post-promotion identical repeat = %#v, %v", postPromotion, err)
	}
	review, err := c.AcceptEnrichmentBatch(ctx, "review", []byte(`{"resolution":"neutral"}`))
	if err != nil || review.Idempotent {
		t.Fatalf("active-generation ambiguity resolution = %#v, %v", review, err)
	}
}

func TestEnrichmentBatchesAreImmutableAndStagingOnly(t *testing.T) {
	ctx := context.Background()
	c := openTestCatalog(t)
	if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "gen", RoutingStateHash: "sha256:routing"}); err != nil {
		t.Fatal(err)
	}
	spec := EnrichmentBatchSpec{ID: "batch", GenerationID: "gen", Kind: BatchToolEnrichment, BatchKey: "tool:0", Required: true, RequestJSON: []byte(`{"a":1}`)}
	if err := c.PutEnrichmentBatches(ctx, []EnrichmentBatchSpec{spec}); err != nil {
		t.Fatal(err)
	}
	if err := c.PutEnrichmentBatches(ctx, []EnrichmentBatchSpec{spec}); err != nil {
		t.Fatalf("identical immutable work item: %v", err)
	}
	changed := spec
	changed.RequestJSON = []byte(`{"a":2}`)
	if err := c.PutEnrichmentBatches(ctx, []EnrichmentBatchSpec{changed}); !errors.Is(err, ErrEnrichmentBatchConflict) {
		t.Fatalf("changed work item = %v", err)
	}
	if _, err := c.AcceptEnrichmentBatch(ctx, "batch", []byte(`{"done":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Promote(ctx, "gen", "sha256:routing"); err != nil {
		t.Fatal(err)
	}
	if err := c.PutEnrichmentBatches(ctx, []EnrichmentBatchSpec{{ID: "late", GenerationID: "gen", Kind: BatchAmbiguityReview, BatchKey: "late", RequestJSON: []byte(`{}`)}}); !errors.Is(err, ErrGenerationNotStaging) {
		t.Fatalf("late batch creation = %v", err)
	}
}
