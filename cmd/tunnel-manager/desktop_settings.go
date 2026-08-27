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
)

type settingsForm struct {
	tunnel     widget.Editor
	idle       widget.Editor
	runtimeKey widget.Editor
	secretRef  widget.Editor
	secretVal  widget.Editor

	launch         widget.Bool
	startMinimized widget.Bool
	confirm        widget.Bool
	autoUpdate     widget.Bool
	disk           widget.Bool

	closeMode int
	themeMode int

	closeBtn       widget.Clickable
	themeBtn       widget.Clickable
	save           widget.Clickable
	saveRuntimeKey widget.Clickable
	store          widget.Clickable
	check          widget.Clickable
	install        widget.Clickable
	rollback       widget.Clickable
}

func (u *desktopUI) initSettingsForm() {
	for _, editor := range []*widget.Editor{
		&u.set.tunnel,
		&u.set.idle,
		&u.set.runtimeKey,
		&u.set.secretRef,
		&u.set.secretVal,
	} {
		*editor = oneLine()
	}
	u.set.runtimeKey.Mask = '•'
	u.set.secretVal.Mask = '•'
}

func (u *desktopUI) loadSettings() {
	cfg := u.core.ManagerConfig()
	s := &u.set
	s.tunnel.SetText(cfg.ManagerTunnel.TunnelID)
	s.runtimeKey.SetText("")
	s.idle.SetText(strconv.Itoa(cfg.ManagedDefaults.IdleTimeoutSeconds))
	s.launch.Value = cfg.General.LaunchAtStartup
	s.startMinimized.Value = cfg.General.StartMinimized
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
	cfg.ManagerTunnel.RuntimeCredentialRef = config.ManagerRuntimeCredentialRef
	cfg.ManagedDefaults.IdleTimeoutSeconds = idle
	cfg.General.LaunchAtStartup = s.launch.Value
	cfg.General.StartMinimized = s.startMinimized.Value
	cfg.General.MinimizeToTray = true
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
	u.handleManagerSelfUpdate(gtx)
	for s.closeBtn.Clicked(gtx) {
		s.closeMode = (s.closeMode + 1) % 2
	}
	for s.themeBtn.Clicked(gtx) {
		s.themeMode = (s.themeMode + 1) % 3
	}
	for s.saveRuntimeKey.Clicked(gtx) {
		value := strings.TrimSpace(s.runtimeKey.Text())
		if value == "" {
			u.setMessage("Enter an OpenAI Runtime API key first.")
			continue
		}
		u.async("storing OpenAI Runtime API key", func() error {
			if err := u.core.PutSecret(context.Background(), config.ManagerRuntimeCredentialRef, value); err != nil {
				return err
			}
			s.runtimeKey.SetText("")
			u.core.RestartManagerTunnel()
			return nil
		})
	}
	for s.store.Clicked(gtx) {
		ref := strings.TrimSpace(s.secretRef.Text())
		value := s.secretVal.Text()
		u.async("storing custom secret", func() error {
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
		apiKey := strings.TrimSpace(s.runtimeKey.Text())
		u.async("saving settings", func() error {
			if apiKey != "" {
				if err := u.core.PutSecret(context.Background(), config.ManagerRuntimeCredentialRef, apiKey); err != nil {
					return err
				}
				s.runtimeKey.SetText("")
			}
			if err := u.core.SaveManager(context.Background(), cfg); err != nil {
				return err
			}
			u.applyTheme()
			return nil
		})
	}

	closeLabels := []string{"Hide to system tray", "Exit"}
	themeLabels := []string{"System (light fallback)", "Light", "Dark"}
	return u.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.H6(u.th, "Manager Tunnel").Layout),
			layout.Rigid(editorLine(u.th, &s.tunnel, "Manager Tunnel ID")),
			layout.Rigid(editorLine(u.th, &s.runtimeKey, "OpenAI Runtime API key")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, material.Caption(u.th, "Enter only the key value. The credential reference is fixed internally and existing values are never displayed.").Layout)
			}),
			layout.Rigid(material.Button(u.th, &s.saveRuntimeKey, "Store API Key").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, material.H6(u.th, "General").Layout)
			}),
			layout.Rigid(editorLine(u.th, &s.idle, "Default Managed idle timeout seconds")),
			layout.Rigid(material.CheckBox(u.th, &s.launch, "Launch at login").Layout),
			layout.Rigid(material.CheckBox(u.th, &s.startMinimized, "Start hidden in system tray").Layout),
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
				return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, material.H6(u.th, "Custom Secrets").Layout)
			}),
			layout.Rigid(material.Caption(u.th, "Use custom secret references only for downstream MCP servers or environment values you define yourself.").Layout),
			layout.Rigid(editorLine(u.th, &s.secretRef, "Custom secret reference (secret://...)")),
			layout.Rigid(editorLine(u.th, &s.secretVal, "Custom secret value")),
			layout.Rigid(material.Button(u.th, &s.store, "Store Custom Secret").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, material.H6(u.th, "Tunnel Client").Layout)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(material.Button(u.th, &s.check, "Check Update").Layout),
					layout.Rigid(buttonInset(u.th, &s.install, "Install Latest")),
					layout.Rigid(buttonInset(u.th, &s.rollback, "Roll Back")),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, u.managerSelfUpdateSection)
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
		u.async("exporting text logs", func() error {
			path, err := u.core.ExportLogs("text")
			if err == nil {
				u.setMessage("Exported logs: " + path)
			}
			return err
		})
	}
	for u.exportJSON.Clicked(gtx) {
		u.async("exporting JSONL logs", func() error {
			path, err := u.core.ExportLogs("jsonl")
			if err == nil {
				u.setMessage("Exported logs: " + path)
			}
			return err
		})
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
