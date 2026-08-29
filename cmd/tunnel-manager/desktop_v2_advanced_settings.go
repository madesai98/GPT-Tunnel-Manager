//go:build !nogui

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

var v2AdvancedSettings struct {
	loaded bool

	localPort           widget.Editor
	embeddingDimensions widget.Editor
	queryCacheEntries   widget.Editor
	managedIdleSeconds  widget.Editor

	captureLevel     string
	captureNext      widget.Clickable
	memoryLimitMB    string
	memoryNext       widget.Clickable
	writeToDisk      widget.Bool
	diskMinimumLevel string
	diskLevelNext    widget.Clickable
	maximumFileMB    widget.Editor
	keepFiles        widget.Editor

	tunnelBinaryPath widget.Editor
	tunnelAutoUpdate widget.Bool
	tunnelChannel    string
	tunnelChannelNext widget.Clickable
	updateHours      widget.Editor

	theme     string
	themeNext widget.Clickable
	save      widget.Clickable
}

func ensureV2AdvancedSettings(u *v2DesktopUI) {
	if v2AdvancedSettings.loaded {
		return
	}
	cfg := u.core.ManagerConfig()
	for _, editor := range []*widget.Editor{
		&v2AdvancedSettings.localPort,
		&v2AdvancedSettings.embeddingDimensions,
		&v2AdvancedSettings.queryCacheEntries,
		&v2AdvancedSettings.managedIdleSeconds,
		&v2AdvancedSettings.maximumFileMB,
		&v2AdvancedSettings.keepFiles,
		&v2AdvancedSettings.tunnelBinaryPath,
		&v2AdvancedSettings.updateHours,
	} {
		editor.SingleLine = true
	}
	v2AdvancedSettings.localPort.SetText(strconv.Itoa(cfg.LocalManager.Port))
	if cfg.Embedding.Dimensions != nil {
		v2AdvancedSettings.embeddingDimensions.SetText(strconv.Itoa(*cfg.Embedding.Dimensions))
	}
	v2AdvancedSettings.queryCacheEntries.SetText(strconv.Itoa(cfg.Index.QueryEmbeddingCacheEntries))
	v2AdvancedSettings.managedIdleSeconds.SetText(strconv.Itoa(cfg.ManagedDefaults.IdleTimeoutSeconds))
	v2AdvancedSettings.captureLevel = cfg.Logging.CaptureLevel
	v2AdvancedSettings.memoryLimitMB = strconv.Itoa(cfg.Logging.MemoryLimitMB)
	v2AdvancedSettings.writeToDisk.Value = cfg.Logging.WriteToDisk
	v2AdvancedSettings.diskMinimumLevel = cfg.Logging.DiskMinimumLevel
	v2AdvancedSettings.maximumFileMB.SetText(strconv.Itoa(cfg.Logging.MaximumFileSizeMB))
	v2AdvancedSettings.keepFiles.SetText(strconv.Itoa(cfg.Logging.KeepFiles))
	v2AdvancedSettings.tunnelBinaryPath.SetText(cfg.TunnelClient.BinaryPath)
	v2AdvancedSettings.tunnelAutoUpdate.Value = cfg.TunnelClient.AutoUpdate
	v2AdvancedSettings.tunnelChannel = cfg.TunnelClient.Channel
	v2AdvancedSettings.updateHours.SetText(strconv.Itoa(cfg.TunnelClient.UpdateCheckIntervalHours))
	v2AdvancedSettings.theme = cfg.Appearance.Theme
	v2AdvancedSettings.loaded = true
}

func v2RequiredInt(editor *widget.Editor, label string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(editor.Text()))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", label)
	}
	return value, nil
}

func v2OptionalPositiveInt(editor *widget.Editor, label string) (*int, error) {
	text := strings.TrimSpace(editor.Text())
	if text == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(text)
	if err != nil || value <= 0 {
		return nil, fmt.Errorf("%s must be a positive integer or blank", label)
	}
	return &value, nil
}

func v2Cycle(current string, values ...string) string {
	for i, value := range values {
		if strings.EqualFold(current, value) {
			return values[(i+1)%len(values)]
		}
	}
	return values[0]
}

