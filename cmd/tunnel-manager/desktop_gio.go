//go:build !nogui

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	gioapp "gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/marker"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/platform"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
)

type rowActions struct{ start, stop, restart, edit, marker, delete widget.Clickable }

type serverForm struct {
	id                                                     string
	name, plugin, tunnel, cred, exe, cwd, url              widget.Editor
	args, env, secretEnv                                   widget.Editor
	startup, shutdown, idle                                widget.Editor
	enabled                                                  widget.Bool
	mode, transport                                         int
	modeBtn, transBtn, save, cancel, lifecycleMarker       widget.Clickable
}

type settingsForm struct {
	tunnel, cred, idle, secretRef, secretVal widget.Editor
	launch, startMinimized, tray              widget.Bool
	confirm, autoUpdate, disk                  widget.Bool
	closeMode, themeMode                       int
	closeBtn, themeBtn, save, store            widget.Clickable
	openWeb, check, install, rollback           widget.Clickable
}

type desktopUI struct {
	core *coreapp.App
	win  *gioapp.Window
	th   *material.Theme
	deco widget.Decorations

	page              string
	list, logs         widget.List
	serversNav, logsNav, settingsNav widget.Clickable
	addServer, refresh, exit          widget.Clickable
	rows                              map[string]*rowActions
	form                              serverForm
	settingsForm                      settingsForm

	logSearch                  widget.Editor
	logLevel                   int
	levelBtn, clearLogs         widget.Clickable
	exportText, exportJSONL     widget.Clickable

	confirmingExit bool
	dontAskAgain   widget.Bool
	cancelExit     widget.Clickable
	confirmExit    widget.Clickable

	mu       sync.RWMutex
	busy     bool
	message  string
	trayStop chan struct{}
}

func oneLine() widget.Editor { return widget.Editor{SingleLine: true} }

func newDesktopUI(core *coreapp.App, win *gioapp.Window) *desktopUI {
	u := &desktopUI{
		core: core, win: win, th: material.NewTheme(), page: "servers",
		list: widget.List{List: layout.List{Axis: layout.Vertical}},
		logs: widget.List{List: layout.List{Axis: layout.Vertical}},
		rows: make(map[string]*rowActions), trayStop: make(chan struct{}),
	}
	u.th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	u.logSearch = oneLine()
	for _, ed := range []*widget.Editor{&u.form.name, &u.form.plugin, &u.form.tunnel, &u.form.cred, &u.form.exe, &u.form.cwd, &u.form.url, &u.form.startup, &u.form.shutdown, &u.form.idle, &u.settingsForm.tunnel, &u.settingsForm.cred, &u.settingsForm.idle, &u.settingsForm.secretRef, &u.settingsForm.secretVal} {
		*ed = oneLine()
	}
	u.settingsForm.secretVal.Mask = '•'
	u.loadSettings()
	u.applyTheme()
	return u
}

func runDesktop(core *coreapp.App, setFocus func(func())) error {
	errCh := make(chan error, 1)
	ready := make(chan *desktopUI, 1)
	go func() {
		win := new(gioapp.Window)
		win.Option(gioapp.Title("GPT Tunnel Manager"), gioapp.Size(unit.Dp(1120), unit.Dp(760)), gioapp.MinSize(unit.Dp(780), unit.Dp(520)), gioapp.Decorated(false))
		u := newDesktopUI(core, win)
		ready <- u
		if core.ManagerConfig().General.StartMinimized { win.Option(gioapp.Minimized.Option()) }
		errCh <- u.loop()
	}()
	u := <-ready
	if setFocus != nil { setFocus(u.showWindow) }
	var stopTray func()
	if core.ManagerConfig().General.MinimizeToTray {
		start, stop := systray.RunWithExternalLoop(u.trayReady, func(){})
		start()
		stopTray = func(){ close(u.trayStop); stop() }
	}
	gioapp.Main()
	if stopTray != nil { stopTray() }
	return <-errCh
}

func (u *desktopUI) loop() error {
	go func(){
		t := time.NewTicker(time.Second); defer t.Stop()
		for { select { case <-u.core.Done(): u.win.Perform(system.ActionClose); return; case <-t.C: u.win.Invalidate() } }
	}()
	var ops op.Ops
	for {
		switch e := u.win.Event().(type) {
		case gioapp.ConfigEvent:
			u.deco.Maximized = e.Config.Mode == gioapp.Maximized
		case gioapp.DestroyEvent:
			u.core.RequestShutdown(); <-u.core.Done(); return e.Err
		case gioapp.FrameEvent:
			gtx := gioapp.NewContext(&ops, e); u.layout(gtx); e.Frame(gtx.Ops)
		}
	}
}

