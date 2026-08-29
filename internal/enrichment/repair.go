package enrichment

import (
	"context"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
)

func (c *Coordinator) repairAcceptedBatches(ctx context.Context, generationID string, kind catalog.EnrichmentBatchKind) error {
	batches, err := c.catalog.AcceptedEnrichmentBatches(ctx, generationID, kind)
	if err != nil {
		return err
	}
	for _, batch := range batches {
		if len(batch.AcceptedResponseJSON) == 0 {
			return fmt.Errorf("accepted enrichment batch %s has no persisted response", batch.ID)
		}
		if _, err := c.SubmitBatch(ctx, batch.ID, batch.AcceptedResponseJSON); err != nil {
			return fmt.Errorf("repair accepted %s batch %s: %w", kind, batch.ID, err)
		}
	}
	return nil
}
