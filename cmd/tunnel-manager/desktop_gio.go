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
	"os"
	"runtime"
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

type desktopUI struct {
	core *coreapp.App
	win  *gioapp.Window
	th   *material.Theme
	deco widget.Decorations

	page string
	list widget.List
	logs widget.List

	serversNav  widget.Clickable
	settingsNav widget.Clickable
	logsNav     widget.Clickable
	refresh     widget.Clickable
	exit        widget.Clickable

	rows map[string]*rowActions
	form serverForm
	set  settingsForm

	logSearch    widget.Editor
	logLevel     int
	levelBtn     widget.Clickable
	clearLogs    widget.Clickable
	exportText   widget.Clickable
	exportJSON   widget.Clickable
	logsToggle   widget.Clickable
	logsExpanded bool

	confirmingExit  bool
	resetExitChoice bool
	dontAskAgain    widget.Bool
	cancelExit      widget.Clickable
	confirmExit     widget.Clickable

	mu           sync.RWMutex
	busy         bool
	message      string
	windowHidden bool
	hidePending  bool
	exiting      bool

	showReq  chan struct{}
	trayStop chan struct{}
}

func oneLine() widget.Editor { return widget.Editor{SingleLine: true} }

func newDesktopUI(core *coreapp.App) *desktopUI {
	u := &desktopUI{
		core:         core,
		th:           material.NewTheme(),
		page:         "servers",
		list:         widget.List{List: layout.List{Axis: layout.Vertical}},
		logs:         widget.List{List: layout.List{Axis: layout.Vertical}},
		rows:         make(map[string]*rowActions),
		windowHidden: core.ManagerConfig().General.StartMinimized,
		showReq:      make(chan struct{}, 1),
		trayStop:     make(chan struct{}),
	}
	u.th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	u.logSearch = oneLine()
	u.initServerForm()
	u.initSettingsForm()
	u.loadSettings()
	u.applyTheme()
	return u
}

func (u *desktopUI) applyWindowOptions(win *gioapp.Window) {
	win.Option(
		gioapp.Title("GPT Tunnel Manager"),
		gioapp.Size(unit.Dp(1240), unit.Dp(820)),
		gioapp.MinSize(unit.Dp(900), unit.Dp(620)),
		gioapp.Decorated(false),
	)
}

func runDesktop(core *coreapp.App, setFocus func(func())) error {
	ready := make(chan *desktopUI, 1)
	done := make(chan error, 1)
	go func() {
		u := newDesktopUI(core)
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

func (u *desktopUI) currentWindow() *gioapp.Window {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.win
}

func (u *desktopUI) setWindow(win *gioapp.Window) {
	u.mu.Lock()
	u.win = win
	u.mu.Unlock()
}

func (u *desktopUI) coreDone() bool {
	select {
	case <-u.core.Done():
		return true
	default:
		return false
	}
}

func (u *desktopUI) loop() error {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-u.core.Done():
				if win := u.currentWindow(); win != nil {
					win.Perform(system.ActionClose)
				}
				return
			case <-ticker.C:
				if win := u.currentWindow(); win != nil {
					win.Invalidate()
				}
			}
		}
	}()

	for {
		u.mu.RLock()
		hidden := u.windowHidden
		exiting := u.exiting
		u.mu.RUnlock()

		if hidden {
			if exiting {
				<-u.core.Done()
				return nil
			}
			select {
			case <-u.core.Done():
				return nil
			case <-u.showReq:
				u.mu.Lock()
				if u.exiting {
					u.mu.Unlock()
					<-u.core.Done()
					return nil
				}
				u.windowHidden = false
				u.mu.Unlock()
			}
		}

		if err := u.runWindow(); err != nil {
			return err
		}
	}
}

func (u *desktopUI) runWindow() error {
	win := new(gioapp.Window)
	u.applyWindowOptions(win)
	u.deco = widget.Decorations{}
	u.setWindow(win)
	defer u.setWindow(nil)

	var ops op.Ops
	for {
		u.handleShowRequest(win)

		switch event := win.Event().(type) {
		case gioapp.ConfigEvent:
			u.deco.Maximized = event.Config.Mode == gioapp.Maximized
		case gioapp.DestroyEvent:
			u.mu.Lock()
			requestedHidden := u.windowHidden
			exiting := u.exiting
			u.windowHidden = true
			u.mu.Unlock()

			if event.Err != nil {
				return event.Err
			}
			if u.coreDone() {
				return nil
			}
			if exiting || requestedHidden {
				return nil
			}

			cfg := u.core.ManagerConfig()
			if cfg.General.CloseBehavior == "exit" {
				if cfg.General.ConfirmExit {
					u.mu.Lock()
					u.confirmingExit = true
					u.resetExitChoice = true
					u.windowHidden = false
					u.mu.Unlock()
				} else {
					u.shutdownNow()
				}
			}
			return nil
		case gioapp.FrameEvent:
			gtx := gioapp.NewContext(&ops, event)
			u.layout(gtx)
			event.Frame(gtx.Ops)
			u.applyPendingHide(win)
		}
	}
}

