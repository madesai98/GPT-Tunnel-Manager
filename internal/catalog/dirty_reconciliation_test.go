package catalog

import (
	"context"
	"testing"
)

func TestClearDirtyIfUnchangedClearsObservedMarker(t *testing.T) {
	ctx := context.Background()
	c := openTestCatalog(t)
	if err := c.MarkDirty(ctx, "server:srv_1", "live downstream tool contract changed", "sha256:first"); err != nil {
		t.Fatal(err)
	}
	partitions, err := c.DirtyPartitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 1 {
		t.Fatalf("dirty partition count = %d, want 1", len(partitions))
	}
	cleared, err := c.ClearDirtyIfUnchanged(ctx, partitions[0])
	if err != nil {
		t.Fatal(err)
	}
	if !cleared {
		t.Fatal("observed dirty marker was not cleared")
	}
	partitions, err = c.DirtyPartitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 0 {
		t.Fatalf("dirty partition count after clear = %d, want 0", len(partitions))
	}
}

func TestClearDirtyIfUnchangedPreservesReplacementMarker(t *testing.T) {
	ctx := context.Background()
	c := openTestCatalog(t)
	if err := c.MarkDirty(ctx, "server:srv_1", "first drift", "sha256:first"); err != nil {
		t.Fatal(err)
	}
	partitions, err := c.DirtyPartitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed := partitions[0]
	if err := c.MarkDirty(ctx, "server:srv_1", "newer drift", "sha256:second"); err != nil {
		t.Fatal(err)
	}
	cleared, err := c.ClearDirtyIfUnchanged(ctx, observed)
	if err != nil {
		t.Fatal(err)
	}
	if cleared {
		t.Fatal("compare-and-clear removed a replacement dirty marker")
	}
	partitions, err = c.DirtyPartitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 1 || partitions[0].Reason != "newer drift" || partitions[0].ObservedFingerprint != "sha256:second" {
		t.Fatalf("replacement dirty marker = %#v", partitions)
	}
}

func TestSupersedeStagingLeavesGenerationNonPromotable(t *testing.T) {
	ctx := context.Background()
	c := openTestCatalog(t)
	if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "gen_old", RoutingStateHash: "sha256:routing"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SupersedeStaging(ctx, "gen_old"); err != nil {
		t.Fatal(err)
	}
	generation, err := c.Generation(ctx, "gen_old")
	if err != nil {
		t.Fatal(err)
	}
	if generation.Status != GenerationSuperseded {
		t.Fatalf("generation status = %s, want %s", generation.Status, GenerationSuperseded)
	}
	if err := c.SupersedeStaging(ctx, "gen_old"); err != ErrGenerationNotStaging {
		t.Fatalf("second supersede error = %v, want %v", err, ErrGenerationNotStaging)
	}
}
