package routedlifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionrouter"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ErrServerNotFound      = errors.New("server_not_found")
	ErrManualServerStopped = errors.New("manual_server_stopped")
	ErrServerDisabled      = errors.New("server_disabled")
	ErrServerBusy          = errors.New("server_busy")
	ErrManagerShuttingDown = errors.New("manager_shutting_down")
	ErrAlwaysOnMaintained  = errors.New("always_on_maintained")
)

const defaultMaximumTaskLease = 24 * time.Hour

type RuntimeSession interface {
	InitialTools() downstream.ToolSnapshot
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
	Close(context.Context) error
	Done() <-chan struct{}
}

type ConnectFunc func(context.Context, v2config.ServerEntry) (RuntimeSession, error)

func ConnectWithFactory(factory *downstream.Factory) ConnectFunc {
	return func(ctx context.Context, entry v2config.ServerEntry) (RuntimeSession, error) {
		if factory == nil {
			return nil, errors.New("downstream factory is unavailable")
		}
		return factory.Connect(ctx, entry)
	}
}

type Options struct {
	MaximumTaskLease time.Duration
}

type LifecycleError struct {
	Code      string
	Message   string
	Retryable bool
	cause     error
}

func (e *LifecycleError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *LifecycleError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *LifecycleError) ExecutionCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *LifecycleError) ExecutionRetryable() bool {
	return e != nil && e.Retryable
}

type ServerBusyError struct {
	ServerID        string `json:"server_id"`
	ActiveCallCount int    `json:"active_call_count"`
}

func (e *ServerBusyError) Error() string {
	if e == nil {
		return ErrServerBusy.Error()
	}
	return fmt.Sprintf("%s: server %s has %d active use lease(s)", ErrServerBusy, e.ServerID, e.ActiveCallCount)
}

func (e *ServerBusyError) Unwrap() error { return ErrServerBusy }
func (e *ServerBusyError) ExecutionCode() string {
	return executionrouter.CodeServerBusy
}
func (e *ServerBusyError) ExecutionRetryable() bool { return true }

type Snapshot struct {
	ServerID        string              `json:"server_id"`
	Name            string              `json:"name"`
	Mode            v2config.ServerMode `json:"mode"`
	Running         bool                `json:"running"`
	ActiveCallCount int                 `json:"active_call_count"`
	LastActivityAt  *time.Time          `json:"last_activity_at,omitempty"`
	IdleDeadlineAt  *time.Time          `json:"idle_deadline_at,omitempty"`
}

type Service struct {
	ctx    context.Context
	cancel context.CancelFunc

	connect      ConnectFunc
	defaultIdle  int
	maxTaskLease time.Duration
	closing      atomic.Bool

	mu     sync.RWMutex
	states map[string]*serverState

	mutationMu sync.Mutex
	pending    map[string]struct{}
}

type serverState struct {
	op chan struct{}
	mu sync.Mutex

	entry           v2config.ServerEntry
	session         RuntimeSession
	active          int
	lastActivity    time.Time
	idleTimer       *time.Timer
	idleDeadline    time.Time
	idleSequence    uint64
	generation      uint64
	mutating        bool
	runtimeChanging bool
	deleted         bool
}

func newServerState(entry v2config.ServerEntry) *serverState {
	st := &serverState{entry: entry, op: make(chan struct{}, 1)}
	st.op <- struct{}{}
	return st
}

