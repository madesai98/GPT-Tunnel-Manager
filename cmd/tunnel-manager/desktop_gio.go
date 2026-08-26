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

type rowActions struct {
	start   widget.Clickable
	stop    widget.Clickable
	restart widget.Clickable
	edit    widget.Clickable
	marker  widget.Clickable
	delete  widget.Clickable
}

type serverForm struct {
	id        string
	name      widget.Editor
	plugin    widget.Editor
	tunnel    widget.Editor
	cred      widget.Editor
	exe       widget.Editor
	cwd       widget.Editor
	args      widget.Editor
	url       widget.Editor
	env       widget.Editor
	secretEnv widget.Editor
	startup   widget.Editor
	shutdown  widget.Editor
	idle      widget.Editor
	enabled   widget.Bool
	mode      int
	transport int
	modeBtn   widget.Clickable
	transBtn  widget.Clickable
	save      widget.Clickable
	cancel    widget.Clickable
	copyMark  widget.Clickable
}

type settingsForm struct {
	tunnel     widget.Editor
	cred       widget.Editor
	idle       widget.Editor
	secretRef  widget.Editor
	secretVal  widget.Editor
	launch     widget.Bool
	confirm    widget.Bool
	autoUpdate widget.Bool
	disk       widget.Bool
	closeMode  int
	themeMode  int
	closeBtn   widget.Clickable
	themeBtn   widget.Clickable
	save       widget.Clickable
	store      widget.Clickable
	openWeb    widget.Clickable
	check      widget.Clickable
	install    widget.Clickable
	rollback   widget.Clickable
}

type desktopUI struct {
	core *coreapp.App
	win  *gioapp.Window
	th   *material.Theme

	page string
	list widget.List
	logs widget.List

	serversNav  widget.Clickable
	logsNav     widget.Clickable
	settingsNav widget.Clickable
	addServer   widget.Clickable
	refresh     widget.Clickable
	exit        widget.Clickable

	rows map[string]*rowActions
	form serverForm
	set  settingsForm

	logSearch widget.Editor
	logLevel  int
	levelBtn  widget.Clickable
	clearLogs widget.Clickable

	mu      sync.RWMutex
	busy    bool
	message string
	trayStop chan struct{}
}

func singleEditor() widget.Editor { return widget.Editor{SingleLine: true} }

func newDesktopUI(core *coreapp.App, win *gioapp.Window) *desktopUI {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	u := &desktopUI{
		core: core,
		win: win,
		th: th,
		page: "servers",
		list: widget.List{List: layout.List{Axis: layout.Vertical}},
		logs: widget.List{List: layout.List{Axis: layout.Vertical}},
		rows: map[string]*rowActions{},
		trayStop: make(chan struct{}),
	}
	u.logSearch = singleEditor()
	u.form.name = singleEditor()
	u.form.plugin = singleEditor()
	u.form.tunnel = singleEditor()
	u.form.cred = singleEditor()
	u.form.exe = singleEditor()
	u.form.cwd = singleEditor()
	u.form.url = singleEditor()
	u.form.startup = singleEditor()
	u.form.shutdown = singleEditor()
	u.form.idle = singleEditor()
	u.form.args = widget.Editor{}
	u.form.env = widget.Editor{}
	u.form.secretEnv = widget.Editor{}
	u.set.tunnel = singleEditor()
	u.set.cred = singleEditor()
	u.set.idle = singleEditor()
	u.set.secretRef = singleEditor()
	u.set.secretVal = singleEditor()
	u.set.secretVal.Mask = '•'
	u.loadSettings()
	return u
}

func runDesktop(core *coreapp.App, setFocus func(func())) error {
	errCh := make(chan error, 1)
	ready := make(chan *desktopUI, 1)
	go func() {
		win := new(gioapp.Window)
		win.Option(gioapp.Title("GPT Tunnel Manager"), gioapp.Size(unit.Dp(1120), unit.Dp(760)), gioapp.MinSize(unit.Dp(780), unit.Dp(520)))
		u := newDesktopUI(core, win)
		ready <- u
		if core.ManagerConfig().General.StartMinimized {
			win.Option(gioapp.Minimized.Option())
		}
		errCh <- u.loop()
	}()
	u := <-ready
	if setFocus != nil {
		setFocus(func() {
			u.win.Option(gioapp.Windowed.Option())
			u.win.Perform(system.ActionRaise)
			u.win.Invalidate()
		})
	}
	startTray, endTray := systray.RunWithExternalLoop(func() { u.trayReady() }, func() {})
	startTray()
	gioapp.Main()
	close(u.trayStop)
	endTray()
	return <-errCh
}