func (u *desktopUI) showWindow(){ u.win.Option(gioapp.Windowed.Option()); u.win.Perform(system.ActionRaise); u.win.Invalidate() }
func (u *desktopUI) requestClose(){ if u.core.ManagerConfig().General.CloseBehavior == "minimize" { u.win.Perform(system.ActionMinimize); return }; u.requestExit() }
func (u *desktopUI) requestExit(){
	if u.core.ManagerConfig().General.ConfirmExit { u.confirmingExit = true; u.dontAskAgain.Value = false; u.showWindow(); return }
	u.shutdownNow()
}
func (u *desktopUI) shutdownNow(){
	u.mu.Lock(); if u.message == "Shutting down…" { u.mu.Unlock(); return }; u.busy=true; u.message="Shutting down…"; u.mu.Unlock(); u.win.Invalidate()
	go func(){ u.core.RequestShutdown(); <-u.core.Done(); u.win.Perform(system.ActionClose) }()
}

func (u *desktopUI) trayReady(){
	systray.SetIcon(trayIcon()); systray.SetTitle("GPT Tunnel Manager"); systray.SetTooltip("GPT Tunnel Manager"); systray.SetOnTapped(u.showWindow)
	open := systray.AddMenuItem("Open Manager", "Show the GPT Tunnel Manager window")
	status := systray.AddMenuItem("Status", "Runtime status summary"); status.Disable()
	advanced := systray.AddMenuItem("Open Advanced Web UI", "Open the local loopback management UI")
	systray.AddSeparator(); exit := systray.AddMenuItem("Exit Tunnel Manager", "Disconnect tunnels and stop owned MCP servers")
	go func(){
		t := time.NewTicker(2*time.Second); defer t.Stop()
		for { select {
		case <-u.trayStop: return
		case <-open.ClickedCh: u.showWindow()
		case <-advanced.ClickedCh: _ = platform.OpenURL(context.Background(), u.core.AdminURL())
		case <-exit.ClickedCh: u.requestExit()
		case <-t.C:
			ready,degraded:=0,0; snaps:=u.core.Snapshots(); for _,s:=range snaps { if s.Ready{ready++}; if s.Observed=="degraded"{degraded++} }
			status.SetTitle(fmt.Sprintf("Status: %d ready, %d degraded, %d total",ready,degraded,len(snaps)))
		} }
	}()
}

func trayIcon() []byte {
	img:=image.NewNRGBA(image.Rect(0,0,16,16)); for y:=2;y<14;y++{for x:=2;x<14;x++{if x==2||x==13||y==2||y==13||(x>=6&&x<=9){img.SetNRGBA(x,y,color.NRGBA{R:75,G:112,B:240,A:255})}}}
	var p bytes.Buffer; _=png.Encode(&p,img); data:=p.Bytes(); var ico bytes.Buffer
	_=binary.Write(&ico,binary.LittleEndian,uint16(0)); _=binary.Write(&ico,binary.LittleEndian,uint16(1)); _=binary.Write(&ico,binary.LittleEndian,uint16(1)); ico.Write([]byte{16,16,0,0}); _=binary.Write(&ico,binary.LittleEndian,uint16(1)); _=binary.Write(&ico,binary.LittleEndian,uint16(32)); _=binary.Write(&ico,binary.LittleEndian,uint32(len(data))); _=binary.Write(&ico,binary.LittleEndian,uint32(22)); ico.Write(data); return ico.Bytes()
}

func (u *desktopUI) layout(gtx layout.Context) layout.Dimensions {
	paint.Fill(gtx.Ops,u.th.Bg)
	a:=u.deco.Update(gtx)
	if a&system.ActionMinimize!=0 { u.win.Perform(system.ActionMinimize) }
	if a&system.ActionMaximize!=0 { if u.deco.Maximized { u.win.Perform(system.ActionUnmaximize) } else { u.win.Perform(system.ActionMaximize) } }
	if a&system.ActionClose!=0 { u.requestClose() }
	return layout.Flex{Axis:layout.Vertical}.Layout(gtx,
		layout.Rigid(material.Decorations(u.th,&u.deco,system.ActionMinimize|system.ActionMaximize|system.ActionClose,"GPT Tunnel Manager").Layout),
		layout.Flexed(1,func(gtx layout.Context)layout.Dimensions{return layout.UniformInset(unit.Dp(14)).Layout(gtx,func(gtx layout.Context)layout.Dimensions{return layout.Flex{Axis:layout.Vertical}.Layout(gtx,layout.Rigid(u.header),layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Spacer{Height:unit.Dp(10)}.Layout(gtx)}),layout.Flexed(1,func(gtx layout.Context)layout.Dimensions{if u.confirmingExit{return u.exitDialog(gtx)};return u.body(gtx)}),layout.Rigid(u.footer))})}),
	)
}

