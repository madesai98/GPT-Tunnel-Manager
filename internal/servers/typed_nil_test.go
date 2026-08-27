package servers

import (
	"errors"
	"testing"
	"time"
)

func TestCombinedRuntimeTreatsTypedNilOwnedProcessAsAbsent(t *testing.T) {
	tunnel := newFakeRuntime(false)
	var owned *fakeOwned
	combined := newCombined(tunnel, owned, 50*time.Millisecond)

	select {
	case <-combined.Done():
		t.Fatalf("combined runtime finished before tunnel exit: %v", combined.Err())
	case <-time.After(50 * time.Millisecond):
	}

	tunnel.crash(errors.New("tunnel crashed"))
	select {
	case <-combined.Done():
	case <-time.After(time.Second):
		t.Fatal("combined runtime did not finish after tunnel exit")
	}

	if err := combined.Err(); err == nil || err.Error() != "tunnel crashed" {
		t.Fatalf("combined error = %v, want tunnel crashed", err)
	}
}
