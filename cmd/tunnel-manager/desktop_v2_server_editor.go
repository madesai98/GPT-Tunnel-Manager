//go:build !nogui

package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

var v2ServerEditorState struct {
	active    bool
	editingID string

	name       widget.Editor
	mode       string
	modeNext   widget.Clickable
	transport  string
	transportNext widget.Clickable

	executable widget.Editor
	args       widget.Editor
	workingDir widget.Editor
	url        widget.Editor
	launchExecutable widget.Editor
	launchArgs widget.Editor
	launchWorkingDir widget.Editor

	authMode widget.Clickable
	auth     string
	oauthScopes widget.Editor
	staticHeader widget.Editor
	staticScheme widget.Editor
	staticCredential widget.Editor
	allowInsecure widget.Bool

	plainEnvironment widget.Editor
	secretEnvironment widget.Editor
	startupTimeout widget.Editor
	shutdownTimeout widget.Editor
	idleTimeout widget.Editor
	logOverride string
	logOverrideNext widget.Clickable

	save   widget.Clickable
	cancel widget.Clickable
	add    widget.Clickable

	editRows map[string]*widget.Clickable
}

func initV2ServerEditorWidgets() {
	if v2ServerEditorState.editRows != nil {
		return
	}
	v2ServerEditorState.editRows = make(map[string]*widget.Clickable)
	for _, editor := range []*widget.Editor{
		&v2ServerEditorState.name,
		&v2ServerEditorState.executable,
		&v2ServerEditorState.args,
		&v2ServerEditorState.workingDir,
		&v2ServerEditorState.url,
		&v2ServerEditorState.launchExecutable,
		&v2ServerEditorState.launchArgs,
		&v2ServerEditorState.launchWorkingDir,
		&v2ServerEditorState.oauthScopes,
		&v2ServerEditorState.staticHeader,
		&v2ServerEditorState.staticScheme,
		&v2ServerEditorState.startupTimeout,
		&v2ServerEditorState.shutdownTimeout,
		&v2ServerEditorState.idleTimeout,
	} {
		editor.SingleLine = true
	}
	v2ServerEditorState.staticCredential.SingleLine = true
	v2ServerEditorState.staticCredential.Mask = '*'
	v2ServerEditorState.plainEnvironment.SingleLine = false
	v2ServerEditorState.secretEnvironment.SingleLine = false
}

func v2ServerEditorActive() bool {
	initV2ServerEditorWidgets()
	return v2ServerEditorState.active
}

func cycleV2Value(current string, values []string) string {
	for i, value := range values {
		if current == value {
			return values[(i+1)%len(values)]
		}
	}
	return values[0]
}

func openNewV2ServerEditor() {
	initV2ServerEditorWidgets()
	v2ServerEditorState.active = true
	v2ServerEditorState.editingID = ""
	v2ServerEditorState.name.SetText("")
	v2ServerEditorState.mode = string(v2config.ModeManaged)
	v2ServerEditorState.transport = string(v2config.TransportStdio)
	v2ServerEditorState.auth = string(v2config.HTTPAuthNone)
	v2ServerEditorState.executable.SetText("")
	v2ServerEditorState.args.SetText("")
	v2ServerEditorState.workingDir.SetText("")
	v2ServerEditorState.url.SetText("")
	v2ServerEditorState.launchExecutable.SetText("")
	v2ServerEditorState.launchArgs.SetText("")
	v2ServerEditorState.launchWorkingDir.SetText("")
	v2ServerEditorState.oauthScopes.SetText("")
	v2ServerEditorState.staticHeader.SetText("Authorization")
	v2ServerEditorState.staticScheme.SetText("Bearer")
	v2ServerEditorState.staticCredential.SetText("")
	v2ServerEditorState.allowInsecure.Value = false
	v2ServerEditorState.plainEnvironment.SetText("")
	v2ServerEditorState.secretEnvironment.SetText("")
	v2ServerEditorState.startupTimeout.SetText("30")
	v2ServerEditorState.shutdownTimeout.SetText("10")
	v2ServerEditorState.idleTimeout.SetText("")
	v2ServerEditorState.logOverride = "inherit"
}

