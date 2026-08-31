//go:build !nogui

package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
)

type v2RouteGraphNode struct {
	key, kind, title, subtitle, status, targetKey string
	x, y, w, h                                    float32
}

type v2RouteGraphEdge struct{ from, to f32.Point }
type v2RouteGraphHit struct {
	key, targetKey string
	rect           image.Rectangle
}

type v2RouteToolState struct {
	agent, preference string
	needsReview       bool
}

var v2RouteWorkspace struct {
	inspector layout.List
	pan       f32.Point
	zoom      float32
	dragging  bool
	pointerID pointer.ID
	last      f32.Point
	press     f32.Point
	moved     bool
	hits      []v2RouteGraphHit
	selected  string
	signature string
	needsFit  bool
	fit       widget.Clickable
	zoomIn    widget.Clickable
	zoomOut   widget.Clickable
	clear     widget.Clickable
	preferAll widget.Clickable
	lowerAll  widget.Clickable
	clearAll  widget.Clickable
	viewNext  widget.Clickable
	view      string
}

func v2RoutingWorkspacePage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	ensureV2RoutingEditor()
	v2ExplorerEnsure()

	// The routing workspace used to rebuild its full snapshot synchronously on
	// every Gio frame. With hundreds or thousands of tools that meant database
	// reads, sorting, capability aggregation, and filtering could all block the
	// window event loop. The UI now renders an immutable prepared snapshot while
	// a background worker refreshes it.
	v2EnsureRoutingSnapshot(u)
	prepared, loaded, loading, loadErr := v2RoutingSnapshotFor(u)
	if !loaded {
		return v2RoutingWorkspaceLoading(u, gtx, loading, loadErr)
	}

	status := prepared.Status
	prefs := prepared.Prefs
	targets := prepared.Targets
	toolBatches := prepared.ToolBatches
	capBatches := prepared.CapBatches
	reviews := prepared.Reviews
	hierarchy := prepared.Hierarchy
	hierarchyUsable := prepared.HierarchyUsable
	serverNames := prepared.ServerNames
	states := prepared.States

	v2WorkspaceIndexActions(u, gtx)
	v2WorkspacePreferenceActions(u, gtx, prefs, targets)

	if !hierarchyUsable && v2RouteExplorer.groupBy == "capabilities" {
		v2RouteExplorer.groupBy = "servers"
		v2RouteExplorer.selectedGroup = "all"
		v2RouteWorkspace.selected = "catalog"
	}
	var groups []v2RoutingExplorerGroup
	if hierarchyUsable && v2RouteExplorer.groupBy == "capabilities" {
		groups = prepared.CapabilityGroups
	} else {
		groups = prepared.ServerGroups
	}
	v2ExplorerNormalizeGroup(groups)
	selectedGroup := v2ExplorerSelectedGroup(groups)
	filtered := v2CachedFilteredTargets(u, prepared, selectedGroup, v2RouteExplorer.search.Text(), v2RouteExplorer.attentionOnly)
	v2ExplorerSelectionFallback(filtered, selectedGroup)
	counts := prepared.Counts

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(v2WorkspaceStatus(u, status, len(targets), len(toolBatches)+len(capBatches), len(reviews), loading, loadErr)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v2ExplorerToolbar(u, gtx, hierarchyUsable, counts) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			inspector := v2WorkspaceInspector(u, prefs, targets, states, hierarchy, hierarchyUsable, status, toolBatches, capBatches, reviews)
			if v2RouteExplorer.graph {
				if gtx.Constraints.Max.X < gtx.Dp(unit.Dp(760)) {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Flexed(.58, func(gtx layout.Context) layout.Dimensions { return v2ExplorerGraphPane(u, gtx, targets, groups, serverNames, states, hierarchy, hierarchyUsable, status) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
						layout.Flexed(.42, inspector),
					)
				}
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(.66, func(gtx layout.Context) layout.Dimensions { return v2ExplorerGraphPane(u, gtx, targets, groups, serverNames, states, hierarchy, hierarchyUsable, status) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(12)}.Layout(gtx) }),
					layout.Flexed(.34, inspector),
				)
			}

			groupPane := v2ExplorerGroupPane(u, groups)
			toolPane := v2ExplorerToolPane(u, filtered, len(targets), serverNames, states, status.Ready)
			switch {
			case gtx.Constraints.Max.X >= gtx.Dp(unit.Dp(920)):
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(.23, groupPane),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
					layout.Flexed(.43, toolPane),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
					layout.Flexed(.34, inspector),
				)
			case gtx.Constraints.Max.X >= gtx.Dp(unit.Dp(700)):
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Flexed(.56, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
							layout.Flexed(.31, groupPane),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
							layout.Flexed(.69, toolPane),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
					layout.Flexed(.44, inspector),
				)
			default:
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Flexed(.25, groupPane),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx) }),
					layout.Flexed(.35, toolPane),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx) }),
					layout.Flexed(.40, inspector),
				)
			}
		}),
	)
}

