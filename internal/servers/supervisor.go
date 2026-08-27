package servers

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/events"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycle"
)

var ErrRestartRequired = errors.New("restart_required")

type Supervisor struct {
	appCtx      context.Context
	entry       config.ServerEntry
	defaultIdle int
	factory     *Factory
	bus         *events.Bus

	opMu sync.Mutex
	mu   sync.RWMutex

	runtime          Runtime
	desired          lifecycle.DesiredState
	observed         lifecycle.ObservedState
	phase            lifecycle.Phase
	lastActivity     *time.Time
	retryAfter       *time.Time
	lastErr          *StatusError
	generation       uint64
	backoff          *lifecycle.Backoff
	changed          chan struct{}
	activityTracking bool
}

func NewSupervisor(ctx context.Context, e config.ServerEntry, defaultIdle int, f *Factory, b *events.Bus) *Supervisor {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Supervisor{
		appCtx:      ctx,
		entry:       e,
		defaultIdle: defaultIdle,
		factory:     f,
		bus:         b,
		desired:     lifecycle.DesiredStopped,
		observed:    lifecycle.Stopped,
		backoff:     lifecycle.NewBackoff(rand.Int63()),
		changed:     make(chan struct{}),
	}
}

func (s *Supervisor) Entry() config.ServerEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entry
}

func (s *Supervisor) UpdateEntry(e config.ServerEntry) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime != nil || s.observed == lifecycle.Starting || s.observed == lifecycle.Stopping {
		return ErrRestartRequired
	}
	s.entry = e
	s.notifyLocked()
	return nil
}

func (s *Supervisor) SetDefaultIdle(seconds int) {
	s.mu.Lock()
	if s.defaultIdle != seconds {
		s.defaultIdle = seconds
		s.notifyLocked()
	}
	s.mu.Unlock()
}

func (s *Supervisor) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *Supervisor) snapshotLocked() Snapshot {
	tracking := "unknown"
	if s.activityTracking {
		tracking = "tunnel_client_telemetry"
	}
	idle := s.entry.Mode == config.ModeManaged && s.entry.IdleTimeout(s.defaultIdle) > 0 && s.activityTracking
	return Snapshot{
		ServerID:            s.entry.ID,
		Name:                s.entry.Name,
		Enabled:             s.entry.Enabled,
		Mode:                string(s.entry.Mode),
		Desired:             s.desired,
		Observed:            s.observed,
		Phase:               s.phase,
		Ready:               s.observed == lifecycle.Ready,
		TunnelReady:         s.observed == lifecycle.Ready,
		IdleShutdownEnabled: idle,
		ActivityTracking:    tracking,
		LastActivityAt:      cloneTime(s.lastActivity),
		RetryAfter:          cloneTime(s.retryAfter),
		LastError:           s.lastErr,
	}
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func (s *Supervisor) allowed(source Source) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.entry.Enabled {
		return ErrDisabled
	}
	if source == SourceMCP && s.entry.Mode != config.ModeManaged {
		return ErrModeConflict
	}
	return nil
}

func (s *Supervisor) allowedShutdown(source Source) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.entry.Enabled {
		return ErrDisabled
	}
	if source == SourceMCP && s.entry.Mode != config.ModeManaged {
		return ErrModeConflict
	}
	if source == SourceUI && s.entry.Mode == config.ModeAlwaysOn {
		return ErrAlwaysOnStop
	}
	return nil
}

func (s *Supervisor) Start(ctx context.Context, source Source) (Snapshot, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.allowed(source); err != nil {
		return s.Snapshot(), err
	}
	return s.startLocked(ctx)
}

