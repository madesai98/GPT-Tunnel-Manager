package routedlifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionrouter"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverA = "srv_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	serverB = "srv_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	serverC = "srv_cccccccccccccccccccccccccccccccc"
)

type fakeRuntime struct {
	mu        sync.Mutex
	snapshot  downstream.ToolSnapshot
	done      chan struct{}
	closed    bool
	closeHits int
	calls     int
	callHook  func(context.Context, *mcp.CallToolParams, int) (*mcp.CallToolResult, error)
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		snapshot: downstream.ToolSnapshot{Fingerprint: "sha256:stable"},
		done:     make(chan struct{}),
	}
}

func (r *fakeRuntime) InitialTools() downstream.ToolSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshot.Clone()
}

func (r *fakeRuntime) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	r.mu.Lock()
	r.calls++
	callNumber := r.calls
	hook := r.callHook
	r.mu.Unlock()
	if hook != nil {
		return hook(ctx, params, callNumber)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
}

func (r *fakeRuntime) Close(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeHits++
	if !r.closed {
		r.closed = true
		close(r.done)
	}
	return nil
}

func (r *fakeRuntime) Done() <-chan struct{} { return r.done }

func (r *fakeRuntime) Crash() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.done)
	}
}

func (r *fakeRuntime) CloseHits() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeHits
}

func (r *fakeRuntime) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type fakeConnector struct {
	mu       sync.Mutex
	starts   map[string]int
	runtimes map[string][]*fakeRuntime
	hook     func(context.Context, v2config.ServerEntry, int) (RuntimeSession, error)
}

func newFakeConnector() *fakeConnector {
	return &fakeConnector{starts: map[string]int{}, runtimes: map[string][]*fakeRuntime{}}
}

func (c *fakeConnector) Connect(ctx context.Context, entry v2config.ServerEntry) (RuntimeSession, error) {
	c.mu.Lock()
	c.starts[entry.ID]++
	startNumber := c.starts[entry.ID]
	hook := c.hook
	c.mu.Unlock()
	var (
		runtime RuntimeSession
		err     error
	)
	if hook != nil {
		runtime, err = hook(ctx, entry, startNumber)
	} else {
		runtime = newFakeRuntime()
	}
	if err != nil {
		return nil, err
	}
	if concrete, ok := runtime.(*fakeRuntime); ok {
		c.mu.Lock()
		c.runtimes[entry.ID] = append(c.runtimes[entry.ID], concrete)
		c.mu.Unlock()
	}
	return runtime, nil
}

func (c *fakeConnector) Starts(serverID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts[serverID]
}

func (c *fakeConnector) Latest(serverID string) *fakeRuntime {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := c.runtimes[serverID]
	if len(items) == 0 {
		return nil
	}
	return items[len(items)-1]
}

func testEntry(id string, mode v2config.ServerMode) v2config.ServerEntry {
	return v2config.ServerEntry{
		ID:   id,
		Name: "test-" + id[len(id)-4:],
		Mode: mode,
		Transport: v2config.TransportConfig{
			Type: v2config.TransportExternalHTTP,
			ExternalHTTP: &v2config.ExternalHTTPTransport{
				URL:  "http://127.0.0.1:43199/mcp",
				Auth: v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthNone},
			},
		},
		Runtime: v2config.RuntimeConfig{StartupTimeoutSeconds: 1, ShutdownTimeoutSeconds: 1},
	}
}

func testConfig(entries ...v2config.ServerEntry) v2config.ServersConfig {
	return v2config.ServersConfig{SchemaVersion: v2config.SchemaVersion, Servers: append([]v2config.ServerEntry(nil), entries...)}
}

func cloneConfig(cfg v2config.ServersConfig) v2config.ServersConfig {
	copy := cfg
	copy.Servers = append([]v2config.ServerEntry(nil), cfg.Servers...)
	return copy
}

func newTestService(t *testing.T, connector *fakeConnector, cfg v2config.ServersConfig) *Service {
	t.Helper()
	manager := v2config.DefaultManagerConfig(43150)
	manager.ManagedDefaults.IdleTimeoutSeconds = 1
	s, err := New(context.Background(), manager, cfg, connector.Connect, Options{MaximumTaskLease: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})
	return s
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !condition() {
		t.Fatal(message)
	}
}

