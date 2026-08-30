//go:build !nogui

package main

import (
	"context"
	"fmt"
	"sort"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

var v2ToolVisibilityEditorState struct {
	active     bool
	serverID   string
	serverName string
	tools      []string
	hidden     map[string]bool
	toggles    map[string]*widget.Clickable
	save       widget.Clickable
	cancel     widget.Clickable
	list       widget.List
}

func initV2ToolVisibilityEditor() {
	if v2ToolVisibilityEditorState.hidden == nil {
		v2ToolVisibilityEditorState.hidden = make(map[string]bool)
	}
	if v2ToolVisibilityEditorState.toggles == nil {
		v2ToolVisibilityEditorState.toggles = make(map[string]*widget.Clickable)
	}
	v2ToolVisibilityEditorState.list.List.Axis = layout.Vertical
}

func v2ToolVisibilityEditorActive() bool {
	initV2ToolVisibilityEditor()
	return v2ToolVisibilityEditorState.active
}

func openV2ToolVisibilityEditor(entry v2config.ServerEntry, names []string) {
	initV2ToolVisibilityEditor()
	set := make(map[string]struct{}, len(names)+len(entry.ToolVisibility.Hidden))
	for _, name := range names {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	for _, name := range entry.ToolVisibility.Hidden {
		set[name] = struct{}{}
	}
	tools := make([]string, 0, len(set))
	for name := range set {
		tools = append(tools, name)
	}
	sort.Strings(tools)

	v2ToolVisibilityEditorState.active = true
	v2ToolVisibilityEditorState.serverID = entry.ID
	v2ToolVisibilityEditorState.serverName = entry.Name
	v2ToolVisibilityEditorState.tools = tools
	v2ToolVisibilityEditorState.hidden = make(map[string]bool, len(entry.ToolVisibility.Hidden))
	for _, name := range entry.ToolVisibility.Hidden {
		v2ToolVisibilityEditorState.hidden[name] = true
	}
	v2ToolVisibilityEditorState.toggles = make(map[string]*widget.Clickable, len(tools))
	v2ToolVisibilityEditorState.list.Position = layout.Position{}
}

func closeV2ToolVisibilityEditor() {
	v2ToolVisibilityEditorState.active = false
	v2ToolVisibilityEditorState.serverID = ""
	v2ToolVisibilityEditorState.serverName = ""
	v2ToolVisibilityEditorState.tools = nil
}

func v2ToolVisibilityEditorPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	initV2ToolVisibilityEditor()
	for v2ToolVisibilityEditorState.cancel.Clicked(gtx) {
		closeV2ToolVisibilityEditor()
		u.invalidate()
	}
	for v2ToolVisibilityEditorState.save.Clicked(gtx) {
		var entry v2config.ServerEntry
		found := false
		for _, candidate := range u.core.Entries() {
			if candidate.ID == v2ToolVisibilityEditorState.serverID {
				entry = candidate
				found = true
				break
			}
		}
		if !found {
			u.setMessage("server no longer exists")
			closeV2ToolVisibilityEditor()
			break
		}
		hidden := make([]string, 0)
		for _, name := range v2ToolVisibilityEditorState.tools {
			if v2ToolVisibilityEditorState.hidden[name] {
				hidden = append(hidden, name)
			}
		}
		sort.Strings(hidden)
		entry.ToolVisibility.Hidden = hidden
		label := "saving tool visibility for " + entry.Name
		closeV2ToolVisibilityEditor()
		u.async(label, func() error {
			return u.core.SaveServer(context.Background(), entry)
		})
	}

	exposed := 0
	for _, name := range v2ToolVisibilityEditorState.tools {
		if !v2ToolVisibilityEditorState.hidden[name] {
			exposed++
		}
	}
	hiddenCount := len(v2ToolVisibilityEditorState.tools) - exposed

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(sectionTitle(u.th, "Tool Exposure · "+v2ToolVisibilityEditorState.serverName)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%d tools found · %d exposed · %d hidden", len(v2ToolVisibilityEditorState.tools), exposed, hiddenCount)))
						}),
					)
				}),
				layout.Rigid(secondaryButton(u.th, &v2ToolVisibilityEditorState.cancel, "Cancel")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
				layout.Rigid(primaryButton(u.th, &v2ToolVisibilityEditorState.save, "Save")),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(v2ToolVisibilityEditorState.tools) == 0 {
				return compactCard(func(gtx layout.Context) layout.Dimensions {
					return mutedCaption(u.th, "No tools have been discovered for this server yet. Start it once or refresh the index, then reopen this editor.")(gtx)
				})(gtx)
			}
			return material.List(u.th, &v2ToolVisibilityEditorState.list).Layout(gtx, len(v2ToolVisibilityEditorState.tools), func(gtx layout.Context, index int) layout.Dimensions {
				name := v2ToolVisibilityEditorState.tools[index]
				button := v2ToolVisibilityEditorState.toggles[name]
				if button == nil {
					button = new(widget.Clickable)
					v2ToolVisibilityEditorState.toggles[name] = button
				}
				for button.Clicked(gtx) {
					v2ToolVisibilityEditorState.hidden[name] = !v2ToolVisibilityEditorState.hidden[name]
				}
				exposed := !v2ToolVisibilityEditorState.hidden[name]
				state, bg, fg := "EXPOSED", uiSuccessSoft, uiSuccess
				if !exposed {
					state, bg, fg = "HIDDEN", uiSurfaceRaised, uiMuted
				}
				return layout.Inset{Bottom: unit.Dp(7)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							label := material.Body1(u.th, name)
							label.Color = uiText
							label.TextSize = unit.Sp(13)
							return label.Layout(gtx)
						}),
						layout.Rigid(pill(u.th, state, bg, fg)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
						layout.Rigid(v2ServerToggle(button, exposed)),
					)
				}))
			})
		}),
	)
}
