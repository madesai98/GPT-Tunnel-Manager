package servers

import (
	"sort"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
)

func (r *Registry) Entries() []config.ServerEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]config.ServerEntry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) SetDefaultIdle(seconds int) {
	r.mu.Lock()
	r.defaultIdle = seconds
	list := make([]*Supervisor, 0, len(r.items))
	for _, s := range r.items {
		list = append(list, s)
	}
	r.mu.Unlock()
	for _, s := range list {
		s.SetDefaultIdle(seconds)
	}
}
