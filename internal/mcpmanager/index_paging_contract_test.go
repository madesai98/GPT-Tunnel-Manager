package mcpmanager

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestIndexGetEnrichmentBatchAdvertisesPagingParameters(t *testing.T) {
	fixture := newPhase10Fixture(t, false)
	session := connectPhase10Modern(t, fixture.endpoint)
	defer session.Close()

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list Manager tools: %v", err)
	}
	var target *mcp.Tool
	for _, tool := range listed.Tools {
		if tool != nil && tool.Name == "index_get_enrichment_batch" {
			target = tool
			break
		}
	}
	if target == nil {
		t.Fatal("index_get_enrichment_batch was not advertised")
	}
	body, err := json.Marshal(target.InputSchema)
	if err != nil {
		t.Fatalf("marshal advertised input schema: %v", err)
	}
	var schema struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties any                        `json:"additionalProperties"`
	}
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatalf("decode advertised input schema: %v", err)
	}
	for _, name := range []string{"kind", "limit", "request_offset", "request_item_limit"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("advertised index_get_enrichment_batch schema is missing %q: %s", name, body)
		}
	}
	if allowed, ok := schema.AdditionalProperties.(bool); !ok || allowed {
		t.Fatalf("advertised schema must reject unknown properties: %s", body)
	}
}

func TestResolveIndexGetBatchPagingSupportsStaleSchemaCursor(t *testing.T) {
	batchLimit, requestOffset := resolveIndexGetBatchPaging(indexGetBatchInput{
		Kind:  catalog.BatchCapabilityReconciliation,
		Limit: 16,
	})
	if batchLimit != 1 || requestOffset != 16 {
		t.Fatalf("stale-schema compatibility = batchLimit %d requestOffset %d, want 1/16", batchLimit, requestOffset)
	}

	batchLimit, requestOffset = resolveIndexGetBatchPaging(indexGetBatchInput{
		Kind:          catalog.BatchCapabilityReconciliation,
		Limit:         32,
		RequestOffset: 48,
	})
	if batchLimit != 1 || requestOffset != 48 {
		t.Fatalf("explicit request_offset must win = batchLimit %d requestOffset %d, want 1/48", batchLimit, requestOffset)
	}

	batchLimit, requestOffset = resolveIndexGetBatchPaging(indexGetBatchInput{
		Kind:  catalog.BatchToolEnrichment,
		Limit: 16,
	})
	if batchLimit != 16 || requestOffset != 0 {
		t.Fatalf("non-reconciliation limit semantics changed = batchLimit %d requestOffset %d", batchLimit, requestOffset)
	}
}

func TestCapabilityBatchInjectsStaleSchemaRecoveryInstruction(t *testing.T) {
	batch := catalog.EnrichmentBatch{
		ID:                 "batch:compat",
		GenerationID:       "gen",
		Kind:               catalog.BatchCapabilityReconciliation,
		BatchKey:           "global",
		Required:           true,
		RequestFingerprint: "sha256:compat",
		RequestJSON:        json.RawMessage(`{"protocol":"` + enrichment.CapabilityProtocolVersion + `","items":[{"tool":{"member_key":"srv/tool","server_id":"srv","tool_name":"tool","contract":{"name":"tool"}},"enrichment":{"purpose":"test"}}]}`),
	}
	projected, err := projectIndexBatchPage(batch, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(projected.AgentInstructions, "\n")
	if !strings.Contains(joined, "limit=request_page.next_offset") || !strings.Contains(joined, "cached an older") {
		t.Fatalf("missing stale-schema recovery instruction: %s", joined)
	}
}
