//go:build !nogui

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
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
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

type v2ServerActions struct {
	start   widget.Clickable
	stop    widget.Clickable
	restart widget.Clickable
	oauth   widget.Clickable
	remove  widget.Clickable
}

type v2DesktopUI struct {
	core *coreapp.V2App
	win  *gioapp.Window
	th   *material.Theme

	page string
	list widget.List

	serversNav  widget.Clickable
	indexNav    widget.Clickable
	routingNav  widget.Clickable
	settingsNav widget.Clickable
	logsNav     widget.Clickable
	exit        widget.Clickable

	serverRows map[string]*v2ServerActions

	indexRefresh widget.Clickable
	indexCommit  widget.Clickable

	protection      widget.Bool
	managerTunnel   widget.Bool
	managerTunnelID widget.Editor
	embeddingBase   widget.Editor
	embeddingModel  widget.Editor
	embeddingKey    widget.Editor
	saveSettings    widget.Clickable

	mu      sync.RWMutex
	busy    bool
	message string
	exiting bool

	showReq  chan struct{}
	trayStop chan struct{}
}

func runDesktopV2(core *coreapp.V2App, setFocus func(func())) error {
	ready := make(chan *v2DesktopUI, 1)
	done := make(chan error, 1)
	go func() {
		u := newV2DesktopUI(core)
		ready <- u
		done <- u.loop()
	}()

	u := <-ready
	if setFocus != nil {
		setFocus(u.showWindow)
	}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		systray.Run(u.trayReady, func() {})
	}()
	go func() {
		err := <-done
		close(u.trayStop)
		systray.Quit()
		if err != nil {
			fmt.Fprintln(os.Stderr, "desktop:", err)
		}
	}()
	gioapp.Main()
	return nil
}

func newV2DesktopUI(core *coreapp.V2App) *v2DesktopUI {
	u := &v2DesktopUI{
		core:       core,
		th:         material.NewTheme(),
		page:       "servers",
		list:       widget.List{List: layout.List{Axis: layout.Vertical}},
		serverRows: make(map[string]*v2ServerActions),
		showReq:    make(chan struct{}, 1),
		trayStop:   make(chan struct{}),
	}
	u.th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	u.managerTunnelID.SingleLine = true
	u.embeddingBase.SingleLine = true
	u.embeddingModel.SingleLine = true
	u.embeddingKey.SingleLine = true
	u.embeddingKey.Mask = '*'
	u.loadSettings()
	return u
}

func (u *v2DesktopUI) loadSettings() {
	cfg := u.core.ManagerConfig()
	u.protection.Value = cfg.LocalManager.AccessProtectionEnabled
	u.managerTunnel.Value = cfg.ManagerTunnel.Enabled
	u.managerTunnelID.SetText(cfg.ManagerTunnel.TunnelID)
	u.embeddingBase.SetText(cfg.Embedding.BaseURL)
	u.embeddingModel.SetText(cfg.Embedding.Model)
	u.embeddingKey.SetText("")
}

func (u *v2DesktopUI) loop() error {
	win := new(gioapp.Window)
	win.Option(
		gioapp.Title("GPT Tunnel Manager"),
		gioapp.Size(unit.Dp(1180), unit.Dp(800)),
		gioapp.MinSize(unit.Dp(880), unit.Dp(600)),
	)
	u.mu.Lock()
	u.win = win
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.win = nil
		u.mu.Unlock()
	}()

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-u.core.Done():
				win.Perform(system.ActionClose)
				return
			case <-u.showReq:
				win.Option(gioapp.Windowed.Option())
				win.Perform(system.ActionRaise)
				win.Invalidate()
			case <-ticker.C:
				win.Invalidate()
			}
		}
	}()

	var ops op.Ops
	for {
		switch event := win.Event().(type) {
		case gioapp.DestroyEvent:
			if event.Err != nil {
				return event.Err
			}
			u.requestExit()
			return nil
		case gioapp.FrameEvent:
			gtx := gioapp.NewContext(&ops, event)
			u.layout(gtx)
			event.Frame(gtx.Ops)
		}
	}
}

func (u *v2DesktopUI) currentWindow() *gioapp.Window {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.win
}

func (u *v2DesktopUI) invalidate() {
	if win := u.currentWindow(); win != nil {
		win.Invalidate()
	}
}

func (u *v2DesktopUI) showWindow() {
	select {
	case u.showReq <- struct{}{}:
	default:
	}
	u.invalidate()
}

func (u *v2DesktopUI) requestExit() {
	u.mu.Lock()
	if u.exiting {
		u.mu.Unlock()
		return
	}
	u.exiting = true
	u.message = "Shutting down…"
	u.mu.Unlock()
	u.core.RequestShutdown()
}

