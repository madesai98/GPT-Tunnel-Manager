package app

import (
	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
)

func (a *App) Entries() []config.ServerEntry { return a.registry.Entries() }
func (a *App) Snapshots() []servers.Snapshot { return a.registry.List() }
func (a *App) ManagerMCPURL() string { return a.mcp.URL() }
