package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/events"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycle"
)

var ErrStartupCrashQuarantined = errors.New("startup_crash_quarantined")

type startupGuardRecord struct {
	ServerID  string    `json:"server_id"`
	StartedAt time.Time `json:"started_at"`
}

func (r *Registry) startupGuardPath(id string) string {
	store, ok := r.store.(*config.Store)
	if !ok || store.Root == "" || id == "" {
		return ""
	}
	return filepath.Join(store.Root, "data", "startup-guards", id+".json")
}

func (r *Registry) hasStartupGuard(id string) bool {
	path := r.startupGuardPath(id)
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var record startupGuardRecord
	if json.Unmarshal(data, &record) != nil {
		return true
	}
	return record.ServerID == "" || record.ServerID == id
}

func (r *Registry) writeStartupGuard(id string) error {
	path := r.startupGuardPath(id)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	record := startupGuardRecord{ServerID: id, StartedAt: time.Now().UTC()}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".startup-guard-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func (r *Registry) clearStartupGuard(id string) {
	path := r.startupGuardPath(id)
	if path != "" {
		_ = os.Remove(path)
	}
}

func (s *Supervisor) markStartupQuarantined() Snapshot {
	s.mu.Lock()
	s.runtime = nil
	s.desired = lifecycle.DesiredStopped
	s.observed = lifecycle.Degraded
	s.phase = lifecycle.PhaseNone
	s.retryAfter = nil
	s.activityTracking = false
	s.lastErr = &StatusError{
		Code:      "startup_crash_quarantined",
		Message:   "automatic start skipped because GPT Tunnel Manager exited during this server's previous startup; start it manually to retry",
		Retryable: false,
	}
	s.notifyLocked()
	out := s.snapshotLocked()
	s.mu.Unlock()
	return out
}

func (r *Registry) startSupervisor(ctx context.Context, s *Supervisor, src Source) (Snapshot, error) {
	entry := s.Entry()
	if src == SourceApp && r.hasStartupGuard(entry.ID) {
		out := s.markStartupQuarantined()
		if r.bus != nil {
			r.bus.Publish(events.Event{
				Kind:     events.ServerCrashed,
				ServerID: entry.ID,
				Fields: map[string]any{
					"error": "automatic start quarantined after previous Manager exit during startup",
					"code":  "startup_crash_quarantined",
				},
			})
		}
		return out, fmt.Errorf("%w: %s", ErrStartupCrashQuarantined, entry.ID)
	}

	// A guard exists only while startup is in progress. Normal startup errors
	// clear it because the Manager survived them. If the entire process dies,
	// the file remains and the next launch skips only the offending Always On
	// entry, keeping the Manager UI recoverable without editing servers.json.
	if err := r.writeStartupGuard(entry.ID); err != nil {
		return s.Snapshot(), fmt.Errorf("write startup crash guard: %w", err)
	}
	out, err := s.Start(ctx, src)
	r.clearStartupGuard(entry.ID)
	return out, err
}
