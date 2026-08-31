//go:build !nogui

package main

import (
	"fmt"
	"image"
	"math"
	"sort"
	"strings"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
)

type v2RoutingExplorerGroup struct {
	Key         string
	Title       string
	Description string
	Depth       int
	Members     map[string]bool
}

type v2RoutingExplorerCounts struct {
	New, Agent, Review, Ready int
}

var v2RouteExplorer struct {
	initialized     bool
	search          widget.Editor
	groupNext       widget.Clickable
	graphToggle     widget.Clickable
	attentionToggle widget.Clickable
	groupBy         string
	graph           bool
	attentionOnly   bool
	selectedGroup   string
	groups          layout.List
	tools           layout.List
	groupClicks     map[string]*widget.Clickable
	toolClicks      map[string]*widget.Clickable
}

func v2ExplorerEnsure() {
	if !v2RouteExplorer.initialized {
		v2RouteExplorer.initialized = true
		v2RouteExplorer.search.SingleLine = true
		v2RouteExplorer.groupBy = "capabilities"
		v2RouteExplorer.selectedGroup = "all"
		v2RouteExplorer.groups.Axis = layout.Vertical
		v2RouteExplorer.tools.Axis = layout.Vertical
	}
	if v2RouteExplorer.groupClicks == nil {
		v2RouteExplorer.groupClicks = map[string]*widget.Clickable{}
	}
	if v2RouteExplorer.toolClicks == nil {
		v2RouteExplorer.toolClicks = map[string]*widget.Clickable{}
	}
}

func v2ExplorerGroupClick(key string) *widget.Clickable {
	if click := v2RouteExplorer.groupClicks[key]; click != nil {
		return click
	}
	click := new(widget.Clickable)
	v2RouteExplorer.groupClicks[key] = click
	return click
}

func v2ExplorerToolClick(key string) *widget.Clickable {
	if click := v2RouteExplorer.toolClicks[key]; click != nil {
		return click
	}
	click := new(widget.Clickable)
	v2RouteExplorer.toolClicks[key] = click
	return click
}

func v2ExplorerCounts(targets []coreapp.V2RoutingTarget, states map[string]v2RouteToolState) v2RoutingExplorerCounts {
	counts := v2RoutingExplorerCounts{}
	for _, target := range targets {
		state := states[v2RoutingTargetKey(target)]
		switch {
		case target.AssumptionFingerprint == "":
			counts.New++
		case state.agent != "":
			counts.Agent++
		case state.needsReview:
			counts.Review++
		default:
			counts.Ready++
		}
	}
	return counts
}

func v2ExplorerNeedsAttention(target coreapp.V2RoutingTarget, state v2RouteToolState) bool {
	return target.AssumptionFingerprint == "" || state.agent != "" || state.needsReview
}

