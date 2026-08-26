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

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/events"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycleskill"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/logging"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpmanager"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/platform"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/tunnelclient"
)

type managerStatus struct {
	State     string `json:"state"`
	Ready     bool   `json:"ready"`
	Error     string `json:"error,omitempty"`
	HealthURL string `json:"health_url,omitempty"`
}

type App struct {
	root string
	exe  string
	ctx  context.Context

	cancel       context.CancelFunc
	done         chan struct{}
	shutdownOnce sync.Once

	store       *config.Store
	cfgMu       sync.RWMutex
	managerCfg  config.ManagerConfig
	secretStore secrets.Store
	log         *logging.Logger
	bus         *events.Bus
	installer   *tunnelclient.Installer
	factory     *servers.Factory
	registry    *servers.Registry
	mcp         *mcpmanager.Server

	mgrMu      sync.Mutex
	mgrRuntime *tunnelclient.Runtime
	mgrGen     uint64
	mgrStatus  managerStatus

	latestMu sync.RWMutex
	latest   string
}

func New(root, exe string) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())
	store := config.NewStore(root)
	managerCfg, serverCfg, err := store.LoadOrCreate()
	if err != nil {
		cancel()
		return nil, err
	}

	secretStore := secrets.New(root)

	// The Manager Runtime API key has one canonical, internal secret reference.
	// Users only enter the key value in the native UI. If an older configuration
	// used a custom reference, migrate its stored value before normalizing config.
	oldCredentialRef := managerCfg.ManagerTunnel.RuntimeCredentialRef
	if oldCredentialRef != "" && oldCredentialRef != config.ManagerRuntimeCredentialRef {
		value, getErr := secretStore.Get(ctx, oldCredentialRef)
		if getErr == nil {
			if putErr := secretStore.Put(ctx, config.ManagerRuntimeCredentialRef, value); putErr != nil {
				cancel()
				return nil, putErr
			}
			_ = secretStore.Delete(ctx, oldCredentialRef)
		} else if !errors.Is(getErr, secrets.ErrNotFound) {
			cancel()
			return nil, getErr
		}
	}
	if managerCfg.ManagerTunnel.RuntimeCredentialRef != config.ManagerRuntimeCredentialRef || !managerCfg.General.MinimizeToTray {
		managerCfg.ManagerTunnel.RuntimeCredentialRef = config.ManagerRuntimeCredentialRef
		managerCfg.General.MinimizeToTray = true
		if err := store.SaveManager(managerCfg); err != nil {
			cancel()
			return nil, err
		}
	}

	log, err := logging.New(
		root,
		managerCfg.Logging.CaptureLevel,
		managerCfg.Logging.MemoryLimitMB,
		managerCfg.Logging.WriteToDisk,
		managerCfg.Logging.DiskMinimumLevel,
		managerCfg.Logging.MaximumFileSizeMB,
		managerCfg.Logging.KeepFiles,
	)
	if err != nil {
		cancel()
		return nil, err
	}

	bus := events.New()
	installer := tunnelclient.NewInstaller(root)
	factory := &servers.Factory{
		Installer:            installer,
		BinaryOverride:       managerCfg.TunnelClient.BinaryPath,
		Secrets:              secretStore,
		DefaultCredentialRef: config.ManagerRuntimeCredentialRef,
		HealthRoot:           filepath.Join(root, "data", "tunnel-client", "state"),
		Log:                  log,
	}
	registry := servers.NewRegistry(ctx, store, serverCfg, managerCfg.ManagedDefaults.IdleTimeoutSeconds, factory, bus)

	a := &App{
		root:        root,
		exe:         exe,
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		store:       store,
		managerCfg:  managerCfg,
		secretStore: secretStore,
		log:         log,
		bus:         bus,
		installer:   installer,
		factory:     factory,
		registry:    registry,
	}
	a.mcp = mcpmanager.New(registry)

	ch, unsub := bus.Subscribe(256)
	go func() {
		defer unsub()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-ch:
				if !ok {
					return
				}
				log.Log(logging.Info, sourceFor(event), "Lifecycle", string(event.Kind), event.Fields)
			}
		}
	}()

	return a, nil
}

func sourceFor(event events.Event) string {
	if event.ServerID != "" {
		return event.ServerID
	}
	return "Manager"
}

func (a *App) Start() error {
	if err := a.mcp.Start(); err != nil {
		return err
	}
	a.log.Log(logging.Info, "Manager", "Application", "GPT Tunnel Manager started", map[string]any{
		"manager_mcp_url": a.mcp.URL(),
	})
	go a.restartManagerTunnel()
	a.registry.StartAlwaysOn(a.ctx)
	go a.updaterLoop()
	go func() {
		<-a.ctx.Done()
		a.shutdown()
	}()
	return nil
}

func (a *App) Done() <-chan struct{} { return a.done }
func (a *App) Root() string          { return a.root }
func (a *App) RequestShutdown()      { a.cancel() }

func (a *App) ManagerConfig() config.ManagerConfig {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.managerCfg
}

