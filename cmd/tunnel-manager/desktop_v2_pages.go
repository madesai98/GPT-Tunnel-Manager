//go:build !nogui

package main

import (
	"context"
	"fmt"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func v2ServersPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	if v2ToolVisibilityEditorActive() {
		return v2ToolVisibilityEditorPage(u, gtx)
	}
	if v2ServerEditorActive() {
		return v2ServerEditorPage(u, gtx)
	}
	entries := u.core.Entries()
	manager := u.core.ManagerConfig()
	toolCounts := u.core.KnownServerToolCounts(context.Background())
	snapshots := make(map[string]routedlifecycle.Snapshot)
	for _, snapshot := range u.core.Snapshots() {
		snapshots[snapshot.ServerID] = snapshot
	}

	body := func(gtx layout.Context) layout.Dimensions {
		if len(entries) == 0 {
			return card(func(gtx layout.Context) layout.Dimensions {
				return mutedCaption(u.th, "No downstream MCP servers are configured yet.")(gtx)
			})(gtx)
		}
		u.list.List.Axis = layout.Vertical
		u.list.List.Position.Count = len(entries)
		return material.List(u.th, &u.list).Layout(gtx, len(entries), func(gtx layout.Context, index int) layout.Dimensions {
			entry := entries[index]
			snapshot := snapshots[entry.ID]
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
					connected := status.Connected
					u.async("connecting OAuth for "+entry.Name, func() error {
						if connected {
							_, err := u.core.ReconnectOAuth(context.Background(), id)
							return err
						}
						_, err := u.core.ConnectOAuth(context.Background(), id)
						return err
					})
				}
			}
			for actions.edit.Clicked(gtx) {
				openExistingV2ServerEditor(entry)
				u.invalidate()
			}
			for actions.tools.Clicked(gtx) {
				names, err := u.core.KnownServerToolNames(context.Background(), entry.ID)
				if err != nil {
					u.setMessage("loading tools for " + entry.Name + ": " + err.Error())
				} else {
					openV2ToolVisibilityEditor(entry, names)
					u.invalidate()
				}
			}
			for actions.remove.Clicked(gtx) {
				id := entry.ID
				u.async("removing "+entry.Name, func() error { return u.core.DeleteServer(context.Background(), id) })
			}
			for actions.toggle.Clicked(gtx) {
				updated := entry
				updated.Mode = v2ToggledServerMode(entry.Mode)
				action := "enabling " + entry.Name
				if updated.Mode == v2config.ModeDisabled {
					action = "disabling " + entry.Name
				}
				u.async(action, func() error { return u.core.SaveServer(context.Background(), updated) })
			}

			enabled := entry.Mode != v2config.ModeDisabled
			state := "STOPPED"
			stateBG, stateFG := uiSurfaceRaised, uiMuted
			if snapshot.Running {
				state, stateBG, stateFG = "RUNNING", uiSuccessSoft, uiSuccess
			}
			if !enabled {
				state, stateBG, stateFG = "DISABLED", uiWarningSoft, uiWarning
			}

			modeText := strings.ToUpper(string(entry.Mode))
			modeBG, modeFG := uiAccentSoft, uiAccent
			switch entry.Mode {
			case v2config.ModeAlwaysOn:
				modeText, modeBG, modeFG = "ALWAYS ON", uiSuccessSoft, uiSuccess
			case v2config.ModeManual:
				modeText, modeBG, modeFG = "MANUAL", uiSurfaceRaised, uiMuted
			case v2config.ModeDisabled:
				modeText, modeBG, modeFG = "DISABLED", uiWarningSoft, uiWarning
			}

			showStart := enabled && !snapshot.Running
			showStop := enabled && snapshot.Running && entry.Mode != v2config.ModeAlwaysOn
			showRestart := enabled && snapshot.Running
			oauth := u.core.OAuthStatus(context.Background(), entry.ID)
			idleTimeout := manager.ManagedDefaults.IdleTimeoutSeconds
			if entry.Runtime.IdleTimeoutSeconds != nil {
				idleTimeout = *entry.Runtime.IdleTimeoutSeconds
			}
			showIdle := enabled && entry.Mode == v2config.ModeManaged && snapshot.Running && snapshot.ActiveCallCount == 0

			return layout.Inset{Bottom: unit.Dp(10)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								children := []layout.FlexChild{
									layout.Rigid(sectionTitle(u.th, entry.Name)),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(12)}.Layout(gtx) }),
								}
								if enabled {
									children = append(children, layout.Rigid(pill(u.th, modeText, modeBG, modeFG)))
								}
								children = append(children,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
									layout.Rigid(pill(u.th, state, stateBG, stateFG)),
								)
								return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(3)}.Layout(gtx, faintCaption(u.th, fmt.Sprintf("%s · %d tools · %s", entry.Transport.Type, toolCounts[entry.ID], entry.ID)))
							}),
						)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, v2IdleCountdown(u.th, snapshot.IdleDeadlineAt, idleTimeout, showIdle))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						children := make([]layout.FlexChild, 0, 14)
						if showStart {
							children = append(children, layout.Rigid(v2ServerIconButton(u.th, &actions.start, "▶")))
						}
						if showStop {
							children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, v2ServerIconButton(u.th, &actions.stop, "■"))
							}))
						}
						if showRestart {
							children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, v2ServerIconButton(u.th, &actions.restart, "↻"))
							}))
						}
						if oauth.Configured {
							glyph := "○"
							if oauth.Connected {
								glyph = "●"
							}
							children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, v2ServerIconButton(u.th, &actions.oauth, glyph))
							}))
						}
						children = append(children,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, v2ServerIconButton(u.th, &actions.tools, "≡"))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, v2ServerIconButton(u.th, &actions.edit, "✎"))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Left: unit.Dp(4)}.Layout(gtx, v2DangerIconButton(u.th, &actions.remove, "×"))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
							layout.Rigid(v2ServerToggle(&actions.toggle, enabled)),
						)
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
					}),
				)
			}))
		})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2ServerListHeader(u, gtx) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
		layout.Flexed(1, body),
	)
}

func v2IndexPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	return v2RoutingWorkspacePage(u, gtx)
}

func v2RoutingPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	return v2RoutingWorkspacePage(u, gtx)
}
