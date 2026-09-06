package catalog

import (
	"context"
	"errors"
	"fmt"
)

// ClearIndex removes every generated or cached routing-index artifact while
// preserving user configuration, routing profiles/preferences, and live
// continuation mappings. The next index refresh therefore starts from a truly
// empty semantic index and cannot reuse prior tool contracts, enrichments, or
// embeddings.
func (c *Catalog) ClearIndex(ctx context.Context) error {
	if c == nil || c.db == nil {
		return errors.New("catalog is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clear index transaction: %w", err)
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		return cause
	}

	// Delete generations first so generation-scoped rows disappear through
	// their foreign-key cascades. Content-addressed artifacts and persistent
	// tool contracts are intentionally deleted afterward so a rebuild cannot
	// silently reuse semantic work from the previous index.
	for _, statement := range []struct {
		label string
		sql   string
	}{
		{label: "generations", sql: "DELETE FROM generations"},
		{label: "artifacts", sql: "DELETE FROM artifacts"},
		{label: "tool contract cache", sql: "DELETE FROM tool_contract_cache"},
		{label: "dirty partitions", sql: "DELETE FROM dirty_partitions"},
	} {
		if _, err := tx.ExecContext(ctx, statement.sql); err != nil {
			return rollback(fmt.Errorf("clear index %s: %w", statement.label, err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clear index transaction: %w", err)
	}
	return nil
}
