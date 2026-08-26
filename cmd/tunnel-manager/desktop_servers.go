//go:build !nogui

package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/marker"
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
	id string

	name      widget.Editor
	plugin    widget.Editor
	tunnel    widget.Editor
	cred      widget.Editor
	exe       widget.Editor
	cwd       widget.Editor
	url       widget.Editor
	args      widget.Editor
	env       widget.Editor
	secretEnv widget.Editor
	startup   widget.Editor
	shutdown  widget.Editor
	idle      widget.Editor

	enabled   widget.Bool
	mode      int
	transport int

	modeBtn         widget.Clickable
	transportBtn    widget.Clickable
	save            widget.Clickable
	cancel          widget.Clickable
	lifecycleMarker widget.Clickable
}

func (u *desktopUI) initServerForm() {
	for _, editor := range []*widget.Editor{
		&u.form.name,
		&u.form.plugin,
		&u.form.tunnel,
		&u.form.cred,
		&u.form.exe,
		&u.form.cwd,
		&u.form.url,
		&u.form.startup,
		&u.form.shutdown,
		&u.form.idle,
	} {
		*editor = oneLine()
	}
}

func (u *desktopUI) serversPage(gtx layout.Context) layout.Dimensions {
	entries := u.core.Entries()
	snapshots := u.core.Snapshots()
	byID := make(map[string]servers.Snapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byID[snapshot.ServerID] = snapshot
	}

	// A persistent add button is stored in the zero-ID row action entry.
	addActions := u.row("")
	for addActions.edit.Clicked(gtx) {
		u.editServer(config.ServerEntry{})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, material.Body1(u.th, fmt.Sprintf("%d configured Server Entries", len(entries))).Layout),
				layout.Rigid(material.Button(u.th, &addActions.edit, "Add Server").Layout),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return u.list.Layout(gtx, len(entries), func(gtx layout.Context, index int) layout.Dimensions {
				entry := entries[index]
				return u.serverRow(gtx, entry, byID[entry.ID])
			})
		}),
	)
}

func (u *desktopUI) row(id string) *rowActions {
	if actions := u.rows[id]; actions != nil {
		return actions
	}
	actions := new(rowActions)
	u.rows[id] = actions
	return actions
}

func (u *desktopUI) serverRow(gtx layout.Context, entry config.ServerEntry, snapshot servers.Snapshot) layout.Dimensions {
	actions := u.row(entry.ID)
	for actions.start.Clicked(gtx) {
		u.lifecycle(entry.ID, "start")
	}
	for actions.stop.Clicked(gtx) {
		u.lifecycle(entry.ID, "shutdown")
	}
	for actions.restart.Clicked(gtx) {
		u.lifecycle(entry.ID, "restart")
	}
	for actions.edit.Clicked(gtx) {
		u.editServer(entry)
	}
	for actions.marker.Clicked(gtx) {
		u.setMessage(marker.Generate(entry.ID))
	}
	for actions.delete.Clicked(gtx) {
		entryID := entry.ID
		name := entry.Name
		u.async("deleting "+name, func() error {
			return u.core.DeleteServer(context.Background(), entryID)
		})
	}

	state := string(snapshot.Observed)
	if state == "" {
		state = "stopped"
	}
	detail := fmt.Sprintf("%s · %s · tunnel %v · activity %s", entry.Mode, state, snapshot.TunnelReady, snapshot.ActivityTracking)

	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, entry.Name).Layout),
			layout.Rigid(material.Caption(u.th, entry.ID+" · "+detail).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Rigid(buttonInset(u.th, &actions.start, "Start")),
						layout.Rigid(buttonInset(u.th, &actions.stop, "Stop")),
						layout.Rigid(buttonInset(u.th, &actions.restart, "Restart")),
						layout.Rigid(buttonInset(u.th, &actions.edit, "Edit")),
						layout.Rigid(buttonInset(u.th, &actions.marker, "Marker")),
						layout.Rigid(buttonInset(u.th, &actions.delete, "Delete")),
					)
				})
			}),
		)
	})
}

func (u *desktopUI) lifecycle(id, action string) {
	u.async(action+" "+id, func() error {
		_, err := u.core.Lifecycle(context.Background(), id, action)
		return err
	})
}