func (u *desktopUI) exitDialog(gtx layout.Context) layout.Dimensions {
	for u.cancelExit.Clicked(gtx){u.confirmingExit=false}
	for u.confirmExit.Clicked(gtx){if u.dontAskAgain.Value{cfg:=u.core.ManagerConfig();cfg.General.ConfirmExit=false;_ = u.core.SaveManager(context.Background(),cfg)};u.confirmingExit=false;u.shutdownNow()}
	return layout.Center.Layout(gtx,func(gtx layout.Context)layout.Dimensions{return layout.Flex{Axis:layout.Vertical,Alignment:layout.Middle}.Layout(gtx,
		layout.Rigid(material.H5(u.th,"Exit GPT Tunnel Manager?").Layout),
		layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Inset{Top:unit.Dp(10),Bottom:unit.Dp(10)}.Layout(gtx,material.Body1(u.th,"This will disconnect all tunnels and stop all MCP servers currently owned by Tunnel Manager.").Layout)}),
		layout.Rigid(material.CheckBox(u.th,&u.dontAskAgain,"Don't ask again").Layout),
		layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Inset{Top:unit.Dp(12)}.Layout(gtx,func(gtx layout.Context)layout.Dimensions{return layout.Flex{}.Layout(gtx,layout.Rigid(material.Button(u.th,&u.cancelExit,"Cancel").Layout),layout.Rigid(buttonInset(u.th,&u.confirmExit,"Exit")))})}),
	)})
}

func (u *desktopUI) header(gtx layout.Context) layout.Dimensions {
	for u.serversNav.Clicked(gtx){u.page="servers"}; for u.logsNav.Clicked(gtx){u.page="logs"}; for u.settingsNav.Clicked(gtx){u.page="settings";u.loadSettings()}; for u.refresh.Clicked(gtx){u.win.Invalidate()}; for u.exit.Clicked(gtx){u.requestExit()}
	return layout.Flex{Alignment:layout.Middle}.Layout(gtx,layout.Flexed(1,material.H6(u.th,"Runtime Control").Layout),layout.Rigid(buttonInset(u.th,&u.serversNav,"Servers")),layout.Rigid(buttonInset(u.th,&u.logsNav,"Logs")),layout.Rigid(buttonInset(u.th,&u.settingsNav,"Settings")),layout.Rigid(buttonInset(u.th,&u.refresh,"Refresh")),layout.Rigid(buttonInset(u.th,&u.exit,"Exit")))
}
func buttonInset(th *material.Theme,b *widget.Clickable,s string)layout.Widget{return func(gtx layout.Context)layout.Dimensions{return layout.Inset{Left:unit.Dp(4)}.Layout(gtx,material.Button(th,b,s).Layout)}}
func (u *desktopUI) body(gtx layout.Context)layout.Dimensions{switch u.page{case"editor":return u.serverEditor(gtx);case"settings":return u.settings(gtx);case"logs":return u.logPage(gtx);default:return u.servers(gtx)}}
func (u *desktopUI) footer(gtx layout.Context)layout.Dimensions{u.mu.RLock();msg,busy:=u.message,u.busy;u.mu.RUnlock();if busy&&msg!="Shutting down…"{msg="Working… "+msg};if msg==""{msg="Manager MCP: "+u.core.ManagerMCPURL()};return layout.Inset{Top:unit.Dp(8)}.Layout(gtx,material.Caption(u.th,msg).Layout)}

