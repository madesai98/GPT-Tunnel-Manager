//go:build !nogui

package main

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/platform"
)

type settingsForm struct {
	tunnel         widget.Editor
	cred           widget.Editor
	idle           widget.Editor
	secretRef      widget.Editor
	secretVal      widget.Editor
	launch         widget.Bool
	startMinimized widget.Bool
	minimizeToTray widget.Bool
	confirm        widget.Bool
	autoUpdate     widget.Bool
	disk           widget.Bool
	closeMode      int
	themeMode      int
	closeBtn       widget.Clickable
	themeBtn       widget.Clickable
	save           widget.Clickable
	store          widget.Clickable
	openWeb        widget.Clickable
	check          widget.Clickable
	install        widget.Clickable
	rollback       widget.Clickable
}

func (u *desktopUI) initSettingsForm() {
	for _, editor := range []*widget.Editor{
		&u.set.tunnel,
		&u.set.cred,
		&u.set.idle,
		&u.set.secretRef,
		&u.set.secretVal,
	} {
		*editor = oneLine()
	}
	u.set.secretVal.Mask = '•'
}

func (u *desktopUI) loadSettings() {
	cfg := u.core.ManagerConfig()
	s := &u.set
	s.tunnel.SetText(cfg.ManagerTunnel.TunnelID)
	s.cred.SetText(cfg.ManagerTunnel.RuntimeCredentialRef)
	s.idle.SetText(strconv.Itoa(cfg.ManagedDefaults.IdleTimeoutSeconds))
	s.launch.Value = cfg.General.LaunchAtStartup
	s.startMinimized.Value = cfg.General.StartMinimized
	s.minimizeToTray.Value = cfg.General.MinimizeToTray
	s.confirm.Value = cfg.General.ConfirmExit
	s.autoUpdate.Value = cfg.TunnelClient.AutoUpdate
	s.disk.Value = cfg.Logging.WriteToDisk
	if cfg.General.CloseBehavior == "exit" {
		s.closeMode = 1
	} else {
		s.closeMode = 0
	}
	themes := []string{"system", "light", "dark"}
	s.themeMode = 0
	for i, theme := range themes {
		if cfg.Appearance.Theme == theme {
			s.themeMode = i
		}
	}
	if s.secretRef.Text() == "" {
		ref := cfg.ManagerTunnel.RuntimeCredentialRef
		if ref == "" {
			ref = "secret://openai/runtime/default"
		}
		s.secretRef.SetText(ref)
	}
}

func (u *desktopUI) applyTheme() {
	if u.core.ManagerConfig().Appearance.Theme == "dark" {
		u.th.Palette = material.Palette{
			Bg:         color.NRGBA{R: 28, G: 30, B: 34, A: 255},
			Fg:         color.NRGBA{R: 235, G: 237, B: 240, A: 255},
			ContrastBg: color.NRGBA{R: 77, G: 108, B: 240, A: 255},
			ContrastFg: color.NRGBA{R: 255, G: 255, B: 255, A: 255},
		}
		return
	}
	u.th.Palette = material.NewTheme().Palette
}

func (u *desktopUI) settingsConfig() (config.ManagerConfig, error) {
	cfg := u.core.ManagerConfig()
	s := &u.set
	idle, err := parseNonNegative(s.idle.Text(), 300)
	if err != nil {
		return cfg, fmt.Errorf("default managed idle timeout: %w", err)
	}
	cfg.ManagerTunnel.TunnelID = strings.TrimSpace(s.tunnel.Text())
	cfg.ManagerTunnel.RuntimeCredentialRef = strings.TrimSpace(s.cred.Text())
	cfg.ManagedDefaults.IdleTimeoutSeconds = idle
	cfg.General.LaunchAtStartup = s.launch.Value
	cfg.General.StartMinimized = s.startMinimized.Value
	cfg.General.MinimizeToTray = s.minimizeToTray.Value
	cfg.General.ConfirmExit = s.confirm.Value
	cfg.TunnelClient.AutoUpdate = s.autoUpdate.Value
	cfg.Logging.WriteToDisk = s.disk.Value
	if s.closeMode == 1 {
		cfg.General.CloseBehavior = "exit"
	} else {
		cfg.General.CloseBehavior = "minimize"
	}
	cfg.Appearance.Theme = []string{"system", "light", "dark"}[s.themeMode]
	return cfg, nil
}

