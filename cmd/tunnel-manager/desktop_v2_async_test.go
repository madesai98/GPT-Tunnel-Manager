//go:build !nogui

package main

import (
	"testing"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
)

func TestV2CachedFilteredTargetsInvalidatesByRevision(t *testing.T) {
	u := &v2DesktopUI{}
	defer v2UIAsyncStates.Delete(u)

	prepared := v2RoutingPrepared{
		Revision: 1,
		Targets: []coreapp.V2RoutingTarget{
			{ServerID: "alpha", ToolName: "alpha_tool", AssumptionFingerprint: "a"},
			{ServerID: "beta", ToolName: "beta_tool", AssumptionFingerprint: "b"},
		},
		ServerNames: map[string]string{"alpha": "Alpha", "beta": "Beta"},
		States:      map[string]v2RouteToolState{},
	}
	group := v2RoutingExplorerGroup{Key: "all"}

	got := v2CachedFilteredTargets(u, prepared, group, "beta", false)
	if len(got) != 1 || got[0].ToolName != "beta_tool" {
		t.Fatalf("expected beta_tool, got %#v", got)
	}

	prepared.Revision = 2
	prepared.Targets = []coreapp.V2RoutingTarget{
		{ServerID: "alpha", ToolName: "alpha_tool", AssumptionFingerprint: "a"},
	}
	got = v2CachedFilteredTargets(u, prepared, group, "beta", false)
	if len(got) != 0 {
		t.Fatalf("expected revision change to invalidate cached result, got %#v", got)
	}
}

func TestV2CachedFilteredTargetsAttentionOnly(t *testing.T) {
	u := &v2DesktopUI{}
	defer v2UIAsyncStates.Delete(u)

	ready := coreapp.V2RoutingTarget{ServerID: "server", ToolName: "ready", AssumptionFingerprint: "ready-fp"}
	newTool := coreapp.V2RoutingTarget{ServerID: "server", ToolName: "new_tool"}
	prepared := v2RoutingPrepared{
		Revision:    1,
		Targets:     []coreapp.V2RoutingTarget{newTool, ready},
		ServerNames: map[string]string{"server": "Server"},
		States:      map[string]v2RouteToolState{},
	}

	got := v2CachedFilteredTargets(u, prepared, v2RoutingExplorerGroup{Key: "all"}, "", true)
	if len(got) != 1 || got[0].ToolName != "new_tool" {
		t.Fatalf("expected only new tool to require attention, got %#v", got)
	}
}

func TestV2PostUIDrainsOnUIThreadBoundary(t *testing.T) {
	u := &v2DesktopUI{}
	defer v2UIAsyncStates.Delete(u)

	runs := 0
	u.postUI(func() { runs++ })
	if runs != 0 {
		t.Fatalf("callback ran before UI drain")
	}
	u.drainUI()
	if runs != 1 {
		t.Fatalf("expected one callback after drain, got %d", runs)
	}
	u.drainUI()
	if runs != 1 {
		t.Fatalf("callback ran more than once")
	}
}