func (u *desktopUI) servers(gtx layout.Context) layout.Dimensions {
	for u.addServer.Clicked(gtx){u.editServer(config.ServerEntry{})}
	entries:=u.core.Entries(); snaps:=u.core.Snapshots(); by:=map[string]servers.Snapshot{};for _,s:=range snaps{by[s.ServerID]=s}
	return layout.Flex{Axis:layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Flex{Alignment:layout.Middle}.Layout(gtx,layout.Flexed(1,material.Body1(u.th,fmt.Sprintf("%d configured Server Entries",len(entries))).Layout),layout.Rigid(material.Button(u.th,&u.addServer,"Add Server").Layout))}),
		layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Spacer{Height:unit.Dp(8)}.Layout(gtx)}),
		layout.Flexed(1,func(gtx layout.Context)layout.Dimensions{return u.list.Layout(gtx,len(entries),func(gtx layout.Context,i int)layout.Dimensions{return u.serverRow(gtx,entries[i],by[entries[i].ID])})}),
	)
}
func (u *desktopUI) row(id string)*rowActions{if r:=u.rows[id];r!=nil{return r};r:=new(rowActions);u.rows[id]=r;return r}
func (u *desktopUI) serverRow(gtx layout.Context,e config.ServerEntry,s servers.Snapshot)layout.Dimensions{
	r:=u.row(e.ID);for r.start.Clicked(gtx){u.lifecycle(e.ID,"start")};for r.stop.Clicked(gtx){u.lifecycle(e.ID,"shutdown")};for r.restart.Clicked(gtx){u.lifecycle(e.ID,"restart")};for r.edit.Clicked(gtx){u.editServer(e)};for r.marker.Clicked(gtx){u.setMessage(marker.Generate(e.ID))};for r.delete.Clicked(gtx){u.async("deleting "+e.Name,func()error{return u.core.DeleteServer(context.Background(),e.ID)})}
	state:=string(s.Observed);if state==""{state="stopped"};detail:=fmt.Sprintf("%s · %s · tunnel %v · activity %s",e.Mode,state,s.TunnelReady,s.ActivityTracking)
	return layout.Inset{Bottom:unit.Dp(8)}.Layout(gtx,func(gtx layout.Context)layout.Dimensions{return layout.Flex{Axis:layout.Vertical}.Layout(gtx,layout.Rigid(material.Body1(u.th,e.Name).Layout),layout.Rigid(material.Caption(u.th,e.ID+" · "+detail).Layout),layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Inset{Top:unit.Dp(4)}.Layout(gtx,func(gtx layout.Context)layout.Dimensions{return layout.Flex{}.Layout(gtx,layout.Rigid(buttonInset(u.th,&r.start,"Start")),layout.Rigid(buttonInset(u.th,&r.stop,"Stop")),layout.Rigid(buttonInset(u.th,&r.restart,"Restart")),layout.Rigid(buttonInset(u.th,&r.edit,"Edit")),layout.Rigid(buttonInset(u.th,&r.marker,"Marker")),layout.Rigid(buttonInset(u.th,&r.delete,"Delete")))})}))})})
}
func (u *desktopUI) lifecycle(id,action string){u.async(action+" "+id,func()error{_,err:=u.core.Lifecycle(context.Background(),id,action);return err})}

func sortedMapLines(m map[string]string)string{keys:=make([]string,0,len(m));for k:=range m{keys=append(keys,k)};sort.Strings(keys);var b strings.Builder;for i,k:=range keys{if i>0{b.WriteByte('\n')};fmt.Fprintf(&b,"%s=%s",k,m[k])};return b.String()}
func parseMapLines(s string)(map[string]string,error){out:=map[string]string{};for _,line:=range strings.Split(s,"\n"){line=strings.TrimSpace(line);if line==""{continue};i:=strings.IndexByte(line,'=');if i<=0{return nil,fmt.Errorf("expected KEY=value: %s",line)};out[strings.TrimSpace(line[:i])]=strings.TrimSpace(line[i+1:])};return out,nil}
func argLines(s string)[]string{var out []string;for _,line:=range strings.Split(s,"\n"){if line=strings.TrimSpace(line);line!=""{out=append(out,line)}};return out}
func valueOr(v,f string)string{if v==""{return f};return v}
func intText(v,d int)string{if v<=0{v=d};return strconv.Itoa(v)}
func editorLine(th *material.Theme,e *widget.Editor,label string)layout.Widget{return func(gtx layout.Context)layout.Dimensions{return layout.Flex{Axis:layout.Vertical}.Layout(gtx,layout.Rigid(material.Caption(th,label).Layout),layout.Rigid(material.Editor(th,e,label).Layout))}}