func openExistingV2ServerEditor(entry v2config.ServerEntry) {
	openNewV2ServerEditor()
	v2ServerEditorState.editingID = entry.ID
	v2ServerEditorState.name.SetText(entry.Name)
	v2ServerEditorState.mode = string(entry.Mode)
	v2ServerEditorState.transport = string(entry.Transport.Type)
	v2ServerEditorState.startupTimeout.SetText(strconv.Itoa(entry.Runtime.StartupTimeoutSeconds))
	v2ServerEditorState.shutdownTimeout.SetText(strconv.Itoa(entry.Runtime.ShutdownTimeoutSeconds))
	if entry.Runtime.IdleTimeoutSeconds != nil {
		v2ServerEditorState.idleTimeout.SetText(strconv.Itoa(*entry.Runtime.IdleTimeoutSeconds))
	}
	if entry.Logging.CaptureLevelOverride != nil {
		v2ServerEditorState.logOverride = *entry.Logging.CaptureLevelOverride
	}
	plain := make([]string, 0, len(entry.Environment.Values))
	for key, value := range entry.Environment.Values {
		plain = append(plain, key+"="+value)
	}
	sort.Strings(plain)
	v2ServerEditorState.plainEnvironment.SetText(strings.Join(plain, "\n"))
	secretNames := coreapp.EnvironmentSecretNames(entry)
	sort.Strings(secretNames)
	secretLines := make([]string, 0, len(secretNames))
	for _, name := range secretNames {
		secretLines = append(secretLines, name+"=")
	}
	v2ServerEditorState.secretEnvironment.SetText(strings.Join(secretLines, "\n"))

	switch entry.Transport.Type {
	case v2config.TransportStdio:
		if entry.Transport.Stdio != nil {
			v2ServerEditorState.executable.SetText(entry.Transport.Stdio.Executable)
			v2ServerEditorState.args.SetText(strings.Join(entry.Transport.Stdio.Args, " "))
			v2ServerEditorState.workingDir.SetText(entry.Transport.Stdio.WorkingDirectory)
		}
	case v2config.TransportManagedHTTP:
		if entry.Transport.ManagedHTTP != nil {
			cfg := entry.Transport.ManagedHTTP
			v2ServerEditorState.url.SetText(cfg.URL)
			v2ServerEditorState.launchExecutable.SetText(cfg.Launch.Executable)
			v2ServerEditorState.launchArgs.SetText(strings.Join(cfg.Launch.Args, " "))
			v2ServerEditorState.launchWorkingDir.SetText(cfg.Launch.WorkingDirectory)
			v2ServerEditorState.allowInsecure.Value = cfg.AllowInsecureCredentialTransport
			loadV2HTTPAuth(cfg.Auth)
		}
	case v2config.TransportExternalHTTP:
		if entry.Transport.ExternalHTTP != nil {
			cfg := entry.Transport.ExternalHTTP
			v2ServerEditorState.url.SetText(cfg.URL)
			v2ServerEditorState.allowInsecure.Value = cfg.AllowInsecureCredentialTransport
			loadV2HTTPAuth(cfg.Auth)
		}
	}
}

func loadV2HTTPAuth(auth v2config.HTTPAuthConfig) {
	v2ServerEditorState.auth = string(auth.Mode)
	if auth.OAuth != nil {
		v2ServerEditorState.oauthScopes.SetText(strings.Join(auth.OAuth.Scopes, " "))
	}
	if auth.Static != nil {
		v2ServerEditorState.staticHeader.SetText(auth.Static.HeaderName)
		v2ServerEditorState.staticScheme.SetText(auth.Static.Scheme)
	}
}

func v2ServerListHeader(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	initV2ServerEditorWidgets()
	for v2ServerEditorState.add.Clicked(gtx) {
		openNewV2ServerEditor()
		u.invalidate()
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, mutedCaption(u.th, "Server IDs and secret references are generated and managed automatically.")),
		layout.Rigid(primaryButton(u.th, &v2ServerEditorState.add, "Add MCP Server")),
	)
}

