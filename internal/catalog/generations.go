package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GenerationStatus string

const (
	GenerationStaging    GenerationStatus = "staging"
	GenerationActive     GenerationStatus = "active"
	GenerationSuperseded GenerationStatus = "superseded"
)

type GenerationSpec struct {
	ID               string
	RoutingStateHash string
}

type Generation struct {
	ID                   string
	Status               GenerationStatus
	RoutingStateHash     string
	SourceSetFingerprint string
	CreatedAt            time.Time
	ActivatedAt          *time.Time
}

type DirtyPartition struct {
	PartitionKey        string
	Reason              string
	ObservedFingerprint string
	MarkedAt            time.Time
}

func (c *Catalog) CreateStaging(ctx context.Context, spec GenerationSpec) (Generation, error) {
	if c == nil || c.db == nil {
		return Generation{}, errors.New("catalog is closed")
	}
	if strings.TrimSpace(spec.RoutingStateHash) == "" {
		return Generation{}, errors.New("routing state hash is required")
	}
	id := strings.TrimSpace(spec.ID)
	if id == "" {
		generated, err := newGenerationID()
		if err != nil {
			return Generation{}, err
		}
		id = generated
	}
	now := time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO generations(generation_id, status, routing_state_hash, created_at_unix_ms)
		VALUES (?, 'staging', ?, ?)
	`, id, spec.RoutingStateHash, now.UnixMilli())
	if err != nil {
		return Generation{}, fmt.Errorf("create staging generation: %w", err)
	}
	return Generation{ID: id, Status: GenerationStaging, RoutingStateHash: spec.RoutingStateHash, CreatedAt: now}, nil
}

func newGenerationID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate generation id: %w", err)
	}
	return "gen_" + hex.EncodeToString(random[:]), nil
}

func (c *Catalog) Generation(ctx context.Context, id string) (Generation, error) {
	return scanGeneration(c.db.QueryRowContext(ctx, `
		SELECT generation_id, status, routing_state_hash, source_set_fingerprint,
		       created_at_unix_ms, activated_at_unix_ms
		FROM generations WHERE generation_id = ?
	`, id))
}

func (c *Catalog) ActiveGeneration(ctx context.Context) (Generation, error) {
	return scanGeneration(c.db.QueryRowContext(ctx, `
		SELECT generation_id, status, routing_state_hash, source_set_fingerprint,
		       created_at_unix_ms, activated_at_unix_ms
		FROM generations WHERE status = 'active'
	`))
}

type rowScanner interface { Scan(dest ...any) error }

func scanGeneration(row rowScanner) (Generation, error) {
	var generation Generation
	var status string
	var createdMS int64
	var activatedMS sql.NullInt64
	if err := row.Scan(&generation.ID, &status, &generation.RoutingStateHash, &generation.SourceSetFingerprint, &createdMS, &activatedMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return Generation{}, ErrGenerationNotFound }
		return Generation{}, fmt.Errorf("scan generation: %w", err)
	}
	generation.Status = GenerationStatus(status)
	generation.CreatedAt = time.UnixMilli(createdMS).UTC()
	if activatedMS.Valid { activated := time.UnixMilli(activatedMS.Int64).UTC(); generation.ActivatedAt = &activated }
	return generation, nil
}

func (c *Catalog) ReconcileStaging(ctx context.Context, currentRoutingStateHash string) (int64, error) {
	if strings.TrimSpace(currentRoutingStateHash) == "" { return 0, errors.New("current routing state hash is required") }
	result, err := c.db.ExecContext(ctx, `UPDATE generations SET status = 'superseded' WHERE status = 'staging' AND routing_state_hash <> ?`, currentRoutingStateHash)
	if err != nil { return 0, fmt.Errorf("reconcile staging generations: %w", err) }
	count, err := result.RowsAffected()
	if err != nil { return 0, fmt.Errorf("reconcile staging generations rows affected: %w", err) }
	return count, nil
}

func (c *Catalog) RequireSourceTool(ctx context.Context, generationID, serverID, toolName, expectedFingerprint string) error {
	if generationID == "" || serverID == "" || toolName == "" || expectedFingerprint == "" { return errors.New("generation id, server id, tool name, and expected fingerprint are required") }
	if err := c.requireStaging(ctx, generationID); err != nil { return err }
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO generation_members(generation_id, server_id, tool_name, expected_source_fingerprint, required, complete)
		VALUES (?, ?, ?, ?, 1, 0)
		ON CONFLICT(generation_id, server_id, tool_name) DO UPDATE SET
			expected_source_fingerprint = excluded.expected_source_fingerprint,
			required = 1,
			complete = CASE WHEN generation_members.actual_source_fingerprint = excluded.expected_source_fingerprint THEN generation_members.complete ELSE 0 END
	`, generationID, serverID, toolName, expectedFingerprint)
	if err != nil { return fmt.Errorf("require source tool %s/%s: %w", serverID, toolName, err) }
	return nil
}

