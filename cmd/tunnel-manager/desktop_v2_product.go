//go:build !nogui

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/buildinfo"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/logging"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/selfupdate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

var v2ProductControls struct {
	loaded bool

	launchAtStartup widget.Bool
	startMinimized  widget.Bool
	minimizeToTray  widget.Bool
	confirmExit     widget.Bool
	exitOnClose     widget.Bool

	managerCredential  widget.Editor
	saveGeneral        widget.Clickable
	saveCredential     widget.Clickable
	checkTunnelClient  widget.Clickable
	updateTunnelClient widget.Clickable
	selfUpdate         widget.Clickable

	logSearch  widget.Editor
	logList    widget.List
	logLevel   widget.Clickable
	clearLogs  widget.Clickable
	exportText widget.Clickable
	exportJSON widget.Clickable
}

func ensureV2ProductControls(u *v2DesktopUI) {
	if v2ProductControls.loaded {
		return
	}
	cfg := u.core.ManagerConfig()
	v2ProductControls.launchAtStartup.Value = cfg.General.LaunchAtStartup
	v2ProductControls.startMinimized.Value = cfg.General.StartMinimized
	v2ProductControls.minimizeToTray.Value = cfg.General.MinimizeToTray
	v2ProductControls.confirmExit.Value = cfg.General.ConfirmExit
	v2ProductControls.exitOnClose.Value = cfg.General.CloseBehavior == "exit"
	v2ProductControls.managerCredential.SingleLine = true
	v2ProductControls.managerCredential.Mask = '*'
	v2ProductControls.logSearch.SingleLine = true
	v2ProductControls.logList.List.Axis = layout.Vertical
	v2ProductControls.loaded = true
}

func applyV2GeneralControls(cfg *v2config.ManagerConfig) {
	cfg.General.LaunchAtStartup = v2ProductControls.launchAtStartup.Value
	cfg.General.StartMinimized = v2ProductControls.startMinimized.Value
	cfg.General.MinimizeToTray = v2ProductControls.minimizeToTray.Value
	cfg.General.ConfirmExit = v2ProductControls.confirmExit.Value
	cfg.General.CloseBehavior = "minimize"
	if v2ProductControls.exitOnClose.Value {
		cfg.General.CloseBehavior = "exit"
	}
}

func v2ProductSettingsSection(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	ensureV2ProductControls(u)
	for v2ProductControls.saveGeneral.Clicked(gtx) {
		u.async("saving native behavior", func() error {
			cfg := u.core.ManagerConfig()
			applyV2GeneralControls(&cfg)
			return u.core.SaveManager(context.Background(), cfg)
		})
	}
	for v2ProductControls.saveCredential.Clicked(gtx) {
		credential := v2ProductControls.managerCredential.Text()
		if strings.TrimSpace(credential) != "" {
			u.async("saving Manager tunnel credential", func() error {
				if err := u.core.SetManagerTunnelCredential(context.Background(), []byte(credential)); err != nil {
					return err
				}
				v2ProductControls.managerCredential.SetText("")
				return nil
			})
		}
	}
	for v2ProductControls.checkTunnelClient.Clicked(gtx) {
		u.async("checking tunnel-client update", func() error {
			release, err := u.core.CheckTunnelClientUpdate(context.Background())
			if err != nil {
				return err
			}
			u.setMessage("Latest tunnel-client: " + release.TagName)
			return nil
		})
	}
	for v2ProductControls.updateTunnelClient.Clicked(gtx) {
		u.async("updating tunnel-client", func() error {
			active, err := u.core.InstallTunnelClientUpdate(context.Background())
			if err != nil {
				return err
			}
			u.setMessage("tunnel-client updated to " + active.Version)
			return nil
		})
	}
	for v2ProductControls.selfUpdate.Clicked(gtx) {
		u.async("checking GPT Tunnel Manager update", func() error {
			if !buildinfo.IsRelease() {
				return errors.New("self-update is available only in published release builds")
			}
			executable, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve GPT Tunnel Manager executable: %w", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			plan, err := selfupdate.CheckAndStage(ctx, buildinfo.Version, executable)
			if err != nil {
				return err
			}
			if !plan.UpdateAvailable {
				u.core.LogSelfUpdate("GPT Tunnel Manager is already on the latest published version", map[string]any{"current_version": plan.CurrentVersion, "latest_version": plan.LatestVersion})
				u.setMessage(fmt.Sprintf("GPT Tunnel Manager is already up to date (%s).", plan.CurrentVersion))
				return nil
			}
			u.core.LogSelfUpdate("GPT Tunnel Manager update staged", map[string]any{"current_version": plan.CurrentVersion, "latest_version": plan.LatestVersion})
			if err := selfupdate.Launch(plan, os.Getpid(), os.Args[1:]); err != nil {
				plan.Cleanup()
				return err
			}
			u.setMessage(fmt.Sprintf("Updating GPT Tunnel Manager %s → %s…", plan.CurrentVersion, plan.LatestVersion))
			u.requestExit()
			return nil
		})
	}

	tunnel := u.core.ManagerTunnelStatus()
	credential := "not configured"
	if u.core.ManagerTunnelCredentialConfigured(context.Background()) {
		credential = "configured"
	}
	selfUpdateCaption := "Checks and installs the latest published GPT Tunnel Manager release."
	if !buildinfo.IsRelease() {
		selfUpdateCaption = "Development build: application self-update is disabled."
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(18)}.Layout(gtx) }),
		layout.Rigid(sectionTitle(u.th, "Native behavior")),
		layout.Rigid(material.CheckBox(u.th, &v2ProductControls.launchAtStartup, "Launch at startup").Layout),
		layout.Rigid(material.CheckBox(u.th, &v2ProductControls.startMinimized, "Start minimized").Layout),
		layout.Rigid(material.CheckBox(u.th, &v2ProductControls.minimizeToTray, "Minimize to system tray").Layout),
		layout.Rigid(material.CheckBox(u.th, &v2ProductControls.exitOnClose, "Exit when the window closes").Layout),
		layout.Rigid(material.CheckBox(u.th, &v2ProductControls.confirmExit, "Confirm before exit").Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, secondaryButton(u.th, &v2ProductControls.saveGeneral, "Save Native Behavior")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(18)}.Layout(gtx) }),
		layout.Rigid(sectionTitle(u.th, "Manager Secure MCP Tunnel")),
		layout.Rigid(mutedCaption(u.th, fmt.Sprintf("State: %s · credential: %s", tunnel.State, credential))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if tunnel.Error == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, mutedCaption(u.th, tunnel.Error))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ProductControls.managerCredential, "Runtime API key (leave blank to keep current)")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2ProductControls.saveCredential, "Save Runtime API Key")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(18)}.Layout(gtx) }),
		layout.Rigid(sectionTitle(u.th, "Updates")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(secondaryButton(u.th, &v2ProductControls.checkTunnelClient, "Check tunnel-client")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &v2ProductControls.updateTunnelClient, "Update tunnel-client")),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, mutedCaption(u.th, selfUpdateCaption)) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2ProductControls.selfUpdate, "Update GPT Tunnel Manager")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2AdvancedSettingsSection(u, gtx) }),
	)
}

