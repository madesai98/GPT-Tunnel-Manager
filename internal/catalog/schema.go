package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const SchemaVersion = 2

var (
	ErrCatalogCorrupt            = errors.New("catalog is corrupt")
	ErrUnsupportedSchema         = errors.New("catalog schema version is newer than this build")
	ErrGenerationNotFound        = errors.New("generation not found")
	ErrGenerationNotStaging      = errors.New("generation is not staging")
	ErrRoutingStateChanged       = errors.New("routing state changed")
	ErrDirtyPartitions           = errors.New("dirty routing partitions remain")
	ErrGenerationIncomplete      = errors.New("generation is incomplete")
	ErrSourceFingerprintMismatch = errors.New("source fingerprint mismatch")
	ErrDependencyMismatch        = errors.New("dependency mismatch")
)

const schemaSQL = `
CREATE TABLE routing_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    routing_revision TEXT NOT NULL,
    routing_state_hash TEXT NOT NULL,
    preference_revision TEXT NOT NULL
);
INSERT INTO routing_state(singleton, routing_revision, routing_state_hash, preference_revision)
VALUES (1, '0', '', '0');

CREATE TABLE generations (
    generation_id TEXT PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('staging', 'active', 'superseded')),
    routing_state_hash TEXT NOT NULL,
    source_set_fingerprint TEXT NOT NULL DEFAULT '',
    created_at_unix_ms INTEGER NOT NULL,
    activated_at_unix_ms INTEGER
);
CREATE UNIQUE INDEX generations_one_active
ON generations(status) WHERE status = 'active';
CREATE INDEX generations_status_created
ON generations(status, created_at_unix_ms DESC);

CREATE TABLE source_servers (
    generation_id TEXT NOT NULL,
    server_id TEXT NOT NULL,
    source_fingerprint TEXT NOT NULL,
    contract_json BLOB NOT NULL,
    PRIMARY KEY (generation_id, server_id),
    FOREIGN KEY (generation_id) REFERENCES generations(generation_id) ON DELETE CASCADE
);

CREATE TABLE generation_members (
    generation_id TEXT NOT NULL,
    server_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    expected_source_fingerprint TEXT NOT NULL,
    actual_source_fingerprint TEXT,
    required INTEGER NOT NULL DEFAULT 1 CHECK (required IN (0, 1)),
    complete INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0, 1)),
    PRIMARY KEY (generation_id, server_id, tool_name),
    FOREIGN KEY (generation_id) REFERENCES generations(generation_id) ON DELETE CASCADE
);

CREATE TABLE source_tools (
    generation_id TEXT NOT NULL,
    server_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    source_fingerprint TEXT NOT NULL,
    invocation_identity_json BLOB NOT NULL,
    contract_json BLOB NOT NULL,
    PRIMARY KEY (generation_id, server_id, tool_name),
    FOREIGN KEY (generation_id, server_id, tool_name)
        REFERENCES generation_members(generation_id, server_id, tool_name) ON DELETE CASCADE
);

CREATE TABLE dirty_partitions (
    partition_key TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    observed_fingerprint TEXT NOT NULL DEFAULT '',
    marked_at_unix_ms INTEGER NOT NULL
);

CREATE TABLE generation_dependencies (
    generation_id TEXT NOT NULL,
    dependency_key TEXT NOT NULL,
    expected_fingerprint TEXT NOT NULL,
    actual_fingerprint TEXT,
    required INTEGER NOT NULL DEFAULT 1 CHECK (required IN (0, 1)),
    complete INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0, 1)),
    PRIMARY KEY (generation_id, dependency_key),
    FOREIGN KEY (generation_id) REFERENCES generations(generation_id) ON DELETE CASCADE
);

CREATE TABLE artifacts (
    artifact_key TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    content_fingerprint TEXT NOT NULL,
    dependency_fingerprint TEXT NOT NULL,
    context_fingerprint TEXT NOT NULL DEFAULT '',
    payload BLOB NOT NULL,
    created_at_unix_ms INTEGER NOT NULL
);
CREATE INDEX artifacts_kind_content
ON artifacts(kind, content_fingerprint);

CREATE TABLE artifact_dependencies (
    artifact_key TEXT NOT NULL,
    dependency_key TEXT NOT NULL,
    dependency_fingerprint TEXT NOT NULL,
    PRIMARY KEY (artifact_key, dependency_key),
    FOREIGN KEY (artifact_key) REFERENCES artifacts(artifact_key) ON DELETE CASCADE
);

CREATE TABLE generation_artifacts (
    generation_id TEXT NOT NULL,
    role TEXT NOT NULL,
    member_key TEXT NOT NULL,
    artifact_key TEXT NOT NULL,
    required INTEGER NOT NULL DEFAULT 1 CHECK (required IN (0, 1)),
    complete INTEGER NOT NULL DEFAULT 1 CHECK (complete IN (0, 1)),
    PRIMARY KEY (generation_id, role, member_key),
    FOREIGN KEY (generation_id) REFERENCES generations(generation_id) ON DELETE CASCADE,
    FOREIGN KEY (artifact_key) REFERENCES artifacts(artifact_key)
);

CREATE TABLE neighborhood_contexts (
    generation_id TEXT NOT NULL,
    member_key TEXT NOT NULL,
    context_fingerprint TEXT NOT NULL,
    PRIMARY KEY (generation_id, member_key),
    FOREIGN KEY (generation_id) REFERENCES generations(generation_id) ON DELETE CASCADE
);

-- Phase 4 establishes storage boundaries for later retrieval without building
-- the lexical/FTS query pipeline yet.
CREATE TABLE lexical_records (
    generation_id TEXT NOT NULL,
    member_key TEXT NOT NULL,
    lexical_fingerprint TEXT NOT NULL,
    lexical_text TEXT NOT NULL,
    PRIMARY KEY (generation_id, member_key),
    FOREIGN KEY (generation_id) REFERENCES generations(generation_id) ON DELETE CASCADE
);

-- Routing preferences/profiles are deliberately not generation-owned. Their
-- revision is tracked in routing_state and later phases may populate these
-- payload-oriented rows without invalidating a semantic generation.
CREATE TABLE routing_profiles (
    profile_id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    payload_json BLOB NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL
);
CREATE TABLE routing_preferences (
    preference_id TEXT PRIMARY KEY,
    profile_id TEXT,
    target_key TEXT NOT NULL,
    assumption_fingerprint TEXT NOT NULL,
    review_state TEXT NOT NULL,
    payload_json BLOB NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL,
    FOREIGN KEY (profile_id) REFERENCES routing_profiles(profile_id) ON DELETE CASCADE
);

-- Task/resource continuation mappings are router-owned runtime state, not
-- semantic generation membership. Later phases define the payload contract.
CREATE TABLE continuation_mappings (
    mapping_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('task', 'resource')),
    server_id TEXT NOT NULL,
    payload BLOB NOT NULL,
    created_at_unix_ms INTEGER NOT NULL,
    expires_at_unix_ms INTEGER
);
`