func New(ctx context.Context, manager v2config.ManagerConfig, servers v2config.ServersConfig, connect ConnectFunc, opts Options) (*Service, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if connect == nil {
		return nil, errors.New("downstream connect function is required")
	}
	if err := v2config.ValidateManager(manager); err != nil {
		return nil, fmt.Errorf("validate v2 Manager config: %w", err)
	}
	if err := v2config.ValidateServers(servers); err != nil {
		return nil, fmt.Errorf("validate v2 servers config: %w", err)
	}
	maximumTaskLease := opts.MaximumTaskLease
	if maximumTaskLease <= 0 {
		maximumTaskLease = defaultMaximumTaskLease
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	s := &Service{
		ctx:           serviceCtx,
		cancel:        cancel,
		connect:       connect,
		defaultIdle:   manager.ManagedDefaults.IdleTimeoutSeconds,
		maxTaskLease:  maximumTaskLease,
		states:        make(map[string]*serverState, len(servers.Servers)),
	}
	for _, entry := range servers.Servers {
		s.states[entry.ID] = newServerState(entry)
	}
	return s, nil
}

var _ executionrouter.SessionProvider = (*Service)(nil)

func (s *Service) Session(ctx context.Context, serverID string) (executionrouter.Session, error) {
	return s.Acquire(ctx, serverID)
}

func (s *Service) Acquire(ctx context.Context, serverID string) (*UseLease, error) {
	if s.closing.Load() {
		return nil, lifecycleError(executionrouter.CodeManagerShuttingDown, ErrManagerShuttingDown, true)
	}
	st, err := s.lookup(serverID)
	if err != nil {
		return nil, err
	}
	if err := lockOperation(ctx, st); err != nil {
		return nil, err
	}
	defer unlockOperation(st)

	var stale RuntimeSession
	st.mu.Lock()
	if st.deleted {
		st.mu.Unlock()
		return nil, lifecycleError("server_not_found", ErrServerNotFound, false)
	}
	if st.mutating || st.runtimeChanging {
		busy := busyErrorLocked(st)
		st.mu.Unlock()
		return nil, busy
	}
	entry := st.entry
	if entry.Mode == v2config.ModeDisabled {
		st.mu.Unlock()
		return nil, lifecycleError(executionrouter.CodeServerDisabled, ErrServerDisabled, false)
	}
	if st.session != nil && sessionExited(st.session) {
		stale = st.session
		st.session = nil
		st.generation++
		cancelIdleLocked(st)
	}
	if entry.Mode == v2config.ModeManual && st.session == nil {
		st.mu.Unlock()
		if stale != nil {
			_ = closeRuntime(context.Background(), entry, stale)
		}
		return nil, lifecycleError(executionrouter.CodeManualServerStopped, ErrManualServerStopped, false)
	}

	cancelIdleLocked(st)
	st.active++
	st.lastActivity = time.Now().UTC()
	session := st.session
	st.mu.Unlock()

	if stale != nil {
		_ = closeRuntime(context.Background(), entry, stale)
	}
	if session == nil {
		connectCtx, cancel := s.operationContext(ctx)
		connected, connectErr := s.connect(connectCtx, entry)
		cancel()
		if connectErr != nil {
			st.mu.Lock()
			if st.active > 0 {
				st.active--
			}
			st.lastActivity = time.Now().UTC()
			st.mu.Unlock()
			return nil, fmt.Errorf("connect server %s: %w", serverID, connectErr)
		}
		if connected == nil {
			st.mu.Lock()
			if st.active > 0 {
				st.active--
			}
			st.lastActivity = time.Now().UTC()
			st.mu.Unlock()
			return nil, errors.New("downstream connect returned a nil session")
		}
		if s.closing.Load() {
			_ = closeRuntime(context.Background(), entry, connected)
			st.mu.Lock()
			if st.active > 0 {
				st.active--
			}
			st.lastActivity = time.Now().UTC()
			st.mu.Unlock()
			return nil, lifecycleError(executionrouter.CodeManagerShuttingDown, ErrManagerShuttingDown, true)
		}

		st.mu.Lock()
		if st.deleted || st.mutating || st.runtimeChanging {
			if st.active > 0 {
				st.active--
			}
			st.lastActivity = time.Now().UTC()
			busy := busyErrorLocked(st)
			st.mu.Unlock()
			_ = closeRuntime(context.Background(), entry, connected)
			return nil, busy
		}
		st.session = connected
		st.generation++
		generation := st.generation
		st.lastActivity = time.Now().UTC()
		session = connected
		st.mu.Unlock()
		s.watchSession(st, generation, connected)
	}

	return &UseLease{service: s, state: st, session: session}, nil
}

type UseLease struct {
	service *Service
	state   *serverState
	session RuntimeSession

	released atomic.Bool
	taskMu   sync.Mutex
	task     *time.Timer
	expires  time.Time
}

func (l *UseLease) InitialTools() downstream.ToolSnapshot {
	if l == nil || l.session == nil {
		return downstream.ToolSnapshot{}
	}
	return l.session.InitialTools()
}

func (l *UseLease) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if l == nil || l.service == nil || l.session == nil {
		return nil, fmt.Errorf("%w: missing routed lifecycle session", downstream.ErrDownstreamUnavailable)
	}
	if l.released.Load() {
		return nil, fmt.Errorf("%w: routed lifecycle lease already released", downstream.ErrDownstreamUnavailable)
	}
	l.touch()
	callCtx, cancel := l.service.operationContext(ctx)
	defer cancel()
	result, err := l.session.CallTool(callCtx, params)
	l.touch()
	return result, err
}