func v2ExplorerServerGroups(targets []coreapp.V2RoutingTarget, names map[string]string) []v2RoutingExplorerGroup {
	members := map[string]map[string]bool{}
	for _, target := range targets {
		set := members[target.ServerID]
		if set == nil {
			set = map[string]bool{}
			members[target.ServerID] = set
		}
		set[v2RoutingTargetKey(target)] = true
	}
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := names[ids[i]], names[ids[j]]
		if a == "" {
			a = ids[i]
		}
		if b == "" {
			b = ids[j]
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
	groups := []v2RoutingExplorerGroup{{Key: "all", Title: "All tools"}}
	for _, id := range ids {
		title := names[id]
		if title == "" {
			title = id
		}
		groups = append(groups, v2RoutingExplorerGroup{Key: "server:" + id, Title: title, Description: id, Members: members[id]})
	}
	return groups
}

func v2ExplorerCapabilityGroups(targets []coreapp.V2RoutingTarget, hierarchy enrichment.CapabilityHierarchy) []v2RoutingExplorerGroup {
	byID := make(map[string]enrichment.CapabilityNode, len(hierarchy.Capabilities))
	children := map[string][]string{}
	memberToKey := map[string]string{}
	for _, target := range targets {
		memberToKey[target.ServerID+"/"+target.ToolName] = v2RoutingTargetKey(target)
	}
	for _, capability := range hierarchy.Capabilities {
		byID[capability.ID] = capability
		children[capability.ParentID] = append(children[capability.ParentID], capability.ID)
	}
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool {
			return strings.ToLower(byID[children[parent][i]].Name) < strings.ToLower(byID[children[parent][j]].Name)
		})
	}

	memo := map[string]map[string]bool{}
	var collect func(string, map[string]bool) map[string]bool
	collect = func(id string, visiting map[string]bool) map[string]bool {
		if got := memo[id]; got != nil {
			return got
		}
		if visiting[id] {
			return map[string]bool{}
		}
		visiting[id] = true
		set := map[string]bool{}
		if capability, ok := byID[id]; ok {
			for _, member := range capability.ToolMembers {
				if key := memberToKey[member]; key != "" {
					set[key] = true
				}
			}
		}
		for _, child := range children[id] {
			for key := range collect(child, visiting) {
				set[key] = true
			}
		}
		delete(visiting, id)
		memo[id] = set
		return set
	}

	groups := []v2RoutingExplorerGroup{{Key: "all", Title: "All tools"}}
	visited := map[string]bool{}
	var appendTree func(string, int)
	appendTree = func(id string, depth int) {
		if visited[id] {
			return
		}
		visited[id] = true
		capability, ok := byID[id]
		if !ok {
			return
		}
		groups = append(groups, v2RoutingExplorerGroup{
			Key:         "cap:" + id,
			Title:       capability.Name,
			Description: capability.Description,
			Depth:       depth,
			Members:     collect(id, map[string]bool{}),
		})
		for _, child := range children[id] {
			appendTree(child, depth+1)
		}
	}
	roots := append([]string(nil), children[""]...)
	for _, capability := range hierarchy.Capabilities {
		if capability.ParentID != "" {
			if _, ok := byID[capability.ParentID]; !ok {
				roots = append(roots, capability.ID)
			}
		}
	}
	sort.Slice(roots, func(i, j int) bool { return strings.ToLower(byID[roots[i]].Name) < strings.ToLower(byID[roots[j]].Name) })
	for _, id := range roots {
		appendTree(id, 0)
	}
	remaining := make([]string, 0)
	for id := range byID {
		if !visited[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Slice(remaining, func(i, j int) bool { return strings.ToLower(byID[remaining[i]].Name) < strings.ToLower(byID[remaining[j]].Name) })
	for _, id := range remaining {
		appendTree(id, 0)
	}
	return groups
}

func v2ExplorerNormalizeGroup(groups []v2RoutingExplorerGroup) {
	if v2RouteExplorer.selectedGroup == "" {
		v2RouteExplorer.selectedGroup = "all"
	}
	for _, group := range groups {
		if group.Key == v2RouteExplorer.selectedGroup {
			return
		}
	}
	v2RouteExplorer.selectedGroup = "all"
	v2RouteWorkspace.selected = "catalog"
}

func v2ExplorerSelectedGroup(groups []v2RoutingExplorerGroup) v2RoutingExplorerGroup {
	for _, group := range groups {
		if group.Key == v2RouteExplorer.selectedGroup {
			return group
		}
	}
	return v2RoutingExplorerGroup{Key: "all", Title: "All tools"}
}

func v2ExplorerFilteredTargets(targets []coreapp.V2RoutingTarget, group v2RoutingExplorerGroup, names map[string]string, states map[string]v2RouteToolState, query string, attentionOnly bool) []coreapp.V2RoutingTarget {
	query = strings.ToLower(strings.TrimSpace(query))
	filtered := make([]coreapp.V2RoutingTarget, 0, len(targets))
	for _, target := range targets {
		key := v2RoutingTargetKey(target)
		if group.Key != "all" && !group.Members[key] {
			continue
		}
		state := states[key]
		if attentionOnly && !v2ExplorerNeedsAttention(target, state) {
			continue
		}
		if query != "" {
			serverName := names[target.ServerID]
			haystack := strings.ToLower(target.ToolName + " " + target.ServerID + " " + serverName + " " + v2WorkspaceToolLabel(state, target.AssumptionFingerprint != ""))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		filtered = append(filtered, target)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if strings.EqualFold(filtered[i].ToolName, filtered[j].ToolName) {
			return filtered[i].ServerID < filtered[j].ServerID
		}
		return strings.ToLower(filtered[i].ToolName) < strings.ToLower(filtered[j].ToolName)
	})
	return filtered
}

func v2ExplorerSelectionFallback(filtered []coreapp.V2RoutingTarget, group v2RoutingExplorerGroup) {
	if strings.HasPrefix(v2RouteWorkspace.selected, "tool:") {
		selected := strings.TrimPrefix(v2RouteWorkspace.selected, "tool:")
		for _, target := range filtered {
			if v2RoutingTargetKey(target) == selected {
				return
			}
		}
	switch {
	case strings.HasPrefix(group.Key, "cap:"):
		v2RouteWorkspace.selected = group.Key
	case strings.HasPrefix(group.Key, "server:"):
		v2RouteWorkspace.selected = group.Key
	default:
		v2RouteWorkspace.selected = "catalog"
	}
}

func v2ExplorerToolbar(u *v2DesktopUI, gtx layout.Context, hierarchyUsable bool, counts v2RoutingExplorerCounts) layout.Dimensions {
	for v2RouteExplorer.groupNext.Clicked(gtx) {
		if hierarchyUsable {
			if v2RouteExplorer.groupBy == "capabilities" {
				v2RouteExplorer.groupBy = "servers"
			} else {
				v2RouteExplorer.groupBy = "capabilities"
			}
			v2RouteExplorer.selectedGroup = "all"
			v2RouteWorkspace.selected = "catalog"
			v2RouteWorkspace.needsFit = true
		}
	}
	for v2RouteExplorer.graphToggle.Clicked(gtx) {
		v2RouteExplorer.graph = !v2RouteExplorer.graph
		v2RouteWorkspace.needsFit = true
	}
	for v2RouteExplorer.attentionToggle.Clicked(gtx) {
		v2RouteExplorer.attentionOnly = !v2RouteExplorer.attentionOnly
	}
	if !hierarchyUsable && v2RouteExplorer.groupBy == "capabilities" {
		v2RouteExplorer.groupBy = "servers"
		v2RouteExplorer.selectedGroup = "all"
	}
	groupLabel := "Group: Capabilities"
	if v2RouteExplorer.groupBy == "servers" {
		groupLabel = "Group: Servers"
	}
	attentionLabel := "Needs attention"
	if v2RouteExplorer.attentionOnly {
		attentionLabel = "Showing attention only"
	}
	viewLabel := "Graph"
	if v2RouteExplorer.graph {
		viewLabel = "Explorer"
	}
	return compactCard(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, editorSurface(u.th, &v2RouteExplorer.search, "Search tools or servers…")),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
					layout.Rigid(secondaryButton(u.th, &v2RouteExplorer.groupNext, groupLabel)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
					layout.Rigid(secondaryButton(u.th, &v2RouteExplorer.attentionToggle, attentionLabel)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
					layout.Rigid(secondaryButton(u.th, &v2RouteExplorer.graphToggle, viewLabel)),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if gtx.Constraints.Max.X < gtx.Dp(unit.Dp(650)) {
						return faintCaption(u.th, fmt.Sprintf("%d new · %d agent · %d review · %d ready", counts.New, counts.Agent, counts.Review, counts.Ready))(gtx)
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(pill(u.th, fmt.Sprintf("%d NEW", counts.New), uiWarningSoft, uiWarning)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }),
						layout.Rigid(pill(u.th, fmt.Sprintf("%d AGENT", counts.Agent), uiWarningSoft, uiWarning)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }),
						layout.Rigid(pill(u.th, fmt.Sprintf("%d REVIEW", counts.Review), uiDangerSoft, uiDanger)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }),
						layout.Rigid(pill(u.th, fmt.Sprintf("%d READY", counts.Ready), uiSuccessSoft, uiSuccess)),
					)
				})
			}),
		)
	})(gtx)
}

