package catalog

import (
	"context"
	"errors"
	"fmt"
)

// AcceptedEnrichmentBatches returns persisted accepted work in deterministic
// order. Coordinators use the stored accepted response to repair required
// materialization after a crash or downstream failure without asking an agent
// to regenerate an answer that has already won the batch race.
func (c *Catalog) AcceptedEnrichmentBatches(ctx context.Context, generationID string, kind EnrichmentBatchKind) ([]EnrichmentBatch, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("catalog is closed")
	}
	if generationID == "" || !validBatchKind(kind) {
		return nil, errors.New("generation id and valid enrichment batch kind are required")
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT batch_id, generation_id, kind, batch_key, required,
		       request_fingerprint, request_json, accepted_fingerprint,
		       accepted_response_json, created_at_unix_ms, accepted_at_unix_ms
		FROM enrichment_batches
		WHERE generation_id = ? AND kind = ? AND accepted_fingerprint IS NOT NULL
		ORDER BY batch_key, batch_id
	`, generationID, string(kind))
	if err != nil {
		return nil, fmt.Errorf("list accepted enrichment batches: %w", err)
	}
	defer rows.Close()
	var batches []EnrichmentBatch
	for rows.Next() {
		batch, err := scanEnrichmentBatch(rows)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accepted enrichment batches: %w", err)
	}
	return batches, nil
}