func TestUnrelatedServerAcquisitionIsConcurrent(t *testing.T) {
	connector := newFakeConnector()
	enteredA := make(chan struct{})
	releaseA := make(chan struct{})
	var once sync.Once
	connector.hook = func(ctx context.Context, entry v2config.ServerEntry, _ int) (RuntimeSession, error) {
		if entry.ID == serverA {
			once.Do(func() { close(enteredA) })
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-releaseA:
			}
		}
		return newFakeRuntime(), nil
	}
	s := newTestService(t, connector, testConfig(testEntry(serverA, v2config.ModeManaged), testEntry(serverB, v2config.ModeManaged)))

	type acquireResult struct {
		lease *UseLease
		err   error
	}
	aResult := make(chan acquireResult, 1)
	go func() {
		lease, err := s.Acquire(context.Background(), serverA)
		aResult <- acquireResult{lease: lease, err: err}
	}()
	select {
	case <-enteredA:
	case <-time.After(time.Second):
		t.Fatal("server A acquisition did not enter connector")
	}

	ctxB, cancelB := context.WithTimeout(context.Background(), 250*time.Millisecond)
	leaseB, err := s.Acquire(ctxB, serverB)
	cancelB()
	if err != nil {
		close(releaseA)
		t.Fatalf("unrelated server B was blocked by server A acquisition: %v", err)
	}
	leaseB.Release()
	close(releaseA)
	result := <-aResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	result.lease.Release()
}

func TestSameServerAcquisitionSerializesStartAndSupportsMultipleLeases(t *testing.T) {
	connector := newFakeConnector()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	connector.hook = func(ctx context.Context, entry v2config.ServerEntry, _ int) (RuntimeSession, error) {
		if entry.ID == serverA {
			once.Do(func() { close(entered) })
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
			}
		}
		return newFakeRuntime(), nil
	}
	s := newTestService(t, connector, testConfig(testEntry(serverA, v2config.ModeManaged)))

	leases := make(chan *UseLease, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			lease, err := s.Acquire(context.Background(), serverA)
			if err != nil {
				errs <- err
				return
			}
			leases <- lease
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first same-server acquisition did not start")
	}
	time.Sleep(50 * time.Millisecond)
	if got := connector.Starts(serverA); got != 1 {
		t.Fatalf("same-server connector starts while first start blocked = %d, want 1", got)
	}
	close(release)

	var acquired []*UseLease
	for len(acquired) < 2 {
		select {
		case err := <-errs:
			t.Fatal(err)
		case lease := <-leases:
			acquired = append(acquired, lease)
		case <-time.After(time.Second):
			t.Fatal("same-server acquisitions did not complete")
		}
	}
	if got := connector.Starts(serverA); got != 1 {
		t.Fatalf("same-server acquisitions started %d runtimes, want 1", got)
	}
	snap, err := s.Snapshot(serverA)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ActiveCallCount != 2 || !snap.Running {
		t.Fatalf("snapshot with two leases = %#v", snap)
	}
	acquired[0].Release()
	acquired[1].Release()
	snap, _ = s.Snapshot(serverA)
	if snap.ActiveCallCount != 0 {
		t.Fatalf("active count after releases = %d, want 0", snap.ActiveCallCount)
	}
}

func TestManagedAutoStartActiveProtectionIdleStopAndReset(t *testing.T) {
	connector := newFakeConnector()
	s := newTestService(t, connector, testConfig(testEntry(serverA, v2config.ModeManaged)))
	lease, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	first := connector.Latest(serverA)
	if first == nil || connector.Starts(serverA) != 1 {
		t.Fatal("Managed acquisition did not auto-start exactly one runtime")
	}
	time.Sleep(1100 * time.Millisecond)
	if first.CloseHits() != 0 {
		t.Fatal("Managed runtime idle-stopped while use lease was active")
	}
	lease.Release()
	waitFor(t, 2*time.Second, func() bool { return first.CloseHits() == 1 }, "Managed runtime did not stop after final lease plus idle timeout")

	lease2, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	second := connector.Latest(serverA)
	if connector.Starts(serverA) != 2 {
		t.Fatalf("Managed reacquisition starts = %d, want 2", connector.Starts(serverA))
	}
	lease2.Release()
	time.Sleep(600 * time.Millisecond)
	lease3, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	if connector.Starts(serverA) != 2 {
		t.Fatal("new call before idle deadline did not reuse Managed runtime")
	}
	time.Sleep(600 * time.Millisecond)
	if second.CloseHits() != 0 {
		t.Fatal("stale idle timer stopped a runtime after a new lease reset activity")
	}
	lease3.Release()
	waitFor(t, 2*time.Second, func() bool { return second.CloseHits() == 1 }, "reset Managed idle deadline did not eventually stop runtime")
}

