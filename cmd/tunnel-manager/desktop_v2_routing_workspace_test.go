//go:build !nogui

package main

import "testing"

func TestLiveOnlyRoutingTargetStateIsMarkedForRefresh(t *testing.T) {
	target := v2RouteToolState{preference: "NEW · REFRESH INDEX"}
	if got := v2WorkspaceToolLabel(target, false); got != "NEW · REFRESH INDEX" {
		t.Fatalf("tool label = %q, want live refresh marker", got)
	}
}
