package indexing

import "github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"

// SetServers updates the authoritative Server Entry set used by subsequent
// explicit index refreshes. It deliberately does not start indexing; normal
// configuration writes only make the current routing generation stale.
func (s *Service) SetServers(servers v2config.ServersConfig) error {
	if err := v2config.ValidateServers(servers); err != nil {
		return err
	}
	copyConfig := v2config.ServersConfig{SchemaVersion: servers.SchemaVersion, Servers: append([]v2config.ServerEntry(nil), servers.Servers...)}
	s.mu.Lock()
	s.servers = copyConfig
	s.mu.Unlock()
	return nil
}
