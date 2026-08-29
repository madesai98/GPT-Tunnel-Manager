package catalog

import (
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

type EnrichmentBatchKind string

const (
	BatchToolEnrichment           EnrichmentBatchKind = "tool_enrichment"
	BatchCapabilityReconciliation EnrichmentBatchKind = "capability_reconciliation"
	BatchAmbiguityReview          EnrichmentBatchKind = "ambiguity_review"
)

var ErrEnrichmentBatchConflict = errors.New("enrichment_batch_conflict")

type SourceToolRecord struct {
	ServerID          string          `json:"server_id"`
	ToolName          string          `json:"tool_name"`
	SourceFingerprint string          `json:"source_fingerprint"`
	ContractJSON      json.RawMessage `json:"contract"`
}

type EnrichmentBatchSpec struct {
	ID           string
	GenerationID string
	Kind         EnrichmentBatchKind
	BatchKey     string
	Required     bool
	RequestJSON  []byte
}

type EnrichmentBatch struct {
	ID                   string              `json:"batch_id"`
	GenerationID         string              `json:"generation_id"`
	Kind                 EnrichmentBatchKind `json:"kind"`
	BatchKey             string              `json:"batch_key"`
	Required             bool                `json:"required"`
	RequestFingerprint   string              `json:"request_fingerprint"`
	RequestJSON          json.RawMessage     `json:"request"`
	AcceptedFingerprint  string              `json:"accepted_fingerprint,omitempty"`
	AcceptedResponseJSON json.RawMessage     `json:"accepted_response,omitempty"`
	CreatedAt            time.Time           `json:"created_at"`
	AcceptedAt           *time.Time          `json:"accepted_at,omitempty"`
}

type EnrichmentBatchCounts struct {
	PendingRequired  int `json:"pending_required"`
	PendingOptional  int `json:"pending_optional"`
	AcceptedRequired int `json:"accepted_required"`
	AcceptedOptional int `json:"accepted_optional"`
}

type BatchSubmissionResult struct {
	Batch      EnrichmentBatch
	Idempotent bool
}

func (c *Catalog) SourceTools(ctx context.Context, generationID string) ([]SourceToolRecord, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("catalog is closed")
	}
	if strings.TrimSpace(generationID) == "" {
		return nil, errors.New("generation id is required")
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT server_id, tool_name, source_fingerprint, contract_json
		FROM source_tools
		WHERE generation_id = ?
		ORDER BY server_id, tool_name
	`, generationID)
	if err != nil {
		return nil, fmt.Errorf("list source tools: %w", err)
	}
	defer rows.Close()
	var result []SourceToolRecord
	for rows.Next() {
		var record SourceToolRecord
		var body []byte
		if err := rows.Scan(&record.ServerID, &record.ToolName, &record.SourceFingerprint, &body); err != nil {
			return nil, fmt.Errorf("scan source tool: %w", err)
		}
		if toolcontract.FingerprintJSON(body) != record.SourceFingerprint {
			return nil, fmt.Errorf("%w: source tool %s/%s fingerprint mismatch", ErrSourceFingerprintMismatch, record.ServerID, record.ToolName)
		}
		record.ContractJSON = append(json.RawMessage(nil), body...)
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list source tools: %w", err)
	}
	return result, nil
}

func (c *Catalog) NeighborhoodContext(ctx context.Context, generationID, memberKey string) (string, error) {
	if c == nil || c.db == nil {
		return "", errors.New("catalog is closed")
	}
	var fingerprint string
	if err := c.db.QueryRowContext(ctx, `
		SELECT context_fingerprint FROM neighborhood_contexts
		WHERE generation_id = ? AND member_key = ?
	`, generationID, memberKey).Scan(&fingerprint); err != nil {
		return "", err
	}
	return fingerprint, nil
}

func (c *Catalog) PutEnrichmentBatches(ctx context.Context, specs []EnrichmentBatchSpec) error {
	if c == nil || c.db == nil {
		return errors.New("catalog is closed")
	}
	if len(specs) == 0 {
		return nil
	}
	normalized := make([]EnrichmentBatchSpec, len(specs))
	copy(normalized, specs)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	seen := make(map[string]struct{}, len(normalized))
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin enrichment batch transaction: %w", err)
	}
	defer tx.Rollback()
	validatedGenerations := make(map[string]struct{})
	for _, spec := range normalized {
		if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.GenerationID) == "" || strings.TrimSpace(spec.BatchKey) == "" {
			return errors.New("batch id, generation id, and batch key are required")
		}
		if !validBatchKind(spec.Kind) {
			return fmt.Errorf("unsupported enrichment batch kind %q", spec.Kind)
		}
		if _, ok := validatedGenerations[spec.GenerationID]; !ok {
			var status string
			if err := tx.QueryRowContext(ctx, `SELECT status FROM generations WHERE generation_id = ?`, spec.GenerationID).Scan(&status); err != nil {
				return fmt.Errorf("load enrichment generation status: %w", err)
			}
			if GenerationStatus(status) != GenerationStaging {
				return ErrGenerationNotStaging
			}
			validatedGenerations[spec.GenerationID] = struct{}{}
		}
		if _, ok := seen[spec.ID]; ok {
			return fmt.Errorf("duplicate enrichment batch id %q", spec.ID)
		}
		seen[spec.ID] = struct{}{}
		request, err := canonicalJSON(spec.RequestJSON)
		if err != nil {
			return fmt.Errorf("canonicalize enrichment batch %s request: %w", spec.ID, err)
		}
		requestFingerprint := toolcontract.FingerprintJSON(request)
		required := 0
		if spec.Required {
			required = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO enrichment_batches(
				batch_id, generation_id, kind, batch_key, required,
				request_fingerprint, request_json, created_at_unix_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, spec.ID, spec.GenerationID, string(spec.Kind), spec.BatchKey, required, requestFingerprint, request, time.Now().UTC().UnixMilli()); err != nil {
			return fmt.Errorf("store enrichment batch %s: %w", spec.ID, err)
		}
		var generationID, kind, batchKey, storedRequestFingerprint string
		var storedRequired int
		var storedRequest []byte
		if err := tx.QueryRowContext(ctx, `
			SELECT generation_id, kind, batch_key, required, request_fingerprint, request_json
			FROM enrichment_batches WHERE batch_id = ?
		`, spec.ID).Scan(&generationID, &kind, &batchKey, &storedRequired, &storedRequestFingerprint, &storedRequest); err != nil {
			return fmt.Errorf("verify enrichment batch %s: %w", spec.ID, err)
		}
		if generationID != spec.GenerationID || kind != string(spec.Kind) || batchKey != spec.BatchKey || storedRequired != required || storedRequestFingerprint != requestFingerprint || string(storedRequest) != string(request) {
			return fmt.Errorf("%w: immutable batch %s differs from existing work item", ErrEnrichmentBatchConflict, spec.ID)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit enrichment batches: %w", err)
	}
	return nil
}

func (c *Catalog) EnrichmentBatch(ctx context.Context, batchID string) (EnrichmentBatch, error) {
	if c == nil || c.db == nil {
		return EnrichmentBatch{}, errors.New("catalog is closed")
	}
	return scanEnrichmentBatch(c.db.QueryRowContext(ctx, `
		SELECT batch_id, generation_id, kind, batch_key, required,
		       request_fingerprint, request_json, accepted_fingerprint,
		       accepted_response_json, created_at_unix_ms, accepted_at_unix_ms
		FROM enrichment_batches WHERE batch_id = ?
	`, batchID))
}

func (c *Catalog) EnrichmentBatchCount(ctx context.Context, generationID string, kind EnrichmentBatchKind) (int, error) {
	if c == nil || c.db == nil {
		return 0, errors.New("catalog is closed")
	}
	var count int
	if err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM enrichment_batches WHERE generation_id = ? AND kind = ?
	`, generationID, string(kind)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count enrichment batches: %w", err)
	}
	return count, nil
}

