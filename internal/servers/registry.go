package servers

import(
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/events"
)
type Registry struct{ctx context.Context;store *config.Store;factory *Factory;bus *events.Bus;defaultIdle int;mu sync.RWMutex;items map[string]*Supervisor;entries map[string]config.ServerEntry}
func NewRegistry(ctx context.Context,store *config.Store,cfg config.ServersConfig,defaultIdle int,f *Factory,b *events.Bus)*Registry{r:=&Registry{ctx:ctx,store:store,factory:f,bus:b,defaultIdle:defaultIdle,items:map[string]*Supervisor{},entries:map[string]config.ServerEntry{}};for _,e:=range cfg.Servers{r.entries[e.ID]=e;r.items[e.ID]=NewSupervisor(ctx,e,defaultIdle,f,b)};return r}
func(r *Registry)List()[]Snapshot{r.mu.RLock();xs:=make([]*Supervisor,0,len(r.items));for _,s:=range r.items{xs=append(xs,s)};r.mu.RUnlock();out:=make([]Snapshot,0,len(xs));for _,s:=range xs{out=append(out,s.Snapshot())};sort.Slice(out,func(i,j int)bool{return out[i].Name<out[j].Name});return out}
func(r *Registry)Get(id string)(*Supervisor,error){r.mu.RLock();defer r.mu.RUnlock();s:=r.items[id];if s==nil{return nil,ErrNotFound};return s,nil}
func(r *Registry)Entry(id string)(config.ServerEntry,error){r.mu.RLock();defer r.mu.RUnlock();e,ok:=r.entries[id];if !ok{return e,ErrNotFound};return e,nil}
func(r *Registry)Start(ctx context.Context,id string,src Source)(Snapshot,error){s,err:=r.Get(id);if err!=nil{return Snapshot{},err};return s.Start(ctx,src)}
func(r *Registry)Restart(ctx context.Context,id string,src Source)(Snapshot,error){s,err:=r.Get(id);if err!=nil{return Snapshot{},err};return s.Restart(ctx,src)}
func(r *Registry)Shutdown(ctx context.Context,id string,src Source)(Snapshot,error){s,err:=r.Get(id);if err!=nil{return Snapshot{},err};return s.Shutdown(ctx,src)}
func(r *Registry)Save(e config.ServerEntry)(config.ServerEntry,error){if e.ID==""{id,err:=config.NewServerID();if err!=nil{return e,err};e.ID=id};if err:=config.ValidateServer(e);err!=nil{return e,err};r.mu.Lock();defer r.mu.Unlock();if s:=r.items[e.ID];s!=nil{if err:=s.UpdateEntry(e);err!=nil{return e,err}}else{r.items[e.ID]=NewSupervisor(r.ctx,e,r.defaultIdle,r.factory,r.bus)};r.entries[e.ID]=e;if err:=r.persistLocked();err!=nil{return e,err};return e,nil}
func(r *Registry)Delete(ctx context.Context,id string)error{r.mu.RLock();s:=r.items[id];r.mu.RUnlock();if s==nil{return ErrNotFound};c,cancel:=context.WithTimeout(ctx,s.Entry().ShutdownTimeout()+3*time.Second);_ = s.ForceStop(c);cancel();r.mu.Lock();defer r.mu.Unlock();delete(r.items,id);delete(r.entries,id);return r.persistLocked()}
func(r *Registry)persistLocked()error{cfg:=config.ServersConfig{SchemaVersion:config.SchemaVersion,Servers:make([]config.ServerEntry,0,len(r.entries))};for _,e:=range r.entries{cfg.Servers=append(cfg.Servers,e)};sort.Slice(cfg.Servers,func(i,j int)bool{return cfg.Servers[i].Name<cfg.Servers[j].Name});return r.store.SaveServers(cfg)}
func(r *Registry)StartAlwaysOn(ctx context.Context){r.mu.RLock();list:=make([]*Supervisor,0);for _,s:=range r.items{e:=s.Entry();if e.Enabled&&e.Mode==config.ModeAlwaysOn{list=append(list,s)}};r.mu.RUnlock();sem:=make(chan struct{},4);for _,s:=range list{s:=s;go func(){sem<-struct{}{};defer func(){<-sem}();c,cancel:=context.WithTimeout(ctx,s.Entry().StartupTimeout()+5*time.Second);defer cancel();_,_=s.Start(c,SourceApp)}()}}
func(r *Registry)StopAll(ctx context.Context)error{r.mu.RLock();list:=make([]*Supervisor,0,len(r.items));for _,s:=range r.items{list=append(list,s)};r.mu.RUnlock();var wg sync.WaitGroup;sem:=make(chan struct{},4);errs:=make(chan error,len(list));for _,s:=range list{s:=s;wg.Add(1);go func(){defer wg.Done();sem<-struct{}{};defer func(){<-sem}();c,cancel:=context.WithTimeout(ctx,s.Entry().ShutdownTimeout()+3*time.Second);defer cancel();if err:=s.ForceStop(c);err!=nil{errs<-fmt.Errorf("%s: %w",s,err)}}()};wg.Wait();close(errs);for err:=range errs{return err};return nil}
