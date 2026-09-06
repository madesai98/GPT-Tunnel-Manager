package catalog

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestObserveServerToolsRepairsWebMCPSourceDecorationChurn(t *testing.T) {
	ctx := context.Background()
	c := openTestCatalog(t)
	entry := v2config.ServerEntry{
		ID:   "srv_skill_tree",
		Name: "Skill Tree Builder",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type: v2config.TransportStdio,
		},
	}
	activeTool := &mcp.Tool{
		Name:        "skill_tree_list_skills",
		Description: "[WebMCP tab-old • Skill Tree Maker] List skills in the current project.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "gen_active", RoutingStateHash: "sha256:routing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceServer(ctx, "gen_active", entry); err != nil {
		t.Fatal(err)
	}
	activeFP, activeBody, err := toolcontract.FingerprintTool(activeTool)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireSourceTool(ctx, "gen_active", entry.ID, activeTool.Name, activeFP); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceTool(ctx, "gen_active", entry.ID, activeTool, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Promote(ctx, "gen_active", "sha256:routing"); err != nil {
		t.Fatal(err)
	}

	// Simulate the pre-fix behavior: a browser reconnect changed only the relay
	// source label and overwrote the persistent cache with a new exact fingerprint.
	reconnected := &mcp.Tool{
		Name:        activeTool.Name,
		Description: "[WebMCP tab-new • Skill Tree Maker] List skills in the current project.",
		InputSchema: activeTool.InputSchema,
	}
	reconnectedFP, reconnectedBody, err := toolcontract.FingerprintTool(reconnected)
	if err != nil {
		t.Fatal(err)
	}
	if reconnectedFP == activeFP {
		t.Fatal("test setup did not change the exact source fingerprint")
	}
	if _, err := c.db.ExecContext(ctx, `
		INSERT INTO tool_contract_cache(
			server_id, tool_name, source_fingerprint, contract_json, available, last_seen_at_unix_ms
		) VALUES (?, ?, ?, ?, 1, ?)
	`, entry.ID, activeTool.Name, reconnectedFP, reconnectedBody, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	result, err := c.ObserveServerTools(ctx, entry.ID, []*mcp.Tool{reconnected})
	if err != nil {
		t.Fatal(err)
	}
	if result.SemanticChanged {
		t.Fatal("WebMCP source-label churn was treated as a semantic tool change")
	}
	cached, err := c.CachedTools(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 {
		t.Fatalf("cached tool count = %d, want 1", len(cached))
	}
	if cached[0].SourceFingerprint != activeFP {
		t.Fatalf("cache fingerprint = %s, want authoritative active fingerprint %s", cached[0].SourceFingerprint, activeFP)
	}
	if !bytes.Equal(cached[0].ContractJSON, activeBody) {
		t.Fatal("cache did not restore the authoritative exact contract")
	}
	available, known, err := c.ToolAvailability(ctx, entry.ID, activeTool.Name, activeFP)
	if err != nil {
		t.Fatal(err)
	}
	if !known || !available {
		t.Fatalf("authoritative tool availability = available:%v known:%v", available, known)
	}

	changed := &mcp.Tool{
		Name:        activeTool.Name,
		Description: "[WebMCP tab-next • Skill Tree Maker] List skills and prerequisite details in the current project.",
		InputSchema: activeTool.InputSchema,
	}
	changedFP, _, err := toolcontract.FingerprintTool(changed)
	if err != nil {
		t.Fatal(err)
	}
	result, err = c.ObserveServerTools(ctx, entry.ID, []*mcp.Tool{changed})
	if err != nil {
		t.Fatal(err)
	}
	if !result.SemanticChanged {
		t.Fatal("real description change was hidden by WebMCP normalization")
	}
	cached, err = c.CachedTools(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 || cached[0].SourceFingerprint != changedFP {
		t.Fatalf("changed semantic contract was not stored: %#v", cached)
	}
}