func (u *desktopUI) handleShowRequest(win *gioapp.Window) {
	select {
	case <-u.showReq:
	default:
		return
	}

	u.mu.RLock()
	hidden := u.windowHidden
	exiting := u.exiting
	u.mu.RUnlock()
	if exiting {
		return
	}

	if hidden {
		if !keepDesktopWindowAliveForTray() {
			u.signalShow()
			return
		}
		if !restoreDesktopWindow() {
			u.mu.Lock()
			u.message = "Unable to restore GPT Tunnel Manager from the system tray."
			u.mu.Unlock()
			win.Invalidate()
			return
		}
		u.mu.Lock()
		u.windowHidden = false
		u.hidePending = false
		u.mu.Unlock()
	}

	win.Option(gioapp.Windowed.Option())
	win.Perform(system.ActionRaise)
	win.Invalidate()
}

func (u *desktopUI) signalShow() {
	select {
	case u.showReq <- struct{}{}:
	default:
	}
}

func (u *desktopUI) showWindow() {
	u.mu.RLock()
	exiting := u.exiting
	win := u.win
	u.mu.RUnlock()
	if exiting {
		return
	}

	u.signalShow()
	if win != nil {
		win.Invalidate()
	}
}

func (u *desktopUI) hideToTray() {
	u.mu.Lock()
	if u.windowHidden || u.exiting || u.win == nil {
		u.mu.Unlock()
		return
	}
	win := u.win
	u.windowHidden = true
	u.hidePending = true
	u.mu.Unlock()
	win.Invalidate()
}

func (u *desktopUI) applyPendingHide(win *gioapp.Window) {
	u.mu.Lock()
	if !u.hidePending {
		u.mu.Unlock()
		return
	}
	u.hidePending = false
	hidden := u.windowHidden
	exiting := u.exiting
	u.mu.Unlock()

	if !hidden || exiting {
		return
	}
	if hideDesktopWindow() {
		return
	}
	if keepDesktopWindowAliveForTray() {
		u.mu.Lock()
		u.windowHidden = false
		u.message = "Unable to hide GPT Tunnel Manager to the system tray."
		u.mu.Unlock()
		win.Invalidate()
		return
	}
	win.Perform(system.ActionClose)
}

func (u *desktopUI) requestClose() {
	if u.core.ManagerConfig().General.CloseBehavior == "minimize" {
		u.hideToTray()
		return
	}
	u.requestExit()
}

func (u *desktopUI) requestExit() {
	if u.core.ManagerConfig().General.ConfirmExit {
		u.mu.Lock()
		u.confirmingExit = true
		u.resetExitChoice = true
		u.mu.Unlock()
		u.showWindow()
		return
	}
	u.shutdownNow()
}

func (u *desktopUI) shutdownNow() {
	u.mu.Lock()
	if u.exiting {
		u.mu.Unlock()
		return
	}
	u.exiting = true
	u.busy = true
	u.message = "Shutting down…"
	win := u.win
	u.mu.Unlock()
	if win != nil {
		win.Invalidate()
	}

	go func() {
		u.core.RequestShutdown()
		<-u.core.Done()
		if win := u.currentWindow(); win != nil {
			win.Perform(system.ActionClose)
		}
	}()
}

func (u *desktopUI) perform(action system.Action) {
	if win := u.currentWindow(); win != nil {
		win.Perform(action)
	}
}

func (u *desktopUI) invalidate() {
	if win := u.currentWindow(); win != nil {
		win.Invalidate()
	}
}