func (c *Catalog) PutSourceTool(ctx context.Context, generationID, serverID string, tool *mcp.Tool, complete bool) (string, error) {
	if generationID == "" || serverID == "" { return "", errors.New("generation id and server id are required") }
	if err := c.requireStaging(ctx, generationID); err != nil { return "", err }
	fingerprint, contractJSON, err := toolcontract.FingerprintTool(tool)
	if err != nil { return "", err }
	identityJSON, err := json.Marshal(struct { ServerID string `json:"server_id"`; ToolName string `json:"tool_name"` }{ServerID: serverID, ToolName: tool.Name})
	if err != nil { return "", fmt.Errorf("marshal invocation identity: %w", err) }
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil { return "", fmt.Errorf("begin source tool transaction: %w", err) }
	defer tx.Rollback()
	var expected string
	if err := tx.QueryRowContext(ctx, `SELECT expected_source_fingerprint FROM generation_members WHERE generation_id = ? AND server_id = ? AND tool_name = ?`, generationID, serverID, tool.Name).Scan(&expected); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return "", fmt.Errorf("source tool %s/%s was not declared as a generation member", serverID, tool.Name) }
		return "", fmt.Errorf("load source member: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO source_tools(generation_id, server_id, tool_name, source_fingerprint, invocation_identity_json, contract_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(generation_id, server_id, tool_name) DO UPDATE SET
			source_fingerprint = excluded.source_fingerprint,
			invocation_identity_json = excluded.invocation_identity_json,
			contract_json = excluded.contract_json
	`, generationID, serverID, tool.Name, fingerprint, identityJSON, contractJSON); err != nil { return "", fmt.Errorf("store source tool %s/%s: %w", serverID, tool.Name, err) }
	completeValue := 0; if complete { completeValue = 1 }
	if _, err := tx.ExecContext(ctx, `UPDATE generation_members SET actual_source_fingerprint = ?, complete = ? WHERE generation_id = ? AND server_id = ? AND tool_name = ?`, fingerprint, completeValue, generationID, serverID, tool.Name); err != nil { return "", fmt.Errorf("update source member %s/%s: %w", serverID, tool.Name, err) }
	if err := tx.Commit(); err != nil { return "", fmt.Errorf("commit source tool %s/%s: %w", serverID, tool.Name, err) }
	_ = expected
	return fingerprint, nil
}

func (c *Catalog) RequireDependency(ctx context.Context, generationID, dependencyKey, expectedFingerprint string) error {
	if generationID == "" || dependencyKey == "" || expectedFingerprint == "" { return errors.New("generation id, dependency key, and expected fingerprint are required") }
	if err := c.requireStaging(ctx, generationID); err != nil { return err }
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO generation_dependencies(generation_id, dependency_key, expected_fingerprint, required, complete)
		VALUES (?, ?, ?, 1, 0)
		ON CONFLICT(generation_id, dependency_key) DO UPDATE SET
			expected_fingerprint = excluded.expected_fingerprint,
			required = 1,
			complete = CASE WHEN generation_dependencies.actual_fingerprint = excluded.expected_fingerprint THEN generation_dependencies.complete ELSE 0 END
	`, generationID, dependencyKey, expectedFingerprint)
	if err != nil { return fmt.Errorf("require generation dependency %q: %w", dependencyKey, err) }
	return nil
}

