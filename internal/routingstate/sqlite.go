package routingstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

type SQLiteBackend struct {
	db *sql.DB
}

func NewSQLiteBackend(db *sql.DB) (*SQLiteBackend, error) {
	if db == nil {
		return nil, errors.New("sqlite database is required")
	}
	return &SQLiteBackend{db: db}, nil
}

func (b *SQLiteBackend) Load(ctx context.Context) (Snapshot, error) {
	var routingRevision, preferenceRevision string
	var routingStateHash string
	err := b.db.QueryRowContext(ctx, `
		SELECT routing_revision, routing_state_hash, preference_revision
		FROM routing_state
		WHERE singleton = 1
	`).Scan(&routingRevision, &routingStateHash, &preferenceRevision)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load routing state: %w", err)
	}
	routing, err := strconv.ParseUint(routingRevision, 10, 64)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse routing revision: %w", err)
	}
	preference, err := strconv.ParseUint(preferenceRevision, 10, 64)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse preference revision: %w", err)
	}
	return Snapshot{
		RoutingRevision:    routing,
		RoutingStateHash:   routingStateHash,
		PreferenceRevision: preference,
	}, nil
}

func (b *SQLiteBackend) Store(ctx context.Context, state Snapshot) error {
	result, err := b.db.ExecContext(ctx, `
		UPDATE routing_state
		SET routing_revision = ?, routing_state_hash = ?, preference_revision = ?
		WHERE singleton = 1
	`, strconv.FormatUint(state.RoutingRevision, 10), state.RoutingStateHash, strconv.FormatUint(state.PreferenceRevision, 10))
	if err != nil {
		return fmt.Errorf("store routing state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store routing state rows affected: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("store routing state: expected one singleton row, updated %d", rows)
	}
	return nil
}
