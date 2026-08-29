package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ContinuationKind string

const (
	ContinuationTask     ContinuationKind = "task"
	ContinuationResource ContinuationKind = "resource"
)

var (
	ErrContinuationNotFound = errors.New("continuation_not_found")
	ErrContinuationExpired  = errors.New("continuation_expired")
	ErrContinuationConflict = errors.New("continuation_conflict")
)

type ContinuationMapping struct {
	ID        string           `json:"mapping_id"`
	Kind      ContinuationKind `json:"kind"`
	ServerID  string           `json:"server_id"`
	Payload   json.RawMessage  `json:"payload"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
}

func (c *Catalog) PutContinuation(ctx context.Context, mapping ContinuationMapping) (ContinuationMapping, bool, error) {
	if c == nil || c.db == nil {
		return ContinuationMapping{}, false, errors.New("catalog is closed")
	}
	mapping.ID = strings.TrimSpace(mapping.ID)
	mapping.ServerID = strings.TrimSpace(mapping.ServerID)
	if mapping.ID == "" || mapping.ServerID == "" {
		return ContinuationMapping{}, false, errors.New("mapping id and server id are required")
	}
	if mapping.Kind != ContinuationTask && mapping.Kind != ContinuationResource {
		return ContinuationMapping{}, false, fmt.Errorf("unsupported continuation kind %q", mapping.Kind)
	}
	payload, err := canonicalJSON(mapping.Payload)
	if err != nil {
		return ContinuationMapping{}, false, fmt.Errorf("canonicalize continuation payload: %w", err)
	}
	created := mapping.CreatedAt.UTC()
	if created.IsZero() {
		created = time.Now().UTC()
	}
	var expires any
	if mapping.ExpiresAt != nil {
		value := mapping.ExpiresAt.UTC()
		if !value.After(created) {
			return ContinuationMapping{}, false, errors.New("continuation expiry must be after creation")
		}
		expires = value.UnixMilli()
		mapping.ExpiresAt = &value
	}

	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ContinuationMapping{}, false, fmt.Errorf("begin continuation write: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO continuation_mappings(
			mapping_id, kind, server_id, payload, created_at_unix_ms, expires_at_unix_ms
		) VALUES (?, ?, ?, ?, ?, ?)
	`, mapping.ID, string(mapping.Kind), mapping.ServerID, payload, created.UnixMilli(), expires)
	if err != nil {
		return ContinuationMapping{}, false, fmt.Errorf("store continuation %s: %w", mapping.ID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return ContinuationMapping{}, false, fmt.Errorf("continuation rows affected: %w", err)
	}
	stored, err := continuationByID(ctx, tx, mapping.ID)
	if err != nil {
		return ContinuationMapping{}, false, err
	}
	if rows == 0 && (stored.Kind != mapping.Kind || stored.ServerID != mapping.ServerID || string(stored.Payload) != string(payload) || !sameContinuationExpiry(stored.ExpiresAt, mapping.ExpiresAt)) {
		return ContinuationMapping{}, false, fmt.Errorf("%w: mapping %s differs from existing mapping", ErrContinuationConflict, mapping.ID)
	}
	if err := tx.Commit(); err != nil {
		return ContinuationMapping{}, false, fmt.Errorf("commit continuation %s: %w", mapping.ID, err)
	}
	return stored, rows == 0, nil
}

func (c *Catalog) Continuation(ctx context.Context, mappingID string) (ContinuationMapping, error) {
	if c == nil || c.db == nil {
		return ContinuationMapping{}, errors.New("catalog is closed")
	}
	mapping, err := continuationByID(ctx, c.db, strings.TrimSpace(mappingID))
	if err != nil {
		return ContinuationMapping{}, err
	}
	if mapping.ExpiresAt != nil && !time.Now().UTC().Before(*mapping.ExpiresAt) {
		_, _ = c.db.ExecContext(ctx, `DELETE FROM continuation_mappings WHERE mapping_id = ?`, mapping.ID)
		return ContinuationMapping{}, ErrContinuationExpired
	}
	return mapping, nil
}

func (c *Catalog) Continuations(ctx context.Context, kind ContinuationKind) ([]ContinuationMapping, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("catalog is closed")
	}
	if kind != ContinuationTask && kind != ContinuationResource {
		return nil, fmt.Errorf("unsupported continuation kind %q", kind)
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT mapping_id, kind, server_id, payload, created_at_unix_ms, expires_at_unix_ms
		FROM continuation_mappings
		WHERE kind = ? AND (expires_at_unix_ms IS NULL OR expires_at_unix_ms > ?)
		ORDER BY created_at_unix_ms, mapping_id
	`, string(kind), time.Now().UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("list continuations: %w", err)
	}
	defer rows.Close()
	var out []ContinuationMapping
	for rows.Next() {
		mapping, err := scanContinuation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mapping)
	}
	return out, rows.Err()
}

func (c *Catalog) DeleteContinuation(ctx context.Context, mappingID string) error {
	if c == nil || c.db == nil {
		return errors.New("catalog is closed")
	}
	mappingID = strings.TrimSpace(mappingID)
	if mappingID == "" {
		return errors.New("mapping id is required")
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM continuation_mappings WHERE mapping_id = ?`, mappingID); err != nil {
		return fmt.Errorf("delete continuation %s: %w", mappingID, err)
	}
	return nil
}

func (c *Catalog) DeleteExpiredContinuations(ctx context.Context, now time.Time) (int64, error) {
	if c == nil || c.db == nil {
		return 0, errors.New("catalog is closed")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := c.db.ExecContext(ctx, `DELETE FROM continuation_mappings WHERE expires_at_unix_ms IS NOT NULL AND expires_at_unix_ms <= ?`, now.UTC().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("delete expired continuations: %w", err)
	}
	return result.RowsAffected()
}

type continuationScanner interface {
	Scan(...any) error
}

type continuationQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func continuationByID(ctx context.Context, q continuationQueryer, mappingID string) (ContinuationMapping, error) {
	if mappingID == "" {
		return ContinuationMapping{}, ErrContinuationNotFound
	}
	mapping, err := scanContinuation(q.QueryRowContext(ctx, `
		SELECT mapping_id, kind, server_id, payload, created_at_unix_ms, expires_at_unix_ms
		FROM continuation_mappings WHERE mapping_id = ?
	`, mappingID))
	if errors.Is(err, sql.ErrNoRows) {
		return ContinuationMapping{}, ErrContinuationNotFound
	}
	return mapping, err
}

func scanContinuation(row continuationScanner) (ContinuationMapping, error) {
	var mapping ContinuationMapping
	var kind string
	var payload []byte
	var createdMS int64
	var expiresMS sql.NullInt64
	if err := row.Scan(&mapping.ID, &kind, &mapping.ServerID, &payload, &createdMS, &expiresMS); err != nil {
		return ContinuationMapping{}, err
	}
	mapping.Kind = ContinuationKind(kind)
	mapping.Payload = append(json.RawMessage(nil), payload...)
	mapping.CreatedAt = time.UnixMilli(createdMS).UTC()
	if expiresMS.Valid {
		value := time.UnixMilli(expiresMS.Int64).UTC()
		mapping.ExpiresAt = &value
	}
	return mapping, nil
}

func sameContinuationExpiry(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
