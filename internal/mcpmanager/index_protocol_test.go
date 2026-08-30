package mcpmanager

import (
	"encoding/json"
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
		RequestJSON:        json.RawMessage(`{"protocol":"capability-reconciliation/v1","items":[]}`),
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
	if len(projected.AgentInstructions) == 0 {
		t.Fatal("agent instructions were not injected")
	}
	body, err := json.Marshal(projected.ResponseSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(body) {
		t.Fatalf("injected response schema is not valid JSON: %s", body)
	}
}

func TestProjectIndexBatchRejectsProtocolMismatch(t *testing.T) {
	batch := catalog.EnrichmentBatch{
		ID:           "batch:test",
		GenerationID: "gen",
		Kind:         catalog.BatchCapabilityReconciliation,
		BatchKey:     "global",
		RequestJSON:  json.RawMessage(`{"protocol":"tool-enrichment/v1","items":[]}`),
	}
	if _, err := projectIndexBatch(batch); err == nil {
		t.Fatal("expected protocol mismatch to fail closed")
	}
}
