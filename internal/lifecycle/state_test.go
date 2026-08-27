package lifecycle

import (
	"testing"
	"time"
)

func TestBackoffNeverRetriesAtZeroDelay(t *testing.T) {
	b := NewBackoff(1)
	bases := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	for i, base := range bases {
		got := b.Next()
		if got < base/2 || got > base {
			t.Fatalf("step %d delay=%s, want between %s and %s", i, got, base/2, base)
		}
	}
	got := b.Next()
	if got < 30*time.Second || got > 60*time.Second {
		t.Fatalf("capped delay=%s, want between 30s and 60s", got)
	}
}