func (c *Catalog) SatisfyDependency(ctx context.Context, generationID, dependencyKey, actualFingerprint string, complete bool) error {
	if actualFingerprint == "" { return errors.New("actual dependency fingerprint is required") }
	if err := c.requireStaging(ctx, generationID); err != nil { return err }
	completeValue := 0; if complete { completeValue = 1 }
	result, err := c.db.ExecContext(ctx, `UPDATE generation_dependencies SET actual_fingerprint = ?, complete = ? WHERE generation_id = ? AND dependency_key = ?`, actualFingerprint, completeValue, generationID, dependencyKey)
	if err != nil { return fmt.Errorf("satisfy generation dependency %q: %w", dependencyKey, err) }
	rows, err := result.RowsAffected(); if err != nil { return fmt.Errorf("satisfy generation dependency rows affected: %w", err) }
	if rows != 1 { return fmt.Errorf("generation dependency %q was not declared", dependencyKey) }
	return nil
}

func (c *Catalog) SetNeighborhoodContext(ctx context.Context, generationID, memberKey, contextFingerprint string) error {
	if generationID == "" || memberKey == "" || contextFingerprint == "" { return errors.New("generation id, member key, and context fingerprint are required") }
	if err := c.requireStaging(ctx, generationID); err != nil { return err }
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO neighborhood_contexts(generation_id, member_key, context_fingerprint) VALUES (?, ?, ?)
		ON CONFLICT(generation_id, member_key) DO UPDATE SET context_fingerprint = excluded.context_fingerprint
	`, generationID, memberKey, contextFingerprint)
	if err != nil { return fmt.Errorf("store neighborhood context: %w", err) }
	return nil
}

func (c *Catalog) MarkDirty(ctx context.Context, partitionKey, reason, observedFingerprint string) error {
	if strings.TrimSpace(partitionKey) == "" || strings.TrimSpace(reason) == "" { return errors.New("partition key and reason are required") }
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO dirty_partitions(partition_key, reason, observed_fingerprint, marked_at_unix_ms) VALUES (?, ?, ?, ?)
		ON CONFLICT(partition_key) DO UPDATE SET reason = excluded.reason, observed_fingerprint = excluded.observed_fingerprint, marked_at_unix_ms = excluded.marked_at_unix_ms
	`, partitionKey, reason, observedFingerprint, time.Now().UTC().UnixMilli())
	if err != nil { return fmt.Errorf("mark dirty partition %q: %w", partitionKey, err) }
	return nil
}

func (c *Catalog) ClearDirty(ctx context.Context, partitionKey string) error {
	if partitionKey == "" { return errors.New("partition key is required") }
	if _, err := c.db.ExecContext(ctx, `DELETE FROM dirty_partitions WHERE partition_key = ?`, partitionKey); err != nil { return fmt.Errorf("clear dirty partition %q: %w", partitionKey, err) }
	return nil
}

