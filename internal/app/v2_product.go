package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/logging"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/platform"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/tunnelclient"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

// V2ManagerTunnelSnapshot is safe for native/headless status surfaces. It never
// contains the runtime credential or its secret-store reference.
type V2ManagerTunnelSnapshot struct {
	State     string
	Ready     bool
	Error     string
	HealthURL string
}

type v2ProductRuntime struct {
	root      string
	exe       string
	log       *logging.Logger
	installer *tunnelclient.Installer

	mgrMu      sync.RWMutex
	mgrRuntime *tunnelclient.Runtime
	mgrGen     uint64
	mgrStatus  V2ManagerTunnelSnapshot
	mgrBackoff *lifecycle.Backoff
}

func newV2ProductRuntime(root string, cfg v2config.ManagerConfig) (*v2ProductRuntime, error) {
	logger, err := logging.New(
		root,
		cfg.Logging.CaptureLevel,
		cfg.Logging.MemoryLimitMB,
		cfg.Logging.WriteToDisk,
		cfg.Logging.DiskMinimumLevel,
		cfg.Logging.MaximumFileSizeMB,
		cfg.Logging.KeepFiles,
	)
	if err != nil {
		return nil, err
	}
	exe, _ := os.Executable()
	return &v2ProductRuntime{
		root:      root,
		exe:       exe,
		log:       logger,
		installer: tunnelclient.NewInstaller(root),
		mgrStatus: V2ManagerTunnelSnapshot{State: "disabled"},
		mgrBackoff: lifecycle.NewBackoff(time.Now().UnixNano()),
	}, nil
}

func (p *v2ProductRuntime) start(a *V2App) {
	if p == nil {
		return
	}
	p.log.Log(logging.Info, "Manager", "Application", "GPT Tunnel Manager v2 started", map[string]any{
		"manager_mcp_url": a.ManagerSnapshot().MCPURL,
	})
	go p.restartManagerTunnel(a)
	go p.updaterLoop(a)
}

func (p *v2ProductRuntime) close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.log.Log(logging.Info, "Manager", "Application", "shutting down", nil)
	p.mgrMu.Lock()
	runtime := p.mgrRuntime
	p.mgrRuntime = nil
	p.mgrGen++
	p.mgrStatus = V2ManagerTunnelSnapshot{State: "stopped"}
	p.mgrMu.Unlock()
	var joined error
	if runtime != nil {
		joined = errors.Join(joined, runtime.Stop(ctx))
	}
	joined = errors.Join(joined, p.log.Close())
	return joined
}

func (p *v2ProductRuntime) tunnelSnapshot() V2ManagerTunnelSnapshot {
	if p == nil {
		return V2ManagerTunnelSnapshot{State: "unavailable"}
	}
	p.mgrMu.RLock()
	defer p.mgrMu.RUnlock()
	return p.mgrStatus
}

func (p *v2ProductRuntime) logDownstream(serverID, stream, line string) {
	if p == nil || p.log == nil {
		return
	}
	p.log.Log(logging.Debug, serverID, "Downstream MCP", line, map[string]any{"stream": stream})
}