func TestTaskHeldLeaseKeepsManagedRuntimeAliveAndReleasesOnTerminalOrExpiry(t *testing.T) {
	connector := newFakeConnector()
	s := newTestService(t, connector, testConfig(testEntry(serverA, v2config.ModeManaged)))
	lease, err := s.AcquireTaskLease(context.Background(), serverA, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if lease.ExpiresAt() == nil {
		t.Fatal("task lease has no bounded expiry")
	}
	runtime := connector.Latest(serverA)
	snap, _ := s.Snapshot(serverA)
	if snap.ActiveCallCount != 1 || !snap.Running {
		t.Fatalf("task-held lease snapshot = %#v", snap)
	}
	waitFor(t, 500*time.Millisecond, func() bool {
		snap, _ := s.Snapshot(serverA)
		return snap.ActiveCallCount == 0
	}, "task lease expiry did not release active use")
	if runtime.CloseHits() != 0 {
		t.Fatal("Managed runtime stopped immediately at task expiry instead of entering idle window")
	}

	terminal, err := s.AcquireTaskLease(context.Background(), serverA, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	expires := terminal.ExpiresAt()
	if expires == nil || time.Until(*expires) > 400*time.Millisecond {
		t.Fatalf("task lease TTL was not bounded by configured maximum: %v", expires)
	}
	terminal.Release()
	snap, _ = s.Snapshot(serverA)
	if snap.ActiveCallCount != 0 {
		t.Fatalf("terminal task release active count = %d, want 0", snap.ActiveCallCount)
	}
}

func TestManualDisabledAndAlwaysOnSemantics(t *testing.T) {
	connector := newFakeConnector()
	cfg := testConfig(
		testEntry(serverA, v2config.ModeManual),
		testEntry(serverB, v2config.ModeDisabled),
		testEntry(serverC, v2config.ModeAlwaysOn),
	)
	s := newTestService(t, connector, cfg)

	if _, err := s.Acquire(context.Background(), serverA); !errors.Is(err, ErrManualServerStopped) {
		t.Fatalf("stopped Manual acquisition error = %v", err)
	}
	if connector.Starts(serverA) != 0 {
		t.Fatal("stopped Manual server was auto-started")
	}
	if _, err := s.Start(context.Background(), serverA); err != nil {
		t.Fatal(err)
	}
	manual, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	manual.Release()
	if connector.Starts(serverA) != 1 {
		t.Fatal("running Manual server was not reused")
	}
	if _, err := s.Stop(context.Background(), serverA); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Acquire(context.Background(), serverB); !errors.Is(err, ErrServerDisabled) {
		t.Fatalf("Disabled acquisition error = %v", err)
	}
	if _, err := s.Start(context.Background(), serverB); !errors.Is(err, ErrServerDisabled) {
		t.Fatalf("Disabled explicit start error = %v", err)
	}
	if connector.Starts(serverB) != 0 {
		t.Fatal("Disabled server started")
	}

	if err := s.StartAlwaysOn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if connector.Starts(serverC) != 1 {
		t.Fatalf("Always On startup count = %d, want 1", connector.Starts(serverC))
	}
	always1, err := s.Acquire(context.Background(), serverC)
	if err != nil {
		t.Fatal(err)
	}
	always1.Release()
	always2, err := s.Acquire(context.Background(), serverC)
	if err != nil {
		t.Fatal(err)
	}
	always2.Release()
	if connector.Starts(serverC) != 1 {
		t.Fatal("Always On routed acquisitions did not reuse maintained runtime")
	}
	if _, err := s.Stop(context.Background(), serverC); !errors.Is(err, ErrAlwaysOnMaintained) {
		t.Fatalf("Always On ordinary stop error = %v", err)
	}
}

func TestServerBusyProtectsEditDisableDeleteStopRestartWithAccurateCount(t *testing.T) {
	connector := newFakeConnector()
	entry := testEntry(serverA, v2config.ModeManaged)
	current := testConfig(entry)
	s := newTestService(t, connector, current)
	lease1, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	lease2, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	assertBusy := func(label string, err error) {
		t.Helper()
		var busy *ServerBusyError
		if !errors.As(err, &busy) || busy.ActiveCallCount != 2 || busy.ServerID != serverA {
			t.Fatalf("%s busy error = %#v / %v", label, busy, err)
		}
	}

	edit := cloneConfig(current)
	edit.Servers[0].Name = "routing-relevant-edit"
	assertBusy("edit", s.PrepareServerMutation(current, edit))
	disable := cloneConfig(current)
	disable.Servers[0].Mode = v2config.ModeDisabled
	assertBusy("disable", s.PrepareServerMutation(current, disable))
	deleteCfg := testConfig()
	assertBusy("delete", s.PrepareServerMutation(current, deleteCfg))
	_, err = s.Stop(context.Background(), serverA)
	assertBusy("stop", err)
	_, err = s.Restart(context.Background(), serverA)
	assertBusy("restart", err)

	loggingOnly := cloneConfig(current)
	level := "debug"
	loggingOnly.Servers[0].Logging.CaptureLevelOverride = &level
	if err := s.PrepareServerMutation(current, loggingOnly); err != nil {
		t.Fatalf("non-runtime logging mutation was blocked: %v", err)
	}
	s.CommitServerMutation(loggingOnly)
	snap, _ := s.Snapshot(serverA)
	if snap.ActiveCallCount != 2 || !snap.Running || connector.Starts(serverA) != 1 {
		t.Fatalf("logging-only mutation disturbed active runtime: %#v starts=%d", snap, connector.Starts(serverA))
	}

	lease1.Release()
	lease2.Release()
	if err := s.PrepareServerMutation(loggingOnly, disable); err != nil {
		t.Fatalf("disable after leases released: %v", err)
	}
	s.CommitServerMutation(disable)
	snap, err = s.Snapshot(serverA)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Mode != v2config.ModeDisabled || snap.Running || snap.ActiveCallCount != 0 {
		t.Fatalf("disabled snapshot = %#v", snap)
	}
}

func TestManagedStartupFailureRollsBackLeaseAccounting(t *testing.T) {
	connector := newFakeConnector()
	connector.hook = func(_ context.Context, _ v2config.ServerEntry, start int) (RuntimeSession, error) {
		if start == 1 {
			return nil, errors.New("injected startup failure")
		}
		return newFakeRuntime(), nil
	}
	s := newTestService(t, connector, testConfig(testEntry(serverA, v2config.ModeManaged)))
	if _, err := s.Acquire(context.Background(), serverA); err == nil {
		t.Fatal("expected Managed startup failure")
	}
	snap, _ := s.Snapshot(serverA)
	if snap.ActiveCallCount != 0 || snap.Running {
		t.Fatalf("failed startup leaked lifecycle state: %#v", snap)
	}
	lease, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatalf("retry after startup failure: %v", err)
	}
	lease.Release()
	if connector.Starts(serverA) != 2 {
		t.Fatalf("startup attempts = %d, want 2", connector.Starts(serverA))
	}
}

func TestCrashCleanupDoesNotLeakLeaseAndAlwaysOnRecovers(t *testing.T) {
	connector := newFakeConnector()
	s := newTestService(t, connector, testConfig(testEntry(serverA, v2config.ModeManaged), testEntry(serverB, v2config.ModeAlwaysOn)))
	managed, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	connector.Latest(serverA).Crash()
	waitFor(t, time.Second, func() bool {
		snap, _ := s.Snapshot(serverA)
		return !snap.Running
	}, "crashed Managed runtime was not detached")
	snap, _ := s.Snapshot(serverA)
	if snap.ActiveCallCount != 1 {
		t.Fatalf("crash corrupted active lease count: %#v", snap)
	}
	managed.Release()
	snap, _ = s.Snapshot(serverA)
	if snap.ActiveCallCount != 0 {
		t.Fatal("lease release after crash did not reach zero")
	}
	replacement, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	replacement.Release()
	if connector.Starts(serverA) != 2 {
		t.Fatalf("Managed post-crash starts = %d, want 2", connector.Starts(serverA))
	}

	if err := s.StartAlwaysOn(context.Background()); err != nil {
		t.Fatal(err)
	}
	always := connector.Latest(serverB)
	always.Crash()
	waitFor(t, 2*time.Second, func() bool { return connector.Starts(serverB) >= 2 }, "Always On runtime was not re-maintained after crash")
	waitFor(t, time.Second, func() bool {
		snap, _ := s.Snapshot(serverB)
		return snap.Running
	}, "replacement Always On runtime is not running")
}

func TestCallCancellationAndManagerCloseCleanUpOwnedRuntime(t *testing.T) {
	connector := newFakeConnector()
	callStarted := make(chan struct{})
	var once sync.Once
	connector.hook = func(_ context.Context, _ v2config.ServerEntry, _ int) (RuntimeSession, error) {
		runtime := newFakeRuntime()
		runtime.callHook = func(ctx context.Context, _ *mcp.CallToolParams, _ int) (*mcp.CallToolResult, error) {
			once.Do(func() { close(callStarted) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return runtime, nil
	}
	manager := v2config.DefaultManagerConfig(43151)
	manager.ManagedDefaults.IdleTimeoutSeconds = 1
	s, err := New(context.Background(), manager, testConfig(testEntry(serverA, v2config.ModeManaged)), connector.Connect, Options{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	callDone := make(chan error, 1)
	go func() {
		_, err := lease.CallTool(context.Background(), &mcp.CallToolParams{Name: "blocked"})
		callDone <- err
	}()
	select {
	case <-callStarted:
	case <-time.After(time.Second):
		t.Fatal("downstream call did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-callDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("call error after Manager shutdown = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Manager shutdown did not cancel active routed call")
	}
	runtime := connector.Latest(serverA)
	if runtime.CloseHits() == 0 {
		t.Fatal("Manager shutdown did not close Manager-owned downstream session/runtime")
	}
	lease.Release()
	snap, _ := s.Snapshot(serverA)
	if snap.ActiveCallCount != 0 || snap.Running {
		t.Fatalf("shutdown cleanup snapshot = %#v", snap)
	}
	if _, err := s.Acquire(context.Background(), serverA); !errors.Is(err, ErrManagerShuttingDown) {
		t.Fatalf("post-shutdown acquisition error = %v", err)
	}
}

func TestReusedSessionPreservesToolDriftErrorsWithoutReconnect(t *testing.T) {
	connector := newFakeConnector()
	dispatches := 0
	var dispatchMu sync.Mutex
	connector.hook = func(_ context.Context, _ v2config.ServerEntry, _ int) (RuntimeSession, error) {
		runtime := newFakeRuntime()
		runtime.callHook = func(_ context.Context, _ *mcp.CallToolParams, call int) (*mcp.CallToolResult, error) {
			if call == 2 {
				return nil, downstream.ErrToolContractChanged
			}
			dispatchMu.Lock()
			dispatches++
			dispatchMu.Unlock()
			return &mcp.CallToolResult{}, nil
		}
		return runtime, nil
	}
	s := newTestService(t, connector, testConfig(testEntry(serverA, v2config.ModeManaged)))
	first, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.CallTool(context.Background(), &mcp.CallToolParams{Name: "tool"}); err != nil {
		t.Fatal(err)
	}
	first.Release()
	second, err := s.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	_, err = second.CallTool(context.Background(), &mcp.CallToolParams{Name: "tool"})
	second.Release()
	if !errors.Is(err, downstream.ErrToolContractChanged) {
		t.Fatalf("reused session drift error = %v", err)
	}
	if connector.Starts(serverA) != 1 {
		t.Fatalf("drift path reconnected server %d times; expected reused session", connector.Starts(serverA))
	}
	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	if dispatches != 1 {
		t.Fatalf("pre-call drift guard allowed %d downstream dispatches, want 1", dispatches)
	}
}

func TestLifecycleErrorClassificationMatchesRouterBlockers(t *testing.T) {
	tests := []struct {
		err  *LifecycleError
		code string
	}{
		{lifecycleError(executionrouter.CodeManualServerStopped, ErrManualServerStopped, false), executionrouter.CodeManualServerStopped},
		{lifecycleError(executionrouter.CodeServerDisabled, ErrServerDisabled, false), executionrouter.CodeServerDisabled},
		{lifecycleError(executionrouter.CodeManagerShuttingDown, ErrManagerShuttingDown, true), executionrouter.CodeManagerShuttingDown},
	}
	for _, test := range tests {
		if test.err.ExecutionCode() != test.code {
			t.Fatalf("error %v code = %q, want %q", test.err, test.err.ExecutionCode(), test.code)
		}
	}
	if got := (&ServerBusyError{ServerID: serverA, ActiveCallCount: 7}).Error(); got != fmt.Sprintf("server_busy: server %s has 7 active use lease(s)", serverA) {
		t.Fatalf("stable busy error text = %q", got)
	}
}