func (l *UseLease) Release() {
	if l == nil || l.service == nil || l.state == nil || !l.released.CompareAndSwap(false, true) {
		return
	}
	l.taskMu.Lock()
	if l.task != nil {
		l.task.Stop()
		l.task = nil
	}
	l.taskMu.Unlock()
	l.service.release(l.state)
}

func (l *UseLease) ExpiresAt() *time.Time {
	if l == nil {
		return nil
	}
	l.taskMu.Lock()
	defer l.taskMu.Unlock()
	if l.expires.IsZero() {
		return nil
	}
	copy := l.expires
	return &copy
}

func (l *UseLease) touch() {
	if l == nil || l.state == nil || l.released.Load() {
		return
	}
	l.state.mu.Lock()
	l.state.lastActivity = time.Now().UTC()
	l.state.mu.Unlock()
}

func (s *Service) AcquireTaskLease(ctx context.Context, serverID string, ttl time.Duration) (*UseLease, error) {
	lease, err := s.Acquire(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 || ttl > s.maxTaskLease {
		ttl = s.maxTaskLease
	}
	expires := time.Now().UTC().Add(ttl)
	lease.taskMu.Lock()
	lease.expires = expires
	lease.task = time.AfterFunc(ttl, lease.Release)
	lease.taskMu.Unlock()
	return lease, nil
}

func (s *Service) release(st *serverState) {
	st.mu.Lock()
	if st.active > 0 {
		st.active--
	}
	st.lastActivity = time.Now().UTC()
	if st.active == 0 {
		s.scheduleIdleLocked(st)
	}
	st.mu.Unlock()
}

func (s *Service) Snapshot(serverID string) (Snapshot, error) {
	st, err := s.lookup(serverID)
	if err != nil {
		return Snapshot{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted {
		return Snapshot{}, lifecycleError("server_not_found", ErrServerNotFound, false)
	}
	return snapshotLocked(st), nil
}

func (s *Service) Snapshots() []Snapshot {
	s.mu.RLock()
	states := make([]*serverState, 0, len(s.states))
	for _, st := range s.states {
		states = append(states, st)
	}
	s.mu.RUnlock()
	out := make([]Snapshot, 0, len(states))
	for _, st := range states {
		st.mu.Lock()
		if !st.deleted {
			out = append(out, snapshotLocked(st))
		}
		st.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServerID < out[j].ServerID })
	return out
}

func snapshotLocked(st *serverState) Snapshot {
	out := Snapshot{
		ServerID:        st.entry.ID,
		Name:            st.entry.Name,
		Mode:            st.entry.Mode,
		Running:         st.session != nil && !sessionExited(st.session),
		ActiveCallCount: st.active,
	}
	if !st.lastActivity.IsZero() {
		value := st.lastActivity
		out.LastActivityAt = &value
	}
	if !st.idleDeadline.IsZero() {
		value := st.idleDeadline
		out.IdleDeadlineAt = &value
	}
	return out
}

func (s *Service) Start(ctx context.Context, serverID string) (Snapshot, error) {
	st, err := s.lookup(serverID)
	if err != nil {
		return Snapshot{}, err
	}
	return s.startState(ctx, st)
}

func (s *Service) startState(ctx context.Context, st *serverState) (Snapshot, error) {
	if s.closing.Load() {
		return Snapshot{}, lifecycleError(executionrouter.CodeManagerShuttingDown, ErrManagerShuttingDown, true)
	}
	if err := lockOperation(ctx, st); err != nil {
		return Snapshot{}, err
	}
	defer unlockOperation(st)

	var stale RuntimeSession
	st.mu.Lock()
	if st.deleted {
		st.mu.Unlock()
		return Snapshot{}, lifecycleError("server_not_found", ErrServerNotFound, false)
	}
	if st.mutating || st.runtimeChanging {
		busy := busyErrorLocked(st)
		st.mu.Unlock()
		return Snapshot{}, busy
	}
	entry := st.entry
	if entry.Mode == v2config.ModeDisabled {
		st.mu.Unlock()
		return Snapshot{}, lifecycleError(executionrouter.CodeServerDisabled, ErrServerDisabled, false)
	}
	if st.session != nil && sessionExited(st.session) {
		stale = st.session
		st.session = nil
		st.generation++
		cancelIdleLocked(st)
	}
	if st.session != nil {
		out := snapshotLocked(st)
		st.mu.Unlock()
		if stale != nil {
			_ = closeRuntime(context.Background(), entry, stale)
		}
		return out, nil
	}
	st.runtimeChanging = true
	cancelIdleLocked(st)
	st.mu.Unlock()

	if stale != nil {
		_ = closeRuntime(context.Background(), entry, stale)
	}
	connectCtx, cancel := s.operationContext(ctx)
	connected, connectErr := s.connect(connectCtx, entry)
	cancel()
	if connectErr != nil {
		st.mu.Lock()
		st.runtimeChanging = false
		st.lastActivity = time.Now().UTC()
		out := snapshotLocked(st)
		st.mu.Unlock()
		return out, fmt.Errorf("connect server %s: %w", entry.ID, connectErr)
	}
	if connected == nil {
		st.mu.Lock()
		st.runtimeChanging = false
		out := snapshotLocked(st)
		st.mu.Unlock()
		return out, errors.New("downstream connect returned a nil session")
	}
	if s.closing.Load() {
		_ = closeRuntime(context.Background(), entry, connected)
		st.mu.Lock()
		st.runtimeChanging = false
		out := snapshotLocked(st)
		st.mu.Unlock()
		return out, lifecycleError(executionrouter.CodeManagerShuttingDown, ErrManagerShuttingDown, true)
	}

	st.mu.Lock()
	st.session = connected
	st.generation++
	generation := st.generation
	st.runtimeChanging = false
	st.lastActivity = time.Now().UTC()
	if st.active == 0 {
		s.scheduleIdleLocked(st)
	}
	out := snapshotLocked(st)
	st.mu.Unlock()
	s.watchSession(st, generation, connected)
	return out, nil
}

func (s *Service) Stop(ctx context.Context, serverID string) (Snapshot, error) {
	st, err := s.lookup(serverID)
	if err != nil {
		return Snapshot{}, err
	}
	st.mu.Lock()
	if st.entry.Mode == v2config.ModeAlwaysOn {
		out := snapshotLocked(st)
		st.mu.Unlock()
		return out, lifecycleError("always_on_maintained", ErrAlwaysOnMaintained, false)
	}
	if st.active > 0 || st.mutating || st.runtimeChanging {
		out := snapshotLocked(st)
		busy := busyErrorLocked(st)
		st.mu.Unlock()
		return out, busy
	}
	st.mu.Unlock()
	return s.stopState(ctx, st, false)
}

func (s *Service) stopState(ctx context.Context, st *serverState, managerShutdown bool) (Snapshot, error) {
	if err := lockOperation(ctx, st); err != nil {
		return Snapshot{}, err
	}
	defer unlockOperation(st)

	st.mu.Lock()
	if st.deleted {
		st.mu.Unlock()
		return Snapshot{}, lifecycleError("server_not_found", ErrServerNotFound, false)
	}
	if !managerShutdown && st.entry.Mode == v2config.ModeAlwaysOn {
		out := snapshotLocked(st)
		st.mu.Unlock()
		return out, lifecycleError("always_on_maintained", ErrAlwaysOnMaintained, false)
	}
	if !managerShutdown && (st.active > 0 || st.mutating || st.runtimeChanging) {
		out := snapshotLocked(st)
		busy := busyErrorLocked(st)
		st.mu.Unlock()
		return out, busy
	}
	entry := st.entry
	current := st.session
	st.session = nil
	st.generation++
	cancelIdleLocked(st)
	st.runtimeChanging = current != nil
	st.lastActivity = time.Now().UTC()
	st.mu.Unlock()

	var closeErr error
	if current != nil {
		closeErr = closeRuntime(ctx, entry, current)
	}
	st.mu.Lock()
	st.runtimeChanging = false
	st.lastActivity = time.Now().UTC()
	out := snapshotLocked(st)
	st.mu.Unlock()
	return out, closeErr
}

func (s *Service) Restart(ctx context.Context, serverID string) (Snapshot, error) {
	st, err := s.lookup(serverID)
	if err != nil {
		return Snapshot{}, err
	}
	st.mu.Lock()
	if st.active > 0 || st.mutating || st.runtimeChanging {
		out := snapshotLocked(st)
		busy := busyErrorLocked(st)
		st.mu.Unlock()
		return out, busy
	}
	if st.entry.Mode == v2config.ModeDisabled {
		out := snapshotLocked(st)
		st.mu.Unlock()
		return out, lifecycleError(executionrouter.CodeServerDisabled, ErrServerDisabled, false)
	}
	st.mu.Unlock()

	if err := lockOperation(ctx, st); err != nil {
		return Snapshot{}, err
	}
	defer unlockOperation(st)
	st.mu.Lock()
	if st.active > 0 || st.mutating || st.runtimeChanging {
		out := snapshotLocked(st)
		busy := busyErrorLocked(st)
		st.mu.Unlock()
		return out, busy
	}
	entry := st.entry
	old := st.session
	st.session = nil
	st.generation++
	cancelIdleLocked(st)
	st.runtimeChanging = true
	st.lastActivity = time.Now().UTC()
	st.mu.Unlock()
	if old != nil {
		if err := closeRuntime(ctx, entry, old); err != nil {
			st.mu.Lock()
			st.runtimeChanging = false
			out := snapshotLocked(st)
			st.mu.Unlock()
			return out, err
		}
	}
	connectCtx, cancel := s.operationContext(ctx)
	connected, connectErr := s.connect(connectCtx, entry)
	cancel()
	if connectErr != nil {
		st.mu.Lock()
		st.runtimeChanging = false
		out := snapshotLocked(st)
		st.mu.Unlock()
		return out, fmt.Errorf("connect server %s: %w", entry.ID, connectErr)
	}
	if connected == nil {
		st.mu.Lock()
		st.runtimeChanging = false
		out := snapshotLocked(st)
		st.mu.Unlock()
		return out, errors.New("downstream connect returned a nil session")
	}
	st.mu.Lock()
	st.session = connected
	st.generation++
	generation := st.generation
	st.runtimeChanging = false
	st.lastActivity = time.Now().UTC()
	if st.active == 0 {
		s.scheduleIdleLocked(st)
	}
	out := snapshotLocked(st)
	st.mu.Unlock()
	s.watchSession(st, generation, connected)
	return out, nil
}

func (s *Service) StartAlwaysOn(ctx context.Context) error {
	s.mu.RLock()
	states := make([]*serverState, 0)
	for _, st := range s.states {
		st.mu.Lock()
		alwaysOn := !st.deleted && st.entry.Mode == v2config.ModeAlwaysOn
		st.mu.Unlock()
		if alwaysOn {
			states = append(states, st)
		}
	}
	s.mu.RUnlock()

	var wg sync.WaitGroup
	errs := make(chan error, len(states))
	for _, st := range states {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.startState(ctx, st); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	var joined error
	for err := range errs {
		joined = errors.Join(joined, err)
	}
	return joined
}

func (s *Service) watchSession(st *serverState, generation uint64, session RuntimeSession) {
	done := session.Done()
	if done == nil {
		return
	}
	go func() {
		select {
		case <-s.ctx.Done():
			return
		case <-done:
		}
		if err := lockOperation(s.ctx, st); err != nil {
			return
		}
		st.mu.Lock()
		if st.deleted || st.generation != generation || st.session != session {
			st.mu.Unlock()
			unlockOperation(st)
			return
		}
		entry := st.entry
		st.session = nil
		st.generation++
		cancelIdleLocked(st)
		st.lastActivity = time.Now().UTC()
		maintain := entry.Mode == v2config.ModeAlwaysOn && !st.mutating && !s.closing.Load()
		st.mu.Unlock()
		unlockOperation(st)
		_ = closeRuntime(context.Background(), entry, session)
		if maintain {
			go s.maintainAlwaysOn(st)
		}
	}()
}

func (s *Service) maintainAlwaysOn(st *serverState) {
	delay := 100 * time.Millisecond
	for !s.closing.Load() {
		st.mu.Lock()
		if st.deleted || st.entry.Mode != v2config.ModeAlwaysOn || st.session != nil {
			st.mu.Unlock()
			return
		}
		blocked := st.mutating || st.runtimeChanging
		st.mu.Unlock()
		if !blocked {
			if _, err := s.startState(s.ctx, st); err == nil {
				return
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 5*time.Second {
			delay *= 2
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
		}
	}
}

func (s *Service) scheduleIdleLocked(st *serverState) {
	cancelIdleLocked(st)
	if s.closing.Load() || st.deleted || st.entry.Mode != v2config.ModeManaged || st.active != 0 || st.session == nil || st.mutating || st.runtimeChanging {
		return
	}
	idle := st.entry.IdleTimeout(s.defaultIdle)
	if idle <= 0 {
		return
	}
	if st.lastActivity.IsZero() {
		st.lastActivity = time.Now().UTC()
	}
	deadline := st.lastActivity.Add(idle)
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	st.idleSequence++
	sequence := st.idleSequence
	generation := st.generation
	session := st.session
	st.idleDeadline = deadline
	st.idleTimer = time.AfterFunc(remaining, func() {
		s.idleStop(st, generation, sequence, session)
	})
}

func (s *Service) idleStop(st *serverState, generation, sequence uint64, session RuntimeSession) {
	if s.closing.Load() {
		return
	}
	if err := lockOperation(s.ctx, st); err != nil {
		return
	}
	defer unlockOperation(st)
	st.mu.Lock()
	if st.deleted || st.entry.Mode != v2config.ModeManaged || st.active != 0 || st.mutating || st.runtimeChanging || st.generation != generation || st.idleSequence != sequence || st.session != session {
		st.mu.Unlock()
		return
	}
	if !st.idleDeadline.IsZero() && time.Now().Before(st.idleDeadline) {
		s.scheduleIdleLocked(st)
		st.mu.Unlock()
		return
	}
	entry := st.entry
	st.runtimeChanging = true
	st.session = nil
	st.generation++
	cancelIdleLocked(st)
	st.lastActivity = time.Now().UTC()
	st.mu.Unlock()
	_ = closeRuntime(context.Background(), entry, session)
	st.mu.Lock()
	st.runtimeChanging = false
	st.lastActivity = time.Now().UTC()
	st.mu.Unlock()
}

func cancelIdleLocked(st *serverState) {
	st.idleSequence++
	if st.idleTimer != nil {
		st.idleTimer.Stop()
		st.idleTimer = nil
	}
	st.idleDeadline = time.Time{}
}

func (s *Service) PrepareServerMutation(current, next v2config.ServersConfig) error {
	if s.closing.Load() {
		return lifecycleError(executionrouter.CodeManagerShuttingDown, ErrManagerShuttingDown, true)
	}
	affected := runtimeAffectingServerIDs(current, next)
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if s.pending != nil {
		return errors.New("server mutation already prepared")
	}
	prepared := make([]*serverState, 0, len(affected))
	pending := make(map[string]struct{}, len(affected))
	for _, serverID := range affected {
		st, err := s.lookup(serverID)
		if errors.Is(err, ErrServerNotFound) {
			continue
		}
		if err != nil {
			for _, prior := range prepared {
				prior.mu.Lock()
				prior.mutating = false
				prior.mu.Unlock()
			}
			return err
		}
		st.mu.Lock()
		if st.deleted || st.active > 0 || st.mutating || st.runtimeChanging {
			busy := busyErrorLocked(st)
			st.mu.Unlock()
			for _, prior := range prepared {
				prior.mu.Lock()
				prior.mutating = false
				prior.mu.Unlock()
			}
			return busy
		}
		st.mutating = true
		st.mu.Unlock()
		prepared = append(prepared, st)
		pending[serverID] = struct{}{}
	}
	s.pending = pending
	return nil
}

func (s *Service) AbortServerMutation() {
	s.mutationMu.Lock()
	pending := s.pending
	s.pending = nil
	s.mutationMu.Unlock()
	for serverID := range pending {
		if st, err := s.lookup(serverID); err == nil {
			st.mu.Lock()
			st.mutating = false
			st.mu.Unlock()
		}
	}
}

func (s *Service) CommitServerMutation(next v2config.ServersConfig) {
	s.mutationMu.Lock()
	pending := s.pending
	s.pending = nil
	s.mutationMu.Unlock()
	if pending == nil {
		pending = map[string]struct{}{}
	}
	nextByID := make(map[string]v2config.ServerEntry, len(next.Servers))
	for _, entry := range next.Servers {
		nextByID[entry.ID] = entry
	}

	s.mu.Lock()
	states := make(map[string]*serverState, len(s.states))
	for id, st := range s.states {
		states[id] = st
	}
	newAlwaysOn := make([]*serverState, 0)
	for id, entry := range nextByID {
		if _, ok := states[id]; ok {
			continue
		}
		st := newServerState(entry)
		s.states[id] = st
		states[id] = st
		if entry.Mode == v2config.ModeAlwaysOn {
			newAlwaysOn = append(newAlwaysOn, st)
		}
	}
	s.mu.Unlock()

	for id, st := range states {
		entry, exists := nextByID[id]
		_, affected := pending[id]
		if !affected {
			if exists {
				st.mu.Lock()
				st.entry = entry
				st.mu.Unlock()
			}
			continue
		}
		_ = lockOperation(context.Background(), st)
		st.mu.Lock()
		oldEntry := st.entry
		oldSession := st.session
		st.session = nil
		st.generation++
		cancelIdleLocked(st)
		st.runtimeChanging = oldSession != nil
		if exists {
			st.entry = entry
		} else {
			st.deleted = true
		}
		st.mu.Unlock()
		if oldSession != nil {
			_ = closeRuntime(context.Background(), oldEntry, oldSession)
		}
		if !exists {
			s.mu.Lock()
			if s.states[id] == st {
				delete(s.states, id)
			}
			s.mu.Unlock()
		}
		st.mu.Lock()
		st.runtimeChanging = false
		st.mutating = false
		st.lastActivity = time.Now().UTC()
		maintain := exists && entry.Mode == v2config.ModeAlwaysOn && !s.closing.Load()
		st.mu.Unlock()
		unlockOperation(st)
		if maintain {
			go s.maintainAlwaysOn(st)
		}
	}
	for _, st := range newAlwaysOn {
		if !s.closing.Load() {
			go s.maintainAlwaysOn(st)
		}
	}
}

func runtimeAffectingServerIDs(current, next v2config.ServersConfig) []string {
	oldByID := make(map[string]v2config.ServerEntry, len(current.Servers))
	newByID := make(map[string]v2config.ServerEntry, len(next.Servers))
	for _, entry := range current.Servers {
		oldByID[entry.ID] = entry
	}
	for _, entry := range next.Servers {
		newByID[entry.ID] = entry
	}
	set := make(map[string]struct{})
	for id, oldEntry := range oldByID {
		newEntry, ok := newByID[id]
		if !ok || runtimeAffectingChange(oldEntry, newEntry) {
			set[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func runtimeAffectingChange(oldEntry, newEntry v2config.ServerEntry) bool {
	return oldEntry.Name != newEntry.Name ||
		oldEntry.Mode != newEntry.Mode ||
		!reflect.DeepEqual(oldEntry.Transport, newEntry.Transport) ||
		!reflect.DeepEqual(oldEntry.Environment, newEntry.Environment) ||
		!reflect.DeepEqual(oldEntry.Runtime, newEntry.Runtime)
}

func (s *Service) Close(ctx context.Context) error {
	if !s.closing.CompareAndSwap(false, true) {
		return nil
	}
	s.cancel()
	s.AbortServerMutation()
	s.mu.RLock()
	states := make([]*serverState, 0, len(s.states))
	for _, st := range s.states {
		states = append(states, st)
	}
	s.mu.RUnlock()

	var wg sync.WaitGroup
	errs := make(chan error, len(states))
	for _, st := range states {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := lockOperation(context.Background(), st); err != nil {
				errs <- err
				return
			}
			defer unlockOperation(st)
			st.mu.Lock()
			entry := st.entry
			current := st.session
			st.session = nil
			st.generation++
			cancelIdleLocked(st)
			st.runtimeChanging = current != nil
			st.lastActivity = time.Now().UTC()
			st.mu.Unlock()
			if current != nil {
				if err := closeRuntime(ctx, entry, current); err != nil {
					errs <- fmt.Errorf("close server %s: %w", entry.ID, err)
				}
			}
			st.mu.Lock()
			st.runtimeChanging = false
			st.mu.Unlock()
		}()
	}
	wg.Wait()
	close(errs)
	var joined error
	for err := range errs {
		joined = errors.Join(joined, err)
	}
	return joined
}

func (s *Service) lookup(serverID string) (*serverState, error) {
	s.mu.RLock()
	st := s.states[serverID]
	s.mu.RUnlock()
	if st == nil {
		return nil, lifecycleError("server_not_found", ErrServerNotFound, false)
	}
	return st, nil
}

func (s *Service) operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (s *Service) scheduleIdleIfEligible(st *serverState) {
	st.mu.Lock()
	if st.active == 0 {
		s.scheduleIdleLocked(st)
	}
	st.mu.Unlock()
}

func lockOperation(ctx context.Context, st *serverState) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-st.op:
		return nil
	}
}

func unlockOperation(st *serverState) { st.op <- struct{}{} }

func sessionExited(session RuntimeSession) bool {
	if session == nil {
		return true
	}
	done := session.Done()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func closeRuntime(parent context.Context, entry v2config.ServerEntry, session RuntimeSession) error {
	if session == nil {
		return nil
	}
	if parent == nil || parent.Err() != nil {
		parent = context.Background()
	}
	if _, ok := parent.Deadline(); ok {
		return session.Close(parent)
	}
	ctx, cancel := context.WithTimeout(parent, entry.ShutdownTimeout()+time.Second)
	defer cancel()
	return session.Close(ctx)
}

func busyErrorLocked(st *serverState) *ServerBusyError {
	return &ServerBusyError{ServerID: st.entry.ID, ActiveCallCount: st.active}
}

func lifecycleError(code string, cause error, retryable bool) *LifecycleError {
	message := code
	if cause != nil {
		message = cause.Error()
	}
	return &LifecycleError{Code: code, Message: message, Retryable: retryable, cause: cause}
}