func v2ServerEditButton(u *v2DesktopUI, gtx layout.Context, entry v2config.ServerEntry) layout.Dimensions {
	initV2ServerEditorWidgets()
	button := v2ServerEditorState.editRows[entry.ID]
	if button == nil {
		button = new(widget.Clickable)
		v2ServerEditorState.editRows[entry.ID] = button
	}
	for button.Clicked(gtx) {
		openExistingV2ServerEditor(entry)
		u.invalidate()
	}
	return secondaryButton(u.th, button, "Edit")(gtx)
}

func parseV2Int(text, label string, allowEmpty bool) (*int, error) {
	text = strings.TrimSpace(text)
	if text == "" && allowEmpty {
		return nil, nil
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < 0 {
		return nil, fmt.Errorf("%s must be a non-negative integer", label)
	}
	return &value, nil
}

func parseV2Environment(text string) (map[string]string, error) {
	out := make(map[string]string)
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("environment line %q must be NAME=value", line)
		}
		out[key] = value
	}
	return out, nil
}

func buildV2ServerEntry(ctx context.Context, u *v2DesktopUI) (v2config.ServerEntry, error) {
	id := v2ServerEditorState.editingID
	if id == "" {
		var err error
		id, err = coreapp.NewServerID()
		if err != nil {
			return v2config.ServerEntry{}, err
		}
	}
	startup, err := parseV2Int(v2ServerEditorState.startupTimeout.Text(), "startup timeout", false)
	if err != nil { return v2config.ServerEntry{}, err }
	shutdown, err := parseV2Int(v2ServerEditorState.shutdownTimeout.Text(), "shutdown timeout", false)
	if err != nil { return v2config.ServerEntry{}, err }
	idle, err := parseV2Int(v2ServerEditorState.idleTimeout.Text(), "idle timeout", true)
	if err != nil { return v2config.ServerEntry{}, err }
	plain, err := parseV2Environment(v2ServerEditorState.plainEnvironment.Text())
	if err != nil { return v2config.ServerEntry{}, err }
	secretValues, err := parseV2Environment(v2ServerEditorState.secretEnvironment.Text())
	if err != nil { return v2config.ServerEntry{}, err }

	entry := v2config.ServerEntry{
		ID: id,
		Name: strings.TrimSpace(v2ServerEditorState.name.Text()),
		Mode: v2config.ServerMode(v2ServerEditorState.mode),
		Environment: v2config.EnvironmentConfig{Values: plain},
		Runtime: v2config.RuntimeConfig{StartupTimeoutSeconds: *startup, ShutdownTimeoutSeconds: *shutdown, IdleTimeoutSeconds: idle},
	}
	if v2ServerEditorState.logOverride != "inherit" {
		value := v2ServerEditorState.logOverride
		entry.Logging.CaptureLevelOverride = &value
	}

	switch v2config.TransportType(v2ServerEditorState.transport) {
	case v2config.TransportStdio:
		entry.Transport = v2config.TransportConfig{Type: v2config.TransportStdio, Stdio: &v2config.StdioTransport{
			Executable: strings.TrimSpace(v2ServerEditorState.executable.Text()),
			Args: strings.Fields(v2ServerEditorState.args.Text()),
			WorkingDirectory: strings.TrimSpace(v2ServerEditorState.workingDir.Text()),
		}}
	case v2config.TransportManagedHTTP:
		entry.Transport = v2config.TransportConfig{Type: v2config.TransportManagedHTTP, ManagedHTTP: &v2config.ManagedHTTPTransport{
			URL: strings.TrimSpace(v2ServerEditorState.url.Text()),
			Launch: v2config.LaunchConfig{
				Executable: strings.TrimSpace(v2ServerEditorState.launchExecutable.Text()),
				Args: strings.Fields(v2ServerEditorState.launchArgs.Text()),
				WorkingDirectory: strings.TrimSpace(v2ServerEditorState.launchWorkingDir.Text()),
			},
			AllowInsecureCredentialTransport: v2ServerEditorState.allowInsecure.Value,
		}}
		if err := applyV2EditorAuth(ctx, u, &entry); err != nil { return v2config.ServerEntry{}, err }
	case v2config.TransportExternalHTTP:
		entry.Transport = v2config.TransportConfig{Type: v2config.TransportExternalHTTP, ExternalHTTP: &v2config.ExternalHTTPTransport{
			URL: strings.TrimSpace(v2ServerEditorState.url.Text()),
			AllowInsecureCredentialTransport: v2ServerEditorState.allowInsecure.Value,
		}}
		if err := applyV2EditorAuth(ctx, u, &entry); err != nil { return v2config.ServerEntry{}, err }
	default:
		return v2config.ServerEntry{}, errors.New("unsupported transport type")
	}

	for name, value := range secretValues {
		if err := u.core.ConfigureEnvironmentSecret(ctx, &entry, name, []byte(value)); err != nil {
			return v2config.ServerEntry{}, err
		}
	}
	if err := v2config.ValidateServer(entry); err != nil {
		return v2config.ServerEntry{}, err
	}
	return entry, nil
}

