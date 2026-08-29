package catalog

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

type RequiredArtifactSpec struct {
	Role               string
	MemberKey          string
	Kind               string
	Dependencies       []ArtifactDependency
	ContextFingerprint string
}

type GenerationArtifact struct {
	Role      string
	MemberKey string
	Artifact  Artifact
}

func artifactRequirementKey(role, memberKey string) (string, error) {
	if strings.TrimSpace(role) == "" || strings.TrimSpace(memberKey) == "" {
		return "", errors.New("artifact role and member key are required")
	}
	body, err := json.Marshal(struct {
		Role      string `json:"role"`
		MemberKey string `json:"member_key"`
	}{Role: role, MemberKey: memberKey})
	if err != nil {
		return "", fmt.Errorf("marshal artifact requirement key: %w", err)
	}
	return "artifact:" + toolcontract.FingerprintJSON(body), nil
}

func ArtifactRequirementFingerprint(spec RequiredArtifactSpec) (string, error) {
	if strings.TrimSpace(spec.Kind) == "" {
		return "", errors.New("artifact kind is required")
	}
	_, dependencyFingerprint, err := normalizeDependencies(spec.Dependencies)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(struct {
		Kind                  string `json:"kind"`
		DependencyFingerprint string `json:"dependency_fingerprint"`
		ContextFingerprint    string `json:"context_fingerprint"`
	}{
		Kind:                  spec.Kind,
		DependencyFingerprint: dependencyFingerprint,
		ContextFingerprint:    spec.ContextFingerprint,
	})
	if err != nil {
		return "", fmt.Errorf("marshal artifact requirement identity: %w", err)
	}
	return toolcontract.FingerprintJSON(body), nil
}

func (c *Catalog) RequireArtifact(ctx context.Context, generationID string, spec RequiredArtifactSpec) (string, error) {
	if c == nil || c.db == nil {
		return "", errors.New("catalog is closed")
	}
	if spec.Role == "" || spec.MemberKey == "" {
		return "", errors.New("artifact role and member key are required")
	}
	key, err := artifactRequirementKey(spec.Role, spec.MemberKey)
	if err != nil {
		return "", err
	}
	fingerprint, err := ArtifactRequirementFingerprint(spec)
	if err != nil {
		return "", err
	}
	if err := c.RequireDependency(ctx, generationID, key, fingerprint); err != nil {
		return "", err
	}
	return fingerprint, nil
}