func (p *v2ProductRuntime) restartManagerTunnel(a *V2App) {
	if p == nil || a == nil || a.ctx.Err() != nil {
		return
	}
	p.mgrMu.Lock()
	old := p.mgrRuntime
	p.mgrRuntime = nil
	p.mgrGen++
	generation := p.mgrGen
	p.mgrStatus = V2ManagerTunnelSnapshot{State: "starting"}
	p.mgrMu.Unlock()

	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		_ = old.Stop(ctx)
		cancel()
	}

	cfg := a.ManagerConfig()
	if !cfg.ManagerTunnel.Enabled {
		p.mgrBackoff.Reset()
		p.setManagerTunnelStatus(generation, V2ManagerTunnelSnapshot{State: "disabled"})
		return
	}
	if cfg.ManagerTunnel.TunnelID == "" {
		p.mgrBackoff.Reset()
		p.setManagerTunnelStatus(generation, V2ManagerTunnelSnapshot{State: "not_configured"})
		return
	}

	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	active, err := p.installer.EnsureChannel(ctx, cfg.TunnelClient.BinaryPath, cfg.TunnelClient.Channel)
	if err != nil {
		p.managerTunnelStartFailed(a, generation, err)
		return
	}
	credential, err := a.secrets.Get(ctx, v2config.ManagerRuntimeCredentialRef)
	if errors.Is(err, secrets.ErrNotFound) {
		p.mgrBackoff.Reset()
		p.setManagerTunnelStatus(generation, V2ManagerTunnelSnapshot{State: "needs_credential", Error: "Manager tunnel runtime API key is not configured"})
		return
	}
	if err != nil {
		p.managerTunnelStartFailed(a, generation, err)
		return
	}
	p.log.Redactor().Register(credential)

	runtime, err := tunnelclient.Start(ctx, tunnelclient.RunSpec{
		Binary:          active.Path,
		TunnelID:        cfg.ManagerTunnel.TunnelID,
		APIKey:          string(credential),
		MCPURL:          a.ManagerSnapshot().MCPURL,
		HealthDir:       filepath.Join(p.root, "data", "tunnel-client", "state", "manager"),
		StartupTimeout:  30 * time.Second,
		ShutdownTimeout: 10 * time.Second,
		OnLog: func(stream, line string) {
			p.log.Log(logging.Info, "Manager", "Tunnel Client", line, map[string]any{"stream": stream})
		},
	})
	if err != nil {
		p.managerTunnelStartFailed(a, generation, err)
		return
	}

	p.mgrMu.Lock()
	if generation != p.mgrGen || a.ctx.Err() != nil {
		p.mgrMu.Unlock()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = runtime.Stop(stopCtx)
		stopCancel()
		return
	}
	p.mgrRuntime = runtime
	p.mgrStatus = V2ManagerTunnelSnapshot{State: "ready", Ready: true, HealthURL: runtime.HealthURL()}
	p.mgrMu.Unlock()
	p.log.Log(logging.Info, "Manager", "Tunnel", "manager tunnel ready", map[string]any{"tunnel_id": cfg.ManagerTunnel.TunnelID})
	go p.watchManagerTunnel(a, generation, runtime)
	go p.resetManagerTunnelBackoff(a, generation, runtime)
}

func (p *v2ProductRuntime) setManagerTunnelStatus(generation uint64, status V2ManagerTunnelSnapshot) {
	p.mgrMu.Lock()
	if generation == p.mgrGen {
		p.mgrStatus = status
	}
	p.mgrMu.Unlock()
}

func (p *v2ProductRuntime) managerTunnelStartFailed(a *V2App, generation uint64, err error) {
	p.log.Log(logging.Error, "Manager", "Tunnel", "manager tunnel start failed", map[string]any{"error": err.Error()})
	p.setManagerTunnelStatus(generation, V2ManagerTunnelSnapshot{State: "degraded", Error: err.Error()})
	if a.ctx.Err() == nil {
		p.scheduleManagerTunnelRetry(a, generation)
	}
}

func (p *v2ProductRuntime) watchManagerTunnel(a *V2App, generation uint64, runtime *tunnelclient.Runtime) {
	<-runtime.Done()
	if a.ctx.Err() != nil {
		return
	}
	p.mgrMu.Lock()
	if generation != p.mgrGen || p.mgrRuntime != runtime {
		p.mgrMu.Unlock()
		return
	}
	p.mgrRuntime = nil
	p.mgrStatus = V2ManagerTunnelSnapshot{State: "degraded", Error: fmt.Sprint(runtime.Err())}
	p.mgrMu.Unlock()
	p.log.Log(logging.Warn, "Manager", "Tunnel", "manager tunnel disconnected", map[string]any{"error": fmt.Sprint(runtime.Err())})
	p.scheduleManagerTunnelRetry(a, generation)
}

func (p *v2ProductRuntime) scheduleManagerTunnelRetry(a *V2App, generation uint64) {
	delay := p.mgrBackoff.Next()
	p.log.Log(logging.Warn, "Manager", "Tunnel", "manager tunnel retry scheduled", map[string]any{"after": delay.String()})
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
		}
		p.mgrMu.RLock()
		valid := generation == p.mgrGen && p.mgrRuntime == nil
		p.mgrMu.RUnlock()
		if valid {
			p.restartManagerTunnel(a)
		}
	}()
}

