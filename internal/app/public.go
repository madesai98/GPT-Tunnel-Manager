package app

import (
	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
)

type ManagerSnapshot struct {
	State     string
	Ready     bool
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
		Error:     status.Error,
		HealthURL: status.HealthURL,
		TunnelID:  tunnelID,
		MCPURL:    a.mcp.URL(),
	}
}

func (a *App) RestartManagerTunnel() {
	go a.restartManagerTunnel()
}
