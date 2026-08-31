//go:build !nogui

package main

import (
	"testing"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
)

func TestLiveOnlyRoutingTargetStateIsMarkedForRefresh(t *testing.T) {
	target := v2RouteToolState{preference: "NEW · REFRESH INDEX"}
	if got := v2WorkspaceToolLabel(target, false); got != "NEW · REFRESH INDEX" {
		t.Fatalf("tool label = %q, want live refresh marker", got)
	}
}

func TestCapabilityExplorerAggregatesDescendantTools(t *testing.T) {
	targets := []coreapp.V2RoutingTarget{
		{ServerID: "srv_a", ToolName: "activate_app", AssumptionFingerprint: "fp1"},
		{ServerID: "srv_a", ToolName: "screenshot", AssumptionFingerprint: "fp2"},
		{ServerID: "srv_b", ToolName: "webmcp_open_page", AssumptionFingerprint: "fp3"},
	}
	hierarchy := enrichment.CapabilityHierarchy{Capabilities: []enrichment.CapabilityNode{
		{ID: "desktop", Name: "Desktop", ToolMembers: []string{"srv_a/activate_app"}},
		{ID: "visual", ParentID: "desktop", Name: "Visual", ToolMembers: []string{"srv_a/screenshot"}},
		{ID: "browser", Name: "Browser", ToolMembers: []string{"srv_b/webmcp_open_page"}},
	}}
	groups := v2ExplorerCapabilityGroups(targets, hierarchy)
	var desktop, visual v2RoutingExplorerGroup
	for _, group := range groups {
		switch group.Key {
		case "cap:desktop":
			desktop = group
		case "cap:visual":
			visual = group
		}
	}
	if len(desktop.Members) != 2 {
		t.Fatalf("desktop aggregate members = %d, want 2", len(desktop.Members))
	}
	if len(visual.Members) != 1 || visual.Depth != 1 {
		t.Fatalf("visual group = members %d depth %d, want 1/1", len(visual.Members), visual.Depth)
	}
	filtered := v2ExplorerFilteredTargets(targets, desktop, map[string]string{"srv_a": "Computer"}, map[string]v2RouteToolState{}, "screen", false)
	if len(filtered) != 1 || filtered[0].ToolName != "screenshot" {
		t.Fatalf("filtered tools = %+v, want screenshot", filtered)
	}
}

func TestCapabilityExplorerAttentionCounts(t *testing.T) {
	targets := []coreapp.V2RoutingTarget{
		{ServerID: "srv", ToolName: "new_tool"},
		{ServerID: "srv", ToolName: "agent_tool", AssumptionFingerprint: "fp1"},
		{ServerID: "srv", ToolName: "review_tool", AssumptionFingerprint: "fp2"},
		{ServerID: "srv", ToolName: "ready_tool", AssumptionFingerprint: "fp3"},
	}
	states := map[string]v2RouteToolState{
		"srv\x00agent_tool":  {agent: "TOOL ENRICHMENT"},
		"srv\x00review_tool": {needsReview: true},
	}
	counts := v2ExplorerCounts(targets, states)
	if counts.New != 1 || counts.Agent != 1 || counts.Review != 1 || counts.Ready != 1 {
		t.Fatalf("counts = %+v, want one in every bucket", counts)
	}
	filtered := v2ExplorerFilteredTargets(targets, v2RoutingExplorerGroup{Key: "all"}, nil, states, "", true)
	if len(filtered) != 3 {
		t.Fatalf("attention-only tools = %d, want 3", len(filtered))
	}
}
