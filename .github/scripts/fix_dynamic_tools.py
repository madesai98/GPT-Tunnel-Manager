from pathlib import Path


def one(text: str, old: str, new: str, label: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{label}: expected 1 match, got {count}")
    return text.replace(old, new, 1)


p = Path("internal/downstream/downstream.go")
text = p.read_text()
text = one(text,
'''type Session struct {
\tserverID string
\tsdk      *mcp.ClientSession
\tinitial  ToolSnapshot

\tsupportsToolListChanged bool
''',
'''type Session struct {
\tserverID string
\tsdk      *mcp.ClientSession
\tinitial  ToolSnapshot

\tcurrentMu sync.RWMutex
\tcurrent   ToolSnapshot
\trefreshMu sync.Mutex

\tsupportsToolListChanged bool
''', "session tool cache fields")

text = one(text,
'''\tchanged := &atomic.Bool{}
\tnotifyOnce := &sync.Once{}
\tcallbacks := &callbackState{}
\tmarkChanged := func() {
''',
'''\tchanged := &atomic.Bool{}
\tnotifyOnce := &sync.Once{}
\tcallbacks := &callbackState{}
\tvar liveSession atomic.Pointer[Session]
\tmarkChanged := func() {
''', "live session bridge")

text = one(text,
'''\t\topts.ToolListChangedHandler = func(callbackCtx context.Context, req *mcp.ToolListChangedRequest) {
\t\t\tmarkChanged()
\t\t\tsafeToolListChangedHandler(previous, callbackCtx, req)
\t\t}
''',
'''\t\topts.ToolListChangedHandler = func(callbackCtx context.Context, req *mcp.ToolListChangedRequest) {
\t\t\tmarkChanged()
\t\t\tif session := liveSession.Load(); session != nil {
\t\t\t\tsession.refreshToolsAsync(callbackCtx)
\t\t\t}
\t\t\tsafeToolListChangedHandler(previous, callbackCtx, req)
\t\t}
''', "tools/list_changed refresh")

text = one(text,
'''\t\tserverID:                server.ID,
\t\tsdk:                     sdkSession,
\t\tinitial:                 initial,
\t\tsupportsToolListChanged: supportsChanged,
''',
'''\t\tserverID:                server.ID,
\t\tsdk:                     sdkSession,
\t\tinitial:                 initial,
\t\tcurrent:                 initial,
\t\tsupportsToolListChanged: supportsChanged,
''', "initialize current tools")

text = one(text,
'''\t\tshutdown:                server.ShutdownTimeout(),
\t}
\treturn s, nil
}

func (s *Session) ServerID() string { return s.serverID }

func (s *Session) InitialTools() ToolSnapshot { return s.initial.Clone() }

func (s *Session) SupportsToolListChanged() bool { return s.supportsToolListChanged }
''',
'''\t\tshutdown:                server.ShutdownTimeout(),
\t}
\tliveSession.Store(s)
\tif changed.Load() {
\t\ts.refreshToolsAsync(context.Background())
\t}
\treturn s, nil
}

func (s *Session) ServerID() string { return s.serverID }

func (s *Session) InitialTools() ToolSnapshot { return s.initial.Clone() }

func (s *Session) CurrentTools() ToolSnapshot {
\tif s == nil {
\t\treturn ToolSnapshot{}
\t}
\ts.currentMu.RLock()
\tcurrent := s.current
\ts.currentMu.RUnlock()
\tif current.Fingerprint == "" && len(current.Tools) == 0 {
\t\treturn s.initial.Clone()
\t}
\treturn current.Clone()
}

func (s *Session) setCurrentTools(snapshot ToolSnapshot) {
\tif s == nil {
\t\treturn
\t}
\ts.currentMu.Lock()
\ts.current = snapshot.Clone()
\ts.currentMu.Unlock()
}

func (s *Session) refreshToolsAsync(parent context.Context) {
\tif s == nil || s.sdk == nil {
\t\treturn
\t}
\tgo func() {
\t\ts.refreshMu.Lock()
\t\tdefer s.refreshMu.Unlock()
\t\tbase := context.Background()
\t\tif parent != nil {
\t\t\tbase = context.WithoutCancel(parent)
\t\t}
\t\trefreshCtx, cancel := context.WithTimeout(base, 10*time.Second)
\t\tdefer cancel()
\t\tcurrent, err := SnapshotTools(refreshCtx, s.sdk)
\t\tif err == nil {
\t\t\ts.setCurrentTools(current)
\t\t}
\t}()
}

func (s *Session) SupportsToolListChanged() bool { return s.supportsToolListChanged }
''', "current tools methods")

text = one(text,
'''func (s *Session) ListTools(ctx context.Context) (ToolSnapshot, error) {
\tif err := s.ensureAvailable(); err != nil {
\t\treturn ToolSnapshot{}, err
\t}
\treturn SnapshotTools(ctx, s.sdk)
}
''',
'''func (s *Session) ListTools(ctx context.Context) (ToolSnapshot, error) {
\tif err := s.ensureAvailable(); err != nil {
\t\treturn ToolSnapshot{}, err
\t}
\tcurrent, err := SnapshotTools(ctx, s.sdk)
\tif err != nil {
\t\treturn ToolSnapshot{}, err
\t}
\ts.setCurrentTools(current)
\treturn current, nil
}
''', "ListTools cache update")

text = one(text,
'''\tcurrent, err := SnapshotTools(ctx, s.sdk)
\tif err != nil {
\t\treturn err
\t}
\tif current.Fingerprint != s.initial.Fingerprint {
''',
'''\tcurrent, err := SnapshotTools(ctx, s.sdk)
\tif err != nil {
\t\treturn err
\t}
\ts.setCurrentTools(current)
\tif current.Fingerprint != s.initial.Fingerprint {
''', "RevalidateTools cache update")
p.write_text(text)

p = Path("internal/routedlifecycle/service.go")
text = p.read_text()
text = one(text,
'''\tif st.session != nil && sessionExited(st.session) {
\t\tstale = st.session
\t\tst.session = nil
\t\tst.generation++
\t\tcancelIdleLocked(st)
\t}
\tif entry.Mode == v2config.ModeManual && st.session == nil {
''',
'''\tif st.session != nil && sessionContractChanged(st.session) && st.active > 0 {
\t\tbusy := busyErrorLocked(st)
\t\tst.mu.Unlock()
\t\treturn nil, busy
\t}
\tif st.session != nil && (sessionExited(st.session) || sessionContractChanged(st.session)) {
\t\tstale = st.session
\t\tst.session = nil
\t\tst.generation++
\t\tcancelIdleLocked(st)
\t}
\tif entry.Mode == v2config.ModeManual && st.session == nil {
''', "Acquire stale contract reconnect")

text = one(text,
'''\tif st.session != nil && sessionExited(st.session) {
\t\tstale = st.session
\t\tst.session = nil
\t\tst.generation++
\t\tcancelIdleLocked(st)
\t}
\tif st.session != nil {
''',
'''\tif st.session != nil && sessionContractChanged(st.session) && st.active > 0 {
\t\tout := snapshotLocked(st)
\t\tbusy := busyErrorLocked(st)
\t\tst.mu.Unlock()
\t\treturn out, busy
\t}
\tif st.session != nil && (sessionExited(st.session) || sessionContractChanged(st.session)) {
\t\tstale = st.session
\t\tst.session = nil
\t\tst.generation++
\t\tcancelIdleLocked(st)
\t}
\tif st.session != nil {
''', "Start stale contract reconnect")

text = one(text,
'''func snapshotLocked(st *serverState) Snapshot {
\tout := Snapshot{
\t\tServerID:        st.entry.ID,
\t\tName:            st.entry.Name,
\t\tMode:            st.entry.Mode,
\t\tRunning:         st.session != nil && !sessionExited(st.session),
\t\tActiveCallCount: st.active,
\t\tToolCount:       len(st.tools.Tools),
\t}
''',
'''func snapshotLocked(st *serverState) Snapshot {
\ttools := currentToolsLocked(st)
\tout := Snapshot{
\t\tServerID:        st.entry.ID,
\t\tName:            st.entry.Name,
\t\tMode:            st.entry.Mode,
\t\tRunning:         st.session != nil && !sessionExited(st.session),
\t\tActiveCallCount: st.active,
\t\tToolCount:       len(tools.Tools),
\t}
''', "snapshot current tool count")

text = one(text,
'''\tif st.deleted {
\t\treturn downstream.ToolSnapshot{}, lifecycleError("server_not_found", ErrServerNotFound, false)
\t}
\treturn st.tools.Clone(), nil
}

func (s *Service) Start(ctx context.Context, serverID string) (Snapshot, error) {
''',
'''\tif st.deleted {
\t\treturn downstream.ToolSnapshot{}, lifecycleError("server_not_found", ErrServerNotFound, false)
\t}
\treturn currentToolsLocked(st), nil
}

type currentToolProvider interface {
\tCurrentTools() downstream.ToolSnapshot
}

type toolContractChangeProvider interface {
\tToolContractChanged() bool
}

func currentToolsLocked(st *serverState) downstream.ToolSnapshot {
\tif st != nil && st.session != nil && !sessionExited(st.session) {
\t\tif provider, ok := st.session.(currentToolProvider); ok {
\t\t\tst.tools = provider.CurrentTools().Clone()
\t\t}
\t}
\treturn st.tools.Clone()
}

func sessionContractChanged(session RuntimeSession) bool {
\tprovider, ok := session.(toolContractChangeProvider)
\treturn ok && provider.ToolContractChanged()
}

func (s *Service) Start(ctx context.Context, serverID string) (Snapshot, error) {
''', "lifecycle current tool helpers")
p.write_text(text)

Path("internal/routedlifecycle/dynamic_tools_test.go").write_text(r'''package routedlifecycle

import (
    "context"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    "github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
    "github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type dynamicToolRuntime struct {
    mu      sync.Mutex
    initial downstream.ToolSnapshot
    current downstream.ToolSnapshot
    changed bool
    done    chan struct{}
    closed  bool
}

func newDynamicToolRuntime(snapshot downstream.ToolSnapshot) *dynamicToolRuntime {
    cloned := snapshot.Clone()
    return &dynamicToolRuntime{initial: cloned, current: cloned, done: make(chan struct{})}
}

func (r *dynamicToolRuntime) InitialTools() downstream.ToolSnapshot {
    r.mu.Lock()
    defer r.mu.Unlock()
    return r.initial.Clone()
}
func (r *dynamicToolRuntime) CurrentTools() downstream.ToolSnapshot {
    r.mu.Lock()
    defer r.mu.Unlock()
    return r.current.Clone()
}
func (r *dynamicToolRuntime) ToolContractChanged() bool {
    r.mu.Lock()
    defer r.mu.Unlock()
    return r.changed
}
func (r *dynamicToolRuntime) setCurrent(snapshot downstream.ToolSnapshot, changed bool) {
    r.mu.Lock()
    r.current = snapshot.Clone()
    r.changed = changed
    r.mu.Unlock()
}
func (r *dynamicToolRuntime) CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error) {
    return &mcp.CallToolResult{}, nil
}
func (r *dynamicToolRuntime) Close(context.Context) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    if !r.closed {
        r.closed = true
        close(r.done)
    }
    return nil
}
func (r *dynamicToolRuntime) Done() <-chan struct{} { return r.done }

func TestDynamicToolsRefreshCardAndReconnectBeforeAcquire(t *testing.T) {
    entry := testEntry(serverA, v2config.ModeManaged)
    manager := v2config.DefaultManagerConfig(43150)
    manager.ManagedDefaults.IdleTimeoutSeconds = 30
    var starts atomic.Int32
    var first *dynamicToolRuntime
    dynamic := downstream.ToolSnapshot{Tools: []*mcp.Tool{{Name: "dynamic_tool"}}}
    connect := func(context.Context, v2config.ServerEntry) (RuntimeSession, error) {
        n := starts.Add(1)
        snapshot := downstream.ToolSnapshot{}
        if n > 1 {
            snapshot = dynamic
        }
        runtime := newDynamicToolRuntime(snapshot)
        if n == 1 {
            first = runtime
        }
        return runtime, nil
    }
    service, err := New(context.Background(), manager, testConfig(entry), connect, Options{})
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() {
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        _ = service.Close(ctx)
    })

    if _, err := service.Start(context.Background(), serverA); err != nil {
        t.Fatal(err)
    }
    if starts.Load() != 1 || first == nil {
        t.Fatalf("initial starts = %d, first=%v", starts.Load(), first)
    }
    first.setCurrent(dynamic, true)

    known, err := service.KnownTools(serverA)
    if err != nil {
        t.Fatal(err)
    }
    if len(known.Tools) != 1 || known.Tools[0].Name != "dynamic_tool" {
        t.Fatalf("KnownTools = %#v, want dynamic tool", known.Tools)
    }
    snapshot, err := service.Snapshot(serverA)
    if err != nil {
        t.Fatal(err)
    }
    if snapshot.ToolCount != 1 {
        t.Fatalf("ToolCount = %d, want 1", snapshot.ToolCount)
    }

    lease, err := service.Acquire(context.Background(), serverA)
    if err != nil {
        t.Fatal(err)
    }
    defer lease.Release()
    if starts.Load() != 2 {
        t.Fatalf("starts after drift acquire = %d, want 2", starts.Load())
    }
    if tools := lease.InitialTools(); len(tools.Tools) != 1 || tools.Tools[0].Name != "dynamic_tool" {
        t.Fatalf("reconnected InitialTools = %#v, want dynamic tool", tools.Tools)
    }
    select {
    case <-first.Done():
    default:
        t.Fatal("stale changed session was not closed before reconnect")
    }
}
''')

Path("internal/downstream/current_tools_test.go").write_text(r'''package downstream

import (
    "testing"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCurrentToolsTracksRefreshedSnapshotWithoutChangingInitial(t *testing.T) {
    initial := ToolSnapshot{Tools: []*mcp.Tool{{Name: "initial"}}}.Clone()
    current := ToolSnapshot{Tools: []*mcp.Tool{{Name: "dynamic"}}}.Clone()
    s := &Session{initial: initial, current: initial}
    s.setCurrentTools(current)
    if got := s.InitialTools(); len(got.Tools) != 1 || got.Tools[0].Name != "initial" {
        t.Fatalf("InitialTools = %#v", got.Tools)
    }
    if got := s.CurrentTools(); len(got.Tools) != 1 || got.Tools[0].Name != "dynamic" {
        t.Fatalf("CurrentTools = %#v", got.Tools)
    }
}
''')
