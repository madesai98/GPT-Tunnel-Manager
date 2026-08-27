package servers

import (
	"context"
	"errors"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
)

func TestAlwaysOnStartupCrashGuardKeepsNextBootRecoverable(t *testing.T) {
	entry := validEntry(config.ModeAlwaysOn, true)
	store := config.NewStore(t.TempDir())
	factory := &fakeFactory{}
	r := NewRegistry(context.Background(), store, config.ServersConfig{
		SchemaVersion: config.SchemaVersion,
		Servers:       []config.ServerEntry{entry},
	}, 300, factory, nil)

	// Simulate a prior Manager process dying after recording that this exact
	// server was entering startup but before startup returned.
	if err := r.writeStartupGuard(entry.ID); err != nil {
		t.Fatal(err)
	}

	snapshot, err := r.Start(context.Background(), entry.ID, SourceApp)
	if !errors.Is(err, ErrStartupCrashQuarantined) {
		t.Fatalf("err=%v, want ErrStartupCrashQuarantined", err)
	}
	if factory.count() != 0 {
		t.Fatalf("factory starts=%d, want 0 for quarantined automatic boot", factory.count())
	}
	if snapshot.LastError == nil || snapshot.LastError.Code != "startup_crash_quarantined" {
		t.Fatalf("last error=%+v", snapshot.LastError)
	}

	// An explicit user start is the recovery action. It is allowed to try the
	// server again and a normal return clears the persisted crash guard.
	if _, err := r.Start(context.Background(), entry.ID, SourceUI); err != nil {
		t.Fatal(err)
	}
	if factory.count() != 1 {
		t.Fatalf("factory starts=%d, want 1 after manual retry", factory.count())
	}
	if r.hasStartupGuard(entry.ID) {
		t.Fatal("startup guard should clear after startup returns normally")
	}

	s, _ := r.Get(entry.ID)
	if err := s.ForceStop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNormalStartupFailureDoesNotQuarantineNextBoot(t *testing.T) {
	entry := validEntry(config.ModeAlwaysOn, true)
	store := config.NewStore(t.TempDir())
	factory := &fakeFactory{startErr: errors.New("child exited normally with an error")}
	r := NewRegistry(context.Background(), store, config.ServersConfig{
		SchemaVersion: config.SchemaVersion,
		Servers:       []config.ServerEntry{entry},
	}, 300, factory, nil)

	if _, err := r.Start(context.Background(), entry.ID, SourceUI); err == nil {
		t.Fatal("expected startup error")
	}
	if r.hasStartupGuard(entry.ID) {
		t.Fatal("contained startup errors must not be treated as Manager crashes")
	}
}