func (c *Catalog) PendingEnrichmentBatches(ctx context.Context, generationID string, kind EnrichmentBatchKind, limit int) ([]EnrichmentBatch, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("catalog is closed")
	}
	if limit <= 0 {
		limit = 1
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT batch_id, generation_id, kind, batch_key, required,
		       request_fingerprint, request_json, accepted_fingerprint,
		       accepted_response_json, created_at_unix_ms, accepted_at_unix_ms
		FROM enrichment_batches
		WHERE generation_id = ? AND kind = ? AND accepted_fingerprint IS NULL
		ORDER BY batch_key, batch_id
		LIMIT ?
	`, generationID, string(kind), limit)
	if err != nil {
		return nil, fmt.Errorf("list pending enrichment batches: %w", err)
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
		return nil, fmt.Errorf("list pending enrichment batches: %w", err)
	}
	return batches, nil
}

func (c *Catalog) EnrichmentBatchCounts(ctx context.Context, generationID string) (EnrichmentBatchCounts, error) {
	if c == nil || c.db == nil {
		return EnrichmentBatchCounts{}, errors.New("catalog is closed")
	}
	var counts EnrichmentBatchCounts
	if err := c.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN required = 1 AND accepted_fingerprint IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN required = 0 AND accepted_fingerprint IS NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN required = 1 AND accepted_fingerprint IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN required = 0 AND accepted_fingerprint IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM enrichment_batches WHERE generation_id = ?
	`, generationID).Scan(&counts.PendingRequired, &counts.PendingOptional, &counts.AcceptedRequired, &counts.AcceptedOptional); err != nil {
		return EnrichmentBatchCounts{}, fmt.Errorf("count enrichment batch states: %w", err)
	}
	return counts, nil
}