func (p *v2ProductRuntime) resetManagerTunnelBackoff(a *V2App, generation uint64, runtime *tunnelclient.Runtime) {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-a.ctx.Done():
		return
	case <-runtime.Done():
		return
	case <-timer.C:
	}
	p.mgrMu.RLock()
	stable := generation == p.mgrGen && p.mgrRuntime == runtime
	p.mgrMu.RUnlock()
	if stable {
		p.mgrBackoff.Reset()
	}
}

func v2LoggingChanged(old, next v2config.LoggingConfig) bool {
	return old.CaptureLevel != next.CaptureLevel ||
		old.MemoryLimitMB != next.MemoryLimitMB ||
		old.WriteToDisk != next.WriteToDisk ||
		old.DiskMinimumLevel != next.DiskMinimumLevel ||
		old.MaximumFileSizeMB != next.MaximumFileSizeMB ||
		old.KeepFiles != next.KeepFiles
}

// prepareManagerChange applies operating-system/runtime settings that must be
// reversible if persistence or Manager reconstruction fails.
func (p *v2ProductRuntime) prepareManagerChange(ctx context.Context, old, next v2config.ManagerConfig) (func(), error) {
	if p == nil {
		return func() {}, nil
	}
	launchChanged := old.General.LaunchAtStartup != next.General.LaunchAtStartup
	loggingChanged := v2LoggingChanged(old.Logging, next.Logging)
	if launchChanged {
		if p.exe == "" {
			return nil, errors.New("cannot configure launch at startup: executable path is unavailable")
		}
		if err := platform.SetLaunchAtStartup(ctx, next.General.LaunchAtStartup, p.exe); err != nil {
			return nil, err
		}
	}
	if loggingChanged {
		if err := p.log.ReconfigureValues(next.Logging.CaptureLevel, next.Logging.MemoryLimitMB, next.Logging.WriteToDisk, next.Logging.DiskMinimumLevel, next.Logging.MaximumFileSizeMB, next.Logging.KeepFiles); err != nil {
			if launchChanged {
				_ = platform.SetLaunchAtStartup(context.Background(), old.General.LaunchAtStartup, p.exe)
			}
			return nil, err
		}
	}
	return func() {
		if loggingChanged {
			_ = p.log.ReconfigureValues(old.Logging.CaptureLevel, old.Logging.MemoryLimitMB, old.Logging.WriteToDisk, old.Logging.DiskMinimumLevel, old.Logging.MaximumFileSizeMB, old.Logging.KeepFiles)
		}
		if launchChanged && p.exe != "" {
			_ = platform.SetLaunchAtStartup(context.Background(), old.General.LaunchAtStartup, p.exe)
		}
	}, nil
}

func (p *v2ProductRuntime) managerChanged(a *V2App, old, next v2config.ManagerConfig) {
	if p == nil {
		return
	}
	p.log.Log(logging.Info, "Manager", "Configuration", "manager settings saved", nil)
	if old.ManagerTunnel != next.ManagerTunnel ||
		old.TunnelClient.BinaryPath != next.TunnelClient.BinaryPath ||
		old.TunnelClient.Channel != next.TunnelClient.Channel {
		go p.restartManagerTunnel(a)
	}
}

func (a *V2App) SetManagerTunnelCredential(ctx context.Context, value []byte) error {
	if len(value) == 0 {
		return errors.New("Manager tunnel runtime API key cannot be empty")
	}
	if _, err := a.state.PutSecret(ctx, v2config.ManagerRuntimeCredentialRef, append([]byte(nil), value...)); err != nil {
		return err
	}
	if a.product != nil {
		a.product.log.Redactor().Register(value)
		a.product.log.Log(logging.Info, "Manager", "Secrets", "Manager tunnel runtime credential stored", nil)
		go a.product.restartManagerTunnel(a)
	}
	return nil
}

func (a *V2App) ManagerTunnelCredentialConfigured(ctx context.Context) bool {
	_, err := a.secrets.Get(ctx, v2config.ManagerRuntimeCredentialRef)
	return err == nil
}

func (a *V2App) ManagerTunnelStatus() V2ManagerTunnelSnapshot {
	if a == nil || a.product == nil {
		return V2ManagerTunnelSnapshot{State: "unavailable"}
	}
	return a.product.tunnelSnapshot()
}

func (a *V2App) Logs() []logging.Event {
	if a == nil || a.product == nil || a.product.log == nil {
		return nil
	}
	return a.product.log.Ring().Snapshot()
}

