package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

type ArtifactDependency struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

type ArtifactSpec struct {
	Kind               string
	Payload            []byte
	Dependencies       []ArtifactDependency
	ContextFingerprint string
}

type Artifact struct {
	Key                   string
	Kind                  string
	ContentFingerprint    string
	DependencyFingerprint string
	ContextFingerprint    string
	Payload               []byte
	CreatedAt             time.Time
}

func (c *Catalog) PutArtifact(ctx context.Context, spec ArtifactSpec) (Artifact, error) {
	if c == nil || c.db == nil {
		return Artifact{}, errors.New("catalog is closed")
	}
	if strings.TrimSpace(spec.Kind) == "" {
		return Artifact{}, errors.New("artifact kind is required")
	}
	dependencies, dependencyFingerprint, err := normalizeDependencies(spec.Dependencies)
	if err != nil {
		return Artifact{}, err
	}
	contentFingerprint := toolcontract.FingerprintJSON(spec.Payload)
	identity, err := json.Marshal(struct {
		Kind                  string `json:"kind"`
		ContentFingerprint    string `json:"content_fingerprint"`
		DependencyFingerprint string `json:"dependency_fingerprint"`
		ContextFingerprint    string `json:"context_fingerprint"`
	}{
		Kind:                  spec.Kind,
		ContentFingerprint:    contentFingerprint,
		DependencyFingerprint: dependencyFingerprint,
		ContextFingerprint:    spec.ContextFingerprint,
	})
	if err != nil {
		return Artifact{}, fmt.Errorf("marshal artifact identity: %w", err)
	}
	key := toolcontract.FingerprintJSON(identity)
	now := time.Now().UTC()

	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Artifact{}, fmt.Errorf("begin artifact transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO artifacts(
			artifact_key, kind, content_fingerprint, dependency_fingerprint,
			context_fingerprint, payload, created_at_unix_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, key, spec.Kind, contentFingerprint, dependencyFingerprint, spec.ContextFingerprint, spec.Payload, now.UnixMilli()); err != nil {
		return Artifact{}, fmt.Errorf("store artifact: %w", err)
	}

	stored, err := scanArtifact(tx.QueryRowContext(ctx, `
		SELECT artifact_key, kind, content_fingerprint, dependency_fingerprint,
		       context_fingerprint, payload, created_at_unix_ms
		FROM artifacts WHERE artifact_key = ?
	`, key))
	if err != nil {
		return Artifact{}, err
	}
	if stored.Kind != spec.Kind || stored.ContentFingerprint != contentFingerprint ||
		stored.DependencyFingerprint != dependencyFingerprint || stored.ContextFingerprint != spec.ContextFingerprint ||
		!bytes.Equal(stored.Payload, spec.Payload) {
		return Artifact{}, fmt.Errorf("%w: artifact key collision for %s", ErrCatalogCorrupt, key)
	}
	for _, dependency := range dependencies {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO artifact_dependencies(artifact_key, dependency_key, dependency_fingerprint)
			VALUES (?, ?, ?)
		`, key, dependency.Key, dependency.Fingerprint); err != nil {
			return Artifact{}, fmt.Errorf("store artifact dependency %q: %w", dependency.Key, err)
		}
	}
	if err := verifyArtifactDependencies(ctx, tx, key, dependencies); err != nil {
		return Artifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Artifact{}, fmt.Errorf("commit artifact: %w", err)
	}
	return stored, nil
}

func (c *Catalog) ReusableArtifact(ctx context.Context, key string, dependencies []ArtifactDependency, contextFingerprint string) (Artifact, error) {
	if strings.TrimSpace(key) == "" {
		return Artifact{}, errors.New("artifact key is required")
	}
	normalized, dependencyFingerprint, err := normalizeDependencies(dependencies)
	if err != nil {
		return Artifact{}, err
	}
	artifact, err := scanArtifact(c.db.QueryRowContext(ctx, `
		SELECT artifact_key, kind, content_fingerprint, dependency_fingerprint,
		       context_fingerprint, payload, created_at_unix_ms
		FROM artifacts WHERE artifact_key = ?
	`, key))
	if err != nil {
		return Artifact{}, err
	}
	if artifact.DependencyFingerprint != dependencyFingerprint || artifact.ContextFingerprint != contextFingerprint {
		return Artifact{}, ErrDependencyMismatch
	}
	if toolcontract.FingerprintJSON(artifact.Payload) != artifact.ContentFingerprint {
		return Artifact{}, fmt.Errorf("%w: artifact %s payload fingerprint mismatch", ErrCatalogCorrupt, key)
	}
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Artifact{}, fmt.Errorf("begin artifact dependency validation: %w", err)
	}
	defer tx.Rollback()
	if err := verifyArtifactDependencies(ctx, tx, key, normalized); err != nil {
		return Artifact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Artifact{}, fmt.Errorf("finish artifact dependency validation: %w", err)
	}
	return artifact, nil
}

func (c *Catalog) AttachArtifact(ctx context.Context, generationID, role, memberKey, artifactKey string, required, complete bool) error {
	if generationID == "" || role == "" || memberKey == "" || artifactKey == "" {
		return errors.New("generation id, role, member key, and artifact key are required")
	}
	if err := c.requireStaging(ctx, generationID); err != nil {
		return err
	}
	requiredValue, completeValue := 0, 0
	if required {
		requiredValue = 1
	}
	if complete {
		completeValue = 1
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO generation_artifacts(generation_id, role, member_key, artifact_key, required, complete)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(generation_id, role, member_key) DO UPDATE SET
			artifact_key = excluded.artifact_key,
			required = excluded.required,
			complete = excluded.complete
	`, generationID, role, memberKey, artifactKey, requiredValue, completeValue)
	if err != nil {
		return fmt.Errorf("attach artifact: %w", err)
	}
	return nil
}