func v2ExplorerGroupPane(u *v2DesktopUI, groups []v2RoutingExplorerGroup) layout.Widget {
	return compactCard(func(gtx layout.Context) layout.Dimensions {
		title := "Capabilities"
		if v2RouteExplorer.groupBy == "servers" {
			title = "Servers"
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(sectionTitle(u.th, title)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(8)}.Layout(gtx, faintCaption(u.th, "Select a group to filter the tool list."))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return v2RouteExplorer.groups.Layout(gtx, len(groups), func(gtx layout.Context, index int) layout.Dimensions {
					group := groups[index]
					click := v2ExplorerGroupClick(group.Key)
					for click.Clicked(gtx) {
						v2RouteExplorer.selectedGroup = group.Key
						switch {
						case strings.HasPrefix(group.Key, "cap:"):
							v2RouteWorkspace.selected = group.Key
						case strings.HasPrefix(group.Key, "server:"):
							v2RouteWorkspace.selected = group.Key
						default:
							v2RouteWorkspace.selected = "catalog"
						}
					}
					selected := v2RouteExplorer.selectedGroup == group.Key
					bg, fg := uiSurface, uiText
					if selected {
						bg = uiAccentSoft
					}
					return layout.Inset{Bottom: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return click.Layout(gtx, fillSurface(bg, unit.Dp(8), layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(9 + float32(group.Depth)*13), Right: unit.Dp(9)}, func(gtx layout.Context) layout.Dimensions {
							label := material.Body2(u.th, group.Title)
							label.Color = fg
							count := len(group.Members)
							if group.Key == "all" {
								count = 0
								for _, candidate := range groups[1:] {
									for key := range candidate.Members {
										_ = key
									}
								}
							}
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, label.Layout),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if group.Key == "all" {
										return layout.Dimensions{}
									}
									return pill(u.th, fmt.Sprintf("%d", count), uiSurfaceRaised, uiMuted)(gtx)
								}),
							)
						}))
					})
				})
			}),
		)
	})
}