func (u *desktopUI) editServer(e config.ServerEntry){
	f:=&u.form;f.id=e.ID;f.name.SetText(e.Name);f.plugin.SetText(e.ChatGPTPluginName);f.tunnel.SetText(e.Tunnel.TunnelID);f.cred.SetText(e.Tunnel.RuntimeCredentialRef);f.enabled.Value=e.Enabled||e.ID==""
	modes:=[]config.ServerMode{config.ModeManaged,config.ModeAlwaysOn,config.ModeManual};f.mode=0;for i,v:=range modes{if e.Mode==v{f.mode=i}}
	trans:=[]config.TransportType{config.TransportStdio,config.TransportManagedHTTP,config.TransportExternalHTTP};f.transport=0;for i,v:=range trans{if e.Transport.Type==v{f.transport=i}}
	f.exe.SetText("");f.cwd.SetText("");f.args.SetText("");f.url.SetText("")
	switch e.Transport.Type{case config.TransportStdio:if e.Transport.Stdio!=nil{f.exe.SetText(e.Transport.Stdio.Executable);f.cwd.SetText(e.Transport.Stdio.WorkingDirectory);f.args.SetText(strings.Join(e.Transport.Stdio.Args,"\n"))};case config.TransportManagedHTTP:if e.Transport.ManagedHTTP!=nil{f.url.SetText(e.Transport.ManagedHTTP.URL);f.exe.SetText(e.Transport.ManagedHTTP.Launch.Executable);f.cwd.SetText(e.Transport.ManagedHTTP.Launch.WorkingDirectory);f.args.SetText(strings.Join(e.Transport.ManagedHTTP.Launch.Args,"\n"))};case config.TransportExternalHTTP:if e.Transport.ExternalHTTP!=nil{f.url.SetText(e.Transport.ExternalHTTP.URL)}}
	f.env.SetText(sortedMapLines(e.Environment.Values));f.secretEnv.SetText(sortedMapLines(e.Environment.SecretRefs));f.startup.SetText(intText(e.Runtime.StartupTimeoutSeconds,30));f.shutdown.SetText(intText(e.Runtime.ShutdownTimeoutSeconds,10));if e.Runtime.IdleTimeoutSeconds!=nil{f.idle.SetText(strconv.Itoa(*e.Runtime.IdleTimeoutSeconds))}else{f.idle.SetText("")};u.page="editor"
}

func (u *desktopUI) serverEditor(gtx layout.Context)layout.Dimensions{
	f:=&u.form;for f.modeBtn.Clicked(gtx){f.mode=(f.mode+1)%3};for f.transBtn.Clicked(gtx){f.transport=(f.transport+1)%3};for f.cancel.Clicked(gtx){u.page="servers"};for f.lifecycleMarker.Clicked(gtx){if f.id==""{u.setMessage("Save the entry first to generate an immutable Server ID.")}else{u.setMessage(marker.Generate(f.id))}};for f.save.Clicked(gtx){e,err:=u.formEntry();if err!=nil{u.setMessage(err.Error())}else{u.async("saving server",func()error{_,err:=u.core.SaveServer(context.Background(),e);if err==nil{u.page="servers"};return err})}}
	modes:=[]string{"Managed","Always On","Manual"};trans:=[]string{"Stdio","Managed HTTP","External HTTP"}
	return u.list.Layout(gtx,1,func(gtx layout.Context,_ int)layout.Dimensions{return layout.Flex{Axis:layout.Vertical}.Layout(gtx,
		layout.Rigid(material.H6(u.th,"Server Editor").Layout),layout.Rigid(material.Caption(u.th,valueOr(f.id,"New entry — ID generated on save")).Layout),layout.Rigid(material.CheckBox(u.th,&f.enabled,"Enabled").Layout),
		layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Flex{}.Layout(gtx,layout.Rigid(buttonInset(u.th,&f.modeBtn,"Mode: "+modes[f.mode])),layout.Rigid(buttonInset(u.th,&f.transBtn,"Transport: "+trans[f.transport]))) }),
		layout.Rigid(editorLine(u.th,&f.name,"Name")),layout.Rigid(editorLine(u.th,&f.plugin,"ChatGPT Developer Plugin name")),layout.Rigid(editorLine(u.th,&f.tunnel,"Tunnel ID")),layout.Rigid(editorLine(u.th,&f.cred,"Runtime credential ref (blank = global)")),
		layout.Rigid(func(gtx layout.Context)layout.Dimensions{if f.transport==2{return layout.Dimensions{}};return editorLine(u.th,&f.exe,"Executable")(gtx)}),layout.Rigid(func(gtx layout.Context)layout.Dimensions{if f.transport==2{return layout.Dimensions{}};return editorLine(u.th,&f.cwd,"Working directory")(gtx)}),layout.Rigid(func(gtx layout.Context)layout.Dimensions{if f.transport==2{return layout.Dimensions{}};return editorLine(u.th,&f.args,"Arguments, one per line")(gtx)}),layout.Rigid(func(gtx layout.Context)layout.Dimensions{if f.transport==0{return layout.Dimensions{}};return editorLine(u.th,&f.url,"MCP URL")(gtx)}),
		layout.Rigid(editorLine(u.th,&f.env,"Environment KEY=value, one per line")),layout.Rigid(editorLine(u.th,&f.secretEnv,"Secret environment KEY=secret://ref, one per line")),layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Flex{}.Layout(gtx,layout.Flexed(1,editorLine(u.th,&f.startup,"Startup timeout seconds")),layout.Flexed(1,editorLine(u.th,&f.shutdown,"Shutdown timeout seconds")),layout.Flexed(1,editorLine(u.th,&f.idle,"Managed idle timeout; blank = global"))) }),
		layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Inset{Top:unit.Dp(12)}.Layout(gtx,func(gtx layout.Context)layout.Dimensions{return layout.Flex{}.Layout(gtx,layout.Rigid(material.Button(u.th,&f.cancel,"Cancel").Layout),layout.Rigid(buttonInset(u.th,&f.lifecycleMarker,"Lifecycle Marker")),layout.Rigid(buttonInset(u.th,&f.save,"Save")))})}),
	)})
}