func (u *desktopUI) loop() error {
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-u.core.Done():
				u.win.Perform(system.ActionClose)
				return
			case <-t.C:
				u.win.Invalidate()
			}
		}
	}()
	var ops op.Ops
	for {
		switch e := u.win.Event().(type) {
		case gioapp.DestroyEvent:
			u.core.RequestShutdown()
			<-u.core.Done()
			return e.Err
		case gioapp.FrameEvent:
			gtx := gioapp.NewContext(&ops, e)
			u.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (u *desktopUI) trayReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle("GPT Tunnel Manager")
	systray.SetTooltip("GPT Tunnel Manager")
	systray.SetOnTapped(func() {
		u.win.Option(gioapp.Windowed.Option())
		u.win.Perform(system.ActionRaise)
	})
	open := systray.AddMenuItem("Open Manager", "Show the GPT Tunnel Manager window")
	status := systray.AddMenuItem("Status", "Runtime status summary")
	status.Disable()
	advanced := systray.AddMenuItem("Open Advanced Web UI", "Open the local loopback management UI")
	systray.AddSeparator()
	exit := systray.AddMenuItem("Exit Tunnel Manager", "Disconnect tunnels and stop owned MCP servers")
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-u.trayStop:
				return
			case <-open.ClickedCh:
				u.win.Option(gioapp.Windowed.Option())
				u.win.Perform(system.ActionRaise)
			case <-advanced.ClickedCh:
				_ = platform.OpenURL(context.Background(), u.core.AdminURL())
			case <-exit.ClickedCh:
				u.core.RequestShutdown()
				u.win.Perform(system.ActionClose)
			case <-t.C:
				snaps := u.core.Snapshots()
				ready, degraded := 0, 0
				for _, s := range snaps {
					if s.Ready { ready++ }
					if s.Observed == "degraded" { degraded++ }
				}
				status.SetTitle(fmt.Sprintf("Status: %d ready, %d degraded, %d total", ready, degraded, len(snaps)))
			}
		}
	}()
}

func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 2; y < 14; y++ {
		for x := 2; x < 14; x++ {
			if x == 2 || x == 13 || y == 2 || y == 13 || (x >= 6 && x <= 9) {
				img.SetNRGBA(x, y, color.NRGBA{R: 75, G: 112, B: 240, A: 255})
			}
		}
	}
	var p bytes.Buffer
	_ = png.Encode(&p, img)
	pngBytes := p.Bytes()
	var ico bytes.Buffer
	_ = binary.Write(&ico, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	ico.WriteByte(16); ico.WriteByte(16); ico.WriteByte(0); ico.WriteByte(0)
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(32))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(len(pngBytes)))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(22))
	ico.Write(pngBytes)
	return ico.Bytes()
}

func (u *desktopUI) layout(gtx layout.Context) layout.Dimensions {
	return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.header),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
			layout.Flexed(1, u.body),
			layout.Rigid(u.footer),
		)
	})
}

func (u *desktopUI) header(gtx layout.Context) layout.Dimensions {
	for u.serversNav.Clicked(gtx) { u.page = "servers" }
	for u.logsNav.Clicked(gtx) { u.page = "logs" }
	for u.settingsNav.Clicked(gtx) { u.page = "settings"; u.loadSettings() }
	for u.refresh.Clicked(gtx) { u.win.Invalidate() }
	for u.exit.Clicked(gtx) { u.core.RequestShutdown(); u.win.Perform(system.ActionClose) }
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, material.H6(u.th, "GPT Tunnel Manager").Layout),
		layout.Rigid(buttonInset(u.th, &u.serversNav, "Servers")),
		layout.Rigid(buttonInset(u.th, &u.logsNav, "Logs")),
		layout.Rigid(buttonInset(u.th, &u.settingsNav, "Settings")),
		layout.Rigid(buttonInset(u.th, &u.refresh, "Refresh")),
		layout.Rigid(buttonInset(u.th, &u.exit, "Exit")),
	)
}

