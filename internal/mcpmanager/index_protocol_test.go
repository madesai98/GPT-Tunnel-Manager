package mcpmanager

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
)

func TestProjectIndexBatchInjectsProtocolContract(t *testing.T) {
	batch := catalog.EnrichmentBatch{
		ID:                 "batch:test",
		GenerationID:       "gen",
		Kind:               catalog.BatchCapabilityReconciliation,
		BatchKey:           "global",
		Required:           true,
		RequestFingerprint: "sha256:test",
		RequestJSON:        json.RawMessage("{\"protocol\":\"capability-reconciliation/v1\",\"items\":[]}"),
	}
	projected, err := projectIndexBatch(batch)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Protocol != enrichment.CapabilityProtocolVersion {
		t.Fatalf("protocol = %q", projected.Protocol)
	}
	if projected.ResponseSchema == nil {
		t.Fatal("response schema was not injected")
	}
	if projected.ResponseSchemaJSON == "" || !json.Valid([]byte(projected.ResponseSchemaJSON)) {
		t.Fatalf("response schema JSON was not injected as valid JSON: %q", projected.ResponseSchemaJSON)
	}
	if len(projected.AgentInstructions) == 0 {
		t.Fatal("agent instructions were not injected")
	}
	foundPagingInstruction := false
	for _, instruction := range projected.AgentInstructions {
		if strings.Contains(instruction, "request_page") && strings.Contains(instruction, "next_offset") {
			foundPagingInstruction = true
			break
		}
	}
	if !foundPagingInstruction {
		t.Fatal("agent instructions do not explain request paging")
	}
	if projected.RequestPage == nil || !projected.RequestPage.Complete || projected.RequestPage.TotalItems != 0 {
		t.Fatalf("unexpected request page metadata: %+v", projected.RequestPage)
	}
}

func TestProjectIndexBatchRejectsProtocolMismatch(t *testing.T) {
	batch := catalog.EnrichmentBatch{
		ID:           "batch:test",
		GenerationID: "gen",
		Kind:         catalog.BatchCapabilityReconciliation,
		BatchKey:     "global",
		RequestJSON:  json.RawMessage("{\"protocol\":\"tool-enrichment/v1\",\"items\":[]}"),
	}
	if _, err := projectIndexBatch(batch); err == nil {
		t.Fatal("expected protocol mismatch to fail closed")
	}
}

func TestProjectIndexBatchPagesLargeReconciliationRequest(t *testing.T) {
	items := make([]map[string]any, 41)
	for i := range items {
		items[i] = map[string]any{
			"tool": map[string]any{
				"member_key": fmt.Sprintf("srv/tool_%02d", i),
				"server_id":  "srv",
				"tool_name":  fmt.Sprintf("tool_%02d", i),
				"contract":   map[string]any{"name": fmt.Sprintf("tool_%02d", i), "description": strings.Repeat("x", 128)},
			},
			"enrichment": map[string]any{"purpose": fmt.Sprintf("purpose %02d", i)},
		}
	}
	request, err := json.Marshal(map[string]any{
		"protocol": enrichment.CapabilityProtocolVersion,
		"items":    items,
	})
	if err != nil {
		t.Fatal(err)
	}
	batch := catalog.EnrichmentBatch{
		ID:                 "batch:large",
		GenerationID:       "gen",
		Kind:               catalog.BatchCapabilityReconciliation,
		BatchKey:           "global",
		Required:           true,
		RequestFingerprint: "sha256:immutable",
		RequestJSON:        request,
	}
	first, err := projectIndexBatchPage(batch, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if first.RequestPage == nil || first.RequestPage.Offset != 0 || first.RequestPage.Returned != 10 || first.RequestPage.TotalItems != 41 || first.RequestPage.Complete || first.RequestPage.NextOffset != 10 {
		t.Fatalf("unexpected first page: %+v", first.RequestPage)
	}
	firstItems, ok := first.Request["items"].([]any)
	if !ok || len(firstItems) != 10 {
		t.Fatalf("first request page items = %#v", first.Request["items"])
	}
	second, err := projectIndexBatchPage(batch, first.RequestPage.NextOffset, 10)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.RequestFingerprint != first.RequestFingerprint {
		t.Fatal("page identity changed across the same immutable batch")
	}
	if second.RequestPage == nil || second.RequestPage.Offset != 10 || second.RequestPage.NextOffset != 20 {
		t.Fatalf("unexpected second page: %+v", second.RequestPage)
	}
	last, err := projectIndexBatchPage(batch, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	if last.RequestPage == nil || !last.RequestPage.Complete || last.RequestPage.Returned != 1 || last.RequestPage.NextOffset != 0 {
		t.Fatalf("unexpected last page: %+v", last.RequestPage)
	}
}

func TestPageIndexRequestBoundsSerializedPayload(t *testing.T) {
	items := make([]any, 20)
	for i := range items {
		items[i] = map[string]any{"payload": strings.Repeat("z", 20*1024)}
	}
	request := map[string]any{"protocol": enrichment.CapabilityProtocolVersion, "items": items}
	paged, page, err := pageIndexRequest(request, 0, maxIndexRequestPageItems, "batch:bytes")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(paged)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > maxIndexRequestPageBytes {
		t.Fatalf("page size = %d, want <= %d", len(body), maxIndexRequestPageBytes)
	}
	if page == nil || page.Returned >= len(items) || page.Complete {
		t.Fatalf("expected byte bound to split request: %+v", page)
	}
}