func (u *v2DesktopUI) async(label string, fn func() error) {
	u.mu.Lock()
	if u.busy || u.exiting {
		u.mu.Unlock()
		return
	}
	u.busy = true
	u.message = label
	u.mu.Unlock()
	go func() {
		err := fn()
		u.mu.Lock()
		u.busy = false
		if err != nil {
			u.message = err.Error()
		} else {
			u.message = "Done: " + label
		}
		u.mu.Unlock()
		u.invalidate()
	}()
}

func (u *v2DesktopUI) layout(gtx layout.Context) layout.Dimensions {
	paint.Fill(gtx.Ops, uiCanvas)
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(u.sidebar),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(24), Bottom: unit.Dp(18), Left: unit.Dp(26), Right: unit.Dp(26)}.Layout(gtx, u.mainArea)
		}),
	)
}

func (u *v2DesktopUI) sidebar(gtx layout.Context) layout.Dimensions {
	for u.serversNav.Clicked(gtx) { u.page = "servers" }
	for u.indexNav.Clicked(gtx) { u.page = "index" }
	for u.routingNav.Clicked(gtx) { u.page = "routing" }
	for u.settingsNav.Clicked(gtx) { u.page = "settings"; u.loadSettings() }
	for u.logsNav.Clicked(gtx) { u.page = "logs" }
	for u.exit.Clicked(gtx) { u.requestExit() }

	width := gtx.Dp(unit.Dp(220))
	gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
	manager := u.core.ManagerSnapshot()
	statusText := "STOPPED"
	statusBg, statusFg := uiSurfaceRaised, uiMuted
	if manager.Running {
		statusText, statusBg, statusFg = "RUNNING", uiSuccessSoft, uiSuccess
	}
	return surface(uiSidebar, 0, layout.Inset{Top: unit.Dp(24), Bottom: unit.Dp(20), Left: unit.Dp(18), Right: unit.Dp(18)}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(sectionTitle(u.th, "GPT TUNNEL")),
			layout.Rigid(mutedCaption(u.th, "MANAGER · V2 ROUTER")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(26)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.serversNav, "Servers", u.page == "servers")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(6)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.indexNav, "Index", u.page == "index")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(6)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.routingNav, "Routing", u.page == "routing")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(6)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.settingsNav, "Settings", u.page == "settings")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(6)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.logsNav, "Logs", u.page == "logs")),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Spacer{}.Layout(gtx) }),
			layout.Rigid(compactCard(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(faintCaption(u.th, "MANAGER MCP")),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, pill(u.th, statusText, statusBg, statusFg)) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%d servers", len(u.core.Entries())))) }),
				)
			})),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { gtx.Constraints.Min.X = gtx.Constraints.Max.X; return dangerButton(u.th, &u.exit, "Exit Manager")(gtx) }),
		)
	})(gtx)
}

func (u *v2DesktopUI) mainArea(gtx layout.Context) layout.Dimensions {
	title, subtitle := "Servers", "Manage downstream MCP runtimes through the router-native lifecycle."
	switch u.page {
	case "index": title, subtitle = "Index", "Build and promote the routing catalog for the current routing state."
	case "routing": title, subtitle = "Routing", "Inspect routing profiles, preference revisions, and review state."
	case "settings": title, subtitle = "Settings", "Configure local Manager protection, tunnel exposure, and embeddings."
	case "logs": title, subtitle = "Logs", "Native v2 operational logging surface."
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(pageTitle(u.th, title)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(18)}.Layout(gtx, mutedCaption(u.th, subtitle)) }),
		layout.Flexed(1, u.body),
		layout.Rigid(u.footer),
	)
}

func (u *v2DesktopUI) body(gtx layout.Context) layout.Dimensions {
	switch u.page {
	case "index": return u.indexPage(gtx)
	case "routing": return u.routingPage(gtx)
	case "settings": return u.settingsPage(gtx)
	case "logs": return u.logsPage(gtx)
	default: return u.serversPage(gtx)
	}
}

