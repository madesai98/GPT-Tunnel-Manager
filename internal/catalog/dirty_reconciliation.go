package catalog

import (
	"context"
	"errors"
	"fmt"
)

// ClearDirtyIfUnchanged clears exactly the dirty marker observed by a caller.
// If the same partition was marked dirty again after that observation, the
// newer marker survives so index refresh cannot accidentally erase concurrent
// downstream contract drift.
func (c *Catalog) ClearDirtyIfUnchanged(ctx context.Context, expected DirtyPartition) (bool, error) {
	if c == nil || c.db == nil {
		return false, errors.New("catalog is closed")
	}
	if expected.PartitionKey == "" {
		return false, errors.New("partition key is required")
	}
	result, err := c.db.ExecContext(ctx, `
		DELETE FROM dirty_partitions
		WHERE partition_key = ?
		  AND reason = ?
		  AND observed_fingerprint = ?
		  AND marked_at_unix_ms = ?
	`, expected.PartitionKey, expected.Reason, expected.ObservedFingerprint, expected.MarkedAt.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("clear unchanged dirty partition %q: %w", expected.PartitionKey, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("clear unchanged dirty partition rows affected: %w", err)
	}
	return rows == 1, nil
}

// SupersedeStaging retires one staging generation without touching an active
// generation. Index refresh uses this when the authoritative downstream source
// set changed while an enrichment build for the same routing-state hash was in
// progress; immutable enrichment work from the old source set must never be
// reused as though it described the changed tools.
func (c *Catalog) SupersedeStaging(ctx context.Context, generationID string) error {
	if c == nil || c.db == nil {
		return errors.New("catalog is closed")
	}
	if generationID == "" {
		return errors.New("generation id is required")
	}
	result, err := c.db.ExecContext(ctx, `
		UPDATE generations
		SET status = 'superseded'
		WHERE generation_id = ? AND status = 'staging'
	`, generationID)
	if err != nil {
		return fmt.Errorf("supersede staging generation %s: %w", generationID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("supersede staging generation rows affected: %w", err)
	}
	if rows != 1 {
		generation, loadErr := c.Generation(ctx, generationID)
		if loadErr != nil {
			return loadErr
		}
		if generation.Status != GenerationStaging {
			return ErrGenerationNotStaging
		}
		return fmt.Errorf("supersede staging generation %s affected no rows", generationID)
	}
	return nil
}