func buttonInset(th *material.Theme, btn *widget.Clickable, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, material.Button(th, btn, label).Layout)
	}
}

func (u *desktopUI) body(gtx layout.Context) layout.Dimensions {
	switch u.page {
	case "editor": return u.serverEditor(gtx)
	case "settings": return u.settings(gtx)
	case "logs": return u.logPage(gtx)
	default: return u.servers(gtx)
	}
}

func (u *desktopUI) footer(gtx layout.Context) layout.Dimensions {
	u.mu.RLock(); msg, busy := u.message, u.busy; u.mu.RUnlock()
	if busy { msg = "Working… " + msg }
	if msg == "" { msg = "Manager MCP: " + u.core.ManagerMCPURL() }
	return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, material.Caption(u.th, msg).Layout)
}

func (u *desktopUI) servers(gtx layout.Context) layout.Dimensions {
	for u.addServer.Clicked(gtx) { u.editServer(config.ServerEntry{}) }
	entries := u.core.Entries()
	snaps := u.core.Snapshots()
	by := map[string]servers.Snapshot{}
	for _, s := range snaps { by[s.ServerID] = s }
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, material.Body1(u.th, fmt.Sprintf("%d configured Server Entries", len(entries))).Layout),
				layout.Rigid(material.Button(u.th, &u.addServer, "Add Server").Layout),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return u.list.Layout(gtx, len(entries), func(gtx layout.Context, i int) layout.Dimensions {
				e := entries[i]; return u.serverRow(gtx, e, by[e.ID])
			})
		}),
	)
}

func (u *desktopUI) row(id string) *rowActions {
	if r := u.rows[id]; r != nil { return r }
	r := new(rowActions); u.rows[id] = r; return r
}

func (u *desktopUI) serverRow(gtx layout.Context, e config.ServerEntry, s servers.Snapshot) layout.Dimensions {
	r := u.row(e.ID)
	for r.start.Clicked(gtx) { u.lifecycle(e.ID, "start") }
	for r.stop.Clicked(gtx) { u.lifecycle(e.ID, "shutdown") }
	for r.restart.Clicked(gtx) { u.lifecycle(e.ID, "restart") }
	for r.edit.Clicked(gtx) { u.editServer(e) }
	for r.marker.Clicked(gtx) { u.setMessage(marker.Generate(e.ID)) }
	for r.delete.Clicked(gtx) { u.async("deleting "+e.Name, func() error { return u.core.DeleteServer(context.Background(), e.ID) }) }
	state := string(s.Observed); if state == "" { state = "stopped" }
	detail := fmt.Sprintf("%s · %s · tunnel %v · activity %s", e.Mode, state, s.TunnelReady, s.ActivityTracking)
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, e.Name).Layout),
			layout.Rigid(material.Caption(u.th, e.ID+" · "+detail).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Rigid(buttonInset(u.th, &r.start, "Start")),
						layout.Rigid(buttonInset(u.th, &r.stop, "Stop")),
						layout.Rigid(buttonInset(u.th, &r.restart, "Restart")),
						layout.Rigid(buttonInset(u.th, &r.edit, "Edit")),
						layout.Rigid(buttonInset(u.th, &r.marker, "Marker")),
						layout.Rigid(buttonInset(u.th, &r.delete, "Delete")),
					)
				})
			}),
		)
	})
}

func (u *desktopUI) lifecycle(id, action string) {
	u.async(action+" "+id, func() error { _, err := u.core.Lifecycle(context.Background(), id, action); return err })
}