func (u *v2DesktopUI) serversPage(gtx layout.Context) layout.Dimensions {
	entries := u.core.Entries()
	snapshots := make(map[string]bool)
	active := make(map[string]int)
	for _, snapshot := range u.core.Snapshots() {
		snapshots[snapshot.ServerID] = snapshot.Running
		active[snapshot.ServerID] = snapshot.ActiveCallCount
	}
	if len(entries) == 0 {
		return card(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, "No downstream MCP servers are configured yet.")(gtx) })(gtx)
	}
	u.list.List.Axis = layout.Vertical
	u.list.List.Position.Count = len(entries)
	return material.List(u.th, &u.list).Layout(gtx, len(entries), func(gtx layout.Context, index int) layout.Dimensions {
		entry := entries[index]
		actions := u.serverRows[entry.ID]
		if actions == nil {
			actions = &v2ServerActions{}
			u.serverRows[entry.ID] = actions
		}
		for actions.start.Clicked(gtx) {
			id := entry.ID
			u.async("starting "+entry.Name, func() error { _, err := u.core.StartServer(context.Background(), id); return err })
		}
		for actions.stop.Clicked(gtx) {
			id := entry.ID
			u.async("stopping "+entry.Name, func() error { _, err := u.core.StopServer(context.Background(), id); return err })
		}
		for actions.restart.Clicked(gtx) {
			id := entry.ID
			u.async("restarting "+entry.Name, func() error { _, err := u.core.RestartServer(context.Background(), id); return err })
		}
		for actions.oauth.Clicked(gtx) {
			id := entry.ID
			status := u.core.OAuthStatus(context.Background(), id)
			if status.Configured {
				u.async("connecting OAuth for "+entry.Name, func() error { _, err := u.core.ConnectOAuth(context.Background(), id); return err })
			}
		}
		for actions.remove.Clicked(gtx) {
			id := entry.ID
			u.async("removing "+entry.Name, func() error { return u.core.DeleteServer(context.Background(), id) })
		}
		running := snapshots[entry.ID]
		state := "STOPPED"
		bg, fg := uiSurfaceRaised, uiMuted
		if running { state, bg, fg = "RUNNING", uiSuccessSoft, uiSuccess }
		if entry.Mode == v2config.ModeDisabled { state, bg, fg = "DISABLED", uiWarningSoft, uiWarning }
		oauth := u.core.OAuthStatus(context.Background(), entry.ID)
		return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, card(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return sectionTitle(u.th, entry.Name)(gtx) }),
						layout.Rigid(pill(u.th, strings.ToUpper(string(entry.Mode)), uiAccentSoft, uiText)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
						layout.Rigid(pill(u.th, state, bg, fg)),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%s · active leases %d · %s", entry.Transport.Type, active[entry.ID], entry.ID))) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						children := []layout.FlexChild{
							layout.Rigid(secondaryButton(u.th, &actions.start, "Start")),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
							layout.Rigid(secondaryButton(u.th, &actions.stop, "Stop")),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
							layout.Rigid(secondaryButton(u.th, &actions.restart, "Restart")),
						}
						if oauth.Configured {
							label := "Connect OAuth"
							if oauth.Connected { label = "Reconnect OAuth" }
							children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }), layout.Rigid(secondaryButton(u.th, &actions.oauth, label)))
						}
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }), layout.Rigid(dangerButton(u.th, &actions.remove, "Remove")))
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
					})
				}),
			)
		}))
	})
}

func (u *v2DesktopUI) indexPage(gtx layout.Context) layout.Dimensions {
	for u.indexRefresh.Clicked(gtx) { u.async("refreshing index", func() error { _, err := u.core.IndexRefresh(context.Background()); return err }) }
	for u.indexCommit.Clicked(gtx) { u.async("committing index", func() error { _, err := u.core.IndexCommit(context.Background()); return err }) }
	status, err := u.core.IndexStatus(context.Background())
	if err != nil { return card(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, err.Error())(gtx) })(gtx) }
	return card(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(sectionTitle(u.th, "Routing catalog")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("Active: %s", status.ActiveGenerationID))) }),
			layout.Rigid(mutedCaption(u.th, fmt.Sprintf("Staging: %s", status.StagingGenerationID))),
			layout.Rigid(mutedCaption(u.th, fmt.Sprintf("Ready: %t · pending required: %d · open reviews: %d", status.Ready, status.PendingRequired, status.OpenReviews))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, layout.Rigid(primaryButton(u.th, &u.indexRefresh, "Refresh Index")), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }), layout.Rigid(secondaryButton(u.th, &u.indexCommit, "Commit Ready Index"))) }) }),
		)
	})(gtx)
}

func (u *v2DesktopUI) routingPage(gtx layout.Context) layout.Dimensions {
	prefs, err := u.core.RoutingPreferences(context.Background())
	if err != nil { return card(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, err.Error())(gtx) })(gtx) }
	sort.Slice(prefs.Profiles, func(i, j int) bool { return prefs.Profiles[i].ID < prefs.Profiles[j].ID })
	return card(func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(sectionTitle(u.th, fmt.Sprintf("Preference revision %d", prefs.PreferenceRevision))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%d profiles · %d rules", len(prefs.Profiles), len(prefs.Rules)))) }),
		}
		for _, profile := range prefs.Profiles {
			p := profile
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, fmt.Sprintf("Profile %s — %s", p.ID, p.Name))(gtx) }))
		}
		for _, rule := range prefs.Rules {
			r := rule
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, fmt.Sprintf("%s · %s · %s", r.ID, r.Spec.Specificity, r.ReviewState))(gtx) }))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})(gtx)
}

