//go:build !nogui

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func v2ServersPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	if v2ServerEditorActive() {
		return v2ServerEditorPage(u, gtx)
	}
	entries := u.core.Entries()
	snapshots := make(map[string]bool)
	active := make(map[string]int)
	for _, snapshot := range u.core.Snapshots() {
		snapshots[snapshot.ServerID] = snapshot.Running
		active[snapshot.ServerID] = snapshot.ActiveCallCount
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
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%s · active leases %d · %s", entry.Transport.Type, active[entry.ID], entry.ID)))
					}),
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
								children = append(children,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
									layout.Rigid(secondaryButton(u.th, &actions.oauth, label)),
								)
							}
							children = append(children,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2ServerEditButton(u, gtx, entry) }),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
								layout.Rigid(dangerButton(u.th, &actions.remove, "Remove")),
							)
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
						})
					}),
				)
			})(gtx))
		})
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2ServerListHeader(u, gtx) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
		layout.Flexed(1, body),
	)
}

func v2IndexPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	for u.indexRefresh.Clicked(gtx) {
		u.async("refreshing index", func() error { _, err := u.core.IndexRefresh(context.Background()); return err })
	}
	for u.indexCommit.Clicked(gtx) {
		u.async("committing index", func() error { _, err := u.core.IndexCommit(context.Background()); return err })
	}
	status, err := u.core.IndexStatus(context.Background())
	if err != nil {
		return card(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, err.Error())(gtx) })(gtx)
	}
	return card(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(sectionTitle(u.th, "Routing catalog")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("Active: %s", status.ActiveGenerationID))) }),
			layout.Rigid(mutedCaption(u.th, fmt.Sprintf("Staging: %s", status.StagingGenerationID))),
			layout.Rigid(mutedCaption(u.th, fmt.Sprintf("Ready: %t · pending required: %d · open reviews: %d", status.Ready, status.PendingRequired, status.OpenReviews))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(14)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Rigid(primaryButton(u.th, &u.indexRefresh, "Refresh Index")),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
						layout.Rigid(secondaryButton(u.th, &u.indexCommit, "Commit Ready Index")),
					)
				})
			}),
		)
	})(gtx)
}

func v2RoutingPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	prefs, err := u.core.RoutingPreferences(context.Background())
	if err != nil {
		return card(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, err.Error())(gtx) })(gtx)
	}
	sort.Slice(prefs.Profiles, func(i, j int) bool { return prefs.Profiles[i].ID < prefs.Profiles[j].ID })
	return card(func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(sectionTitle(u.th, fmt.Sprintf("Preference revision %d", prefs.PreferenceRevision))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%d profiles · %d rules", len(prefs.Profiles), len(prefs.Rules))))
			}),
		}
		for _, profile := range prefs.Profiles {
			p := profile
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return mutedCaption(u.th, fmt.Sprintf("Profile %s — %s", p.ID, p.Name))(gtx)
			}))
		}
		for _, rule := range prefs.Rules {
			r := rule
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return mutedCaption(u.th, fmt.Sprintf("%s · %s · %s", r.ID, r.Spec.Specificity, r.ReviewState))(gtx)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})(gtx)
}
