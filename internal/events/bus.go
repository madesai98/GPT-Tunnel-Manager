package events

import("sync";"time")
type Kind string
const( ServerStarting Kind="server_starting";ServerReady Kind="server_ready";ServerStopping Kind="server_stopping";ServerStopped Kind="server_stopped";ServerCrashed Kind="server_crashed";ServerRetryScheduled Kind="server_retry_scheduled";TunnelStarting Kind="tunnel_starting";TunnelReady Kind="tunnel_ready";TunnelDisconnected Kind="tunnel_disconnected";ManagedActivityObserved Kind="managed_activity_observed";TunnelClientUpdateAvailable Kind="tunnel_client_update_available";TunnelClientUpdated Kind="tunnel_client_updated")
type Event struct{Time time.Time `json:"time"`;Kind Kind `json:"kind"`;ServerID string `json:"server_id,omitempty"`;Fields map[string]any `json:"fields,omitempty"`}
type Bus struct{mu sync.RWMutex;subs map[chan Event]struct{}}
func New()*Bus{return &Bus{subs:map[chan Event]struct{}{}}}
func(b *Bus)Publish(e Event){if e.Time.IsZero(){e.Time=time.Now().UTC()};b.mu.RLock();defer b.mu.RUnlock();for ch:=range b.subs{select{case ch<-e:default:}}}
func(b *Bus)Subscribe(n int)(<-chan Event,func()){if n<1{n=32};ch:=make(chan Event,n);b.mu.Lock();b.subs[ch]=struct{}{};b.mu.Unlock();return ch,func(){b.mu.Lock();if _,ok:=b.subs[ch];ok{delete(b.subs,ch);close(ch)};b.mu.Unlock()}}