func (c *Catalog) FindReusableArtifact(ctx context.Context, kind string, dependencies []ArtifactDependency, contextFingerprint string) (Artifact, error) {
	if c == nil || c.db == nil {
		return Artifact{}, errors.New("catalog is closed")
	}
	if strings.TrimSpace(kind) == "" {
		return Artifact{}, errors.New("artifact kind is required")
	}
	_, dependencyFingerprint, err := normalizeDependencies(dependencies)
	if err != nil {
		return Artifact{}, err
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT artifact_key
		FROM artifacts
		WHERE kind = ? AND dependency_fingerprint = ? AND context_fingerprint = ?
		ORDER BY artifact_key
	`, kind, dependencyFingerprint, contextFingerprint)
	if err != nil {
		return Artifact{}, fmt.Errorf("find reusable artifact: %w", err)
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return Artifact{}, fmt.Errorf("scan reusable artifact key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return Artifact{}, fmt.Errorf("close reusable artifact rows: %w", err)
	}
	if len(keys) == 0 {
		return Artifact{}, sql.ErrNoRows
	}
	for _, key := range keys {
		artifact, err := c.ReusableArtifact(ctx, key, dependencies, contextFingerprint)
		if err != nil {
			return Artifact{}, err
		}
		if artifact.Kind != kind {
			return Artifact{}, fmt.Errorf("%w: reusable artifact %s kind changed", ErrCatalogCorrupt, key)
		}
		return artifact, nil
	}
	return Artifact{}, sql.ErrNoRows
}

func (c *Catalog) FulfillArtifact(ctx context.Context, generationID string, spec RequiredArtifactSpec, artifactKey string) error {
	if c == nil || c.db == nil {
		return errors.New("catalog is closed")
	}
	if strings.TrimSpace(artifactKey) == "" {
		return errors.New("artifact key is required")
	}
	requirementKey, err := artifactRequirementKey(spec.Role, spec.MemberKey)
	if err != nil {
		return err
	}
	requirementFingerprint, err := ArtifactRequirementFingerprint(spec)
	if err != nil {
		return err
	}
	var expected string
	var required int
	if err := c.db.QueryRowContext(ctx, `
		SELECT expected_fingerprint, required
		FROM generation_dependencies
		WHERE generation_id = ? AND dependency_key = ?
	`, generationID, requirementKey).Scan(&expected, &required); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("required artifact %s/%s was not declared", spec.Role, spec.MemberKey)
		}
		return fmt.Errorf("load required artifact declaration: %w", err)
	}
	if required != 1 || expected != requirementFingerprint {
		return fmt.Errorf("%w: required artifact %s/%s identity changed", ErrDependencyMismatch, spec.Role, spec.MemberKey)
	}
	artifact, err := c.ReusableArtifact(ctx, artifactKey, spec.Dependencies, spec.ContextFingerprint)
	if err != nil {
		return err
	}
	if artifact.Kind != spec.Kind {
		return fmt.Errorf("%w: artifact kind = %q, want %q", ErrDependencyMismatch, artifact.Kind, spec.Kind)
	}
	if err := c.AttachArtifact(ctx, generationID, spec.Role, spec.MemberKey, artifactKey, true, true); err != nil {
		return err
	}
	if err := c.SatisfyDependency(ctx, generationID, requirementKey, requirementFingerprint, true); err != nil {
		return err
	}
	return nil
}

func (c *Catalog) GenerationArtifacts(ctx context.Context, generationID, role string) ([]GenerationArtifact, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("catalog is closed")
	}
	query := `
		SELECT ga.role, ga.member_key, a.artifact_key, a.kind, a.content_fingerprint,
		       a.dependency_fingerprint, a.context_fingerprint, a.payload, a.created_at_unix_ms
		FROM generation_artifacts ga
		JOIN artifacts a ON a.artifact_key = ga.artifact_key
		WHERE ga.generation_id = ? AND ga.complete = 1
	`
	args := []any{generationID}
	if role != "" {
		query += " AND ga.role = ?"
		args = append(args, role)
	}
	query += " ORDER BY ga.role, ga.member_key"
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list generation artifacts: %w", err)
	}
	var result []GenerationArtifact
	for rows.Next() {
		var record GenerationArtifact
		var createdMS int64
		if err := rows.Scan(
			&record.Role,
			&record.MemberKey,
			&record.Artifact.Key,
			&record.Artifact.Kind,
			&record.Artifact.ContentFingerprint,
			&record.Artifact.DependencyFingerprint,
			&record.Artifact.ContextFingerprint,
			&record.Artifact.Payload,
			&createdMS,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan generation artifact: %w", err)
		}
		record.Artifact.CreatedAt = time.UnixMilli(createdMS).UTC()
		result = append(result, record)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close generation artifact rows: %w", err)
	}
	for index := range result {
		dependencies, err := c.artifactDependencies(ctx, result[index].Artifact.Key)
		if err != nil {
			return nil, err
		}
		artifact, err := c.ReusableArtifact(ctx, result[index].Artifact.Key, dependencies, result[index].Artifact.ContextFingerprint)
		if err != nil {
			return nil, err
		}
		result[index].Artifact = artifact
	}
	return result, nil
}

func (c *Catalog) artifactDependencies(ctx context.Context, artifactKey string) ([]ArtifactDependency, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT dependency_key, dependency_fingerprint
		FROM artifact_dependencies
		WHERE artifact_key = ?
		ORDER BY dependency_key
	`, artifactKey)
	if err != nil {
		return nil, fmt.Errorf("load artifact dependencies: %w", err)
	}
	defer rows.Close()
	var dependencies []ArtifactDependency
	for rows.Next() {
		var dependency ArtifactDependency
		if err := rows.Scan(&dependency.Key, &dependency.Fingerprint); err != nil {
			return nil, fmt.Errorf("scan artifact dependency: %w", err)
		}
		dependencies = append(dependencies, dependency)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load artifact dependencies: %w", err)
	}
	return dependencies, nil
}