func v2RoutingWorkspaceLoading(u *v2DesktopUI, gtx layout.Context, loading bool, loadErr string) layout.Dimensions {
	label := "LOADING"
	bg, fg := uiAccentSoft, uiAccent
	detail := "Reading and preparing routing data on a background worker. Window controls and other pages remain responsive."
	if !loading && loadErr != "" {
		label, bg, fg = "LOAD FAILED", uiDangerSoft, uiDanger
		detail = "Routing data could not be loaded. The app will retry without blocking the UI."
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if max := gtx.Dp(unit.Dp(620)); gtx.Constraints.Max.X > max {
			gtx.Constraints.Max.X = max
		}
		return card(func(gtx layout.Context) layout.Dimensions {
			rows := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(sectionTitle(u.th, "Routing workspace")),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Left: unit.Dp(9)}.Layout(gtx, pill(u.th, label, bg, fg)) }),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, detail)) }),
			}
			if loadErr != "" {
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, faintCaption(u.th, loadErr))
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})(gtx)
	})
}

func v2WorkspaceIndexActions(u *v2DesktopUI, gtx layout.Context) {
	for u.indexRefresh.Clicked(gtx) {
		u.runTask(
			"routing-index",
			"Refreshing routing index",
			"Discovering tools, rebuilding routing metadata, and preparing local embeddings in the background.",
			func() error { _, err := u.core.IndexRefresh(context.Background()); return err },
		)
	}
	for u.indexCommit.Clicked(gtx) {
		u.runTask(
			"routing-index",
			"Committing routing index",
			"Promoting the prepared routing generation in the background.",
			func() error { _, err := u.core.IndexCommit(context.Background()); return err },
		)
	}
	for v2RouteWorkspace.fit.Clicked(gtx) {
		v2RouteWorkspace.needsFit = true
	}
	for v2RouteWorkspace.zoomIn.Clicked(gtx) {
		v2RouteWorkspace.zoom = min(float32(2.2), max(float32(.3), v2RouteWorkspace.zoom)*1.18)
	}
	for v2RouteWorkspace.zoomOut.Clicked(gtx) {
		v2RouteWorkspace.zoom = max(float32(.3), max(float32(.3), v2RouteWorkspace.zoom)/1.18)
	}
}