func (u *desktopUI) layout(gtx layout.Context) layout.Dimensions {
	paint.Fill(gtx.Ops, uiCanvas)
	actions := u.deco.Update(gtx)
	if actions&system.ActionMinimize != 0 {
		u.hideToTray()
	}
	if actions&system.ActionMaximize != 0 {
		u.perform(system.ActionMaximize)
	}
	if actions&system.ActionUnmaximize != 0 {
		u.perform(system.ActionUnmaximize)
	}
	if actions&system.ActionClose != 0 {
		u.requestClose()
	}

	u.mu.Lock()
	if u.resetExitChoice {
		u.dontAskAgain.Value = false
		u.resetExitChoice = false
	}
	confirmingExit := u.confirmingExit
	u.mu.Unlock()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(material.Decorations(
			u.th,
			&u.deco,
			system.ActionMinimize|system.ActionMaximize|system.ActionClose,
			"GPT Tunnel Manager",
		).Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(u.sidebar),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return u.mainArea(gtx, confirmingExit)
				}),
			)
		}),
	)
}

func (u *desktopUI) sidebar(gtx layout.Context) layout.Dimensions {
	for u.serversNav.Clicked(gtx) {
		u.page = "servers"
	}
	for u.settingsNav.Clicked(gtx) {
		u.page = "settings"
		u.loadSettings()
	}
	for u.logsNav.Clicked(gtx) {
		u.logsExpanded = !u.logsExpanded
	}
	for u.refresh.Clicked(gtx) {
		u.invalidate()
	}
	for u.exit.Clicked(gtx) {
		u.requestExit()
	}

	width := gtx.Dp(unit.Dp(226))
	gtx.Constraints.Min.X = width
	gtx.Constraints.Max.X = width

	manager := u.core.ManagerSnapshot()
	entries := u.core.Entries()
	statusBg, statusFg, statusText := stateColors(manager.State, manager.Ready)
	serversSelected := u.page == "servers" || u.page == "editor"

	return surface(uiSidebar, 0, layout.Inset{Top: unit.Dp(24), Bottom: unit.Dp(20), Left: unit.Dp(18), Right: unit.Dp(18)}, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				brand := material.H6(u.th, "GPT TUNNEL")
				brand.Color = uiText
				return brand.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, mutedCaption(u.th, "MANAGER · NATIVE GIO"))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(28)}.Layout(gtx) }),
			layout.Rigid(faintCaption(u.th, "WORKSPACE")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.serversNav, "Servers", serversSelected)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(7)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.settingsNav, "Settings", u.page == "settings")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(7)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.logsNav, "Logs", u.logsExpanded)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Spacer{}.Layout(gtx) }),
			layout.Rigid(compactCard(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(faintCaption(u.th, "MANAGER MCP")),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, pill(u.th, statusText, statusBg, statusFg))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%d configured servers", len(entries))))
					}),
				)
			})),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
			layout.Rigid(navButton(u.th, &u.refresh, "Refresh", false)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(7)}.Layout(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return dangerButton(u.th, &u.exit, "Exit Manager")(gtx)
			}),
		)
	})(gtx)
}

func (u *desktopUI) mainArea(gtx layout.Context, confirmingExit bool) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(24), Bottom: unit.Dp(14), Left: unit.Dp(26), Right: unit.Dp(26)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.pageHeader),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(18)}.Layout(gtx) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if confirmingExit {
					return u.exitDialog(gtx)
				}
				return u.body(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if confirmingExit {
					return layout.Dimensions{}
				}
				return u.logPane(gtx)
			}),
			layout.Rigid(u.footer),
		)
	})
}

func (u *desktopUI) pageHeader(gtx layout.Context) layout.Dimensions {
	title := "Servers"
	subtitle := "Manage MCP runtimes, tunnel health, and lifecycle actions."
	switch u.page {
	case "settings":
		title = "Settings"
		subtitle = "Runtime behavior, logging, secrets, updates, and manager configuration."
	case "editor":
		title = "Server Editor"
		subtitle = "Configure lifecycle, transport, environment, and tunnel details."
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(pageTitle(u.th, title)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, mutedCaption(u.th, subtitle))
		}),
	)
}