func v2LevelRank(level logging.Level) int {
	switch level {
	case logging.Trace:
		return 0
	case logging.Debug:
		return 1
	case logging.Info:
		return 2
	case logging.Warn:
		return 3
	case logging.Error:
		return 4
	default:
		return 2
	}
}

func v2DisplayRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all":
		return -1
	case "trace":
		return 0
	case "debug":
		return 1
	case "info":
		return 2
	case "warn":
		return 3
	case "error":
		return 4
	default:
		return 2
	}
}

func v2NextDisplayLevel(value string) string {
	levels := []string{"all", "trace", "debug", "info", "warn", "error"}
	for i, level := range levels {
		if strings.EqualFold(value, level) {
			return levels[(i+1)%len(levels)]
		}
	}
	return "all"
}

func v2FilteredLogs(u *v2DesktopUI) []logging.Event {
	cfg := u.core.ManagerConfig()
	minimum := v2DisplayRank(cfg.Logging.DisplayLevel)
	query := strings.ToLower(strings.TrimSpace(v2ProductControls.logSearch.Text()))
	all := u.core.Logs()
	out := make([]logging.Event, 0, len(all))
	for _, event := range all {
		if minimum >= 0 && v2LevelRank(event.Level) < minimum {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(event.Source + " " + event.Component + " " + event.Message)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, event)
	}
	return out
}

func v2LogsPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	ensureV2ProductControls(u)
	for v2ProductControls.logLevel.Clicked(gtx) {
		cfg := u.core.ManagerConfig()
		cfg.Logging.DisplayLevel = v2NextDisplayLevel(cfg.Logging.DisplayLevel)
		u.async("changing log display level", func() error { return u.core.SaveManager(context.Background(), cfg) })
	}
	for v2ProductControls.clearLogs.Clicked(gtx) {
		u.core.ClearLogs()
		u.setMessage("Logs cleared")
	}
	for v2ProductControls.exportText.Clicked(gtx) {
		u.async("exporting text logs", func() error {
			path, err := u.core.ExportLogs("text")
			if err == nil {
				u.setMessage("Logs exported: " + path)
			}
			return err
		})
	}
	for v2ProductControls.exportJSON.Clicked(gtx) {
		u.async("exporting JSONL logs", func() error {
			path, err := u.core.ExportLogs("jsonl")
			if err == nil {
				u.setMessage("Logs exported: " + path)
			}
			return err
		})
	}

	filtered := v2FilteredLogs(u)
	v2ProductControls.logList.List.Position.Count = len(filtered)
	level := u.core.ManagerConfig().Logging.DisplayLevel
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, editorSurface(u.th, &v2ProductControls.logSearch, "Search logs")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &v2ProductControls.logLevel, "Level: "+strings.ToUpper(level))),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &v2ProductControls.clearLogs, "Clear")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &v2ProductControls.exportText, "Export Text")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &v2ProductControls.exportJSON, "Export JSONL")),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%d visible · %d captured", len(filtered), len(u.core.Logs())))) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return material.List(u.th, &v2ProductControls.logList).Layout(gtx, len(filtered), func(gtx layout.Context, index int) layout.Dimensions {
				event := filtered[index]
				line := fmt.Sprintf("%s  %-5s  %s · %s  %s", event.Timestamp.Local().Format("15:04:05"), strings.ToUpper(string(event.Level)), event.Source, event.Component, event.Message)
				return layout.Inset{Bottom: unit.Dp(5)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, line)(gtx) }))
			})
		}),
	)
}