func (u *desktopUI) editServer(entry config.ServerEntry) {
	form := &u.form
	form.id = entry.ID
	form.name.SetText(entry.Name)
	form.plugin.SetText(entry.ChatGPTPluginName)
	form.tunnel.SetText(entry.Tunnel.TunnelID)
	form.cred.SetText(entry.Tunnel.RuntimeCredentialRef)
	form.enabled.Value = entry.Enabled || entry.ID == ""

	modes := []config.ServerMode{config.ModeManaged, config.ModeAlwaysOn, config.ModeManual}
	form.mode = 0
	for index, mode := range modes {
		if entry.Mode == mode {
			form.mode = index
		}
	}

	transports := []config.TransportType{config.TransportStdio, config.TransportManagedHTTP, config.TransportExternalHTTP}
	form.transport = 0
	for index, transport := range transports {
		if entry.Transport.Type == transport {
			form.transport = index
		}
	}

	form.exe.SetText("")
	form.cwd.SetText("")
	form.args.SetText("")
	form.url.SetText("")
	switch entry.Transport.Type {
	case config.TransportStdio:
		if entry.Transport.Stdio != nil {
			form.exe.SetText(entry.Transport.Stdio.Executable)
			form.cwd.SetText(entry.Transport.Stdio.WorkingDirectory)
			form.args.SetText(strings.Join(entry.Transport.Stdio.Args, "\n"))
		}
	case config.TransportManagedHTTP:
		if entry.Transport.ManagedHTTP != nil {
			form.url.SetText(entry.Transport.ManagedHTTP.URL)
			form.exe.SetText(entry.Transport.ManagedHTTP.Launch.Executable)
			form.cwd.SetText(entry.Transport.ManagedHTTP.Launch.WorkingDirectory)
			form.args.SetText(strings.Join(entry.Transport.ManagedHTTP.Launch.Args, "\n"))
		}
	case config.TransportExternalHTTP:
		if entry.Transport.ExternalHTTP != nil {
			form.url.SetText(entry.Transport.ExternalHTTP.URL)
		}
	}

	form.env.SetText(sortedMapLines(entry.Environment.Values))
	form.secretEnv.SetText(sortedMapLines(entry.Environment.SecretRefs))
	form.startup.SetText(intText(entry.Runtime.StartupTimeoutSeconds, 30))
	form.shutdown.SetText(intText(entry.Runtime.ShutdownTimeoutSeconds, 10))
	if entry.Runtime.IdleTimeoutSeconds != nil {
		form.idle.SetText(strconv.Itoa(*entry.Runtime.IdleTimeoutSeconds))
	} else {
		form.idle.SetText("")
	}
	u.page = "editor"
}

func (u *desktopUI) serverEditor(gtx layout.Context) layout.Dimensions {
	form := &u.form
	for form.modeBtn.Clicked(gtx) {
		form.mode = (form.mode + 1) % 3
	}
	for form.transportBtn.Clicked(gtx) {
		form.transport = (form.transport + 1) % 3
	}
	for form.cancel.Clicked(gtx) {
		u.page = "servers"
	}
	for form.lifecycleMarker.Clicked(gtx) {
		if form.id == "" {
			u.setMessage("Save the entry first to generate an immutable Server ID.")
		} else {
			u.setMessage(marker.Generate(form.id))
		}
	}
	for form.save.Clicked(gtx) {
		entry, err := u.formEntry()
		if err != nil {
			u.setMessage(err.Error())
			continue
		}
		u.async("saving server", func() error {
			_, err := u.core.SaveServer(context.Background(), entry)
			if err == nil {
				u.page = "servers"
			}
			return err
		})
	}

	modeLabels := []string{"Managed", "Always On", "Manual"}
	transportLabels := []string{"Stdio", "Managed HTTP", "External HTTP"}

	return u.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.H6(u.th, "Server Editor").Layout),
			layout.Rigid(material.Caption(u.th, valueOr(form.id, "New entry — ID generated on save")).Layout),
			layout.Rigid(material.CheckBox(u.th, &form.enabled, "Enabled").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(buttonInset(u.th, &form.modeBtn, "Mode: "+modeLabels[form.mode])),
					layout.Rigid(buttonInset(u.th, &form.transportBtn, "Transport: "+transportLabels[form.transport])),
				)
			}),
			layout.Rigid(editorLine(u.th, &form.name, "Name")),
			layout.Rigid(editorLine(u.th, &form.plugin, "ChatGPT Developer Plugin name")),
			layout.Rigid(editorLine(u.th, &form.tunnel, "Tunnel ID")),
			layout.Rigid(editorLine(u.th, &form.cred, "Runtime credential ref (blank = global)")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if form.transport == 2 {
					return layout.Dimensions{}
				}
				return editorLine(u.th, &form.exe, "Executable")(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if form.transport == 2 {
					return layout.Dimensions{}
				}
				return editorLine(u.th, &form.cwd, "Working directory")(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if form.transport == 2 {
					return layout.Dimensions{}
				}
				return editorLine(u.th, &form.args, "Arguments, one per line")(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if form.transport == 0 {
					return layout.Dimensions{}
				}
				return editorLine(u.th, &form.url, "MCP URL")(gtx)
			}),
			layout.Rigid(editorLine(u.th, &form.env, "Environment KEY=value, one per line")),
			layout.Rigid(editorLine(u.th, &form.secretEnv, "Secret environment KEY=secret://ref, one per line")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx,
					layout.Flexed(1, editorLine(u.th, &form.startup, "Startup timeout seconds")),
					layout.Flexed(1, editorLine(u.th, &form.shutdown, "Shutdown timeout seconds")),
					layout.Flexed(1, editorLine(u.th, &form.idle, "Managed idle timeout; blank = global")),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Rigid(material.Button(u.th, &form.cancel, "Cancel").Layout),
						layout.Rigid(buttonInset(u.th, &form.lifecycleMarker, "Lifecycle Marker")),
						layout.Rigid(buttonInset(u.th, &form.save, "Save")),
					)
				})
			}),
		)
	})
}