func parseNonNegative(s string,d int)(int,error){s=strings.TrimSpace(s);if s==""{return d,nil};n,err:=strconv.Atoi(s);if err!=nil||n<0{return 0,fmt.Errorf("expected a non-negative integer: %q",s)};return n,nil}
func (u *desktopUI) formEntry()(config.ServerEntry,error){
	f:=&u.form;env,err:=parseMapLines(f.env.Text());if err!=nil{return config.ServerEntry{},err};secretEnv,err:=parseMapLines(f.secretEnv.Text());if err!=nil{return config.ServerEntry{},err};startup,err:=parseNonNegative(f.startup.Text(),30);if err!=nil{return config.ServerEntry{},err};shutdown,err:=parseNonNegative(f.shutdown.Text(),10);if err!=nil{return config.ServerEntry{},err}
	e:=config.ServerEntry{ID:f.id,Name:strings.TrimSpace(f.name.Text()),ChatGPTPluginName:strings.TrimSpace(f.plugin.Text()),Enabled:f.enabled.Value,Mode:[]config.ServerMode{config.ModeManaged,config.ModeAlwaysOn,config.ModeManual}[f.mode],Tunnel:config.TunnelConfig{TunnelID:strings.TrimSpace(f.tunnel.Text()),RuntimeCredentialRef:strings.TrimSpace(f.cred.Text())},Environment:config.EnvironmentConfig{Values:env,SecretRefs:secretEnv},Runtime:config.RuntimeConfig{StartupTimeoutSeconds:startup,ShutdownTimeoutSeconds:shutdown}}
	if strings.TrimSpace(f.idle.Text())!=""{n,err:=parseNonNegative(f.idle.Text(),0);if err!=nil{return e,err};e.Runtime.IdleTimeoutSeconds=&n}
	e.Transport.Type=[]config.TransportType{config.TransportStdio,config.TransportManagedHTTP,config.TransportExternalHTTP}[f.transport];switch e.Transport.Type{case config.TransportStdio:e.Transport.Stdio=&config.StdioTransport{Executable:strings.TrimSpace(f.exe.Text()),Args:argLines(f.args.Text()),WorkingDirectory:strings.TrimSpace(f.cwd.Text())};case config.TransportManagedHTTP:e.Transport.ManagedHTTP=&config.ManagedHTTPTransport{URL:strings.TrimSpace(f.url.Text()),Launch:config.LaunchConfig{Executable:strings.TrimSpace(f.exe.Text()),Args:argLines(f.args.Text()),WorkingDirectory:strings.TrimSpace(f.cwd.Text())}};case config.TransportExternalHTTP:e.Transport.ExternalHTTP=&config.ExternalHTTPTransport{URL:strings.TrimSpace(f.url.Text())}}
	return e,nil
}