func (a *V2App) ClearLogs() {
	if a != nil && a.product != nil && a.product.log != nil {
		a.product.log.Ring().Clear()
	}
}

func (a *V2App) ExportLogs(format string) (string, error) {
	if a == nil || a.product == nil || a.product.log == nil {
		return "", errors.New("logger is unavailable")
	}
	extension := "txt"
	if format == "jsonl" {
		extension = "jsonl"
	} else if format != "text" {
		return "", fmt.Errorf("unsupported log export format %q", format)
	}
	dir := filepath.Join(a.root, "data", "log-exports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("gpt-tunnel-manager-%s.%s", time.Now().UTC().Format("20060102-150405"), extension))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	for _, event := range a.Logs() {
		if format == "jsonl" {
			line, err := json.Marshal(event)
			if err != nil {
				return "", err
			}
			if _, err := fmt.Fprintf(file, "%s\n", line); err != nil {
				return "", err
			}
			continue
		}
		fields := ""
		if len(event.Fields) != 0 {
			encoded, err := json.Marshal(event.Fields)
			if err != nil {
				return "", err
			}
			fields = " " + string(encoded)
		}
		if _, err := fmt.Fprintf(file, "%s %-5s %-18s %-14s %s%s\n", event.Timestamp.Format(time.RFC3339), event.Level, event.Source, event.Component, event.Message, fields); err != nil {
			return "", err
		}
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	return path, nil
}

func (a *V2App) LogSelfUpdate(message string, fields map[string]any) {
	if a != nil && a.product != nil && a.product.log != nil {
		a.product.log.Log(logging.Info, "Manager", "Self Update", message, fields)
	}
}

func (a *V2App) CheckTunnelClientUpdate(ctx context.Context) (tunnelclient.Release, error) {
	if a == nil || a.product == nil {
		return tunnelclient.Release{}, errors.New("tunnel-client installer is unavailable")
	}
	cfg := a.ManagerConfig().TunnelClient
	release, err := a.product.installer.CheckChannel(ctx, cfg.Channel)
	if err == nil {
		a.product.log.Log(logging.Info, "Manager", "Updater", "tunnel-client update checked", map[string]any{"version": release.TagName, "channel": cfg.Channel})
	}
	return release, err
}

func (a *V2App) InstallTunnelClientUpdate(ctx context.Context) (tunnelclient.Active, error) {
	if a == nil || a.product == nil {
		return tunnelclient.Active{}, errors.New("tunnel-client installer is unavailable")
	}
	cfg := a.ManagerConfig().TunnelClient
	active, err := a.product.installer.InstallChannel(ctx, cfg.Channel)
	if err == nil {
		a.product.log.Log(logging.Info, "Manager", "Updater", "tunnel-client updated", map[string]any{"version": active.Version, "channel": cfg.Channel})
		go a.product.restartManagerTunnel(a)
	}
	return active, err
}

func (a *V2App) RollbackTunnelClient(ctx context.Context) (tunnelclient.Active, error) {
	if a == nil || a.product == nil {
		return tunnelclient.Active{}, errors.New("tunnel-client installer is unavailable")
	}
	active, err := a.product.installer.Rollback()
	if err == nil {
		a.product.log.Log(logging.Warn, "Manager", "Updater", "tunnel-client rolled back", map[string]any{"version": active.Version})
		go a.product.restartManagerTunnel(a)
	}
	return active, err
}

func (p *v2ProductRuntime) updaterLoop(a *V2App) {
	select {
	case <-a.ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	for {
		cfg := a.ManagerConfig().TunnelClient
		release, err := a.CheckTunnelClientUpdate(a.ctx)
		if err != nil {
			p.log.Log(logging.Warn, "Manager", "Updater", "update check failed", map[string]any{"error": err.Error()})
		} else if cfg.AutoUpdate {
			current, _ := p.installer.Current()
			if release.TagName != "" && release.TagName != current.Version {
				if _, err := a.InstallTunnelClientUpdate(a.ctx); err != nil {
					p.log.Log(logging.Warn, "Manager", "Updater", "automatic update failed", map[string]any{"error": err.Error()})
				}
			}
		}
		delay := time.Duration(cfg.UpdateCheckIntervalHours) * time.Hour
		if delay <= 0 {
			delay = 24 * time.Hour
		}
		timer := time.NewTimer(delay)
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
