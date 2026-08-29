//go:build !nogui

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
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

	page           string
	list           widget.List
	settingsScroll layout.List

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

	confirmingExit bool
	dontAskAgain   widget.Bool
	cancelExit     widget.Clickable
	confirmExit    widget.Clickable

	mu           sync.RWMutex
	busy         bool
	message      string
	exiting      bool
	windowHidden bool

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
			os.Exit(1)
		}
		os.Exit(0)
	}()
	gioapp.Main()
	return nil
}

func newV2DesktopUI(core *coreapp.V2App) *v2DesktopUI {
	cfg := core.ManagerConfig()
	u := &v2DesktopUI{
		core:         core,
		th:           material.NewTheme(),
		page:         "servers",
		list:         widget.List{List: layout.List{Axis: layout.Vertical}},
		serverRows:   make(map[string]*v2ServerActions),
		windowHidden: cfg.General.StartMinimized,
		showReq:      make(chan struct{}, 1),
		trayStop:     make(chan struct{}),
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
	ensureV2ProductControls(u)
}

func (u *v2DesktopUI) loop() error {
	for {
		u.mu.RLock()
		hidden := u.windowHidden
		exiting := u.exiting
		u.mu.RUnlock()
		if exiting {
			<-u.core.Done()
			return nil
		}
		if hidden {
			select {
			case <-u.core.Done():
				return nil
			case <-u.showReq:
				u.mu.Lock()
				u.windowHidden = false
				u.mu.Unlock()
			}
		}
		if err := u.runWindow(); err != nil {
			return err
		}
	}
}

func (u *v2DesktopUI) runWindow() error {
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
		if u.win == win {
			u.win = nil
		}
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
			u.mu.RLock()
			exiting := u.exiting
			u.mu.RUnlock()
			if exiting {
				return nil
			}
			select {
			case <-u.core.Done():
				return nil
			default:
			}
			cfg := u.core.ManagerConfig()
			if cfg.General.CloseBehavior == "minimize" || cfg.General.MinimizeToTray {
				u.mu.Lock()
				u.windowHidden = true
				u.mu.Unlock()
				return nil
			}
			u.mu.Lock()
			u.windowHidden = true
			u.mu.Unlock()
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
	u.mu.RLock()
	exiting := u.exiting
	u.mu.RUnlock()
	if exiting {
		return
	}
	select {
	case u.showReq <- struct{}{}:
	default:
	}
	u.invalidate()
}

func (u *v2DesktopUI) requestExit() {
	cfg := u.core.ManagerConfig()
	if cfg.General.ConfirmExit {
		u.mu.Lock()
		if !u.exiting {
			u.confirmingExit = true
			u.windowHidden = false
		}
		u.mu.Unlock()
		u.showWindow()
		return
	}
	u.shutdownNow()
}

func (u *v2DesktopUI) shutdownNow() {
	u.mu.Lock()
	if u.exiting {
		u.mu.Unlock()
		return
	}
	u.exiting = true
	u.confirmingExit = false
	u.message = "Shutting down…"
	u.mu.Unlock()
	u.invalidate()
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
		} else if u.message == label {
			u.message = "Done: " + label
		}
		u.mu.Unlock()
		u.invalidate()
	}()
}

func (u *v2DesktopUI) setMessage(message string) {
	u.mu.Lock()
	u.message = message
	u.mu.Unlock()
	u.invalidate()
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
	for u.serversNav.Clicked(gtx) {
		u.page = "servers"
	}
	for u.indexNav.Clicked(gtx) {
		u.page = "index"
	}
	for u.routingNav.Clicked(gtx) {
		u.page = "routing"
	}
	for u.settingsNav.Clicked(gtx) {
		u.page = "settings"
		u.loadSettings()
	}
	for u.logsNav.Clicked(gtx) {
		u.page = "logs"
	}
	for u.exit.Clicked(gtx) {
		u.requestExit()
	}

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
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, pill(u.th, statusText, statusBg, statusFg))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%d servers", len(u.core.Entries()))))
					}),
				)
			})),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return dangerButton(u.th, &u.exit, "Exit Manager")(gtx)
			}),
		)
	})(gtx)
}

func (u *v2DesktopUI) mainArea(gtx layout.Context) layout.Dimensions {
	u.mu.RLock()
	confirmingExit := u.confirmingExit
	u.mu.RUnlock()
	if confirmingExit {
		return u.exitDialog(gtx)
	}
	title, subtitle := "Servers", "Manage downstream MCP runtimes through the router-native lifecycle."
	switch u.page {
	case "index":
		title, subtitle = "Index", "Build and promote the routing catalog for the current routing state."
	case "routing":
		title, subtitle = "Routing", "Manage routing profiles, preference revisions, and review state."
	case "settings":
		title, subtitle = "Settings", "Configure local Manager protection, tunnel exposure, embeddings, and native behavior."
	case "logs":
		title, subtitle = "Logs", "Filter, inspect, clear, and export structured v2 runtime logs."
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(pageTitle(u.th, title)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(18)}.Layout(gtx, mutedCaption(u.th, subtitle))
		}),
		layout.Flexed(1, u.body),
		layout.Rigid(u.footer),
	)
}

