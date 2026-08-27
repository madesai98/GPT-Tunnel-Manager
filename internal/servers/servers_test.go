package servers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycle"
)

type fakeRuntime struct {
	done     chan struct{}
	activity chan time.Time
	tracking bool

	mu      sync.Mutex
	err     error
	stopped bool
	once    sync.Once
}

func newFakeRuntime(tracking bool) *fakeRuntime {
	return &fakeRuntime{done: make(chan struct{}), activity: make(chan time.Time, 8), tracking: tracking}
}

func (r *fakeRuntime) Done() <-chan struct{}      { return r.done }
func (r *fakeRuntime) Activity() <-chan time.Time { return r.activity }
func (r *fakeRuntime) ActivityTracking() bool     { return r.tracking }
func (r *fakeRuntime) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}
func (r *fakeRuntime) Stop(context.Context) error {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.once.Do(func() { close(r.done) })
	return nil
}
func (r *fakeRuntime) crash(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
	r.once.Do(func() { close(r.done) })
}
func (r *fakeRuntime) wasStopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

type fakeFactory struct {
	mu       sync.Mutex
	tracking bool
	entries  []config.ServerEntry
	runtimes []*fakeRuntime
	startErr error
}

func (f *fakeFactory) Start(_ context.Context, entry config.ServerEntry) (Runtime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return nil, f.startErr
	}
	runtime := newFakeRuntime(f.tracking)
	f.entries = append(f.entries, entry)
	f.runtimes = append(f.runtimes, runtime)
	return runtime, nil
}
func (f *fakeFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runtimes)
}
func (f *fakeFactory) runtime(index int) *fakeRuntime {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runtimes[index]
}

type fakeStore struct {
	mu       sync.Mutex
	failNext bool
	saved    config.ServersConfig
}

func (s *fakeStore) SaveServers(cfg config.ServersConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext {
		s.failNext = false
		return errors.New("disk full")
	}
	s.saved = cfg
	return nil
}

func validEntry(mode config.ServerMode, enabled bool) config.ServerEntry {
	return config.ServerEntry{
		ID:      "srv_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:    "Test server",
		Enabled: enabled,
		Mode:    mode,
		Transport: config.TransportConfig{
			Type:         config.TransportExternalHTTP,
			ExternalHTTP: &config.ExternalHTTPTransport{URL: "http://127.0.0.1:9999/mcp"},
		},
		Tunnel: config.TunnelConfig{TunnelID: "tunnel_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		Runtime: config.RuntimeConfig{
			StartupTimeoutSeconds:  1,
			ShutdownTimeoutSeconds: 1,
		},
	}
}

func TestDisabledMCPMutationsReturnServerDisabled(t *testing.T) {
	factory := &fakeFactory{}
	s := NewSupervisor(context.Background(), validEntry(config.ModeManaged, false), 300, factory, nil)
	for _, operation := range []struct {
		name string
		call func() error
	}{
		{"start", func() error { _, err := s.Start(context.Background(), SourceMCP); return err }},
		{"restart", func() error { _, err := s.Restart(context.Background(), SourceMCP); return err }},
		{"shutdown", func() error { _, err := s.Shutdown(context.Background(), SourceMCP); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.call(); !errors.Is(err, ErrDisabled) {
				t.Fatalf("got %v, want ErrDisabled", err)
			}
		})
	}
}

func TestMCPModeRestrictions(t *testing.T) {
	for _, mode := range []config.ServerMode{config.ModeAlwaysOn, config.ModeManual} {
		t.Run(string(mode), func(t *testing.T) {
			s := NewSupervisor(context.Background(), validEntry(mode, true), 300, &fakeFactory{}, nil)
			for _, call := range []func() error{
				func() error { _, err := s.Start(context.Background(), SourceMCP); return err },
				func() error { _, err := s.Restart(context.Background(), SourceMCP); return err },
				func() error { _, err := s.Shutdown(context.Background(), SourceMCP); return err },
			} {
				if err := call(); !errors.Is(err, ErrModeConflict) {
					t.Fatalf("got %v, want ErrModeConflict", err)
				}
			}
		})
	}
}