func (u *desktopUI) loadSettings(){m:=u.core.ManagerConfig();s:=&u.settingsForm;s.tunnel.SetText(m.ManagerTunnel.TunnelID);s.cred.SetText(m.ManagerTunnel.RuntimeCredentialRef);s.idle.SetText(strconv.Itoa(m.ManagedDefaults.IdleTimeoutSeconds));s.launch.Value=m.General.LaunchAtStartup;s.startMinimized.Value=m.General.StartMinimized;s.tray.Value=m.General.MinimizeToTray;s.confirm.Value=m.General.ConfirmExit;s.autoUpdate.Value=m.TunnelClient.AutoUpdate;s.disk.Value=m.Logging.WriteToDisk;if m.General.CloseBehavior=="exit"{s.closeMode=1}else{s.closeMode=0};themes:=[]string{"system","light","dark"};s.themeMode=0;for i,v:=range themes{if m.Appearance.Theme==v{s.themeMode=i}};if s.secretRef.Text()==""{s.secretRef.SetText(valueOr(m.ManagerTunnel.RuntimeCredentialRef,"secret://openai/runtime/default"))}}
func (u *desktopUI) applyTheme(){if u.core.ManagerConfig().Appearance.Theme=="dark"{u.th.Palette=material.Palette{Bg:color.NRGBA{R:28,G:30,B:34,A:255},Fg:color.NRGBA{R:235,G:237,B:240,A:255},ContrastBg:color.NRGBA{R:77,G:108,B:240,A:255},ContrastFg:color.NRGBA{R:255,G:255,B:255,A:255}}}else{u.th.Palette=material.NewTheme().Palette}}
func (u *desktopUI) settingsConfig()config.ManagerConfig{m:=u.core.ManagerConfig();s:=&u.settingsForm;m.ManagerTunnel.TunnelID=strings.TrimSpace(s.tunnel.Text());m.ManagerTunnel.RuntimeCredentialRef=strings.TrimSpace(s.cred.Text());m.ManagedDefaults.IdleTimeoutSeconds,_=parseNonNegative(s.idle.Text(),300);m.General.LaunchAtStartup=s.launch.Value;m.General.StartMinimized=s.startMinimized.Value;m.General.MinimizeToTray=s.tray.Value;m.General.ConfirmExit=s.confirm.Value;m.TunnelClient.AutoUpdate=s.autoUpdate.Value;m.Logging.WriteToDisk=s.disk.Value;if s.closeMode==1{m.General.CloseBehavior="exit"}else{m.General.CloseBehavior="minimize"};m.Appearance.Theme=[]string{"system","light","dark"}[s.themeMode];return m}

func (u *desktopUI) settings(gtx layout.Context)layout.Dimensions{
	s:=&u.settingsForm;for s.closeBtn.Clicked(gtx){s.closeMode=(s.closeMode+1)%2};for s.themeBtn.Clicked(gtx){s.themeMode=(s.themeMode+1)%3};for s.openWeb.Clicked(gtx){_=platform.OpenURL(context.Background(),u.core.AdminURL())};for s.store.Clicked(gtx){ref,val:=strings.TrimSpace(s.secretRef.Text()),s.secretVal.Text();u.async("storing secret",func()error{err:=u.core.PutSecret(context.Background(),ref,val);if err==nil{s.secretVal.SetText("")};return err})};for s.check.Clicked(gtx){u.async("checking tunnel-client update",func()error{r,err:=u.core.CheckUpdate(context.Background());if err==nil{u.setMessage("Latest tunnel-client: "+r.TagName)};return err})};for s.install.Clicked(gtx){u.async("installing tunnel-client",func()error{v,err:=u.core.InstallUpdate(context.Background());if err==nil{u.setMessage("Installed tunnel-client "+v.Version+"; running tunnels switch after restart.")};return err})};for s.rollback.Clicked(gtx){u.async("rolling back tunnel-client",func()error{v,err:=u.core.Rollback(context.Background());if err==nil{u.setMessage("Active tunnel-client: "+v.Version+"; running tunnels switch after restart.")};return err})};for s.save.Clicked(gtx){m:=u.settingsConfig();u.async("saving settings",func()error{if err:=u.core.SaveManager(context.Background(),m);err!=nil{return err};u.applyTheme();return nil})}
	closes:=[]string{"Minimize / keep running","Exit"};themes:=[]string{"System (light fallback)","Light","Dark"}
	return u.list.Layout(gtx,1,func(gtx layout.Context,_ int)layout.Dimensions{return layout.Flex{Axis:layout.Vertical}.Layout(gtx,
		layout.Rigid(material.H6(u.th,"Settings").Layout),layout.Rigid(editorLine(u.th,&s.tunnel,"Manager Tunnel ID")),layout.Rigid(editorLine(u.th,&s.cred,"Manager runtime credential ref")),layout.Rigid(editorLine(u.th,&s.idle,"Default Managed idle timeout seconds")),layout.Rigid(material.CheckBox(u.th,&s.launch,"Launch at login").Layout),layout.Rigid(material.CheckBox(u.th,&s.startMinimized,"Start minimized").Layout),layout.Rigid(material.CheckBox(u.th,&s.tray,"Show system tray icon").Layout),layout.Rigid(material.CheckBox(u.th,&s.confirm,"Confirm explicit exit").Layout),layout.Rigid(material.CheckBox(u.th,&s.autoUpdate,"Auto-update tunnel-client").Layout),layout.Rigid(material.CheckBox(u.th,&s.disk,"Write bounded rotating logs to disk").Layout),layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Flex{}.Layout(gtx,layout.Rigid(buttonInset(u.th,&s.closeBtn,"Close behavior: "+closes[s.closeMode])),layout.Rigid(buttonInset(u.th,&s.themeBtn,"Theme: "+themes[s.themeMode]))) }),
		layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Inset{Top:unit.Dp(10)}.Layout(gtx,material.H6(u.th,"Secret Store").Layout)}),layout.Rigid(editorLine(u.th,&s.secretRef,"Secret reference")),layout.Rigid(editorLine(u.th,&s.secretVal,"Secret value")),layout.Rigid(material.Button(u.th,&s.store,"Store Secret").Layout),layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Inset{Top:unit.Dp(10)}.Layout(gtx,material.H6(u.th,"Tunnel Client").Layout)}),layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Flex{}.Layout(gtx,layout.Rigid(material.Button(u.th,&s.check,"Check Update").Layout),layout.Rigid(buttonInset(u.th,&s.install,"Install Latest")),layout.Rigid(buttonInset(u.th,&s.rollback,"Roll Back")),layout.Rigid(buttonInset(u.th,&s.openWeb,"Advanced Web UI"))) }),layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Inset{Top:unit.Dp(14)}.Layout(gtx,material.Button(u.th,&s.save,"Save Settings").Layout)}),
	)})
}