func applyV2EditorAuth(ctx context.Context, u *v2DesktopUI, entry *v2config.ServerEntry) error {
	switch v2config.HTTPAuthMode(v2ServerEditorState.auth) {
	case v2config.HTTPAuthNone:
		return coreapp.ConfigureNoHTTPAuth(entry)
	case v2config.HTTPAuthOAuth:
		return coreapp.ConfigureOAuthAuth(entry, strings.Fields(v2ServerEditorState.oauthScopes.Text()))
	case v2config.HTTPAuthStatic:
		credential := []byte(v2ServerEditorState.staticCredential.Text())
		return u.core.ConfigureStaticAuth(ctx, entry, v2ServerEditorState.staticHeader.Text(), v2ServerEditorState.staticScheme.Text(), credential)
	default:
		return errors.New("unsupported HTTP auth mode")
	}
}

func v2ServerEditorPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	initV2ServerEditorWidgets()
	for v2ServerEditorState.modeNext.Clicked(gtx) {
		v2ServerEditorState.mode = cycleV2Value(v2ServerEditorState.mode, []string{string(v2config.ModeAlwaysOn), string(v2config.ModeManaged), string(v2config.ModeManual), string(v2config.ModeDisabled)})
	}
	for v2ServerEditorState.transportNext.Clicked(gtx) {
		v2ServerEditorState.transport = cycleV2Value(v2ServerEditorState.transport, []string{string(v2config.TransportStdio), string(v2config.TransportManagedHTTP), string(v2config.TransportExternalHTTP)})
		if v2ServerEditorState.transport == string(v2config.TransportStdio) {
			v2ServerEditorState.auth = string(v2config.HTTPAuthNone)
		}
	}
	for v2ServerEditorState.authMode.Clicked(gtx) {
		v2ServerEditorState.auth = cycleV2Value(v2ServerEditorState.auth, []string{string(v2config.HTTPAuthNone), string(v2config.HTTPAuthOAuth), string(v2config.HTTPAuthStatic)})
	}
	for v2ServerEditorState.logOverrideNext.Clicked(gtx) {
		v2ServerEditorState.logOverride = cycleV2Value(v2ServerEditorState.logOverride, []string{"inherit", "trace", "debug", "info", "warn", "error"})
	}
	for v2ServerEditorState.cancel.Clicked(gtx) {
		v2ServerEditorState.active = false
		v2ServerEditorState.staticCredential.SetText("")
		u.invalidate()
	}
	for v2ServerEditorState.save.Clicked(gtx) {
		u.async("saving MCP server", func() error {
			entry, err := buildV2ServerEntry(context.Background(), u)
			if err != nil {
				return err
			}
			if err := u.core.SaveServer(context.Background(), entry); err != nil {
				return err
			}
			v2ServerEditorState.active = false
			v2ServerEditorState.staticCredential.SetText("")
			v2ServerEditorState.secretEnvironment.SetText("")
			return nil
		})
	}

	isHTTP := v2ServerEditorState.transport != string(v2config.TransportStdio)
	isManagedHTTP := v2ServerEditorState.transport == string(v2config.TransportManagedHTTP)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, sectionTitle(u.th, map[bool]string{true:"Edit MCP Server", false:"Add MCP Server"}[v2ServerEditorState.editingID != ""])),
				layout.Rigid(secondaryButton(u.th, &v2ServerEditorState.cancel, "Cancel")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
				layout.Rigid(primaryButton(u.th, &v2ServerEditorState.save, "Save Server")),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			var list layout.List
			return list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				return card(func(gtx layout.Context) layout.Dimensions {
					children := []layout.FlexChild{
						layout.Rigid(sectionTitle(u.th, "Identity & lifecycle")),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.name, "Server name")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2ServerEditorState.modeNext, "Mode: "+strings.ToUpper(v2ServerEditorState.mode))) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2ServerEditorState.transportNext, "Transport: "+strings.ToUpper(v2ServerEditorState.transport))) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(14)}.Layout(gtx) }),
						layout.Rigid(sectionTitle(u.th, "Transport")),
					}
					if v2ServerEditorState.transport == string(v2config.TransportStdio) {
						children = append(children,
							layout.Rigid(editorSurface(u.th, &v2ServerEditorState.executable, "Executable")),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.args, "Arguments")) }),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.workingDir, "Working directory (optional)")) }),
						)
					} else {
						children = append(children, layout.Rigid(editorSurface(u.th, &v2ServerEditorState.url, "MCP HTTP URL")))
						if isManagedHTTP {
							children = append(children,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.launchExecutable, "Managed server executable")) }),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.launchArgs, "Managed server arguments")) }),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.launchWorkingDir, "Managed server working directory")) }),
							)
						}
					}
					if isHTTP {
						children = append(children,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(14)}.Layout(gtx) }),
							layout.Rigid(sectionTitle(u.th, "HTTP authentication")),
							layout.Rigid(secondaryButton(u.th, &v2ServerEditorState.authMode, "Auth: "+strings.ToUpper(v2ServerEditorState.auth))),
						)
						if v2ServerEditorState.auth == string(v2config.HTTPAuthOAuth) {
							children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.oauthScopes, "OAuth scopes, separated by spaces")) }))
						}
						if v2ServerEditorState.auth == string(v2config.HTTPAuthStatic) {
							children = append(children,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.staticHeader, "Header name")) }),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.staticScheme, "Scheme, e.g. Bearer (optional)")) }),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.staticCredential, "Credential (leave blank to keep stored value)")) }),
							)
						}
						children = append(children, layout.Rigid(material.CheckBox(u.th, &v2ServerEditorState.allowInsecure, "Allow credential transport over non-HTTPS remote HTTP").Layout))
					}
					children = append(children,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(14)}.Layout(gtx) }),
						layout.Rigid(sectionTitle(u.th, "Environment")),
						layout.Rigid(editorSurface(u.th, &v2ServerEditorState.plainEnvironment, "Plain variables, one NAME=value per line")),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.secretEnvironment, "Secret variables, one NAME=value per line; existing secrets can be NAME=")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(14)}.Layout(gtx) }),
						layout.Rigid(sectionTitle(u.th, "Runtime")),
						layout.Rigid(editorSurface(u.th, &v2ServerEditorState.startupTimeout, "Startup timeout seconds")),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.shutdownTimeout, "Shutdown timeout seconds")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2ServerEditorState.idleTimeout, "Managed idle timeout seconds (blank = Manager default)")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2ServerEditorState.logOverrideNext, "Log capture: "+strings.ToUpper(v2ServerEditorState.logOverride))) }),
					)
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
				})(gtx)
			})
		}),
	)
}