func (a *App) shutdown() {
	a.shutdownOnce.Do(func() {
		a.log.Log(logging.Info, "Manager", "Application", "shutting down", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		_ = a.mcp.Stop(ctx)
		_ = a.registry.StopAll(ctx)

		a.mgrMu.Lock()
		runtime := a.mgrRuntime
		a.mgrRuntime = nil
		a.mgrGen++
		a.mgrMu.Unlock()
		if runtime != nil {
			_ = runtime.Stop(ctx)
		}

		_ = a.log.Close()
		close(a.done)
	})
}

func (a *App) restartManagerTunnel() {
	a.mgrMu.Lock()
	old := a.mgrRuntime
	a.mgrRuntime = nil
	a.mgrGen++
	generation := a.mgrGen
	a.mgrStatus = managerStatus{State: "starting"}
	a.mgrMu.Unlock()

	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		_ = old.Stop(ctx)
		cancel()
	}

	a.cfgMu.RLock()
	cfg := a.managerCfg
	a.cfgMu.RUnlock()
	if cfg.ManagerTunnel.TunnelID == "" {
		a.setManagerStatus(generation, managerStatus{State: "not_configured"})
		return
	}

	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()

	active, err := a.installer.Ensure(ctx, cfg.TunnelClient.BinaryPath)
	if err != nil {
		a.managerStartFailed(generation, err)
		return
	}
	key, err := a.secretStore.Get(ctx, config.ManagerRuntimeCredentialRef)
	if err != nil {
		a.managerStartFailed(generation, err)
		return
	}
	a.log.Redactor().Register(key)

	runtime, err := tunnelclient.Start(ctx, tunnelclient.RunSpec{
		Binary:              active.Path,
		TunnelID:            cfg.ManagerTunnel.TunnelID,
		APIKey:              string(key),
		MCPURL:              a.mcp.URL(),
		HealthDir:           filepath.Join(a.root, "data", "tunnel-client", "state", "manager"),
		StartupTimeout:      30 * time.Second,
		ShutdownTimeout:     10 * time.Second,
		TelemetryCompatible: false,
		OnLog: func(stream, line string) {
			a.log.Log(logging.Info, "Manager", "Tunnel Client", line, map[string]any{"stream": stream})
		},
	})
	if err != nil {
		a.managerStartFailed(generation, err)
		return
	}

	a.mgrMu.Lock()
	if generation != a.mgrGen {
		a.mgrMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = runtime.Stop(ctx)
		cancel()
		return
	}
	a.mgrRuntime = runtime
	a.mgrStatus = managerStatus{State: "ready", Ready: true, HealthURL: runtime.HealthURL()}
	a.mgrMu.Unlock()

	a.bus.Publish(events.Event{Kind: events.TunnelReady})
	go a.watchManager(generation, runtime)
}

func (a *App) setManagerStatus(generation uint64, status managerStatus) {
	a.mgrMu.Lock()
	if generation == a.mgrGen {
		a.mgrStatus = status
	}
	a.mgrMu.Unlock()
}

func (a *App) managerStartFailed(generation uint64, err error) {
	a.log.Log(logging.Error, "Manager", "Tunnel", "manager tunnel start failed", map[string]any{"error": err.Error()})
	a.setManagerStatus(generation, managerStatus{State: "degraded", Error: err.Error()})
	if a.ctx.Err() != nil {
		return
	}
	go func() {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		a.mgrMu.Lock()
		valid := generation == a.mgrGen
		a.mgrMu.Unlock()
		if valid {
			a.restartManagerTunnel()
		}
	}()
}

func (a *App) watchManager(generation uint64, runtime *tunnelclient.Runtime) {
	<-runtime.Done()
	if a.ctx.Err() != nil {
		return
	}

	a.mgrMu.Lock()
	if generation != a.mgrGen || a.mgrRuntime != runtime {
		a.mgrMu.Unlock()
		return
	}
	a.mgrRuntime = nil
	a.mgrStatus = managerStatus{State: "degraded", Error: fmt.Sprint(runtime.Err())}
	a.mgrMu.Unlock()

	a.bus.Publish(events.Event{
		Kind:   events.TunnelDisconnected,
		Fields: map[string]any{"error": fmt.Sprint(runtime.Err())},
	})
	go func() {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(3 * time.Second):
			a.restartManagerTunnel()
		}
	}()
}

