//go:build !nogui

package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
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
	ctx := context.Background()
	status, err := u.core.IndexStatus(ctx)
	if err != nil {
		return card(mutedCaption(u.th, err.Error()))(gtx)
	}
	prefs, err := u.core.RoutingPreferences(ctx)
	if err != nil {
		return card(mutedCaption(u.th, err.Error()))(gtx)
	}
	targets, err := u.core.RoutingTargets(ctx)
	if err != nil {
		return card(mutedCaption(u.th, err.Error()))(gtx)
	}
	sort.Slice(prefs.Profiles, func(i, j int) bool { return prefs.Profiles[i].Name < prefs.Profiles[j].Name })
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ServerID == targets[j].ServerID {
			return targets[i].ToolName < targets[j].ToolName
		}
		return targets[i].ServerID < targets[j].ServerID
	})
	toolBatches, _ := u.core.PendingEnrichment(ctx, catalog.BatchToolEnrichment, 100)
	capBatches, _ := u.core.PendingEnrichment(ctx, catalog.BatchCapabilityReconciliation, 100)
	reviews, _ := u.core.PendingEnrichment(ctx, catalog.BatchAmbiguityReview, 100)
	hierarchy, hierarchyFound, _ := u.core.RoutingCapabilityHierarchy(ctx)
	v2WorkspaceIndexActions(u, gtx, hierarchyFound)
	v2WorkspacePreferenceActions(u, gtx, prefs, targets)

	serverNames := map[string]string{}
	for _, entry := range u.core.Entries() {
		serverNames[entry.ID] = entry.Name
	}
	states := v2WorkspaceStates(prefs.Rules, toolBatches, capBatches)
	if v2RouteWorkspace.view == "" {
		if hierarchyFound {
			v2RouteWorkspace.view = "capabilities"
		} else {
			v2RouteWorkspace.view = "sources"
		}
	}
	if !hierarchyFound && v2RouteWorkspace.view == "capabilities" {
		v2RouteWorkspace.view = "sources"
	}
	var nodes []v2RouteGraphNode
	var edges []v2RouteGraphEdge
	var bounds image.Rectangle
	if hierarchyFound && v2RouteWorkspace.view == "capabilities" {
		nodes, edges, bounds = v2WorkspaceCapabilityGraph(targets, serverNames, states, hierarchy, status)
	} else {
		nodes, edges, bounds = v2WorkspaceServerGraph(targets, serverNames, states, status)
	}
	sig := fmt.Sprintf("%s|%s|%d|%d", status.ActiveGenerationID, status.StagingGenerationID, len(targets), status.PendingRequired)
	if sig != v2RouteWorkspace.signature {
		v2RouteWorkspace.signature, v2RouteWorkspace.needsFit = sig, true
	}
	v2WorkspaceSelectDefault(nodes, states)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(v2WorkspaceStatus(u, status, len(targets), len(toolBatches)+len(capBatches), len(reviews))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if gtx.Constraints.Max.X < gtx.Dp(unit.Dp(760)) {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Flexed(.56, v2WorkspaceGraph(u, nodes, edges, bounds, hierarchyFound)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(10)}.Layout(gtx) }),
					layout.Flexed(.44, v2WorkspaceInspector(u, prefs, targets, states, hierarchy, hierarchyFound, toolBatches, capBatches, reviews)),
				)
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(.62, v2WorkspaceGraph(u, nodes, edges, bounds, hierarchyFound)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(12)}.Layout(gtx) }),
				layout.Flexed(.38, v2WorkspaceInspector(u, prefs, targets, states, hierarchy, hierarchyFound, toolBatches, capBatches, reviews)),
			)
		}),
	)
}

func v2WorkspaceIndexActions(u *v2DesktopUI, gtx layout.Context, hierarchyFound bool) {
	for u.indexRefresh.Clicked(gtx) {
		u.async("refreshing index", func() error { _, err := u.core.IndexRefresh(context.Background()); return err })
	}
	for u.indexCommit.Clicked(gtx) {
		u.async("committing index", func() error { _, err := u.core.IndexCommit(context.Background()); return err })
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
	for v2RouteWorkspace.viewNext.Clicked(gtx) {
		if hierarchyFound && v2RouteWorkspace.view == "sources" {
			v2RouteWorkspace.view = "capabilities"
		} else {
			v2RouteWorkspace.view = "sources"
		}
		v2RouteWorkspace.needsFit = true
	}
}

func v2WorkspacePreferenceActions(u *v2DesktopUI, gtx layout.Context, prefs coreapp.V2PreferenceSnapshot, targets []coreapp.V2RoutingTarget) {
	if v2RoutingEditorState.ruleProfileIndex > len(prefs.Profiles) {
		v2RoutingEditorState.ruleProfileIndex = 0
	}
	for v2RoutingEditorState.addProfile.Clicked(gtx) {
		name, desc, expected := strings.TrimSpace(v2RoutingEditorState.profileName.Text()), strings.TrimSpace(v2RoutingEditorState.profileDescription.Text()), prefs.PreferenceRevision
		u.async("creating routing profile", func() error {
			_, err := u.core.PutRoutingProfile(context.Background(), expected, routingprefs.Profile{Name: name, Description: desc})
			return err
		})
	}
	for v2RoutingEditorState.clearDefault.Clicked(gtx) {
		u.async("clearing default routing profile", func() error {
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
		u.async("saving routing preference", func() error { _, err := u.core.PutRoutingRule(context.Background(), expected, spec); return err })
	}
}

func v2WorkspaceStatus(u *v2DesktopUI, status indexing.Status, tools, agentTasks, reviews int) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		label, bg, fg, instruction := v2WorkspaceOverall(status, agentTasks, reviews)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(sectionTitle(u.th, "Index & Routing")), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(9)}.Layout(gtx, pill(u.th, label, bg, fg))
						}))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(5)}.Layout(gtx, mutedCaption(u.th, instruction))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, faintCaption(u.th, fmt.Sprintf("%d tools · %d agent task(s) · %d optional review(s)", tools, agentTasks, reviews)))
					}),
				)
			}),
			layout.Rigid(primaryButton(u.th, &u.indexRefresh, "Refresh")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
			layout.Rigid(secondaryButton(u.th, &u.indexCommit, "Commit")),
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
	default:
		return "CHECK INDEX", uiWarningSoft, uiWarning, "The routing catalog needs attention."
	}
}
