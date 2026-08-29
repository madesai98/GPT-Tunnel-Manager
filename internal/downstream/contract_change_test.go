package downstream

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestToolContractChangeCallbackRunsOnce(t *testing.T) {
	var calls atomic.Int32
	var gotServerID string
	s := &Session{
		serverID:            "srv_50000000000000000000000000000001",
		toolContractChanged: &atomic.Bool{},
		notifyOnce:          &sync.Once{},
		onToolChanged: func(serverID string) {
			calls.Add(1)
			gotServerID = serverID
		},
	}

	s.markToolContractChanged()
	s.markToolContractChanged()

	if !s.ToolContractChanged() {
		t.Fatal("session did not record tool-contract invalidation")
	}
	if calls.Load() != 1 {
		t.Fatalf("tool-contract invalidation callback calls = %d, want 1", calls.Load())
	}
	if gotServerID != s.serverID {
		t.Fatalf("tool-contract invalidation callback server = %q, want %q", gotServerID, s.serverID)
	}
}