func (s *Supervisor) startLocked(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	if s.runtime != nil && (s.observed == lifecycle.Ready || s.observed == lifecycle.Starting) {
		s.desired = lifecycle.DesiredRunning
		s.notifyLocked()
		out := s.snapshotLocked()
		s.mu.Unlock()
		return out, nil
	}
	entry := s.entry
	s.desired = lifecycle.DesiredRunning
	s.observed = lifecycle.Starting
	s.phase = lifecycle.PhasePreflight
	s.lastErr = nil
	s.retryAfter = nil
	s.generation++
	gen := s.generation
	s.notifyLocked()
	s.mu.Unlock()

	s.publish(events.ServerStarting, nil)
	s.mu.Lock()
	s.phase = lifecycle.PhaseTunnel
	s.notifyLocked()
	s.mu.Unlock()

	r, err := s.factory.Start(ctx, entry)
	if err != nil {
		return s.failStart(gen, err)
	}
	now := time.Now().UTC()
	s.mu.Lock()
	if gen != s.generation || s.desired != lifecycle.DesiredRunning {
		s.mu.Unlock()
		c, cancel := context.WithTimeout(context.Background(), entry.ShutdownTimeout())
		_ = r.Stop(c)
		cancel()
		return s.Snapshot(), context.Canceled
	}
	s.runtime = r
	s.observed = lifecycle.Ready
	s.phase = lifecycle.PhaseReady
	s.lastActivity = &now
	s.activityTracking = r.ActivityTracking()
	s.lastErr = nil
	s.notifyLocked()
	out := s.snapshotLocked()
	mode := s.entry.Mode
	s.mu.Unlock()

	s.publish(events.ServerReady, nil)
	go s.watchRuntime(gen, r)
	go s.watchActivity(gen, r)
	go s.resetBackoff(gen, r)
	if mode == config.ModeManaged && r.ActivityTracking() {
		go s.watchIdle(gen, r)
	}
	return out, nil
}

func (s *Supervisor) failStart(gen uint64, err error) (Snapshot, error) {
	s.mu.Lock()
	if gen == s.generation {
		s.runtime = nil
		s.observed = lifecycle.Degraded
		s.phase = lifecycle.PhaseNone
		s.lastErr = &StatusError{Code: "start_failed", Message: sanitize(err), Retryable: true}
		s.notifyLocked()
	}
	out := s.snapshotLocked()
	desired := s.desired
	mode := s.entry.Mode
	s.mu.Unlock()

	s.publish(events.ServerCrashed, map[string]any{"error": sanitize(err)})
	if desired == lifecycle.DesiredRunning && (mode == config.ModeAlwaysOn || mode == config.ModeManaged) {
		s.scheduleRetry(gen)
	}
	return out, err
}

func sanitize(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}

func (s *Supervisor) Restart(ctx context.Context, source Source) (Snapshot, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.allowed(source); err != nil {
		return s.Snapshot(), err
	}
	if _, err := s.stopLocked(ctx, true); err != nil {
		return s.Snapshot(), err
	}
	return s.startLocked(ctx)
}

func (s *Supervisor) Shutdown(ctx context.Context, source Source) (Snapshot, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.allowedShutdown(source); err != nil {
		return s.Snapshot(), err
	}
	return s.stopLocked(ctx, true)
}

func (s *Supervisor) ForceStop(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	_, err := s.stopLocked(ctx, true)
	return err
}

func (s *Supervisor) stopLocked(ctx context.Context, setDesired bool) (Snapshot, error) {
	s.mu.Lock()
	entry := s.entry
	if setDesired {
		s.desired = lifecycle.DesiredStopped
	}
	s.generation++
	r := s.runtime
	s.runtime = nil
	s.retryAfter = nil
	if r == nil {
		s.observed = lifecycle.Stopped
		s.phase = lifecycle.PhaseNone
		s.activityTracking = false
		s.notifyLocked()
		out := s.snapshotLocked()
		s.mu.Unlock()
		return out, nil
	}
	s.observed = lifecycle.Stopping
	s.phase = lifecycle.PhaseStopping
	s.notifyLocked()
	s.mu.Unlock()

	s.publish(events.ServerStopping, nil)
	stopCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		stopCtx, cancel = context.WithTimeout(ctx, entry.ShutdownTimeout()+3*time.Second)
		defer cancel()
	}
	err := r.Stop(stopCtx)
	s.mu.Lock()
	s.observed = lifecycle.Stopped
	s.phase = lifecycle.PhaseNone
	s.activityTracking = false
	if err != nil {
		s.lastErr = &StatusError{Code: "stop_failed", Message: sanitize(err)}
	}
	s.notifyLocked()
	out := s.snapshotLocked()
	s.mu.Unlock()
	s.publish(events.ServerStopped, nil)
	return out, err
}

func (s *Supervisor) watchRuntime(gen uint64, r Runtime) {
	<-r.Done()
	err := r.Err()
	if err == nil {
		err = errors.New("runtime exited unexpectedly")
	}
	s.mu.Lock()
	if gen != s.generation || s.runtime != r {
		s.mu.Unlock()
		return
	}
	s.runtime = nil
	s.observed = lifecycle.Degraded
	s.phase = lifecycle.PhaseNone
	s.activityTracking = false
	s.lastErr = &StatusError{Code: "runtime_exited", Message: sanitize(err), Retryable: true}
	desired := s.desired
	mode := s.entry.Mode
	s.notifyLocked()
	s.mu.Unlock()

	s.publish(events.ServerCrashed, map[string]any{"error": sanitize(err)})
	if desired == lifecycle.DesiredRunning && (mode == config.ModeAlwaysOn || mode == config.ModeManaged) {
		s.scheduleRetry(gen)
	}
}

