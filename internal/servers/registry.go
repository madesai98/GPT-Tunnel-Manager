package servers

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/events"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycle"
)

type serverStore interface {
	SaveServers(config.ServersConfig) error
}

type Registry struct {
	ctx         context.Context
	store       serverStore
	factory     RuntimeFactory
	bus         *events.Bus
	defaultIdle int

	opMu    sync.Mutex
	mu      sync.RWMutex
	items   map[string]*Supervisor
	entries map[string]config.ServerEntry
}

func NewRegistry(ctx context.Context, store serverStore, cfg config.ServersConfig, defaultIdle int, f RuntimeFactory, b *events.Bus) *Registry {
	r := &Registry{
		ctx:         ctx,
		store:       store,
		factory:     f,
		bus:         b,
		defaultIdle: defaultIdle,
		items:       map[string]*Supervisor{},
		entries:     map[string]config.ServerEntry{},
	}
	for _, e := range cfg.Servers {
		r.entries[e.ID] = e
		r.items[e.ID] = NewSupervisor(ctx, e, defaultIdle, f, b)
	}
	return r
}

func (r *Registry) List() []Snapshot {
	r.mu.RLock()
	xs := make([]*Supervisor, 0, len(r.items))
	for _, s := range r.items {
		xs = append(xs, s)
	}
	r.mu.RUnlock()
	out := make([]Snapshot, 0, len(xs))
	for _, s := range xs {
		out = append(out, s.Snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) Get(id string) (*Supervisor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := r.items[id]
	if s == nil {
		return nil, ErrNotFound
	}
	return s, nil
}

func (r *Registry) Entry(id string) (config.ServerEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return e, ErrNotFound
	}
	return e, nil
}

func (r *Registry) Start(ctx context.Context, id string, src Source) (Snapshot, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	s, err := r.Get(id)
	if err != nil {
		return Snapshot{}, err
	}
	return r.startSupervisor(ctx, s, src)
}

func (r *Registry) Restart(ctx context.Context, id string, src Source) (Snapshot, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	s, err := r.Get(id)
	if err != nil {
		return Snapshot{}, err
	}
	return r.restartSupervisor(ctx, s, src)
}

func (r *Registry) Shutdown(ctx context.Context, id string, src Source) (Snapshot, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	s, err := r.Get(id)
	if err != nil {
		return Snapshot{}, err
	}
	return s.Shutdown(ctx, src)
}

func (r *Registry) Save(e config.ServerEntry) (config.ServerEntry, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	if e.ID == "" {
		id, err := config.NewServerID()
		if err != nil {
			return e, err
		}
		e.ID = id
	}
	if err := config.ValidateServer(e); err != nil {
		return e, err
	}

	r.mu.RLock()
	s := r.items[e.ID]
	oldEntry, existed := r.entries[e.ID]
	prospective := cloneEntries(r.entries)
	r.mu.RUnlock()
	prospective[e.ID] = e

	if !existed {
		if err := r.persist(prospective); err != nil {
			return e, err
		}
		s = NewSupervisor(r.ctx, e, r.defaultIdle, r.factory, r.bus)
		r.mu.Lock()
		r.entries[e.ID] = e
		r.items[e.ID] = s
		r.mu.Unlock()
		r.reconcileSaved(s, e, false)
		return e, nil
	}

	snap := s.Snapshot()
	wasDesiredRunning := snap.Desired == lifecycle.DesiredRunning
	if wasDesiredRunning {
		ctx, cancel := context.WithTimeout(context.Background(), oldEntry.ShutdownTimeout()+3*time.Second)
		err := s.ForceStop(ctx)
		cancel()
		if err != nil {
			return e, err
		}
	}

	if err := r.persist(prospective); err != nil {
		if wasDesiredRunning && oldEntry.Enabled {
			r.startBestEffort(s, oldEntry)
		}
		return e, err
	}

	if err := s.UpdateEntry(e); err != nil {
		rollback := cloneEntries(prospective)
		rollback[e.ID] = oldEntry
		_ = r.persist(rollback)
		if wasDesiredRunning && oldEntry.Enabled {
			r.startBestEffort(s, oldEntry)
		}
		return e, err
	}

	r.mu.Lock()
	r.entries[e.ID] = e
	r.mu.Unlock()
	// An edit is an explicit user intervention. If this entry had been
	// quarantined after a process-level crash, let the new configuration try
	// again rather than forcing the user to edit files by hand.
	r.clearStartupGuard(e.ID)
	r.reconcileSaved(s, e, wasDesiredRunning)
	return e, nil
}

func (r *Registry) reconcileSaved(s *Supervisor, e config.ServerEntry, wasDesiredRunning bool) {
	if !e.Enabled {
		return
	}
	if e.Mode == config.ModeAlwaysOn || wasDesiredRunning {
		r.startBestEffort(s, e)
	}
}

func (r *Registry) startBestEffort(s *Supervisor, e config.ServerEntry) {
	ctx, cancel := context.WithTimeout(r.ctx, e.StartupTimeout()+5*time.Second)
	defer cancel()
	_, _ = r.startSupervisor(ctx, s, SourceApp)
}

func (r *Registry) Delete(ctx context.Context, id string) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.RLock()
	s := r.items[id]
	oldEntry, ok := r.entries[id]
	prospective := cloneEntries(r.entries)
	r.mu.RUnlock()
	if s == nil || !ok {
		return ErrNotFound
	}

	snap := s.Snapshot()
	wasDesiredRunning := snap.Desired == lifecycle.DesiredRunning
	stopCtx, cancel := context.WithTimeout(ctx, oldEntry.ShutdownTimeout()+3*time.Second)
	err := s.ForceStop(stopCtx)
	cancel()
	if err != nil {
		return err
	}

	delete(prospective, id)
	if err := r.persist(prospective); err != nil {
		if wasDesiredRunning && oldEntry.Enabled {
			r.startBestEffort(s, oldEntry)
		}
		return err
	}

	r.mu.Lock()
	delete(r.items, id)
	delete(r.entries, id)
	r.mu.Unlock()
	r.clearStartupGuard(id)
	return nil
}

