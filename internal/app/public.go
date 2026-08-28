package app

import (
	"context"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/logging"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
)

type ManagerSnapshot struct {
	State     string
	Ready     bool
	Enabled   bool
	Error     string
	HealthURL string
	TunnelID  string
	MCPURL    string
}

func (a *App) Entries() []config.ServerEntry { return a.registry.Entries() }
func (a *App) Snapshots() []servers.Snapshot { return a.registry.List() }
func (a *App) ManagerMCPURL() string         { return a.mcp.URL() }

func (a *App) ManagerSnapshot() ManagerSnapshot {
	a.cfgMu.RLock()
	tunnelID := a.managerCfg.ManagerTunnel.TunnelID
	a.cfgMu.RUnlock()

	a.mgrMu.Lock()
	status := a.mgrStatus
	a.mgrMu.Unlock()

	return ManagerSnapshot{
		State:     status.State,
		Ready:     status.Ready,
		Enabled:   status.State != "stopped",
		Error:     status.Error,
		HealthURL: status.HealthURL,
		TunnelID:  tunnelID,
		MCPURL:    a.mcp.URL(),
	}
}

func (a *App) RestartManagerTunnel() {
	go a.restartManagerTunnel()
}

func (a *App) SetManagerTunnelEnabled(enabled bool) {
	if enabled {
		go a.restartManagerTunnel()
		return
	}

	go func() {
		a.mgrMu.Lock()
		runtime := a.mgrRuntime
		a.mgrRuntime = nil
		a.mgrGen++
		a.mgrStatus = managerStatus{State: "stopped"}
		a.mgrMu.Unlock()
		a.mgrBackoff.Reset()

		if runtime != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			_ = runtime.Stop(ctx)
			cancel()
		}
		a.log.Log(logging.Info, "Manager", "Tunnel", "manager tunnel disabled", nil)
	}()
}

func (a *App) LogSelfUpdate(message string, fields map[string]any) {
	a.log.Log(logging.Info, "Manager", "Updater", message, fields)
}