func v2AdvancedSettingsSection(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	ensureV2AdvancedSettings(u)
	for v2AdvancedSettings.captureNext.Clicked(gtx) {
		v2AdvancedSettings.captureLevel = v2Cycle(v2AdvancedSettings.captureLevel, "trace", "debug", "info", "warn", "error")
	}
	for v2AdvancedSettings.memoryNext.Clicked(gtx) {
		v2AdvancedSettings.memoryLimitMB = v2Cycle(v2AdvancedSettings.memoryLimitMB, "5", "10", "25", "50", "100")
	}
	for v2AdvancedSettings.diskLevelNext.Clicked(gtx) {
		v2AdvancedSettings.diskMinimumLevel = v2Cycle(v2AdvancedSettings.diskMinimumLevel, "trace", "debug", "info", "warn", "error")
	}
	for v2AdvancedSettings.tunnelChannelNext.Clicked(gtx) {
		v2AdvancedSettings.tunnelChannel = v2Cycle(v2AdvancedSettings.tunnelChannel, "stable", "prerelease")
	}
	for v2AdvancedSettings.themeNext.Clicked(gtx) {
		v2AdvancedSettings.theme = v2Cycle(v2AdvancedSettings.theme, "system", "light", "dark")
	}
	for v2AdvancedSettings.save.Clicked(gtx) {
		u.async("saving advanced v2 settings", func() error {
			port, err := v2RequiredInt(&v2AdvancedSettings.localPort, "local Manager port")
			if err != nil { return err }
			dimensions, err := v2OptionalPositiveInt(&v2AdvancedSettings.embeddingDimensions, "embedding dimensions")
			if err != nil { return err }
			cacheEntries, err := v2RequiredInt(&v2AdvancedSettings.queryCacheEntries, "query embedding cache entries")
			if err != nil { return err }
			idleSeconds, err := v2RequiredInt(&v2AdvancedSettings.managedIdleSeconds, "managed idle timeout")
			if err != nil { return err }
			maxFileMB, err := v2RequiredInt(&v2AdvancedSettings.maximumFileMB, "maximum log file size")
			if err != nil { return err }
			keepFiles, err := v2RequiredInt(&v2AdvancedSettings.keepFiles, "log files to keep")
			if err != nil { return err }
			updateHours, err := v2RequiredInt(&v2AdvancedSettings.updateHours, "tunnel-client update interval")
			if err != nil { return err }

			cfg := u.core.ManagerConfig()
			cfg.LocalManager.Port = port
			cfg.Embedding.Dimensions = dimensions
			cfg.Index.QueryEmbeddingCacheEntries = cacheEntries
			cfg.ManagedDefaults.IdleTimeoutSeconds = idleSeconds
			cfg.Logging.CaptureLevel = v2AdvancedSettings.captureLevel
			memory, _ := strconv.Atoi(v2AdvancedSettings.memoryLimitMB)
			cfg.Logging.MemoryLimitMB = memory
			cfg.Logging.WriteToDisk = v2AdvancedSettings.writeToDisk.Value
			cfg.Logging.DiskMinimumLevel = v2AdvancedSettings.diskMinimumLevel
			cfg.Logging.MaximumFileSizeMB = maxFileMB
			cfg.Logging.KeepFiles = keepFiles
			cfg.TunnelClient.BinaryPath = strings.TrimSpace(v2AdvancedSettings.tunnelBinaryPath.Text())
			cfg.TunnelClient.AutoUpdate = v2AdvancedSettings.tunnelAutoUpdate.Value
			cfg.TunnelClient.Channel = v2AdvancedSettings.tunnelChannel
			cfg.TunnelClient.UpdateCheckIntervalHours = updateHours
			cfg.Appearance.Theme = v2AdvancedSettings.theme
			return u.core.SaveManager(context.Background(), cfg)
		})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(18)}.Layout(gtx) }),
		layout.Rigid(sectionTitle(u.th, "Advanced v2 configuration")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2AdvancedSettings.localPort, "Local Manager port (1024-65535)")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2AdvancedSettings.embeddingDimensions, "Embedding dimensions (blank = provider default)")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2AdvancedSettings.queryCacheEntries, "Query embedding cache entries (0-4096)")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2AdvancedSettings.managedIdleSeconds, "Default Managed idle timeout seconds")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(14)}.Layout(gtx) }),
		layout.Rigid(sectionTitle(u.th, "Logging")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2AdvancedSettings.captureNext, "Capture: "+strings.ToUpper(v2AdvancedSettings.captureLevel))) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2AdvancedSettings.memoryNext, "Memory ring: "+v2AdvancedSettings.memoryLimitMB+" MB")) }),
		layout.Rigid(material.CheckBox(u.th, &v2AdvancedSettings.writeToDisk, "Write logs to disk").Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2AdvancedSettings.diskLevelNext, "Disk minimum: "+strings.ToUpper(v2AdvancedSettings.diskMinimumLevel))) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2AdvancedSettings.maximumFileMB, "Maximum log file size MB")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2AdvancedSettings.keepFiles, "Rotated log files to keep")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(14)}.Layout(gtx) }),
		layout.Rigid(sectionTitle(u.th, "tunnel-client")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2AdvancedSettings.tunnelBinaryPath, "Binary override (blank = managed installer)")) }),
		layout.Rigid(material.CheckBox(u.th, &v2AdvancedSettings.tunnelAutoUpdate, "Automatically update tunnel-client").Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2AdvancedSettings.tunnelChannelNext, "Channel: "+strings.ToUpper(v2AdvancedSettings.tunnelChannel))) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2AdvancedSettings.updateHours, "Update check interval hours")) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(14)}.Layout(gtx) }),
		layout.Rigid(sectionTitle(u.th, "Appearance")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2AdvancedSettings.themeNext, "Theme: "+strings.ToUpper(v2AdvancedSettings.theme))) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, primaryButton(u.th, &v2AdvancedSettings.save, "Save Advanced Settings")) }),
	)
}
