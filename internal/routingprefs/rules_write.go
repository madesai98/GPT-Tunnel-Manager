package routingprefs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) PutRule(ctx context.Context, expectedRevision uint64, spec RuleSpec) (WriteResult, error) {
	normalized, ruleID, conflictKey, assumptionFingerprint, payload, err := normalizeRule(spec)
	if err != nil {
		return WriteResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return WriteResult{}, fmt.Errorf("begin preference write: %w", err)
	}
	defer tx.Rollback()
	current, err := preferenceRevisionTx(ctx, tx)
	if err != nil {
		return WriteResult{}, err
	}
	if normalized.ProfileID != "" {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM routing_profiles WHERE profile_id = ?`, normalized.ProfileID).Scan(&count); err != nil {
			return WriteResult{}, fmt.Errorf("validate preference profile: %w", err)
		}
		if count != 1 {
			return WriteResult{}, fmt.Errorf("routing profile %q not found", normalized.ProfileID)
		}
	}
	var existingPayload []byte
	var existingAssumption, existingState string
	err = tx.QueryRowContext(ctx, `
		SELECT payload_json, assumption_fingerprint, review_state
		FROM routing_preferences WHERE preference_id = ?
	`, ruleID).Scan(&existingPayload, &existingAssumption, &existingState)
	if err == nil && string(existingPayload) == string(payload) && existingAssumption == assumptionFingerprint {
		if err := tx.Commit(); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{PreferenceRevision: current, NeedsReview: ReviewState(existingState) == ReviewNeedsReview, ID: ruleID}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return WriteResult{}, fmt.Errorf("load existing routing preference: %w", err)
	}
	if current != expectedRevision {
		return WriteResult{}, &ConflictError{Expected: expectedRevision, Current: current}
	}
	var profileArg any
	if normalized.ProfileID != "" {
		profileArg = normalized.ProfileID
	}
	var conflicts int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM routing_preferences
		WHERE COALESCE(profile_id, '') = ? AND target_key = ? AND preference_id <> ?
	`, normalized.ProfileID, conflictKey, ruleID).Scan(&conflicts); err != nil {
		return WriteResult{}, fmt.Errorf("find equal-scope preference conflicts: %w", err)
	}
	reviewState := ReviewActive
	if conflicts != 0 {
		reviewState = ReviewNeedsReview
		if _, err := tx.ExecContext(ctx, `
			UPDATE routing_preferences SET review_state = ?, updated_at_unix_ms = ?
			WHERE COALESCE(profile_id, '') = ? AND target_key = ? AND preference_id <> ?
		`, string(ReviewNeedsReview), time.Now().UTC().UnixMilli(), normalized.ProfileID, conflictKey, ruleID); err != nil {
			return WriteResult{}, fmt.Errorf("mark preference conflicts for review: %w", err)
		}
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO routing_preferences(
			preference_id, profile_id, target_key, assumption_fingerprint,
			review_state, payload_json, updated_at_unix_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(preference_id) DO UPDATE SET
			profile_id = excluded.profile_id,
			target_key = excluded.target_key,
			assumption_fingerprint = excluded.assumption_fingerprint,
			review_state = excluded.review_state,
			payload_json = excluded.payload_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`, ruleID, profileArg, conflictKey, assumptionFingerprint, string(reviewState), payload, now.UnixMilli()); err != nil {
		return WriteResult{}, fmt.Errorf("store routing preference: %w", err)
	}
	current++
	if err := setPreferenceRevisionTx(ctx, tx, current); err != nil {
		return WriteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WriteResult{}, fmt.Errorf("commit routing preference: %w", err)
	}
	return WriteResult{PreferenceRevision: current, Changed: true, NeedsReview: reviewState == ReviewNeedsReview, ID: ruleID}, nil
}

func (s *Store) ConfirmRule(ctx context.Context, expectedRevision uint64, preferenceID string) (WriteResult, error) {
	preferenceID = strings.TrimSpace(preferenceID)
	if preferenceID == "" {
		return WriteResult{}, errors.New("preference id is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return WriteResult{}, err
	}
	defer tx.Rollback()
	current, err := preferenceRevisionTx(ctx, tx)
	if err != nil {
		return WriteResult{}, err
	}
	var profileID sql.NullString
	var conflictKey, state string
	if err := tx.QueryRowContext(ctx, `SELECT profile_id, target_key, review_state FROM routing_preferences WHERE preference_id = ?`, preferenceID).Scan(&profileID, &conflictKey, &state); err != nil {
		return WriteResult{}, err
	}
	if ReviewState(state) == ReviewActive {
		if err := tx.Commit(); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{PreferenceRevision: current, ID: preferenceID}, nil
	}
	if current != expectedRevision {
		return WriteResult{}, &ConflictError{Expected: expectedRevision, Current: current}
	}
	scope := ""
	if profileID.Valid {
		scope = profileID.String
	}
	var conflicts int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM routing_preferences
		WHERE COALESCE(profile_id, '') = ? AND target_key = ? AND preference_id <> ?
	`, scope, conflictKey, preferenceID).Scan(&conflicts); err != nil {
		return WriteResult{}, err
	}
	if conflicts != 0 {
		return WriteResult{}, fmt.Errorf("%w: equal-scope preference conflict is still unresolved", ErrPreferenceConflict)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE routing_preferences SET review_state = ?, updated_at_unix_ms = ? WHERE preference_id = ?`, string(ReviewActive), time.Now().UTC().UnixMilli(), preferenceID); err != nil {
		return WriteResult{}, err
	}
	current++
	if err := setPreferenceRevisionTx(ctx, tx, current); err != nil {
		return WriteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{PreferenceRevision: current, Changed: true, ID: preferenceID}, nil
}

func (s *Store) DeleteRule(ctx context.Context, expectedRevision uint64, preferenceID string) (WriteResult, error) {
	preferenceID = strings.TrimSpace(preferenceID)
	if preferenceID == "" {
		return WriteResult{}, errors.New("preference id is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return WriteResult{}, err
	}
	defer tx.Rollback()
	current, err := preferenceRevisionTx(ctx, tx)
	if err != nil {
		return WriteResult{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM routing_preferences WHERE preference_id = ?`, preferenceID).Scan(&count); err != nil {
		return WriteResult{}, err
	}
	if count == 0 {
		if err := tx.Commit(); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{PreferenceRevision: current, ID: preferenceID}, nil
	}
	if current != expectedRevision {
		return WriteResult{}, &ConflictError{Expected: expectedRevision, Current: current}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM routing_preferences WHERE preference_id = ?`, preferenceID); err != nil {
		return WriteResult{}, err
	}
	current++
	if err := setPreferenceRevisionTx(ctx, tx, current); err != nil {
		return WriteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{PreferenceRevision: current, Changed: true, ID: preferenceID}, nil
}