func TestAlwaysOnCannotBePersistentlyStoppedFromUI(t *testing.T) {
	factory := &fakeFactory{}
	s := NewSupervisor(context.Background(), validEntry(config.ModeAlwaysOn, true), 300, factory, nil)
	if _, err := s.Start(context.Background(), SourceApp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Shutdown(context.Background(), SourceUI); !errors.Is(err, ErrAlwaysOnStop) {
		t.Fatalf("got %v, want ErrAlwaysOnStop", err)
	}
	if snap := s.Snapshot(); snap.Desired != lifecycle.DesiredRunning || !snap.Ready {
		t.Fatalf("Always On invariant lost: %+v", snap)
	}
}

func TestLifecycleOperationsAreIdempotent(t *testing.T) {
	factory := &fakeFactory{}
	s := NewSupervisor(context.Background(), validEntry(config.ModeManaged, true), 300, factory, nil)
	if _, err := s.Start(context.Background(), SourceMCP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(context.Background(), SourceMCP); err != nil {
		t.Fatal(err)
	}
	if got := factory.count(); got != 1 {
		t.Fatalf("duplicate start created %d runtimes", got)
	}
	if _, err := s.Restart(context.Background(), SourceMCP); err != nil {
		t.Fatal(err)
	}
	if got := factory.count(); got != 2 {
		t.Fatalf("restart runtime count = %d, want 2", got)
	}
	if _, err := s.Shutdown(context.Background(), SourceMCP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Shutdown(context.Background(), SourceMCP); err != nil {
		t.Fatal(err)
	}
}

func TestSavingNewAlwaysOnEntryStartsImmediately(t *testing.T) {
	factory := &fakeFactory{}
	store := &fakeStore{}
	r := NewRegistry(context.Background(), store, config.DefaultServersConfig(), 300, factory, nil)
	entry := validEntry(config.ModeAlwaysOn, true)
	entry.ID = ""
	saved, err := r.Save(entry)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("server ID was not generated")
	}
	if factory.count() != 1 {
		t.Fatalf("Always On entry did not start immediately; starts=%d", factory.count())
	}
	s, err := r.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); !snap.Ready || snap.Desired != lifecycle.DesiredRunning {
		t.Fatalf("unexpected Always On snapshot: %+v", snap)
	}
}

func TestDisablingRunningEntryStopsIt(t *testing.T) {
	entry := validEntry(config.ModeManaged, true)
	factory := &fakeFactory{}
	store := &fakeStore{}
	r := NewRegistry(context.Background(), store, config.ServersConfig{SchemaVersion: config.SchemaVersion, Servers: []config.ServerEntry{entry}}, 300, factory, nil)
	if _, err := r.Start(context.Background(), entry.ID, SourceUI); err != nil {
		t.Fatal(err)
	}
	oldRuntime := factory.runtime(0)
	entry.Enabled = false
	if _, err := r.Save(entry); err != nil {
		t.Fatal(err)
	}
	if !oldRuntime.wasStopped() {
		t.Fatal("running runtime was not stopped when entry was disabled")
	}
	s, _ := r.Get(entry.ID)
	if snap := s.Snapshot(); snap.Enabled || snap.Desired != lifecycle.DesiredStopped || snap.Observed != lifecycle.Stopped {
		t.Fatalf("unexpected disabled snapshot: %+v", snap)
	}
}

func TestEditingRunningEntryRestartsWithNewConfiguration(t *testing.T) {
	entry := validEntry(config.ModeManaged, true)
	factory := &fakeFactory{}
	store := &fakeStore{}
	r := NewRegistry(context.Background(), store, config.ServersConfig{SchemaVersion: config.SchemaVersion, Servers: []config.ServerEntry{entry}}, 300, factory, nil)
	if _, err := r.Start(context.Background(), entry.ID, SourceUI); err != nil {
		t.Fatal(err)
	}
	entry.Name = "Edited server"
	if _, err := r.Save(entry); err != nil {
		t.Fatal(err)
	}
	if factory.count() != 2 {
		t.Fatalf("active edit starts=%d, want stop/restart to create 2 total runtimes", factory.count())
	}
	got, _ := r.Entry(entry.ID)
	if got.Name != entry.Name {
		t.Fatalf("live entry name=%q, want %q", got.Name, entry.Name)
	}
	s, _ := r.Get(entry.ID)
	if snap := s.Snapshot(); !snap.Ready || snap.Desired != lifecycle.DesiredRunning {
		t.Fatalf("entry was not restored to running after edit: %+v", snap)
	}
}

func TestRegistrySavePersistenceFailureRollsBackLiveState(t *testing.T) {
	entry := validEntry(config.ModeManaged, true)
	factory := &fakeFactory{}
	store := &fakeStore{}
	r := NewRegistry(context.Background(), store, config.ServersConfig{SchemaVersion: config.SchemaVersion, Servers: []config.ServerEntry{entry}}, 300, factory, nil)
	if _, err := r.Start(context.Background(), entry.ID, SourceUI); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.failNext = true
	store.mu.Unlock()
	edited := entry
	edited.Name = "Should not commit"
	if _, err := r.Save(edited); err == nil {
		t.Fatal("expected persistence failure")
	}
	got, _ := r.Entry(entry.ID)
	if got.Name != entry.Name {
		t.Fatalf("in-memory state committed despite persistence failure: %q", got.Name)
	}
	if factory.count() != 2 {
		t.Fatalf("old desired-running entry was not restarted after persistence failure; starts=%d", factory.count())
	}
}

func TestManagedIdleDefaultChangesAffectRunningEntry(t *testing.T) {
	entry := validEntry(config.ModeManaged, true)
	factory := &fakeFactory{tracking: true}
	store := &fakeStore{}
	r := NewRegistry(context.Background(), store, config.ServersConfig{SchemaVersion: config.SchemaVersion, Servers: []config.ServerEntry{entry}}, 0, factory, nil)
	if _, err := r.Start(context.Background(), entry.ID, SourceUI); err != nil {
		t.Fatal(err)
	}
	r.SetDefaultIdle(1)
	s, _ := r.Get(entry.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snap, err := s.Wait(ctx, func(s Snapshot) bool { return s.Observed == lifecycle.Stopped })
	if err != nil {
		t.Fatal(err)
	}
	if snap.Desired != lifecycle.DesiredStopped {
		t.Fatalf("idle shutdown did not set desired stopped: %+v", snap)
	}
}

type fakeOwned struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
	stop bool
	once sync.Once
}

func newFakeOwned() *fakeOwned { return &fakeOwned{done: make(chan struct{})} }
func (p *fakeOwned) Done() <-chan struct{} { return p.done }
func (p *fakeOwned) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}
func (p *fakeOwned) Stop(context.Context, time.Duration) error {
	p.mu.Lock()
	p.stop = true
	p.mu.Unlock()
	p.once.Do(func() { close(p.done) })
	return nil
}
func (p *fakeOwned) crash(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	p.once.Do(func() { close(p.done) })
}
func (p *fakeOwned) wasStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stop
}

func TestCombinedRuntimeCleansOwnedPartnerWhenTunnelExits(t *testing.T) {
	tunnel := newFakeRuntime(false)
	owned := newFakeOwned()
	combined := newCombined(tunnel, owned, 50*time.Millisecond)
	tunnel.crash(errors.New("tunnel crashed"))
	select {
	case <-combined.Done():
	case <-time.After(time.Second):
		t.Fatal("combined runtime did not finish")
	}
	if !owned.wasStopped() {
		t.Fatal("owned MCP process survived tunnel exit")
	}
}

func TestCombinedRuntimeCleansTunnelPartnerWhenOwnedProcessExits(t *testing.T) {
	tunnel := newFakeRuntime(false)
	owned := newFakeOwned()
	combined := newCombined(tunnel, owned, 50*time.Millisecond)
	owned.crash(errors.New("owned process crashed"))
	select {
	case <-combined.Done():
	case <-time.After(time.Second):
		t.Fatal("combined runtime did not finish")
	}
	if !tunnel.wasStopped() {
		t.Fatal("tunnel-client survived owned MCP process exit")
	}
}