func (u *desktopUI) editServer(e config.ServerEntry) {
	f := &u.form
	f.id = e.ID
	f.name.SetText(e.Name); f.plugin.SetText(e.ChatGPTPluginName); f.tunnel.SetText(e.Tunnel.TunnelID); f.cred.SetText(e.Tunnel.RuntimeCredentialRef)
	f.enabled.Value = e.Enabled || e.ID == ""
	modes := []config.ServerMode{config.ModeManaged, config.ModeAlwaysOn, config.ModeManual}; f.mode = 0; for i, m := range modes { if e.Mode == m { f.mode = i } }
	transports := []config.TransportType{config.TransportStdio, config.TransportManagedHTTP, config.TransportExternalHTTP}; f.transport = 0; for i, t := range transports { if e.Transport.Type == t { f.transport = i } }
	f.exe.SetText(""); f.cwd.SetText(""); f.args.SetText(""); f.url.SetText("")
	switch e.Transport.Type {
	case config.TransportStdio: if e.Transport.Stdio != nil { f.exe.SetText(e.Transport.Stdio.Executable); f.cwd.SetText(e.Transport.Stdio.WorkingDirectory); f.args.SetText(strings.Join(e.Transport.Stdio.Args, "\n")) }
	case config.TransportManagedHTTP: if e.Transport.ManagedHTTP != nil { f.url.SetText(e.Transport.ManagedHTTP.URL); f.exe.SetText(e.Transport.ManagedHTTP.Launch.Executable); f.cwd.SetText(e.Transport.ManagedHTTP.Launch.WorkingDirectory); f.args.SetText(strings.Join(e.Transport.ManagedHTTP.Launch.Args, "\n")) }
	case config.TransportExternalHTTP: if e.Transport.ExternalHTTP != nil { f.url.SetText(e.Transport.ExternalHTTP.URL) }
	}
	f.env.SetText(mapLines(e.Environment.Values)); f.secretEnv.SetText(mapLines(e.Environment.SecretRefs))
	f.startup.SetText(strconv.Itoa(defaultInt(e.Runtime.StartupTimeoutSeconds, 30))); f.shutdown.SetText(strconv.Itoa(defaultInt(e.Runtime.ShutdownTimeoutSeconds, 10)))
	if e.Runtime.IdleTimeoutSeconds != nil { f.idle.SetText(strconv.Itoa(*e.Runtime.IdleTimeoutSeconds)) } else { f.idle.SetText("") }
	u.page = "editor"
}

func defaultInt(v, d int) int { if v <= 0 { return d }; return v }
func mapLines(m map[string]string) string { var b strings.Builder; first := true; for k, v := range m { if !first { b.WriteByte('\n') }; first = false; b.WriteString(k); b.WriteByte('='); b.WriteString(v) }; return b.String() }
func parseLines(s string) map[string]string { out := map[string]string{}; for _, line := range strings.Split(s, "\n") { line = strings.TrimSpace(line); if line == "" { continue }; if i := strings.IndexByte(line, '='); i > 0 { out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:]) } }; return out }
func argLines(s string) []string { var out []string; for _, line := range strings.Split(s, "\n") { line = strings.TrimSpace(line); if line != "" { out = append(out, line) } }; return out }

func editorLine(th *material.Theme, ed *widget.Editor, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions { return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(material.Caption(th, label).Layout), layout.Rigid(material.Editor(th, ed, label).Layout)) }
}

