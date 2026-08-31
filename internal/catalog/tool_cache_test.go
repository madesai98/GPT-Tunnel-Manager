package catalog

import (
	"context"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestObserveServerToolsSeparatesAvailabilityFromSemanticChange(t *testing.T) {
	ctx := context.Background()
	c := openTestCatalog(t)
	entry := v2config.ServerEntry{
		ID:   "srv_bridge",
		Name: "Bridge",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{Type: v2config.TransportStdio},
	}
	original := &mcp.Tool{Name: "bridge_tool", Description: "original", InputSchema: map[string]any{"type": "object"}}
	if _, err := c.CreateStaging(ctx, GenerationSpec{ID: "gen", RoutingStateHash: "sha256:routing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceServer(ctx, "gen", entry); err != nil {
		t.Fatal(err)
	}
	originalFP, _, err := toolcontract.FingerprintTool(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireSourceTool(ctx, "gen", entry.ID, original.Name, originalFP); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceTool(ctx, "gen", entry.ID, original, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Promote(ctx, "gen", "sha256:routing"); err != nil {
		t.Fatal(err)
	}

	result, err := c.ObserveServerTools(ctx, entry.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.SemanticChanged {
		t.Fatal("temporary disappearance was treated as semantic change")
	}
	if !result.AvailabilityChanged {
		t.Fatal("temporary disappearance did not update availability")
	}
	available, known, err := c.ToolAvailability(ctx, entry.ID, original.Name, originalFP)
	if err != nil {
		t.Fatal(err)
	}
	if !known || available {
		t.Fatalf("missing cached tool availability = available:%v known:%v", available, known)
	}

	result, err = c.ObserveServerTools(ctx, entry.ID, []*mcp.Tool{original})
	if err != nil {
		t.Fatal(err)
	}
	if result.SemanticChanged {
		t.Fatal("unchanged reappearance was treated as semantic change")
	}
	available, known, err = c.ToolAvailability(ctx, entry.ID, original.Name, originalFP)
	if err != nil {
		t.Fatal(err)
	}
	if !known || !available {
		t.Fatalf("reappeared cached tool availability = available:%v known:%v", available, known)
	}

	changed := &mcp.Tool{Name: original.Name, Description: "changed", InputSchema: map[string]any{"type": "object"}}
	changedFP, _, err := toolcontract.FingerprintTool(changed)
	if err != nil {
		t.Fatal(err)
	}
	result, err = c.ObserveServerTools(ctx, entry.ID, []*mcp.Tool{changed})
	if err != nil {
		t.Fatal(err)
	}
	if !result.SemanticChanged {
		t.Fatal("changed tool contract did not trigger semantic change")
	}
	available, known, err = c.ToolAvailability(ctx, entry.ID, original.Name, originalFP)
	if err != nil {
		t.Fatal(err)
	}
	if !known || available {
		t.Fatalf("stale fingerprint remained available = available:%v known:%v", available, known)
	}
	available, known, err = c.ToolAvailability(ctx, entry.ID, changed.Name, changedFP)
	if err != nil {
		t.Fatal(err)
	}
	if !known || !available {
		t.Fatalf("changed fingerprint availability = available:%v known:%v", available, known)
	}
}

func TestObserveServerToolsBeforeFirstGenerationDoesNotCreateSemanticInvalidation(t *testing.T) {
	c := openTestCatalog(t)
	tool := &mcp.Tool{Name: "first_tool", InputSchema: map[string]any{"type": "object"}}
	result, err := c.ObserveServerTools(context.Background(), "srv_first", []*mcp.Tool{tool})
	if err != nil {
		t.Fatal(err)
	}
	if result.SemanticChanged {
		t.Fatal("first observation before any authoritative generation was treated as drift")
	}
	cached, err := c.CachedTools(context.Background(), "srv_first")
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 || !cached[0].Available || cached[0].ToolName != tool.Name {
		t.Fatalf("cached first observation = %#v", cached)
	}
}
