package routingprefs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

func (s *Store) PutProfile(ctx context.Context, expectedRevision uint64, profile Profile) (WriteResult, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Description = strings.TrimSpace(profile.Description)
	if profile.Name == "" {
		return WriteResult{}, errors.New("profile name is required")
	}
	if profile.ID == "" {
		body, _ := json.Marshal(struct {
			Name string `json:"name"`
		}{profile.Name})
		profile.ID = "profile:" + strings.TrimPrefix(toolcontract.FingerprintJSON(body), "sha256:")
	}
	payload, err := json.Marshal(struct {
		Description string `json:"description,omitempty"`
	}{Description: profile.Description})
	if err != nil {
		return WriteResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return WriteResult{}, fmt.Errorf("begin profile write: %w", err)
	}
	defer tx.Rollback()
	current, err := preferenceRevisionTx(ctx, tx)
	if err != nil {
		return WriteResult{}, err
	}
	var existingName string
	var existingPayload []byte
	err = tx.QueryRowContext(ctx, `SELECT name, payload_json FROM routing_profiles WHERE profile_id = ?`, profile.ID).Scan(&existingName, &existingPayload)
	if err == nil && existingName == profile.Name && string(existingPayload) == string(payload) {
		if err := tx.Commit(); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{PreferenceRevision: current, ID: profile.ID}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return WriteResult{}, fmt.Errorf("load existing routing profile: %w", err)
	}
	if current != expectedRevision {
		return WriteResult{}, &ConflictError{Expected: expectedRevision, Current: current}
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO routing_profiles(profile_id, name, payload_json, updated_at_unix_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(profile_id) DO UPDATE SET
			name = excluded.name,
			payload_json = excluded.payload_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`, profile.ID, profile.Name, payload, now.UnixMilli()); err != nil {
		return WriteResult{}, fmt.Errorf("store routing profile: %w", err)
	}
	current++
	if err := setPreferenceRevisionTx(ctx, tx, current); err != nil {
		return WriteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WriteResult{}, fmt.Errorf("commit routing profile: %w", err)
	}
	return WriteResult{PreferenceRevision: current, Changed: true, ID: profile.ID}, nil
}

func (s *Store) DeleteProfile(ctx context.Context, expectedRevision uint64, profileID string) (WriteResult, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return WriteResult{}, errors.New("profile id is required")
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
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM routing_profiles WHERE profile_id = ?`, profileID).Scan(&count); err != nil {
		return WriteResult{}, err
	}
	if count == 0 {
		if err := tx.Commit(); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{PreferenceRevision: current, ID: profileID}, nil
	}
	if current != expectedRevision {
		return WriteResult{}, &ConflictError{Expected: expectedRevision, Current: current}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM routing_profiles WHERE profile_id = ?`, profileID); err != nil {
		return WriteResult{}, err
	}
	current++
	if err := setPreferenceRevisionTx(ctx, tx, current); err != nil {
		return WriteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{PreferenceRevision: current, Changed: true, ID: profileID}, nil
}

func (s *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT profile_id, name, payload_json, updated_at_unix_ms FROM routing_profiles ORDER BY name, profile_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []Profile
	for rows.Next() {
		var profile Profile
		var payload []byte
		var updatedMS int64
		if err := rows.Scan(&profile.ID, &profile.Name, &payload, &updatedMS); err != nil {
			return nil, err
		}
		var stored struct {
			Description string `json:"description,omitempty"`
		}
		if err := json.Unmarshal(payload, &stored); err != nil {
			return nil, fmt.Errorf("decode routing profile %s: %w", profile.ID, err)
		}
		profile.Description = stored.Description
		profile.UpdatedAt = time.UnixMilli(updatedMS).UTC()
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}