func (u *desktopUI) serverEditor(gtx layout.Context) layout.Dimensions {
	f := &u.form
	for f.modeBtn.Clicked(gtx) { f.mode = (f.mode + 1) % 3 }
	for f.transBtn.Clicked(gtx) { f.transport = (f.transport + 1) % 3 }
	for f.cancel.Clicked(gtx) { u.page = "servers" }
	for f.copyMark.Clicked(gtx) { if f.id != "" { u.setMessage(marker.Generate(f.id)) } else { u.setMessage("Save the entry first to generate an immutable Server ID.") } }
	for f.save.Clicked(gtx) { e, err := u.formEntry(); if err != nil { u.setMessage(err.Error()) } else { u.async("saving server", func() error { _, err := u.core.SaveServer(context.Background(), e); if err == nil { u.page = "servers" }; return err }) } }
	modes := []string{"Managed", "Always On", "Manual"}; transports := []string{"Stdio", "Managed HTTP", "External HTTP"}
	fields := []layout.Widget{
		editorLine(u.th, &f.name, "Name"), editorLine(u.th, &f.plugin, "ChatGPT Developer Plugin name"),
		editorLine(u.th, &f.tunnel, "Tunnel ID"), editorLine(u.th, &f.cred, "Runtime credential ref (blank = global)"),
	}
	return u.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.H6(u.th, "Server Editor").Layout),
			layout.Rigid(material.Caption(u.th, valueOr(f.id, "New entry — ID generated on save")).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, material.CheckBox(u.th, &f.enabled, "Enabled").Layout) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, layout.Rigid(buttonInset(u.th, &f.modeBtn, "Mode: "+modes[f.mode])), layout.Rigid(buttonInset(u.th, &f.transBtn, "Transport: "+transports[f.transport]))) }),
			layout.Rigid(fields[0]), layout.Rigid(fields[1]), layout.Rigid(fields[2]), layout.Rigid(fields[3]),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { if f.transport == 2 { return layout.Dimensions{} }; return editorLine(u.th, &f.exe, "Executable")(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { if f.transport == 2 { return layout.Dimensions{} }; return editorLine(u.th, &f.cwd, "Working directory")(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { if f.transport == 2 { return layout.Dimensions{} }; return editorLine(u.th, &f.args, "Arguments, one per line")(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { if f.transport == 0 { return layout.Dimensions{} }; return editorLine(u.th, &f.url, "MCP URL")(gtx) }),
			layout.Rigid(editorLine(u.th, &f.env, "Environment KEY=value, one per line")),
			layout.Rigid(editorLine(u.th, &f.secretEnv, "Secret environment KEY=secret://ref, one per line")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, layout.Flexed(1, editorLine(u.th, &f.startup, "Startup timeout seconds")), layout.Flexed(1, editorLine(u.th, &f.shutdown, "Shutdown timeout seconds")), layout.Flexed(1, editorLine(u.th, &f.idle, "Managed idle timeout; blank = global"))) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, layout.Rigid(material.Button(u.th, &f.cancel, "Cancel").Layout), layout.Rigid(buttonInset(u.th, &f.copyMark, "Lifecycle Marker")), layout.Rigid(buttonInset(u.th, &f.save, "Save"))) }) }),
		)
	})
}

func valueOr(v, fallback string) string { if v == "" { return fallback }; return v }
func atoiDefault(s string, d int) int { n, err := strconv.Atoi(strings.TrimSpace(s)); if err != nil || n < 0 { return d }; return n }

func (u *desktopUI) formEntry() (config.ServerEntry, error) {
	f := &u.form
	modes := []config.ServerMode{config.ModeManaged, config.ModeAlwaysOn, config.ModeManual}; transports := []config.TransportType{config.TransportStdio, config.TransportManagedHTTP, config.TransportExternalHTTP}
	e := config.ServerEntry{ID:f.id, Name:strings.TrimSpace(f.name.Text()), ChatGPTPluginName:strings.TrimSpace(f.plugin.Text()), Enabled:f.enabled.Value, Mode:modes[f.mode], Tunnel:config.TunnelConfig{TunnelID:strings.TrimSpace(f.tunnel.Text()), RuntimeCredentialRef:strings.TrimSpace(f.cred.Text())}, Environment:config.EnvironmentConfig{Values:parseLines(f.env.Text()), SecretRefs:parseLines(f.secretEnv.Text())}, Runtime:config.RuntimeConfig{StartupTimeoutSeconds:atoiDefault(f.startup.Text(),30), ShutdownTimeoutSeconds:atoiDefault(f.shutdown.Text(),10)}, Logging:config.ServerLoggingConfig{}}
	if strings.TrimSpace(f.idle.Text()) != "" { n, err := strconv.Atoi(strings.TrimSpace(f.idle.Text())); if err != nil || n < 0 { return e, fmt.Errorf("idle timeout must be a non-negative integer") }; e.Runtime.IdleTimeoutSeconds = &n }
	t := transports[f.transport]; e.Transport.Type = t
	switch t {
	case config.TransportStdio: e.Transport.Stdio = &config.StdioTransport{Executable:strings.TrimSpace(f.exe.Text()), Args:argLines(f.args.Text()), WorkingDirectory:strings.TrimSpace(f.cwd.Text())}
	case config.TransportManagedHTTP: e.Transport.ManagedHTTP = &config.ManagedHTTPTransport{URL:strings.TrimSpace(f.url.Text()), Launch:config.LaunchConfig{Executable:strings.TrimSpace(f.exe.Text()), Args:argLines(f.args.Text()), WorkingDirectory:strings.TrimSpace(f.cwd.Text())}}
	case config.TransportExternalHTTP: e.Transport.ExternalHTTP = &config.ExternalHTTPTransport{URL:strings.TrimSpace(f.url.Text())}
	}
	return e, nil
}