func (c *Catalog) AcceptEnrichmentBatch(ctx context.Context, batchID string, responseJSON []byte) (BatchSubmissionResult, error) {
	if c == nil || c.db == nil {
		return BatchSubmissionResult{}, errors.New("catalog is closed")
	}
	response, err := canonicalJSON(responseJSON)
	if err != nil {
		return BatchSubmissionResult{}, fmt.Errorf("canonicalize enrichment batch response: %w", err)
	}
	fingerprint := toolcontract.FingerprintJSON(response)
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return BatchSubmissionResult{}, fmt.Errorf("begin enrichment submission: %w", err)
	}
	defer tx.Rollback()
	batch, err := scanEnrichmentBatch(tx.QueryRowContext(ctx, `
		SELECT batch_id, generation_id, kind, batch_key, required,
		       request_fingerprint, request_json, accepted_fingerprint,
		       accepted_response_json, created_at_unix_ms, accepted_at_unix_ms
		FROM enrichment_batches WHERE batch_id = ?
	`, batchID))
	if err != nil {
		return BatchSubmissionResult{}, err
	}
	if batch.AcceptedFingerprint != "" {
		if batch.AcceptedFingerprint == fingerprint && string(batch.AcceptedResponseJSON) == string(response) {
			if err := tx.Commit(); err != nil {
				return BatchSubmissionResult{}, fmt.Errorf("finish idempotent enrichment submission: %w", err)
			}
			return BatchSubmissionResult{Batch: batch, Idempotent: true}, nil
		}
		return BatchSubmissionResult{}, fmt.Errorf("%w: batch %s already has a different accepted submission", ErrEnrichmentBatchConflict, batchID)
	}
	var generationStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM generations WHERE generation_id = ?`, batch.GenerationID).Scan(&generationStatus); err != nil {
		return BatchSubmissionResult{}, fmt.Errorf("load enrichment generation status: %w", err)
	}
	if batch.Kind == BatchAmbiguityReview {
		if GenerationStatus(generationStatus) == GenerationSuperseded {
			return BatchSubmissionResult{}, ErrGenerationNotStaging
		}
	} else if GenerationStatus(generationStatus) != GenerationStaging {
		return BatchSubmissionResult{}, ErrGenerationNotStaging
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		UPDATE enrichment_batches
		SET accepted_fingerprint = ?, accepted_response_json = ?, accepted_at_unix_ms = ?
		WHERE batch_id = ? AND accepted_fingerprint IS NULL
	`, fingerprint, response, now.UnixMilli(), batchID)
	if err != nil {
		return BatchSubmissionResult{}, fmt.Errorf("accept enrichment batch %s: %w", batchID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return BatchSubmissionResult{}, fmt.Errorf("accept enrichment batch rows affected: %w", err)
	}
	if rows != 1 {
		return BatchSubmissionResult{}, fmt.Errorf("%w: batch %s changed concurrently", ErrEnrichmentBatchConflict, batchID)
	}
	batch.AcceptedFingerprint = fingerprint
	batch.AcceptedResponseJSON = append(json.RawMessage(nil), response...)
	batch.AcceptedAt = &now
	if err := tx.Commit(); err != nil {
		return BatchSubmissionResult{}, fmt.Errorf("commit enrichment submission: %w", err)
	}
	return BatchSubmissionResult{Batch: batch}, nil
}

func validBatchKind(kind EnrichmentBatchKind) bool {
	switch kind {
	case BatchToolEnrichment, BatchCapabilityReconciliation, BatchAmbiguityReview:
		return true
	default:
		return false
	}
}

func scanEnrichmentBatch(row rowScanner) (EnrichmentBatch, error) {
	var batch EnrichmentBatch
	var kind string
	var required int
	var acceptedFingerprint sql.NullString
	var acceptedResponse []byte
	var createdMS int64
	var acceptedMS sql.NullInt64
	if err := row.Scan(
		&batch.ID,
		&batch.GenerationID,
		&kind,
		&batch.BatchKey,
		&required,
		&batch.RequestFingerprint,
		&batch.RequestJSON,
		&acceptedFingerprint,
		&acceptedResponse,
		&createdMS,
		&acceptedMS,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EnrichmentBatch{}, sql.ErrNoRows
		}
		return EnrichmentBatch{}, fmt.Errorf("scan enrichment batch: %w", err)
	}
	batch.Kind = EnrichmentBatchKind(kind)
	batch.Required = required == 1
	batch.CreatedAt = time.UnixMilli(createdMS).UTC()
	if acceptedFingerprint.Valid {
		batch.AcceptedFingerprint = acceptedFingerprint.String
	}
	if len(acceptedResponse) != 0 {
		batch.AcceptedResponseJSON = append(json.RawMessage(nil), acceptedResponse...)
	}
	if acceptedMS.Valid {
		acceptedAt := time.UnixMilli(acceptedMS.Int64).UTC()
		batch.AcceptedAt = &acceptedAt
	}
	return batch, nil
}

func canonicalJSON(data []byte) ([]byte, error) {
	if !json.Valid(data) {
		return nil, errors.New("invalid JSON")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