func (c *Catalog) DirtyPartitions(ctx context.Context) ([]DirtyPartition, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT partition_key, reason, observed_fingerprint, marked_at_unix_ms FROM dirty_partitions ORDER BY partition_key`)
	if err != nil { return nil, fmt.Errorf("list dirty partitions: %w", err) }
	defer rows.Close()
	var partitions []DirtyPartition
	for rows.Next() {
		var partition DirtyPartition; var markedMS int64
		if err := rows.Scan(&partition.PartitionKey, &partition.Reason, &partition.ObservedFingerprint, &markedMS); err != nil { return nil, fmt.Errorf("scan dirty partition: %w", err) }
		partition.MarkedAt = time.UnixMilli(markedMS).UTC(); partitions = append(partitions, partition)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("list dirty partitions: %w", err) }
	return partitions, nil
}

func (c *Catalog) Promote(ctx context.Context, generationID, currentRoutingStateHash string) error {
	if generationID == "" || currentRoutingStateHash == "" { return errors.New("generation id and current routing state hash are required") }
	if err := c.CheckIntegrity(ctx); err != nil { return err }
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil { return fmt.Errorf("begin generation promotion: %w", err) }
	defer tx.Rollback()
	var status, generationHash string
	if err := tx.QueryRowContext(ctx, `SELECT status, routing_state_hash FROM generations WHERE generation_id = ?`, generationID).Scan(&status, &generationHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return ErrGenerationNotFound }
		return fmt.Errorf("load promotion generation: %w", err)
	}
	if GenerationStatus(status) != GenerationStaging { return ErrGenerationNotStaging }
	if generationHash != currentRoutingStateHash { return fmt.Errorf("%w: generation=%s current=%s", ErrRoutingStateChanged, generationHash, currentRoutingStateHash) }
	var dirty int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dirty_partitions`).Scan(&dirty); err != nil { return fmt.Errorf("count dirty partitions: %w", err) }
	if dirty != 0 { return fmt.Errorf("%w: %d partition(s)", ErrDirtyPartitions, dirty) }
	var incomplete int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM generation_members m
		LEFT JOIN source_tools s ON s.generation_id = m.generation_id AND s.server_id = m.server_id AND s.tool_name = m.tool_name
		WHERE m.generation_id = ? AND m.required = 1 AND (m.complete = 0 OR m.actual_source_fingerprint IS NULL OR s.source_fingerprint IS NULL)
	`, generationID).Scan(&incomplete); err != nil { return fmt.Errorf("validate generation members: %w", err) }
	if incomplete != 0 { return fmt.Errorf("%w: %d required source member(s)", ErrGenerationIncomplete, incomplete) }
	var sourceMismatch int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM generation_members m
		JOIN source_tools s ON s.generation_id = m.generation_id AND s.server_id = m.server_id AND s.tool_name = m.tool_name
		WHERE m.generation_id = ? AND m.required = 1 AND (m.expected_source_fingerprint <> m.actual_source_fingerprint OR m.actual_source_fingerprint <> s.source_fingerprint)
	`, generationID).Scan(&sourceMismatch); err != nil { return fmt.Errorf("validate source member fingerprints: %w", err) }
	if sourceMismatch != 0 { return fmt.Errorf("%w: %d required source member(s)", ErrSourceFingerprintMismatch, sourceMismatch) }
	if err := verifySourcePayloads(ctx, tx, generationID); err != nil { return err }
	var dependencyFailures int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_dependencies WHERE generation_id = ? AND required = 1 AND (complete = 0 OR actual_fingerprint IS NULL OR expected_fingerprint <> actual_fingerprint)`, generationID).Scan(&dependencyFailures); err != nil { return fmt.Errorf("validate generation dependencies: %w", err) }
	if dependencyFailures != 0 { return fmt.Errorf("%w: %d required generation dependency(s)", ErrDependencyMismatch, dependencyFailures) }
	var artifactFailures int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_artifacts WHERE generation_id = ? AND required = 1 AND complete = 0`, generationID).Scan(&artifactFailures); err != nil { return fmt.Errorf("validate generation artifacts: %w", err) }
	if artifactFailures != 0 { return fmt.Errorf("%w: %d required generation artifact(s)", ErrGenerationIncomplete, artifactFailures) }
	sourceSetFingerprint, err := sourceSetFingerprint(ctx, tx, generationID); if err != nil { return err }
	if _, err := tx.ExecContext(ctx, `UPDATE generations SET status = 'superseded' WHERE status = 'active'`); err != nil { return fmt.Errorf("supersede previous active generation: %w", err) }
	result, err := tx.ExecContext(ctx, `UPDATE generations SET status = 'active', source_set_fingerprint = ?, activated_at_unix_ms = ? WHERE generation_id = ? AND status = 'staging'`, sourceSetFingerprint, time.Now().UTC().UnixMilli(), generationID)
	if err != nil { return fmt.Errorf("activate generation: %w", err) }
	rows, err := result.RowsAffected(); if err != nil { return fmt.Errorf("activate generation rows affected: %w", err) }
	if rows != 1 { return ErrGenerationNotStaging }
	if err := tx.Commit(); err != nil { return fmt.Errorf("commit generation promotion: %w", err) }
	return nil
}