func v2WorkspacePreferenceActions(u *v2DesktopUI, gtx layout.Context, prefs coreapp.V2PreferenceSnapshot, targets []coreapp.V2RoutingTarget) {
	if v2RoutingEditorState.ruleProfileIndex > len(prefs.Profiles) {
		v2RoutingEditorState.ruleProfileIndex = 0
	}
	for v2RoutingEditorState.addProfile.Clicked(gtx) {
		name, desc, expected := strings.TrimSpace(v2RoutingEditorState.profileName.Text()), strings.TrimSpace(v2RoutingEditorState.profileDescription.Text()), prefs.PreferenceRevision
		u.runTask("routing-preferences", "Creating routing profile", "Saving the routing profile in the background.", func() error {
			_, err := u.core.PutRoutingProfile(context.Background(), expected, routingprefs.Profile{Name: name, Description: desc})
			return err
		})
	}
	for v2RoutingEditorState.clearDefault.Clicked(gtx) {
		u.runTask("routing-preferences", "Clearing default routing profile", "Updating routing configuration in the background.", func() error {
			cfg := u.core.ManagerConfig()
			cfg.Routing.DefaultProfile = ""
			return u.core.SaveManager(context.Background(), cfg)
		})
	}
	for v2RoutingEditorState.ruleProfileNext.Clicked(gtx) {
		v2RoutingEditorState.ruleProfileIndex = (v2RoutingEditorState.ruleProfileIndex + 1) % (len(prefs.Profiles) + 1)
	}
	for v2RoutingEditorState.specificityNext.Clicked(gtx) {
		switch v2RoutingEditorState.specificity {
		case routingprefs.SpecificityServer:
			v2RoutingEditorState.specificity = routingprefs.SpecificityToolSet
		case routingprefs.SpecificityToolSet:
			v2RoutingEditorState.specificity = routingprefs.SpecificityConditionalTool
		default:
			v2RoutingEditorState.specificity = routingprefs.SpecificityServer
		}
	}
	for v2RouteWorkspace.clear.Clicked(gtx) {
		for _, c := range v2RoutingEditorState.targetChoices {
			c.preferred.Value, c.deprioritized.Value = false, false
		}
	}
	for v2RoutingEditorState.addRule.Clicked(gtx) {
		spec := routingprefs.RuleSpec{ProfileID: v2RoutingProfileID(prefs.Profiles), Specificity: v2RoutingEditorState.specificity, SubjectKey: strings.TrimSpace(v2RoutingEditorState.subject.Text()), Condition: strings.TrimSpace(v2RoutingEditorState.condition.Text())}
		for _, target := range targets {
			if target.AssumptionFingerprint == "" {
				continue
			}
			choice := v2RoutingEditorState.targetChoices[v2RoutingTargetKey(target)]
			if choice == nil {
				continue
			}
			value := routingprefs.Target{ServerID: target.ServerID, ToolName: target.ToolName, AssumptionFingerprint: target.AssumptionFingerprint}
			if choice.preferred.Value {
				spec.Preferred = append(spec.Preferred, value)
			} else if choice.deprioritized.Value {
				spec.Deprioritized = append(spec.Deprioritized, value)
			}
		}
		expected := prefs.PreferenceRevision
		u.runTask("routing-preferences", "Saving routing preference", "Writing the routing preference in the background.", func() error {
			_, err := u.core.PutRoutingRule(context.Background(), expected, spec)
			return err
		})
	}
}

func v2WorkspaceStatus(u *v2DesktopUI, status indexing.Status, tools, agentTasks, reviews int, loading bool, loadErr string) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		label, bg, fg, instruction := v2WorkspaceOverall(status, agentTasks, reviews)
		meta := fmt.Sprintf("%d live tools · %d agent task(s) · %d optional review(s)", tools, agentTasks, reviews)
		if loading {
			meta += " · view updating in background"
		}
		if loadErr != "" {
			meta += " · last background refresh failed"
		}
		refreshLabel, commitLabel := "Refresh", "Commit"
		if u.taskActive("routing-index") {
			refreshLabel, commitLabel = "Index task running…", "Index task running…"
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(sectionTitle(u.th, "Routing")), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(9)}.Layout(gtx, pill(u.th, label, bg, fg))
						}))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(5)}.Layout(gtx, mutedCaption(u.th, instruction))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, faintCaption(u.th, meta))
					}),
				)
			}),
			layout.Rigid(primaryButton(u.th, &u.indexRefresh, refreshLabel)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
			layout.Rigid(secondaryButton(u.th, &u.indexCommit, commitLabel)),
		)
	})
}

func v2WorkspaceOverall(status indexing.Status, agentTasks, reviews int) (string, color.NRGBA, color.NRGBA, string) {
	switch {
	case status.ActiveGenerationID == "" && status.StagingGenerationID == "":
		return "NOT BUILT", uiWarningSoft, uiWarning, "You: refresh the index to create the routing catalog."
	case status.PendingRequired > 0 || agentTasks > 0:
		return "AGENT WORK", uiWarningSoft, uiWarning, "Agent: finish the highlighted enrichment work. Required agent work blocks promotion."
	case status.StagingGenerationID != "":
		return "READY TO COMMIT", uiSuccessSoft, uiSuccess, "You: required indexing work is complete; commit the staging index."
	case status.Ready && reviews > 0:
		return "READY", uiSuccessSoft, uiSuccess, "Good to go. Human ambiguity reviews are optional and do not block routing."
	case status.Ready:
		return "READY", uiSuccessSoft, uiSuccess, "Good to go. The active routing index is current."
	case status.ActiveGenerationID != "":
		return "REFRESH NEEDED", uiWarningSoft, uiWarning, "You: the live routing state changed. Refresh the index to incorporate the current tool contract."
	default:
		return "CHECK INDEX", uiWarningSoft, uiWarning, "The routing catalog needs attention."
	}
}