const migrationV2SQL = `
CREATE TABLE source_servers (
    generation_id TEXT NOT NULL,
    server_id TEXT NOT NULL,
    source_fingerprint TEXT NOT NULL,
    contract_json BLOB NOT NULL,
    PRIMARY KEY (generation_id, server_id),
    FOREIGN KEY (generation_id) REFERENCES generations(generation_id) ON DELETE CASCADE
);
`

var requiredTables = []string{
	"routing_state",
	"generations",
	"source_servers",
	"generation_members",
	"source_tools",
	"dirty_partitions",
	"generation_dependencies",
	"artifacts",
	"artifact_dependencies",
	"generation_artifacts",
	"neighborhood_contexts",
	"lexical_records",
	"routing_profiles",
	"routing_preferences",
	"continuation_mappings",
}

func ensureSchema(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read catalog schema version: %w", err)
	}
	if version > SchemaVersion {
		return fmt.Errorf("%w: database=%d supported=%d", ErrUnsupportedSchema, version, SchemaVersion)
	}
	if version == 0 {
		var userTables int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		`).Scan(&userTables); err != nil {
			return fmt.Errorf("inspect unversioned catalog: %w", err)
		}
		if userTables != 0 {
			return fmt.Errorf("%w: unversioned catalog contains application tables", ErrCatalogCorrupt)
		}
		if err := applySchemaStep(ctx, db, schemaSQL, SchemaVersion, "initialize catalog schema"); err != nil {
			return err
		}
		version = SchemaVersion
	}
	if version == 1 {
		if err := applySchemaStep(ctx, db, migrationV2SQL, 2, "migrate catalog schema to v2"); err != nil {
			return err
		}
		version = 2
	}
	if version != SchemaVersion {
		return fmt.Errorf("%w: unsupported catalog schema version %d", ErrCatalogCorrupt, version)
	}
	for _, table := range requiredTables {
		var count int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?
		`, table).Scan(&count); err != nil {
			return fmt.Errorf("verify catalog table %s: %w", table, err)
		}
		if count != 1 {
			return fmt.Errorf("%w: required table %s is missing", ErrCatalogCorrupt, table)
		}
	}
	return nil
}

func applySchemaStep(ctx context.Context, db *sql.DB, statements string, targetVersion int, label string) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin %s transaction: %w", label, err)
	}
	if _, err := tx.ExecContext(ctx, statements); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("%s: %w", label, err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", targetVersion)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("set catalog schema version %d: %w", targetVersion, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", label, err)
	}
	return nil
}