func (a *App) SaveManager(ctx context.Context, cfg config.ManagerConfig) error {
	cfg.ManagerTunnel.RuntimeCredentialRef = config.ManagerRuntimeCredentialRef
	cfg.General.MinimizeToTray = true
	if err := config.ValidateManager(cfg); err != nil {
		return err
	}

	a.cfgMu.Lock()
	old := a.managerCfg
	if err := a.store.SaveManager(cfg); err != nil {
		a.cfgMu.Unlock()
		return err
	}
	a.managerCfg = cfg
	a.factory.DefaultCredentialRef = config.ManagerRuntimeCredentialRef
	a.factory.BinaryOverride = cfg.TunnelClient.BinaryPath
	a.registry.SetDefaultIdle(cfg.ManagedDefaults.IdleTimeoutSeconds)
	a.cfgMu.Unlock()

	if old.General.LaunchAtStartup != cfg.General.LaunchAtStartup {
		if err := platform.SetLaunchAtStartup(ctx, cfg.General.LaunchAtStartup, a.exe); err != nil {
			return err
		}
	}
	if old.Logging != cfg.Logging {
		if err := a.log.Reconfigure(cfg.Logging); err != nil {
			return err
		}
	}
	if old.ManagerTunnel != cfg.ManagerTunnel || old.TunnelClient.BinaryPath != cfg.TunnelClient.BinaryPath {
		go a.restartManagerTunnel()
	}
	a.log.Log(logging.Info, "Manager", "Configuration", "manager settings saved", nil)
	return nil
}

func (a *App) SaveServer(ctx context.Context, entry config.ServerEntry) (config.ServerEntry, error) {
	saved, err := a.registry.Save(entry)
	if err == nil {
		a.log.Log(logging.Info, saved.ID, "Configuration", "server entry saved", nil)
	}
	return saved, err
}

func (a *App) DeleteServer(ctx context.Context, id string) error {
	err := a.registry.Delete(ctx, id)
	if err == nil {
		a.log.Log(logging.Info, id, "Configuration", "server entry deleted", nil)
	}
	return err
}

func (a *App) Lifecycle(ctx context.Context, id, action string) (servers.Snapshot, error) {
	switch action {
	case "start":
		return a.registry.Start(ctx, id, servers.SourceUI)
	case "restart":
		return a.registry.Restart(ctx, id, servers.SourceUI)
	case "shutdown", "stop":
		return a.registry.Shutdown(ctx, id, servers.SourceUI)
	default:
		return servers.Snapshot{}, errors.New("unknown lifecycle action")
	}
}

func (a *App) PutSecret(ctx context.Context, ref, value string) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	if value == "" {
		return errors.New("secret value cannot be empty")
	}
	data := []byte(value)
	if err := a.secretStore.Put(ctx, ref, data); err != nil {
		return err
	}
	a.log.Redactor().Register(data)
	a.log.Log(logging.Info, "Manager", "Secrets", "secret stored", map[string]any{"ref": ref})
	return nil
}

func (a *App) DeleteSecret(ctx context.Context, ref string) error {
	return a.secretStore.Delete(ctx, ref)
}

func (a *App) Logs() []logging.Event { return a.log.Ring().Snapshot() }
func (a *App) ClearLogs()            { a.log.Ring().Clear() }

func (a *App) ExportLogs(format string) (string, error) {
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
		if _, err := fmt.Fprintf(file, "%s %-5s %-18s %-14s %s%s\n",
			event.Timestamp.Format(time.RFC3339),
			event.Level,
			event.Source,
			event.Component,
			event.Message,
			fields,
		); err != nil {
			return "", err
		}
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) CheckUpdate(ctx context.Context) (tunnelclient.Release, error) {
	release, err := a.installer.CheckLatest(ctx)
	if err == nil {
		a.latestMu.Lock()
		a.latest = release.TagName
		a.latestMu.Unlock()
		a.bus.Publish(events.Event{
			Kind:   events.TunnelClientUpdateAvailable,
			Fields: map[string]any{"version": release.TagName},
		})
	}
	return release, err
}

func (a *App) InstallUpdate(ctx context.Context) (tunnelclient.Active, error) {
	active, err := a.installer.InstallLatest(ctx)
	if err == nil {
		a.bus.Publish(events.Event{
			Kind:   events.TunnelClientUpdated,
			Fields: map[string]any{"version": active.Version},
		})
	}
	return active, err
}

func (a *App) Rollback(ctx context.Context) (tunnelclient.Active, error) {
	active, err := a.installer.Rollback()
	if err == nil {
		a.log.Log(logging.Warn, "Manager", "Updater", "tunnel-client rolled back", map[string]any{"version": active.Version})
	}
	return active, err
}

func (a *App) updaterLoop() {
	select {
	case <-a.ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}

	for {
		a.cfgMu.RLock()
		cfg := a.managerCfg.TunnelClient
		a.cfgMu.RUnlock()

		release, err := a.CheckUpdate(a.ctx)
		if err != nil {
			a.log.Log(logging.Warn, "Manager", "Updater", "update check failed", map[string]any{"error": err.Error()})
		} else if cfg.AutoUpdate {
			current, _ := a.installer.Current()
			if release.TagName != "" && release.TagName != current.Version {
				if _, err := a.InstallUpdate(a.ctx); err != nil {
					a.log.Log(logging.Warn, "Manager", "Updater", "automatic update failed", map[string]any{"error": err.Error()})
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

func (a *App) ExportSkill(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return err
	}
	return os.WriteFile(path, []byte(lifecycleskill.Content), 0o600)
}