func v2ExplorerToolPane(u *v2DesktopUI, filtered []coreapp.V2RoutingTarget, total int, names map[string]string, states map[string]v2RouteToolState, ready bool) layout.Widget {
	return compactCard(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(sectionTitle(u.th, "Tools")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%d shown · %d live", len(filtered), total)))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if len(filtered) == 0 {
					return layout.Center.Layout(gtx, mutedCaption(u.th, "No tools match the current filters."))
				}
				return v2RouteExplorer.tools.Layout(gtx, len(filtered), func(gtx layout.Context, index int) layout.Dimensions {
					target := filtered[index]
					key := v2RoutingTargetKey(target)
					click := v2ExplorerToolClick(key)
					for click.Clicked(gtx) {
						v2RouteWorkspace.selected = "tool:" + key
					}
					selected := v2RouteWorkspace.selected == "tool:"+key
					bg := uiSurface
					if selected {
						bg = uiAccentSoft
					}
					state := states[key]
					status := v2WorkspaceToolLabel(state, ready && target.AssumptionFingerprint != "")
					statusFG := v2WorkspaceStateFG(state)
					if status == "INDEXED" {
						statusFG = uiMuted
					}
					serverName := names[target.ServerID]
					if serverName == "" {
						serverName = target.ServerID
					}
					return layout.Inset{Bottom: unit.Dp(5)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return click.Layout(gtx, fillSurface(bg, unit.Dp(8), layout.Inset{Top: unit.Dp(9), Bottom: unit.Dp(9), Left: unit.Dp(10), Right: unit.Dp(10)}, func(gtx layout.Context) layout.Dimensions {
							name := material.Body2(u.th, target.ToolName)
							name.Color = uiText
							server := material.Caption(u.th, serverName)
							server.Color = uiMuted
							stateLabel := material.Caption(u.th, status)
							stateLabel.Color = statusFG
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(name.Layout), layout.Rigid(server.Layout))
								}),
								layout.Rigid(stateLabel.Layout),
							)
						}))
					})
				})
			}),
		)
	})
}

func v2ExplorerBody(u *v2DesktopUI, gtx layout.Context, prefs coreapp.V2PreferenceSnapshot, targets []coreapp.V2RoutingTarget, filtered []coreapp.V2RoutingTarget, groups []v2RoutingExplorerGroup, names map[string]string, states map[string]v2RouteToolState, hierarchy enrichment.CapabilityHierarchy, hierarchyUsable bool, status indexing.Status, toolBatches, capBatches, reviews any) layout.Dimensions {
	panic("unreachable")
}