func (u *v2DesktopUI) exitDialog(gtx layout.Context) layout.Dimensions {
	for u.cancelExit.Clicked(gtx) {
		u.mu.Lock()
		u.confirmingExit = false
		u.dontAskAgain.Value = false
		u.mu.Unlock()
	}
	for u.confirmExit.Clicked(gtx) {
		if u.dontAskAgain.Value {
			cfg := u.core.ManagerConfig()
			cfg.General.ConfirmExit = false
			_ = u.core.SaveManager(context.Background(), cfg)
		}
		u.shutdownNow()
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		maxWidth := gtx.Dp(unit.Dp(520))
		if gtx.Constraints.Max.X > maxWidth {
			gtx.Constraints.Max.X = maxWidth
		}
		return card(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(sectionTitle(u.th, "Exit GPT Tunnel Manager?")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(9), Bottom: unit.Dp(9)}.Layout(gtx, mutedCaption(u.th, "This stops the Manager MCP, its one optional secure tunnel, and any downstream MCP runtimes owned by the router."))
				}),
				layout.Rigid(material.CheckBox(u.th, &u.dontAskAgain, "Don't ask again").Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(secondaryButton(u.th, &u.cancelExit, "Cancel")),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
							layout.Rigid(dangerButton(u.th, &u.confirmExit, "Exit Manager")),
						)
					})
				}),
			)
		})(gtx)
	})
}

func (u *v2DesktopUI) body(gtx layout.Context) layout.Dimensions {
	switch u.page {
	case "index":
		return u.indexPage(gtx)
	case "routing":
		return u.routingPage(gtx)
	case "settings":
		return u.settingsPage(gtx)
	case "logs":
		return u.logsPage(gtx)
	default:
		return u.serversPage(gtx)
	}
}

func (u *v2DesktopUI) serversPage(gtx layout.Context) layout.Dimensions { return v2ServersPage(u, gtx) }
func (u *v2DesktopUI) indexPage(gtx layout.Context) layout.Dimensions   { return v2IndexPage(u, gtx) }
func (u *v2DesktopUI) routingPage(gtx layout.Context) layout.Dimensions { return v2RoutingPage(u, gtx) }

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
			if err := u.core.SaveManager(context.Background(), cfg); err != nil {
				return err
			}
			embed := cfg.Embedding
			embed.BaseURL = strings.TrimSpace(u.embeddingBase.Text())
			embed.Model = strings.TrimSpace(u.embeddingModel.Text())
			var key []byte
			if strings.TrimSpace(u.embeddingKey.Text()) != "" {
				key = []byte(u.embeddingKey.Text())
			}
			if err := u.core.SetEmbedding(context.Background(), embed, key); err != nil {
				return err
			}
			u.embeddingKey.SetText("")
			return nil
		})
	}
	credentialStatus := "not configured"
	if u.core.EmbeddingCredentialConfigured(context.Background()) {
		credentialStatus = "configured"
	}
	u.settingsScroll.Axis = layout.Vertical
	return u.settingsScroll.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
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
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &u.embeddingKey, "API key (leave blank to keep current)"))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(5)}.Layout(gtx, faintCaption(u.th, "Credential: "+credentialStatus))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, primaryButton(u.th, &u.saveSettings, "Save Manager & Embedding Settings"))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2ProductSettingsSection(u, gtx) }),
			)
		})(gtx)
	})
}

func (u *v2DesktopUI) logsPage(gtx layout.Context) layout.Dimensions { return v2LogsPage(u, gtx) }

func (u *v2DesktopUI) footer(gtx layout.Context) layout.Dimensions {
	u.mu.RLock()
	message, busy := u.message, u.busy
	u.mu.RUnlock()
	if message == "" {
		message = "Manager MCP: " + u.core.ManagerSnapshot().MCPURL
	}
	if busy {
		message = "Working… " + message
	}
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
			case <-u.trayStop:
				return
			case <-open.ClickedCh:
				u.showWindow()
			case <-exit.ClickedCh:
				u.requestExit()
			case <-ticker.C:
				running := 0
				for _, snapshot := range u.core.Snapshots() {
					if snapshot.Running {
						running++
					}
				}
				tunnel := u.core.ManagerTunnelStatus()
				status.SetTitle(fmt.Sprintf("Status: Manager running · tunnel %s · %d/%d servers running", tunnel.State, running, len(u.core.Entries())))
			}
		}
	}()
}
