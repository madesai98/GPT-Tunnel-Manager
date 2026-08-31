package app

import "testing"

func TestMergeRoutingTargetsPrefersLiveMembershipAndPreservesIndexedFingerprints(t *testing.T) {
	indexed := []V2RoutingTarget{
		{ServerID: "srv_a", ToolName: "bootstrap", AssumptionFingerprint: "fp-bootstrap"},
		{ServerID: "srv_a", ToolName: "removed", AssumptionFingerprint: "fp-removed"},
		{ServerID: "srv_b", ToolName: "offline", AssumptionFingerprint: "fp-offline"},
	}
	live := map[string][]string{
		"srv_a": {"bootstrap", "new_one", "new_two"},
	}

	got := mergeRoutingTargets(indexed, live)
	if len(got) != 4 {
		t.Fatalf("merged targets = %d, want 4: %#v", len(got), got)
	}

	want := []V2RoutingTarget{
		{ServerID: "srv_a", ToolName: "bootstrap", AssumptionFingerprint: "fp-bootstrap"},
		{ServerID: "srv_a", ToolName: "new_one"},
		{ServerID: "srv_a", ToolName: "new_two"},
		{ServerID: "srv_b", ToolName: "offline", AssumptionFingerprint: "fp-offline"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("target[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}