func (u *desktopUI) settings(gtx layout.Context) layout.Dimensions {
	s := &u.set
	for s.closeBtn.Clicked(gtx) {
		s.closeMode = (s.closeMode + 1) % 2
	}
	for s.themeBtn.Clicked(gtx) {
		s.themeMode = (s.themeMode + 1) % 3
	}
	for s.openWeb.Clicked(gtx) {
		_ = platform.OpenURL(context.Background(), u.core.AdminURL())
	}
	for s.store.Clicked(gtx) {
		ref := strings.TrimSpace(s.secretRef.Text())
		value := s.secretVal.Text()
		u.async("storing secret", func() error {
			if err := u.core.PutSecret(context.Background(), ref, value); err != nil {
				return err
			}
			s.secretVal.SetText("")
			return nil
		})
	}
	for s.check.Clicked(gtx) {
		u.async("checking tunnel-client update", func() error {
			release, err := u.core.CheckUpdate(context.Background())
			if err != nil {
				return err
			}
			u.setMessage("Latest tunnel-client: " + release.TagName)
			return nil
		})
	}
	for s.install.Clicked(gtx) {
		u.async("installing tunnel-client", func() error {
			active, err := u.core.InstallUpdate(context.Background())
			if err != nil {
				return err
			}
			u.setMessage("Installed tunnel-client " + active.Version + "; running tunnels switch after restart.")
			return nil
		})
	}
	for s.rollback.Clicked(gtx) {
		u.async("rolling back tunnel-client", func() error {
			active, err := u.core.Rollback(context.Background())
			if err != nil {
				return err
			}
			u.setMessage("Active tunnel-client: " + active.Version + "; running tunnels switch after restart.")
			return nil
		})
	}
	for s.save.Clicked(gtx) {
		cfg, err := u.settingsConfig()
		if err != nil {
			u.setMessage(err.Error())
			continue
		}
		u.async("saving settings", func() error {
			if err := u.core.SaveManager(context.Background(), cfg); err != nil {
				return err
			}
			u.applyTheme()
			return nil
		})
	}

	closeLabels := []string{"Minimize / keep running", "Exit"}
	themeLabels := []string{"System (light fallback)", "Light", "Dark"}
	return u.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.H6(u.th, "Settings").Layout),
			layout.Rigid(editorLine(u.th, &s.tunnel, "Manager Tunnel ID")),
			layout.Rigid(editorLine(u.th, &s.cred, "Manager runtime credential ref")),
			layout.Rigid(editorLine(u.th, &s.idle, "Default Managed idle timeout seconds")),
			layout.Rigid(material.CheckBox(u.th, &s.launch, "Launch at login").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.startMinimized, "Start minimized").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.minimizeToTray, "Show system tray icon").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.confirm, "Confirm explicit exit").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.autoUpdate, "Auto-update tunnel-client").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.disk, "Write bounded rotating logs to disk").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(buttonInset(u.th, &s.closeBtn, "Close behavior: "+closeLabels[s.closeMode])),
					layout.Rigid(buttonInset(u.th, &s.themeBtn, "Theme: "+themeLabels[s.themeMode])),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, material.H6(u.th, "Secret Store").Layout)
			}),
			layout.Rigid(editorLine(u.th, &s.secretRef, "Secret reference")),
			layout.Rigid(editorLine(u.th, &s.secretVal, "Secret value")),
			layout.Rigid(material.Button(u.th, &s.store, "Store Secret").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, material.H6(u.th, "Tunnel Client").Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(material.Button(u.th, &s.check, "Check Update").Layout),
					layout.Rigid(buttonInset(u.th, &s.install, "Install Latest")),
					layout.Rigid(buttonInset(u.th, &s.rollback, "Roll Back")),
					layout.Rigid(buttonInset(u.th, &s.openWeb, "Advanced Web UI")),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, material.Button(u.th, &s.save, "Save Settings").Layout)
			}),
		)
	})
}

func (u *desktopUI) logPage(gtx layout.Context) layout.Dimensions {
	levels := []string{"All", "TRACE", "DEBUG", "INFO", "WARN", "ERROR"}
	for u.levelBtn.Clicked(gtx) {
		u.logLevel = (u.logLevel + 1) % len(levels)
	}
	for u.clearLogs.Clicked(gtx) {
		u.core.ClearLogs()
	}
	for u.exportText.Clicked(gtx) {
		_ = platform.OpenURL(context.Background(), u.core.AdminURL()+"/api/logs?format=text")
	}
	for u.exportJSON.Clicked(gtx) {
		_ = platform.OpenURL(context.Background(), u.core.AdminURL()+"/api/logs?format=jsonl")
	}

	query := strings.ToLower(strings.TrimSpace(u.logSearch.Text()))
	filtered := make([]string, 0)
	for _, event := range u.core.Logs() {
		if u.logLevel > 0 && strings.ToUpper(string(event.Level)) != levels[u.logLevel] {
			continue
		}
		line := fmt.Sprintf("%s %-5s %-18s %-14s %s",
			event.Timestamp.Format("15:04:05"),
			strings.ToUpper(string(event.Level)),
			event.Source,
			event.Component,
			event.Message,
		)
		if query == "" || strings.Contains(strings.ToLower(line), query) {
			filtered = append(filtered, line)
		}
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, material.Editor(u.th, &u.logSearch, "Search logs").Layout),
				layout.Rigid(buttonInset(u.th, &u.levelBtn, "Level: "+levels[u.logLevel])),
				layout.Rigid(buttonInset(u.th, &u.exportText, "Export Text")),
				layout.Rigid(buttonInset(u.th, &u.exportJSON, "Export JSONL")),
				layout.Rigid(buttonInset(u.th, &u.clearLogs, "Clear")),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return u.logs.Layout(gtx, len(filtered), func(gtx layout.Context, i int) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(3)}.Layout(gtx, material.Caption(u.th, filtered[i]).Layout)
			})
		}),
	)
}
