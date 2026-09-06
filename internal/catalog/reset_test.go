package catalog

import (
	"context"
	"path/filepath"
	"testing"
)

func TestClearIndexRemovesSemanticStateAndPreservesPreferences(t *testing.T) {
	ctx := context.Background()
	c, err := OpenPath(ctx, filepath.Join(t.TempDir(), "catalog.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	seed := []string{
		`INSERT INTO routing_profiles(profile_id, name, payload_json, updated_at_unix_ms) VALUES ('profile-1', 'Default', '{}', 1)`,
		`INSERT INTO routing_preferences(preference_id, profile_id, target_key, assumption_fingerprint, review_state, payload_json, updated_at_unix_ms) VALUES ('pref-1', 'profile-1', 'server/tool', 'assumption', 'active', '{}', 1)`,
		`INSERT INTO continuation_mappings(mapping_id, kind, server_id, payload, created_at_unix_ms) VALUES ('mapping-1', 'task', 'server', X'01', 1)`,
		`INSERT INTO generations(generation_id, status, routing_state_hash, source_set_fingerprint, created_at_unix_ms) VALUES ('gen-1', 'active', 'hash', 'sources', 1)`,
		`INSERT INTO source_servers(generation_id, server_id, source_fingerprint, contract_json) VALUES ('gen-1', 'server', 'server-fp', '{}')`,
		`INSERT INTO generation_members(generation_id, server_id, tool_name, expected_source_fingerprint, actual_source_fingerprint, required, complete) VALUES ('gen-1', 'server', 'tool', 'tool-fp', 'tool-fp', 1, 1)`,
		`INSERT INTO source_tools(generation_id, server_id, tool_name, source_fingerprint, invocation_identity_json, contract_json) VALUES ('gen-1', 'server', 'tool', 'tool-fp', '{}', '{}')`,
		`INSERT INTO generation_dependencies(generation_id, dependency_key, expected_fingerprint, actual_fingerprint, required, complete) VALUES ('gen-1', 'dep', 'dep-fp', 'dep-fp', 1, 1)`,
		`INSERT INTO artifacts(artifact_key, kind, content_fingerprint, dependency_fingerprint, context_fingerprint, payload, created_at_unix_ms) VALUES ('artifact-1', 'semantic.tool-enrichment/v1', 'content', 'dep', 'ctx', X'01', 1)`,
		`INSERT INTO artifact_dependencies(artifact_key, dependency_key, dependency_fingerprint) VALUES ('artifact-1', 'dep', 'dep-fp')`,
		`INSERT INTO generation_artifacts(generation_id, role, member_key, artifact_key, required, complete) VALUES ('gen-1', 'semantic.tool_enrichment', 'server/tool', 'artifact-1', 1, 1)`,
		`INSERT INTO neighborhood_contexts(generation_id, member_key, context_fingerprint) VALUES ('gen-1', 'server/tool', 'ctx')`,
		`INSERT INTO lexical_records(generation_id, member_key, lexical_fingerprint, lexical_text) VALUES ('gen-1', 'server/tool', 'lexical-fp', 'tool text')`,
		`INSERT INTO enrichment_batches(batch_id, generation_id, kind, batch_key, required, request_fingerprint, request_json, created_at_unix_ms) VALUES ('batch-1', 'gen-1', 'tool_enrichment', 'batch', 1, 'request-fp', '{}', 1)`,
		`INSERT INTO tool_contract_cache(server_id, tool_name, source_fingerprint, contract_json, available, last_seen_at_unix_ms) VALUES ('server', 'tool', 'tool-fp', '{}', 1, 1)`,
		`INSERT INTO dirty_partitions(partition_key, reason, observed_fingerprint, marked_at_unix_ms) VALUES ('server:server', 'changed', 'tool-fp', 1)`,
	}
	for _, statement := range seed {
		if _, err := c.DB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed catalog with %q: %v", statement, err)
		}
	}

	if err := c.ClearIndex(ctx); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{
		"generations",
		"source_servers",
		"generation_members",
		"source_tools",
		"generation_dependencies",
		"artifacts",
		"artifact_dependencies",
		"generation_artifacts",
		"neighborhood_contexts",
		"lexical_records",
		"enrichment_batches",
		"tool_contract_cache",
		"dirty_partitions",
	} {
		var count int
		if err := c.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("expected %s to be empty after clear, got %d row(s)", table, count)
		}
	}

	for table, want := range map[string]int{
		"routing_state":         1,
		"routing_profiles":      1,
		"routing_preferences":   1,
		"continuation_mappings": 1,
	} {
		var count int
		if err := c.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != want {
			t.Fatalf("expected %s to retain %d row(s), got %d", table, want, count)
		}
	}
}
