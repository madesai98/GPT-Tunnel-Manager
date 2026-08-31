//go:build !nogui

package main

import (
	"fmt"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
)

func v2WorkspaceInspector(u *v2DesktopUI, prefs coreapp.V2PreferenceSnapshot, targets []coreapp.V2RoutingTarget, states map[string]v2RouteToolState, hierarchy enrichment.CapabilityHierarchy, hierarchyFound bool, status indexing.Status, toolBatches, capBatches, reviews []catalog.EnrichmentBatch) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		v2RouteWorkspace.inspector.Axis = layout.Vertical
		return v2RouteWorkspace.inspector.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(v2WorkspaceSelection(u, targets, states, hierarchy, hierarchyFound, status)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
				layout.Rigid(v2WorkspacePreferenceBuilder(u, prefs, targets)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
				layout.Rigid(v2WorkspaceAgentQueue(u, toolBatches, capBatches)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
				layout.Rigid(v2WorkspaceReviews(u, prefs, reviews)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
				layout.Rigid(v2RoutingProfilesCard(u, prefs, u.core.ManagerConfig().Routing.DefaultProfile)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
				layout.Rigid(v2RoutingRulesCard(u, prefs)),
			)
		})
	}
}

func v2WorkspaceSelection(u *v2DesktopUI, targets []coreapp.V2RoutingTarget, states map[string]v2RouteToolState, hierarchy enrichment.CapabilityHierarchy, hierarchyFound bool, status indexing.Status) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		selected := v2RouteWorkspace.selected
		if strings.HasPrefix(selected, "tool:") {
			key := strings.TrimPrefix(selected, "tool:")
			for _, target := range targets {
				if v2RoutingTargetKey(target) == key {
					s := states[key]
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(sectionTitle(u.th, target.ToolName)), layout.Rigid(mutedCaption(u.th, target.ServerID)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, pill(u.th, v2WorkspaceToolLabel(s, status.Ready), v2WorkspaceStateBG(s), v2WorkspaceStateFG(s)))
					}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, faintCaption(u.th, "Routing preferences are an overlay; they do not modify the tool contract or semantic index."))
					}))
				}
			}
		}
		if strings.HasPrefix(selected, "cap:") && hierarchyFound {
			id := strings.TrimPrefix(selected, "cap:")
			for _, capability := range hierarchy.Capabilities {
				if capability.ID != id {
					continue
				}
				description := capability.Description
				if description == "" {
					description = "Semantic capability group produced by capability reconciliation."
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(sectionTitle(u.th, capability.Name)),
					layout.Rigid(mutedCaption(u.th, fmt.Sprintf("Capability group · %d direct tool member(s)", len(capability.ToolMembers)))),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, faintCaption(u.th, description))
					}),
				)
			}
		}
		if strings.HasPrefix(selected, "server:") {
			id := strings.TrimPrefix(selected, "server:")
			keys := []string{}
			for _, target := range targets {
				if target.ServerID == id {
					keys = append(keys, v2RoutingTargetKey(target))
				}
			}
			v2WorkspaceGroupActions(gtx, keys)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(sectionTitle(u.th, id)), layout.Rigid(mutedCaption(u.th, fmt.Sprintf("Server group · %d tools", len(keys)))), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(9)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx, layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.preferAll, "Prefer all")), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(5)}.Layout(gtx) }), layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.lowerAll, "Lower all")), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(5)}.Layout(gtx) }), layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.clearAll, "Clear")))
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(sectionTitle(u.th, "Routing catalog")), layout.Rigid(mutedCaption(u.th, fmt.Sprintf("%d authoritative tools", len(targets)))), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, faintCaption(u.th, "The graph mirrors the actual source hierarchy: catalog → server → authoritative tool. Click any node to inspect it."))
		}))
	})
}

func v2WorkspaceStateBG(s v2RouteToolState) color.NRGBA {
	if s.agent != "" {
		return uiWarningSoft
	}
	if s.needsReview {
		return uiDangerSoft
	}
	if s.preference != "" {
		return uiAccentSoft
	}
	return uiSuccessSoft
}
func v2WorkspaceStateFG(s v2RouteToolState) color.NRGBA {
	if s.agent != "" {
		return uiWarning
	}
	if s.needsReview {
		return uiDanger
	}
	if s.preference != "" {
		return uiAccent
	}
	return uiSuccess
}

func v2WorkspaceGroupActions(gtx layout.Context, keys []string) {
	for v2RouteWorkspace.preferAll.Clicked(gtx) {
		for _, key := range keys {
			c := v2WorkspaceChoice(key)
			c.preferred.Value, c.deprioritized.Value = true, false
		}
	}
	for v2RouteWorkspace.lowerAll.Clicked(gtx) {
		for _, key := range keys {
			c := v2WorkspaceChoice(key)
			c.preferred.Value, c.deprioritized.Value = false, true
		}
	}
	for v2RouteWorkspace.clearAll.Clicked(gtx) {
		for _, key := range keys {
			c := v2WorkspaceChoice(key)
			c.preferred.Value, c.deprioritized.Value = false, false
		}
	}
}

func v2WorkspaceChoice(key string) *v2RoutingTargetChoice {
	c := v2RoutingEditorState.targetChoices[key]
	if c == nil {
		c = &v2RoutingTargetChoice{}
		v2RoutingEditorState.targetChoices[key] = c
	}
	return c
}

func v2WorkspacePreferenceBuilder(u *v2DesktopUI, prefs coreapp.V2PreferenceSnapshot, targets []coreapp.V2RoutingTarget) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		preferred, lower := 0, 0
		for _, target := range targets {
			c := v2WorkspaceChoice(v2RoutingTargetKey(target))
			if c.preferred.Value {
				preferred++
			}
			if c.deprioritized.Value {
				lower++
			}
		}
		rows := []layout.FlexChild{
			layout.Rigid(sectionTitle(u.th, "Preference draft")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2RoutingEditorState.ruleProfileNext, "Profile: "+v2RoutingProfileLabel(prefs.Profiles)))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2RoutingEditorState.specificityNext, "Scope: "+strings.ToUpper(string(v2RoutingEditorState.specificity))))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2RoutingEditorState.subject, "Subject key"))
			}),
		}
		if v2RoutingEditorState.specificity == routingprefs.SpecificityConditionalTool {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2RoutingEditorState.condition, "Condition"))
			}))
		}
		key := strings.TrimPrefix(v2RouteWorkspace.selected, "tool:")
		for _, target := range targets {
			if v2RoutingTargetKey(target) == key {
				c := v2WorkspaceChoice(key)
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, mutedCaption(u.th, "Selected tool: "+target.ToolName))
				}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx, layout.Rigid(material.CheckBox(u.th, &c.preferred, "Preferred").Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }), layout.Rigid(material.CheckBox(u.th, &c.deprioritized, "Lower priority").Layout))
				}))
				break
			}
		}
		rows = append(rows,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, faintCaption(u.th, fmt.Sprintf("Draft: %d preferred · %d lower priority", preferred, lower)))
			}),
		)
		if preferred == 0 {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, faintCaption(u.th, "Select at least one preferred tool before saving."))
			}))
		}
		rows = append(rows,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx, layout.Rigid(primaryButton(u.th, &v2RoutingEditorState.addRule, "Save Preference")), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }), layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.clear, "Clear draft")))
				})
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}