func (s *Supervisor) scheduleRetry(gen uint64) {
	delay := s.backoff.Next()
	at := time.Now().UTC().Add(delay)
	s.mu.Lock()
	if gen != s.generation || s.desired != lifecycle.DesiredRunning || s.runtime != nil {
		s.mu.Unlock()
		return
	}
	s.observed = lifecycle.RetryWait
	s.phase = lifecycle.PhaseRetry
	s.retryAfter = &at
	s.notifyLocked()
	s.mu.Unlock()

	s.publish(events.ServerRetryScheduled, map[string]any{"after": delay.String()})
	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-s.appCtx.Done():
			return
		case <-t.C:
		}
		s.mu.RLock()
		valid := gen == s.generation && s.desired == lifecycle.DesiredRunning && s.runtime == nil
		entry := s.entry
		s.mu.RUnlock()
		if !valid {
			return
		}
		c, cancel := context.WithTimeout(s.appCtx, entry.StartupTimeout()+5*time.Second)
		defer cancel()
		_, _ = s.Start(c, SourceRetry)
	}()
}

func (s *Supervisor) resetBackoff(gen uint64, r Runtime) {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	select {
	case <-s.appCtx.Done():
		return
	case <-r.Done():
		return
	case <-t.C:
	}
	s.mu.RLock()
	ok := gen == s.generation && s.runtime == r && s.observed == lifecycle.Ready
	s.mu.RUnlock()
	if ok {
		s.backoff.Reset()
	}
}

func (s *Supervisor) watchActivity(gen uint64, r Runtime) {
	for {
		select {
		case <-s.appCtx.Done():
			return
		case <-r.Done():
			return
		case at, ok := <-r.Activity():
			if !ok {
				return
			}
			if at.IsZero() {
				at = time.Now().UTC()
			}
			s.mu.Lock()
			if gen != s.generation || s.runtime != r {
				s.mu.Unlock()
				return
			}
			s.lastActivity = &at
			s.notifyLocked()
			s.mu.Unlock()
			s.publish(events.ManagedActivityObserved, map[string]any{"activity_at": at})
		}
	}
}

func (s *Supervisor) watchIdle(gen uint64, r Runtime) {
	for {
		s.mu.RLock()
		if gen != s.generation || s.runtime != r || s.desired != lifecycle.DesiredRunning || s.entry.Mode != config.ModeManaged {
			s.mu.RUnlock()
			return
		}
		idle := s.entry.IdleTimeout(s.defaultIdle)
		last := cloneTime(s.lastActivity)
		changed := s.changed
		entry := s.entry
		s.mu.RUnlock()

		if idle <= 0 || last == nil {
			select {
			case <-s.appCtx.Done():
				return
			case <-r.Done():
				return
			case <-changed:
				continue
			}
		}

		remaining := idle - time.Since(*last)
		if remaining <= 0 {
			c, cancel := context.WithTimeout(s.appCtx, entry.ShutdownTimeout()+3*time.Second)
			_, _ = s.Shutdown(c, SourceIdle)
			cancel()
			return
		}
		if remaining > 15*time.Second {
			remaining = 15 * time.Second
		}
		timer := time.NewTimer(remaining)
		select {
		case <-s.appCtx.Done():
			timer.Stop()
			return
		case <-r.Done():
			timer.Stop()
			return
		case <-changed:
			timer.Stop()
			continue
		case <-timer.C:
		}
	}
}

func (s *Supervisor) Wait(ctx context.Context, pred func(Snapshot) bool) (Snapshot, error) {
	for {
		s.mu.RLock()
		snap := s.snapshotLocked()
		ch := s.changed
		s.mu.RUnlock()
		if pred(snap) {
			return snap, nil
		}
		select {
		case <-ctx.Done():
			return s.Snapshot(), ctx.Err()
		case <-ch:
		}
	}
}

func (s *Supervisor) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *Supervisor) publish(kind events.Kind, fields map[string]any) {
	if s.bus != nil {
		s.bus.Publish(events.Event{Kind: kind, ServerID: s.entry.ID, Fields: fields})
	}
}

func (s *Supervisor) String() string {
	return fmt.Sprintf("%s(%s)", s.entry.Name, s.entry.ID)
}