func (u *desktopUI) loadSettings() {
	m := u.core.ManagerConfig(); s := &u.set
	s.tunnel.SetText(m.ManagerTunnel.TunnelID); s.cred.SetText(m.ManagerTunnel.RuntimeCredentialRef); s.idle.SetText(strconv.Itoa(m.ManagedDefaults.IdleTimeoutSeconds)); s.launch.Value = m.General.LaunchAtStartup; s.confirm.Value = m.General.ConfirmExit; s.autoUpdate.Value = m.TunnelClient.AutoUpdate; s.disk.Value = m.Logging.WriteToDisk
	if m.General.CloseBehavior == "exit" { s.closeMode = 1 } else { s.closeMode = 0 }
	themes := []string{"system","light","dark"}; s.themeMode = 0; for i, v := range themes { if m.Appearance.Theme == v { s.themeMode = i } }
	if s.secretRef.Text() == "" { s.secretRef.SetText(valueOr(m.ManagerTunnel.RuntimeCredentialRef, "secret://openai/runtime/default")) }
}

func (u *desktopUI) settings(gtx layout.Context) layout.Dimensions {
	s := &u.set
	for s.closeBtn.Clicked(gtx) { s.closeMode = (s.closeMode + 1) % 2 }
	for s.themeBtn.Clicked(gtx) { s.themeMode = (s.themeMode + 1) % 3 }
	for s.openWeb.Clicked(gtx) { _ = platform.OpenURL(context.Background(), u.core.AdminURL()) }
	for s.store.Clicked(gtx) { ref, val := strings.TrimSpace(s.secretRef.Text()), s.secretVal.Text(); u.async("storing secret", func() error { err := u.core.PutSecret(context.Background(), ref, val); if err == nil { s.secretVal.SetText("") }; return err }) }
	for s.check.Clicked(gtx) { u.async("checking tunnel-client update", func() error { r, err := u.core.CheckUpdate(context.Background()); if err == nil { u.setMessage("Latest tunnel-client: "+r.TagName) }; return err }) }
	for s.install.Clicked(gtx) { u.async("installing tunnel-client", func() error { v, err := u.core.InstallUpdate(context.Background()); if err == nil { u.setMessage("Installed tunnel-client "+v.Version) }; return err }) }
	for s.rollback.Clicked(gtx) { u.async("rolling back tunnel-client", func() error { v, err := u.core.Rollback(context.Background()); if err == nil { u.setMessage("Active tunnel-client: "+v.Version) }; return err }) }
	for s.save.Clicked(gtx) { m := u.settingsConfig(); u.async("saving settings", func() error { return u.core.SaveManager(context.Background(), m) }) }
	closes := []string{"Keep running / tray", "Exit"}; themes := []string{"System", "Light", "Dark"}
	return u.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.H6(u.th, "Settings").Layout),
			layout.Rigid(editorLine(u.th, &s.tunnel, "Manager Tunnel ID")),
			layout.Rigid(editorLine(u.th, &s.cred, "Manager runtime credential ref")),
			layout.Rigid(editorLine(u.th, &s.idle, "Default Managed idle timeout seconds")),
			layout.Rigid(material.CheckBox(u.th, &s.launch, "Launch at startup").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.confirm, "Confirm explicit exit").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.autoUpdate, "Auto-update tunnel-client").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.disk, "Write bounded rotating logs to disk").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, layout.Rigid(buttonInset(u.th,&s.closeBtn,"Close behavior: "+closes[s.closeMode])), layout.Rigid(buttonInset(u.th,&s.themeBtn,"Theme: "+themes[s.themeMode]))) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top:unit.Dp(10)}.Layout(gtx, material.H6(u.th,"Secret Store").Layout) }),
			layout.Rigid(editorLine(u.th, &s.secretRef, "Secret reference")),
			layout.Rigid(editorLine(u.th, &s.secretVal, "Secret value")),
			layout.Rigid(material.Button(u.th, &s.store, "Store Secret").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top:unit.Dp(10)}.Layout(gtx, material.H6(u.th,"Tunnel Client").Layout) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, layout.Rigid(material.Button(u.th,&s.check,"Check Update").Layout), layout.Rigid(buttonInset(u.th,&s.install,"Install Latest")), layout.Rigid(buttonInset(u.th,&s.rollback,"Roll Back")), layout.Rigid(buttonInset(u.th,&s.openWeb,"Advanced Web UI"))) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top:unit.Dp(14)}.Layout(gtx, material.Button(u.th,&s.save,"Save Settings").Layout) }),
		)
	})
}