func v2WorkspaceCapabilityOverviewGraph(targets []coreapp.V2RoutingTarget, groups []v2RoutingExplorerGroup, hierarchy enrichment.CapabilityHierarchy, status indexing.Status) ([]v2RouteGraphNode, []v2RouteGraphEdge, image.Rectangle) {
	const nodeW, nodeH, layerGap, itemGap float32 = 238, 72, 105, 24
	byID := make(map[string]enrichment.CapabilityNode, len(hierarchy.Capabilities))
	countByID := map[string]int{}
	for _, group := range groups {
		if strings.HasPrefix(group.Key, "cap:") {
			countByID[strings.TrimPrefix(group.Key, "cap:")] = len(group.Members)
		}
	}
	for _, capability := range hierarchy.Capabilities {
		byID[capability.ID] = capability
	}
	depthMemo := map[string]int{}
	var depth func(string, map[string]bool) int
	depth = func(id string, visiting map[string]bool) int {
		if value, ok := depthMemo[id]; ok {
			return value
		}
		if visiting[id] {
			return 0
		}
		visiting[id] = true
		capability, ok := byID[id]
		if !ok || capability.ParentID == "" {
			delete(visiting, id)
			depthMemo[id] = 0
			return 0
		}
		value := depth(capability.ParentID, visiting) + 1
		delete(visiting, id)
		depthMemo[id] = value
		return value
	}
	layers := map[int][]enrichment.CapabilityNode{}
	maxDepth := 0
	for _, capability := range hierarchy.Capabilities {
		d := depth(capability.ID, map[string]bool{})
		layers[d] = append(layers[d], capability)
		maxDepth = max(maxDepth, d)
	}
	widest := float32(760)
	for d := range layers {
		sort.Slice(layers[d], func(i, j int) bool { return strings.ToLower(layers[d][i].Name) < strings.ToLower(layers[d][j].Name) })
		layerW := float32(len(layers[d]))*nodeW + float32(max(0, len(layers[d])-1))*itemGap + 70
		widest = max(widest, layerW)
	}
	rootW, rootH := float32(250), float32(76)
	rootX := (widest - rootW) / 2
	rootStatus := "ACTIVE"
	if status.StagingGenerationID != "" {
		rootStatus = "STAGING"
	}
	if status.PendingRequired > 0 {
		rootStatus = fmt.Sprintf("AGENT WORK · %d pending", status.PendingRequired)
	} else if status.Ready && status.StagingGenerationID == "" {
		rootStatus = "READY"
	}
	nodes := []v2RouteGraphNode{{key: "catalog", kind: "root", title: "Semantic routing hierarchy", subtitle: fmt.Sprintf("%d capability groups · %d tools", len(hierarchy.Capabilities), len(targets)), status: rootStatus, x: rootX, y: 35, w: rootW, h: rootH}}
	centers := map[string]f32.Point{"catalog": f32.Pt(rootX+rootW/2, 35+rootH/2)}
	y := float32(35) + rootH + layerGap
	for d := 0; d <= maxDepth; d++ {
		layer := layers[d]
		if len(layer) == 0 {
			continue
		}
		layerW := float32(len(layer))*nodeW + float32(max(0, len(layer)-1))*itemGap
		x := (widest - layerW) / 2
		for i, capability := range layer {
			nx := x + float32(i)*(nodeW+itemGap)
			nodes = append(nodes, v2RouteGraphNode{key: "cap:" + capability.ID, kind: "capability", title: capability.Name, subtitle: capability.Description, status: fmt.Sprintf("CAPABILITY · %d tools", countByID[capability.ID]), x: nx, y: y, w: nodeW, h: nodeH})
			centers["cap:"+capability.ID] = f32.Pt(nx+nodeW/2, y+nodeH/2)
		}
		y += nodeH + layerGap
	}
	edges := []v2RouteGraphEdge{}
	for _, capability := range hierarchy.Capabilities {
		fromKey := "catalog"
		if capability.ParentID != "" {
			fromKey = "cap:" + capability.ParentID
		}
		from, fromOK := centers[fromKey]
		to, toOK := centers["cap:"+capability.ID]
		if fromOK && toOK {
			edges = append(edges, v2RouteGraphEdge{from: from, to: to})
		}
	}
	return nodes, edges, image.Rect(0, 0, int(widest), int(y+35))
}

