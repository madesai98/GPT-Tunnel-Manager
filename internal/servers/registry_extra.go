package servers
import("sort";"github.com/madesai98/GPT-Tunnel-Manager/internal/config")
func(r *Registry)Entries()[]config.ServerEntry{r.mu.RLock();defer r.mu.RUnlock();out:=make([]config.ServerEntry,0,len(r.entries));for _,e:=range r.entries{out=append(out,e)};sort.Slice(out,func(i,j int)bool{return out[i].Name<out[j].Name});return out}
func(r *Registry)SetDefaultIdle(seconds int){r.mu.Lock();r.defaultIdle=seconds;for _,s:=range r.items{s.mu.Lock();s.defaultIdle=seconds;s.notifyLocked();s.mu.Unlock()};r.mu.Unlock()}