func normalizeDependencies(dependencies []ArtifactDependency) ([]ArtifactDependency, string, error) {
	normalized := append([]ArtifactDependency(nil), dependencies...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Key < normalized[j].Key })
	for i := range normalized {
		if strings.TrimSpace(normalized[i].Key) == "" || strings.TrimSpace(normalized[i].Fingerprint) == "" {
			return nil, "", errors.New("artifact dependency key and fingerprint are required")
		}
		if i > 0 && normalized[i-1].Key == normalized[i].Key {
			return nil, "", fmt.Errorf("duplicate artifact dependency key %q", normalized[i].Key)
		}
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", fmt.Errorf("marshal artifact dependencies: %w", err)
	}
	return normalized, toolcontract.FingerprintJSON(body), nil
}

func verifyArtifactDependencies(ctx context.Context, tx *sql.Tx, key string, expected []ArtifactDependency) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT dependency_key, dependency_fingerprint
		FROM artifact_dependencies WHERE artifact_key = ? ORDER BY dependency_key
	`, key)
	if err != nil {
		return fmt.Errorf("read artifact dependencies: %w", err)
	}
	defer rows.Close()
	var actual []ArtifactDependency
	for rows.Next() {
		var dependency ArtifactDependency
		if err := rows.Scan(&dependency.Key, &dependency.Fingerprint); err != nil {
			return fmt.Errorf("scan artifact dependency: %w", err)
		}
		actual = append(actual, dependency)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read artifact dependencies: %w", err)
	}
	if len(actual) != len(expected) {
		return ErrDependencyMismatch
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return ErrDependencyMismatch
		}
	}
	return nil
}

func scanArtifact(row rowScanner) (Artifact, error) {
	var artifact Artifact
	var createdMS int64
	if err := row.Scan(
		&artifact.Key,
		&artifact.Kind,
		&artifact.ContentFingerprint,
		&artifact.DependencyFingerprint,
		&artifact.ContextFingerprint,
		&artifact.Payload,
		&createdMS,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Artifact{}, sql.ErrNoRows
		}
		return Artifact{}, fmt.Errorf("scan artifact: %w", err)
	}
	artifact.CreatedAt = time.UnixMilli(createdMS).UTC()
	return artifact, nil
}