func cloneEntries(in map[string]config.ServerEntry) map[string]config.ServerEntry {
	out := make(map[string]config.ServerEntry, len(in))
	for id, entry := range in {
		out[id] = entry
	}
	return out
}

func (r *Registry) persist(entries map[string]config.ServerEntry) error {
	cfg := config.ServersConfig{
		SchemaVersion: config.SchemaVersion,
		Servers:       make([]config.ServerEntry, 0, len(entries)),
	}
	for _, e := range entries {
		cfg.Servers = append(cfg.Servers, e)
	}
	sort.Slice(cfg.Servers, func(i, j int) bool { return cfg.Servers[i].Name < cfg.Servers[j].Name })
	return r.store.SaveServers(cfg)
}

func (r *Registry) StartAlwaysOn(ctx context.Context) {
	r.opMu.Lock()
	r.mu.RLock()
	list := make([]*Supervisor, 0)
	for _, s := range r.items {
		e := s.Entry()
		if e.Enabled && e.Mode == config.ModeAlwaysOn {
			list = append(list, s)
		}
	}
	r.mu.RUnlock()
	r.opMu.Unlock()

	sem := make(chan struct{}, 4)
	for _, s := range list {
		s := s
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			e := s.Entry()
			c, cancel := context.WithTimeout(ctx, e.StartupTimeout()+5*time.Second)
			defer cancel()
			_, _ = r.startSupervisor(c, s, SourceApp)
		}()
	}
}

func (r *Registry) StopAll(ctx context.Context) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.RLock()
	list := make([]*Supervisor, 0, len(r.items))
	for _, s := range r.items {
		list = append(list, s)
	}
	r.mu.RUnlock()

	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	errs := make(chan error, len(list))
	for _, s := range list {
		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			e := s.Entry()
			c, cancel := context.WithTimeout(ctx, e.ShutdownTimeout()+3*time.Second)
			defer cancel()
			if err := s.ForceStop(c); err != nil {
				errs <- fmt.Errorf("%s: %w", s, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}