func editorLine(theme *material.Theme, editor *widget.Editor, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Caption(theme, label).Layout),
			layout.Rigid(material.Editor(theme, editor, label).Layout),
		)
	}
}

func sortedMapLines(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for index, key := range keys {
		if index > 0 {
			builder.WriteByte('\n')
		}
		fmt.Fprintf(&builder, "%s=%s", key, values[key])
	}
	return builder.String()
}

func parseMapLines(text string) (map[string]string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		index := strings.IndexByte(line, '=')
		if index <= 0 {
			return nil, fmt.Errorf("expected KEY=value: %s", line)
		}
		values[strings.TrimSpace(line[:index])] = strings.TrimSpace(line[index+1:])
	}
	return values, nil
}

func argLines(text string) []string {
	var args []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			args = append(args, line)
		}
	}
	return args
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func intText(value, fallback int) string {
	if value <= 0 {
		value = fallback
	}
	return strconv.Itoa(value)
}

func parseNonNegative(text string, fallback int) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("expected a non-negative integer: %q", text)
	}
	return value, nil
}

func (u *desktopUI) formEntry() (config.ServerEntry, error) {
	form := &u.form
	environment, err := parseMapLines(form.env.Text())
	if err != nil {
		return config.ServerEntry{}, err
	}
	secretEnvironment, err := parseMapLines(form.secretEnv.Text())
	if err != nil {
		return config.ServerEntry{}, err
	}
	startup, err := parseNonNegative(form.startup.Text(), 30)
	if err != nil {
		return config.ServerEntry{}, err
	}
	shutdown, err := parseNonNegative(form.shutdown.Text(), 10)
	if err != nil {
		return config.ServerEntry{}, err
	}

	entry := config.ServerEntry{
		ID:                form.id,
		Name:              strings.TrimSpace(form.name.Text()),
		ChatGPTPluginName: strings.TrimSpace(form.plugin.Text()),
		Enabled:           form.enabled.Value,
		Mode:              []config.ServerMode{config.ModeManaged, config.ModeAlwaysOn, config.ModeManual}[form.mode],
		Tunnel: config.TunnelConfig{
			TunnelID:             strings.TrimSpace(form.tunnel.Text()),
			RuntimeCredentialRef: strings.TrimSpace(form.cred.Text()),
		},
		Environment: config.EnvironmentConfig{Values: environment, SecretRefs: secretEnvironment},
		Runtime: config.RuntimeConfig{
			StartupTimeoutSeconds:  startup,
			ShutdownTimeoutSeconds: shutdown,
		},
	}

	if strings.TrimSpace(form.idle.Text()) != "" {
		idle, err := parseNonNegative(form.idle.Text(), 0)
		if err != nil {
			return entry, err
		}
		entry.Runtime.IdleTimeoutSeconds = &idle
	}

	entry.Transport.Type = []config.TransportType{config.TransportStdio, config.TransportManagedHTTP, config.TransportExternalHTTP}[form.transport]
	switch entry.Transport.Type {
	case config.TransportStdio:
		entry.Transport.Stdio = &config.StdioTransport{
			Executable:       strings.TrimSpace(form.exe.Text()),
			Args:             argLines(form.args.Text()),
			WorkingDirectory: strings.TrimSpace(form.cwd.Text()),
		}
	case config.TransportManagedHTTP:
		entry.Transport.ManagedHTTP = &config.ManagedHTTPTransport{
			URL: strings.TrimSpace(form.url.Text()),
			Launch: config.LaunchConfig{
				Executable:       strings.TrimSpace(form.exe.Text()),
				Args:             argLines(form.args.Text()),
				WorkingDirectory: strings.TrimSpace(form.cwd.Text()),
			},
		}
	case config.TransportExternalHTTP:
		entry.Transport.ExternalHTTP = &config.ExternalHTTPTransport{URL: strings.TrimSpace(form.url.Text())}
	}
	return entry, nil
}