func (c *Catalog) ActiveCurrent(ctx context.Context, currentRoutingStateHash string) (Generation, bool, error) {
	if err := c.CheckIntegrity(ctx); err != nil { return Generation{}, false, err }
	generation, err := c.ActiveGeneration(ctx)
	if err != nil {
		if errors.Is(err, ErrGenerationNotFound) { return Generation{}, false, nil }
		return Generation{}, false, err
	}
	if generation.RoutingStateHash != currentRoutingStateHash || generation.SourceSetFingerprint == "" { return generation, false, nil }
	var dirty int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dirty_partitions`).Scan(&dirty); err != nil { return Generation{}, false, fmt.Errorf("count dirty partitions: %w", err) }
	if dirty != 0 { return generation, false, nil }
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}); if err != nil { return Generation{}, false, fmt.Errorf("begin active integrity check: %w", err) }
	defer tx.Rollback()
	if err := verifySourcePayloads(ctx, tx, generation.ID); err != nil { return Generation{}, false, err }
	fingerprint, err := sourceSetFingerprint(ctx, tx, generation.ID); if err != nil { return Generation{}, false, err }
	if err := tx.Commit(); err != nil { return Generation{}, false, fmt.Errorf("finish active integrity check: %w", err) }
	return generation, fingerprint == generation.SourceSetFingerprint, nil
}

func (c *Catalog) requireStaging(ctx context.Context, generationID string) error {
	var status string
	if err := c.db.QueryRowContext(ctx, `SELECT status FROM generations WHERE generation_id = ?`, generationID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) { return ErrGenerationNotFound }
		return fmt.Errorf("load generation status: %w", err)
	}
	if GenerationStatus(status) != GenerationStaging { return ErrGenerationNotStaging }
	return nil
}

func verifySourcePayloads(ctx context.Context, tx *sql.Tx, generationID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT server_id, tool_name, source_fingerprint, contract_json FROM source_tools WHERE generation_id = ?`, generationID)
	if err != nil { return fmt.Errorf("read authoritative source payloads: %w", err) }
	defer rows.Close()
	for rows.Next() {
		var serverID, toolName, storedFingerprint string; var contractJSON []byte
		if err := rows.Scan(&serverID, &toolName, &storedFingerprint, &contractJSON); err != nil { return fmt.Errorf("scan authoritative source payload: %w", err) }
		if actual := toolcontract.FingerprintJSON(contractJSON); actual != storedFingerprint { return fmt.Errorf("%w: stored contract %s/%s has fingerprint %s, content hashes to %s", ErrSourceFingerprintMismatch, serverID, toolName, storedFingerprint, actual) }
	}
	if err := rows.Err(); err != nil { return fmt.Errorf("read authoritative source payloads: %w", err) }
	return nil
}

func sourceSetFingerprint(ctx context.Context, tx *sql.Tx, generationID string) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT server_id, tool_name, source_fingerprint FROM source_tools WHERE generation_id = ? ORDER BY server_id, tool_name`, generationID)
	if err != nil { return "", fmt.Errorf("read source set for fingerprint: %w", err) }
	defer rows.Close()
	type item struct { serverID, toolName, fingerprint string }
	var items []item
	for rows.Next() { var entry item; if err := rows.Scan(&entry.serverID, &entry.toolName, &entry.fingerprint); err != nil { return "", fmt.Errorf("scan source set member: %w", err) }; items = append(items, entry) }
	if err := rows.Err(); err != nil { return "", fmt.Errorf("read source set for fingerprint: %w", err) }
	sort.Slice(items, func(i, j int) bool { if items[i].serverID == items[j].serverID { return items[i].toolName < items[j].toolName }; return items[i].serverID < items[j].serverID })
	h := sha256.New()
	for _, entry := range items { _, _ = h.Write([]byte(entry.serverID)); _, _ = h.Write([]byte{0}); _, _ = h.Write([]byte(entry.toolName)); _, _ = h.Write([]byte{0}); _, _ = h.Write([]byte(entry.fingerprint)); _, _ = h.Write([]byte{'\n'}) }
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