func v2WorkspaceServerOverviewGraph(targets []coreapp.V2RoutingTarget, names map[string]string, states map[string]v2RouteToolState, status indexing.Status) ([]v2RouteGraphNode, []v2RouteGraphEdge, image.Rectangle) {
	byServer := map[string][]coreapp.V2RoutingTarget{}
	for _, target := range targets {
		byServer[target.ServerID] = append(byServer[target.ServerID], target)
	}
	ids := make([]string, 0, len(byServer))
	for id := range byServer {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := names[ids[i]], names[ids[j]]
		if a == "" {
			a = ids[i]
		}
		if b == "" {
			b = ids[j]
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
	const nodeW, nodeH, gapX, gapY float32 = 270, 82, 32, 42
	cols := min(3, max(1, len(ids)))
	width := max(float32(760), float32(cols)*nodeW+float32(max(0, cols-1))*gapX+70)
	rootW, rootH := float32(250), float32(76)
	rootX := (width - rootW) / 2
	rootStatus := "ACTIVE"
	if status.StagingGenerationID != "" {
		rootStatus = "STAGING"
	}
	if status.PendingRequired > 0 {
		rootStatus = fmt.Sprintf("AGENT WORK · %d pending", status.PendingRequired)
	} else if status.Ready && status.StagingGenerationID == "" {
		rootStatus = "READY"
	}
	nodes := []v2RouteGraphNode{{key: "catalog", kind: "root", title: "Routing catalog", subtitle: fmt.Sprintf("%d servers · %d tools", len(ids), len(targets)), status: rootStatus, x: rootX, y: 35, w: rootW, h: rootH}}
	edges := []v2RouteGraphEdge{}
	rootCenter := f32.Pt(rootX+rootW/2, 35+rootH)
	startY := float32(35) + rootH + 115
	for i, id := range ids {
		row, col := i/cols, i%cols
		x := (width-(float32(cols)*nodeW+float32(max(0, cols-1))*gapX))/2 + float32(col)*(nodeW+gapX)
		y := startY + float32(row)*(nodeH+gapY)
		name := names[id]
		if name == "" {
			name = id
		}
		pending := 0
		for _, target := range byServer[id] {
			if v2ExplorerNeedsAttention(target, states[v2RoutingTargetKey(target)]) {
				pending++
			}
		}
		statusText := fmt.Sprintf("%d tools", len(byServer[id]))
		if pending > 0 {
			statusText = fmt.Sprintf("%d tools · %d attention", len(byServer[id]), pending)
		}
		nodes = append(nodes, v2RouteGraphNode{key: "server:" + id, kind: "server", title: name, subtitle: id, status: statusText, x: x, y: y, w: nodeW, h: nodeH})
		edges = append(edges, v2RouteGraphEdge{from: rootCenter, to: f32.Pt(x+nodeW/2, y)})
	}
	rows := int(math.Ceil(float64(max(1, len(ids))) / float64(cols)))
	return nodes, edges, image.Rect(0, 0, int(width), int(startY+float32(rows)*(nodeH+gapY)+35))
}

func v2ExplorerGraphPane(u *v2DesktopUI, gtx layout.Context, targets []coreapp.V2RoutingTarget, groups []v2RoutingExplorerGroup, names map[string]string, states map[string]v2RouteToolState, hierarchy enrichment.CapabilityHierarchy, hierarchyUsable bool, status indexing.Status) layout.Dimensions {
	var nodes []v2RouteGraphNode
	var edges []v2RouteGraphEdge
	var bounds image.Rectangle
	if hierarchyUsable && v2RouteExplorer.groupBy == "capabilities" {
		nodes, edges, bounds = v2WorkspaceCapabilityOverviewGraph(targets, groups, hierarchy, status)
	} else {
		nodes, edges, bounds = v2WorkspaceServerOverviewGraph(targets, names, states, status)
	}
	sig := fmt.Sprintf("overview|%s|%s|%d|%d", status.ActiveGenerationID, v2RouteExplorer.groupBy, len(nodes), len(edges))
	if sig != v2RouteWorkspace.signature {
		v2RouteWorkspace.signature = sig
		v2RouteWorkspace.needsFit = true
	}
	v2WorkspaceSelectDefault(nodes, states)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(compactCard(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.fit, "Fit")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.zoomOut, "−")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(5)}.Layout(gtx) }),
				layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.zoomIn, "+")),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, faintCaption(u.th, "Capability/server overview only · drag to pan · wheel to zoom"))
				}),
			)
		})),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return v2WorkspaceCanvas(u, gtx, nodes, edges, bounds) }),
	)
}