func (u *desktopUI) settingsConfig() config.ManagerConfig {
	m := u.core.ManagerConfig(); s := &u.set
	m.ManagerTunnel.TunnelID = strings.TrimSpace(s.tunnel.Text()); m.ManagerTunnel.RuntimeCredentialRef = strings.TrimSpace(s.cred.Text()); m.ManagedDefaults.IdleTimeoutSeconds = atoiDefault(s.idle.Text(), 0); m.General.LaunchAtStartup = s.launch.Value; m.General.ConfirmExit = s.confirm.Value; m.TunnelClient.AutoUpdate = s.autoUpdate.Value; m.Logging.WriteToDisk = s.disk.Value
	if s.closeMode == 1 { m.General.CloseBehavior = "exit" } else { m.General.CloseBehavior = "minimize" }
	m.Appearance.Theme = []string{"system","light","dark"}[s.themeMode]
	return m
}

func (u *desktopUI) logPage(gtx layout.Context) layout.Dimensions {
	levels := []string{"All","TRACE","DEBUG","INFO","WARN","ERROR"}
	for u.levelBtn.Clicked(gtx) { u.logLevel = (u.logLevel + 1) % len(levels) }
	for u.clearLogs.Clicked(gtx) { u.core.ClearLogs() }
	all := u.core.Logs(); q := strings.ToLower(strings.TrimSpace(u.logSearch.Text())); var filtered []string
	for _, e := range all { if u.logLevel > 0 && strings.ToUpper(string(e.Level)) != levels[u.logLevel] { continue }; line := fmt.Sprintf("%s %-5s %-18s %-14s %s", e.Timestamp.Format("15:04:05"), strings.ToUpper(string(e.Level)), e.Source, e.Component, e.Message); if q != "" && !strings.Contains(strings.ToLower(line), q) { continue }; filtered = append(filtered, line) }
	return layout.Flex{Axis:layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Flex{Alignment:layout.Middle}.Layout(gtx, layout.Flexed(1, material.Editor(u.th,&u.logSearch,"Search logs").Layout), layout.Rigid(buttonInset(u.th,&u.levelBtn,"Level: "+levels[u.logLevel])), layout.Rigid(buttonInset(u.th,&u.clearLogs,"Clear"))) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height:unit.Dp(8)}.Layout(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return u.logs.Layout(gtx,len(filtered),func(gtx layout.Context,i int)layout.Dimensions{return layout.Inset{Bottom:unit.Dp(3)}.Layout(gtx,material.Caption(u.th,filtered[i]).Layout)}) }),
	)
}

func (u *desktopUI) async(label string, fn func() error) {
	u.mu.Lock(); if u.busy { u.mu.Unlock(); return }; u.busy = true; u.message = label; u.mu.Unlock()
	go func() { err := fn(); u.mu.Lock(); u.busy = false; if err != nil { u.message = err.Error() } else if u.message == label { u.message = "Done: "+label }; u.mu.Unlock(); u.win.Invalidate() }()
}
func (u *desktopUI) setMessage(s string) { u.mu.Lock(); u.message = s; u.mu.Unlock(); u.win.Invalidate() }