func editorSurface(th *material.Theme, editor *widget.Editor, hint string) layout.Widget {
	return inputSurface(func(gtx layout.Context) layout.Dimensions {
		style := material.Editor(th, editor, hint)
		style.Color = uiText
		style.HintColor = uiFaint
		return style.Layout(gtx)
	})
}

func (u *v2DesktopUI) settingsPage(gtx layout.Context) layout.Dimensions {
	for u.saveSettings.Clicked(gtx) {
		u.async("saving v2 settings", func() error {
			cfg := u.core.ManagerConfig()
			cfg.LocalManager.AccessProtectionEnabled = u.protection.Value
			cfg.ManagerTunnel.Enabled = u.managerTunnel.Value
			cfg.ManagerTunnel.TunnelID = strings.TrimSpace(u.managerTunnelID.Text())
			if err := u.core.SaveManager(context.Background(), cfg); err != nil { return err }
			embed := cfg.Embedding
			embed.BaseURL = strings.TrimSpace(u.embeddingBase.Text())
			embed.Model = strings.TrimSpace(u.embeddingModel.Text())
			var key []byte
			if strings.TrimSpace(u.embeddingKey.Text()) != "" { key = []byte(u.embeddingKey.Text()) }
			if err := u.core.SetEmbedding(context.Background(), embed, key); err != nil { return err }
			u.embeddingKey.SetText("")
			return nil
		})
	}
	credentialStatus := "not configured"
	if u.core.EmbeddingCredentialConfigured(context.Background()) { credentialStatus = "configured" }
	return card(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(sectionTitle(u.th, "Local Manager")),
			layout.Rigid(material.CheckBox(u.th, &u.protection, "Require local Manager capability").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(16)}.Layout(gtx) }),
			layout.Rigid(sectionTitle(u.th, "Manager Secure MCP Tunnel")),
			layout.Rigid(material.CheckBox(u.th, &u.managerTunnel, "Enable Manager tunnel").Layout),
			layout.Rigid(editorSurface(u.th, &u.managerTunnelID, "Tunnel ID")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(16)}.Layout(gtx) }),
			layout.Rigid(sectionTitle(u.th, "Embeddings")),
			layout.Rigid(editorSurface(u.th, &u.embeddingBase, "OpenAI-compatible base URL")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(7)}.Layout(gtx) }),
			layout.Rigid(editorSurface(u.th, &u.embeddingModel, "Embedding model")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &u.embeddingKey, "API key (leave blank to keep current)")) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(5)}.Layout(gtx, faintCaption(u.th, "Credential: "+credentialStatus)) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, primaryButton(u.th, &u.saveSettings, "Save Settings")) }),
		)
	})(gtx)
}

func (u *v2DesktopUI) logsPage(gtx layout.Context) layout.Dimensions {
	return card(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(sectionTitle(u.th, "Operational log")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, "The v2 runtime is active. Structured logging integration is the next Phase 11 preservation slice.")) }),
		)
	})(gtx)
}

func (u *v2DesktopUI) footer(gtx layout.Context) layout.Dimensions {
	u.mu.RLock()
	message, busy := u.message, u.busy
	u.mu.RUnlock()
	if message == "" { message = "Manager MCP: " + u.core.ManagerSnapshot().MCPURL }
	if busy { message = "Working… " + message }
	return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, faintCaption(u.th, message))
}

func (u *v2DesktopUI) trayReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle("GPT Tunnel Manager")
	systray.SetTooltip("GPT Tunnel Manager v2")
	systray.SetOnTapped(u.showWindow)
	systray.SetOnSecondaryTapped(u.showWindow)
	open := systray.AddMenuItem("Open Manager", "Show GPT Tunnel Manager")
	status := systray.AddMenuItem("Status", "Router-native runtime status")
	status.Disable()
	systray.AddSeparator()
	exit := systray.AddMenuItem("Exit Manager", "Stop Manager and downstream runtimes")
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-u.trayStop: return
			case <-open.ClickedCh: u.showWindow()
			case <-exit.ClickedCh: u.requestExit()
			case <-ticker.C:
				running := 0
				for _, snapshot := range u.core.Snapshots() { if snapshot.Running { running++ } }
				status.SetTitle(fmt.Sprintf("Status: Manager running · %d/%d servers running", running, len(u.core.Entries())))
			}
		}
	}()
}