func (u *desktopUI) exitDialog(gtx layout.Context) layout.Dimensions {
	for u.cancelExit.Clicked(gtx) {
		u.mu.Lock()
		u.confirmingExit = false
		u.mu.Unlock()
	}
	for u.confirmExit.Clicked(gtx) {
		if u.dontAskAgain.Value {
			cfg := u.core.ManagerConfig()
			cfg.General.ConfirmExit = false
			_ = u.core.SaveManager(context.Background(), cfg)
		}
		u.mu.Lock()
		u.confirmingExit = false
		u.mu.Unlock()
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
					return layout.Inset{Top: unit.Dp(9), Bottom: unit.Dp(9)}.Layout(gtx,
						mutedCaption(u.th, "This disconnects all tunnels and stops MCP servers currently owned by Tunnel Manager."),
					)
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

func (u *desktopUI) body(gtx layout.Context) layout.Dimensions {
	switch u.page {
	case "editor":
		return u.serverEditor(gtx)
	case "settings":
		return u.settings(gtx)
	default:
		return u.serversPage(gtx)
	}
}

func (u *desktopUI) logPane(gtx layout.Context) layout.Dimensions {
	for u.logsToggle.Clicked(gtx) {
		u.logsExpanded = !u.logsExpanded
	}

	count := len(u.core.Logs())
	toggleLabel := "Show"
	if u.logsExpanded {
		toggleLabel = "Hide"
	}

	header := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(sectionTitle(u.th, "Logs")),
					layout.Rigid(mutedCaption(u.th, fmt.Sprintf("%d captured events", count))),
				)
			}),
			layout.Rigid(secondaryButton(u.th, &u.logsToggle, toggleLabel)),
		)
	}

	return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !u.logsExpanded {
			return compactCard(header)(gtx)
		}

		height := gtx.Dp(unit.Dp(285))
		if height > gtx.Constraints.Max.Y {
			height = gtx.Constraints.Max.Y
		}
		gtx.Constraints.Min.Y = height
		gtx.Constraints.Max.Y = height
		return compactCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(header),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
				layout.Flexed(1, u.logPage),
			)
		})(gtx)
	})
}

func (u *desktopUI) footer(gtx layout.Context) layout.Dimensions {
	u.mu.RLock()
	message, busy := u.message, u.busy
	u.mu.RUnlock()
	if busy && message != "Shutting down…" {
		message = "Working… " + message
	}
	if message == "" {
		message = "Manager MCP: " + u.core.ManagerMCPURL()
	}
	return layout.Inset{Top: unit.Dp(9)}.Layout(gtx, faintCaption(u.th, message))
}

func buttonInset(th *material.Theme, button *widget.Clickable, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, secondaryButton(th, button, label))
	}
}

func (u *desktopUI) async(label string, fn func() error) {
	u.mu.Lock()
	if u.busy {
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

func (u *desktopUI) setMessage(message string) {
	u.mu.Lock()
	u.message = message
	u.mu.Unlock()
	u.invalidate()
}

func (u *desktopUI) trayReady() {
	systray.SetIcon(trayIcon())
	systray.SetTitle("GPT Tunnel Manager")
	systray.SetTooltip("GPT Tunnel Manager")
	systray.SetOnTapped(u.showWindow)
	systray.SetOnSecondaryTapped(u.showWindow)

	open := systray.AddMenuItem("Open Manager", "Show the GPT Tunnel Manager window")
	status := systray.AddMenuItem("Status", "Runtime status summary")
	status.Disable()
	systray.AddSeparator()
	exit := systray.AddMenuItem("Exit Tunnel Manager", "Disconnect tunnels and stop owned MCP servers")

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
				ready, degraded := 0, 0
				manager := u.core.ManagerSnapshot()
				if manager.Ready {
					ready++
				} else if manager.State == "degraded" {
					degraded++
				}
				snapshots := u.core.Snapshots()
				for _, snapshot := range snapshots {
					if snapshot.Ready {
						ready++
					}
					if snapshot.Observed == "degraded" {
						degraded++
					}
				}
				status.SetTitle(fmt.Sprintf("Status: %d ready, %d degraded, %d total", ready, degraded, len(snapshots)+1))
			}
		}
	}()
}

func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 2; y < 14; y++ {
		for x := 2; x < 14; x++ {
			if x == 2 || x == 13 || y == 2 || y == 13 || (x >= 6 && x <= 9) {
				img.SetNRGBA(x, y, color.NRGBA{R: 96, G: 125, B: 255, A: 255})
			}
		}
	}

	var pngBuffer bytes.Buffer
	_ = png.Encode(&pngBuffer, img)
	data := pngBuffer.Bytes()

	var ico bytes.Buffer
	_ = binary.Write(&ico, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	ico.Write([]byte{16, 16, 0, 0})
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ico, binary.LittleEndian, uint16(32))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(len(data)))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(22))
	ico.Write(data)
	return ico.Bytes()
}
