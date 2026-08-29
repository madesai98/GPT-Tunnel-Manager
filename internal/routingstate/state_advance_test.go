package routingstate

import (
	"context"
	"testing"
)

func TestAdvanceRoutingRevisionDoesNotChangeHashOrPreferenceRevision(t *testing.T) {
	backend := NewMemoryBackend(Snapshot{
		RoutingRevision:    4,
		RoutingStateHash:   "sha256:stable",
		PreferenceRevision: 7,
	})
	tracker, err := NewTracker(backend)
	if err != nil {
		t.Fatal(err)
	}
	state, err := tracker.AdvanceRoutingRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.RoutingRevision != 5 || state.RoutingStateHash != "sha256:stable" || state.PreferenceRevision != 7 {
		t.Fatalf("unexpected state after runtime routing advance: %#v", state)
	}
}