func (u *desktopUI) logPage(gtx layout.Context)layout.Dimensions{
	levels:=[]string{"All","TRACE","DEBUG","INFO","WARN","ERROR"};for u.levelBtn.Clicked(gtx){u.logLevel=(u.logLevel+1)%len(levels)};for u.clearLogs.Clicked(gtx){u.core.ClearLogs()};for u.exportText.Clicked(gtx){_=platform.OpenURL(context.Background(),u.core.AdminURL()+"/api/logs?format=text")};for u.exportJSONL.Clicked(gtx){_=platform.OpenURL(context.Background(),u.core.AdminURL()+"/api/logs?format=jsonl")}
	q:=strings.ToLower(strings.TrimSpace(u.logSearch.Text()));var filtered []string;for _,e:=range u.core.Logs(){if u.logLevel>0&&strings.ToUpper(string(e.Level))!=levels[u.logLevel]{continue};line:=fmt.Sprintf("%s %-5s %-18s %-14s %s",e.Timestamp.Format("15:04:05"),strings.ToUpper(string(e.Level)),e.Source,e.Component,e.Message);if q==""||strings.Contains(strings.ToLower(line),q){filtered=append(filtered,line)}}
	return layout.Flex{Axis:layout.Vertical}.Layout(gtx,layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Flex{Alignment:layout.Middle}.Layout(gtx,layout.Flexed(1,material.Editor(u.th,&u.logSearch,"Search logs").Layout),layout.Rigid(buttonInset(u.th,&u.levelBtn,"Level: "+levels[u.logLevel])),layout.Rigid(buttonInset(u.th,&u.exportText,"Export Text")),layout.Rigid(buttonInset(u.th,&u.exportJSONL,"Export JSONL")),layout.Rigid(buttonInset(u.th,&u.clearLogs,"Clear"))) }),layout.Rigid(func(gtx layout.Context)layout.Dimensions{return layout.Spacer{Height:unit.Dp(8)}.Layout(gtx)}),layout.Flexed(1,func(gtx layout.Context)layout.Dimensions{return u.logs.Layout(gtx,len(filtered),func(gtx layout.Context,i int)layout.Dimensions{return layout.Inset{Bottom:unit.Dp(3)}.Layout(gtx,material.Caption(u.th,filtered[i]).Layout)})}))
}

func (u *desktopUI) async(label string,fn func()error){u.mu.Lock();if u.busy{u.mu.Unlock();return};u.busy=true;u.message=label;u.mu.Unlock();go func(){err:=fn();u.mu.Lock();u.busy=false;if err!=nil{u.message=err.Error()}else if u.message==label{u.message="Done: "+label};u.mu.Unlock();u.win.Invalidate()}()}
func (u *desktopUI) setMessage(s string){u.mu.Lock();u.message=s;u.mu.Unlock();u.win.Invalidate()}
