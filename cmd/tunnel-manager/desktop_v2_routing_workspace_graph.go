//go:build !nogui

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
)

func v2WorkspaceStates(rules []routingprefs.Rule, toolBatches, capBatches []catalog.EnrichmentBatch) map[string]v2RouteToolState {
	states := map[string]v2RouteToolState{}
	for _, batch := range toolBatches {
		var request enrichment.ToolBatchRequest
		if json.Unmarshal(batch.RequestJSON, &request) == nil {
			for _, item := range request.Items {
				k := item.Tool.ServerID + "\x00" + item.Tool.ToolName
				s := states[k]
				s.agent = "TOOL ENRICHMENT"
				states[k] = s
			}
		}
	}
	for _, batch := range capBatches {
		var request enrichment.CapabilityBatchRequest
		if json.Unmarshal(batch.RequestJSON, &request) == nil {
			for _, item := range request.Items {
				k, s := item.Tool.ServerID+"\x00"+item.Tool.ToolName, states[item.Tool.ServerID+"\x00"+item.Tool.ToolName]
				s.agent = "CAPABILITY GROUPING"
				states[k] = s
			}
		}
	}
	for _, rule := range rules {
		for _, target := range rule.Spec.Preferred {
			k, s := target.ServerID+"\x00"+target.ToolName, states[target.ServerID+"\x00"+target.ToolName]
			s.preference, s.needsReview = "PREFERRED", s.needsReview || rule.ReviewState == routingprefs.ReviewNeedsReview
			states[k] = s
		}
		for _, target := range rule.Spec.Deprioritized {
			k, s := target.ServerID+"\x00"+target.ToolName, states[target.ServerID+"\x00"+target.ToolName]
			s.preference, s.needsReview = "LOWER PRIORITY", s.needsReview || rule.ReviewState == routingprefs.ReviewNeedsReview
			states[k] = s
		}
	}
	return states
}

func v2WorkspaceToolLabel(s v2RouteToolState, ready bool) string {
	if s.agent != "" {
		return "AGENT · " + s.agent
	}
	if s.needsReview {
		return "PREFERENCE REVIEW"
	}
	if s.preference != "" {
		return s.preference
	}
	if ready {
		return "READY"
	}
	return "INDEXED"
}

func v2WorkspaceServerGraph(targets []coreapp.V2RoutingTarget, names map[string]string, states map[string]v2RouteToolState, status indexing.Status) ([]v2RouteGraphNode, []v2RouteGraphEdge, image.Rectangle) {
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
		return a < b
	})
	const groupW, groupGapX, groupGapY, toolW, toolH, gap, pad float32 = 560, 70, 55, 164, 66, 14, 18
	cols, y, maxX, maxY := 2, float32(150), float32(0), float32(0)
	if len(ids) <= 1 {
		cols = 1
	}
	nodes, edges := []v2RouteGraphNode{}, []v2RouteGraphEdge{}
	centers := make([]f32.Point, len(ids))
	for row := 0; row*cols < len(ids); row++ {
		rowH := float32(0)
		for col := 0; col < cols; col++ {
			i := row*cols + col
			if i >= len(ids) {
				break
			}
			rows := max(1, int(math.Ceil(float64(len(byServer[ids[i]]))/3)))
			h := float32(76 + rows*int(toolH) + (rows-1)*int(gap))
			rowH = max(rowH, h)
			x := float32(35) + float32(col)*(groupW+groupGapX)
			name := names[ids[i]]
			if name == "" {
				name = ids[i]
			}
			pending := 0
			for _, target := range byServer[ids[i]] {
				if states[v2RoutingTargetKey(target)].agent != "" {
					pending++
				}
			}
			groupStatus := fmt.Sprintf("%d tools", len(byServer[ids[i]]))
			if pending > 0 {
				groupStatus = fmt.Sprintf("AGENT · %d pending", pending)
			}
			nodes = append(nodes, v2RouteGraphNode{key: "server:" + ids[i], kind: "server", title: name, subtitle: ids[i], status: groupStatus, x: x, y: y, w: groupW, h: h})
			centers[i] = f32.Pt(x+groupW/2, y)
			for j, target := range byServer[ids[i]] {
				tx, ty := x+pad+float32(j%3)*(toolW+gap), y+58+pad+float32(j/3)*(toolH+gap)
				key := v2RoutingTargetKey(target)
				nodes = append(nodes, v2RouteGraphNode{key: "tool:" + key, kind: "tool", title: target.ToolName, subtitle: name, status: v2WorkspaceToolLabel(states[key], status.Ready), targetKey: key, x: tx, y: ty, w: toolW, h: toolH})
			}
			maxX, maxY = max(maxX, x+groupW), max(maxY, y+h)
		}
		y += rowH + groupGapY
	}
	rootW, rootX := float32(250), max(float32(35), (maxX-250)/2)
	rootStatus := "ACTIVE"
	if status.StagingGenerationID != "" {
		rootStatus = "STAGING"
	}
	if status.PendingRequired > 0 {
		rootStatus = fmt.Sprintf("AGENT WORK · %d pending", status.PendingRequired)
	}
	if status.Ready && status.StagingGenerationID == "" {
		rootStatus = "READY"
	}
	nodes = append(nodes, v2RouteGraphNode{key: "catalog", kind: "root", title: "Routing catalog", subtitle: fmt.Sprintf("%d servers · %d tools", len(ids), len(targets)), status: rootStatus, x: rootX, y: 35, w: rootW, h: 76})
	for _, c := range centers {
		edges = append(edges, v2RouteGraphEdge{from: f32.Pt(rootX+rootW/2, 111), to: c})
	}
	if maxX == 0 {
		maxX = 800
	}
	if maxY == 0 {
		maxY = 500
	}
	return nodes, edges, image.Rect(0, 0, int(maxX+35), int(maxY+35))
}

func v2WorkspaceCapabilityGraph(targets []coreapp.V2RoutingTarget, names map[string]string, states map[string]v2RouteToolState, hierarchy enrichment.CapabilityHierarchy, status indexing.Status) ([]v2RouteGraphNode, []v2RouteGraphEdge, image.Rectangle) {
	const nodeW, nodeH, layerGap, itemGap float32 = 238, 72, 115, 24
	byID := make(map[string]enrichment.CapabilityNode, len(hierarchy.Capabilities))
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
	for d := range layers {
		sort.Slice(layers[d], func(i, j int) bool { return layers[d][i].Name < layers[d][j].Name })
	}

	memberTarget := make(map[string]coreapp.V2RoutingTarget, len(targets))
	for _, target := range targets {
		memberTarget[target.ServerID+"/"+target.ToolName] = target
	}
	toolMembers := make([]string, 0, len(targets))
	for member := range memberTarget {
		toolMembers = append(toolMembers, member)
	}
	sort.Strings(toolMembers)

	widest := float32(760)
	for _, layer := range layers {
		widest = max(widest, float32(len(layer))*nodeW+float32(max(0, len(layer)-1))*itemGap+70)
	}
	toolCols := min(6, max(1, int(math.Ceil(math.Sqrt(float64(max(1, len(toolMembers))))))))
	toolW, toolH := float32(184), float32(66)
	toolLayerW := float32(toolCols)*toolW + float32(max(0, toolCols-1))*itemGap + 70
	widest = max(widest, toolLayerW)

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
			statusText := fmt.Sprintf("CAPABILITY · %d tools", len(capability.ToolMembers))
			nodes = append(nodes, v2RouteGraphNode{key: "cap:" + capability.ID, kind: "capability", title: capability.Name, subtitle: capability.Description, status: statusText, x: nx, y: y, w: nodeW, h: nodeH})
			centers["cap:"+capability.ID] = f32.Pt(nx+nodeW/2, y+nodeH/2)
		}
		y += nodeH + layerGap
	}

	toolStartY := y
	toolLayerWidth := float32(toolCols)*toolW + float32(max(0, toolCols-1))*itemGap
	toolStartX := (widest - toolLayerWidth) / 2
	for i, member := range toolMembers {
		target := memberTarget[member]
		key := v2RoutingTargetKey(target)
		serverName := names[target.ServerID]
		if serverName == "" {
			serverName = target.ServerID
		}
		x := toolStartX + float32(i%toolCols)*(toolW+itemGap)
		ty := toolStartY + float32(i/toolCols)*(toolH+itemGap)
		nodes = append(nodes, v2RouteGraphNode{key: "tool:" + key, kind: "tool", title: target.ToolName, subtitle: serverName, status: v2WorkspaceToolLabel(states[key], status.Ready), targetKey: key, x: x, y: ty, w: toolW, h: toolH})
		centers["tool:"+key] = f32.Pt(x+toolW/2, ty+toolH/2)
	}

	edges := []v2RouteGraphEdge{}
	for _, capability := range hierarchy.Capabilities {
		childKey := "cap:" + capability.ID
		fromKey := "catalog"
		if capability.ParentID != "" {
			fromKey = "cap:" + capability.ParentID
		}
		if from, ok := centers[fromKey]; ok {
			if to, ok := centers[childKey]; ok {
				edges = append(edges, v2RouteGraphEdge{from: from, to: to})
			}
		}
		from, ok := centers[childKey]
		if !ok {
			continue
		}
		for _, member := range capability.ToolMembers {
			target, ok := memberTarget[member]
			if !ok {
				continue
			}
			if to, ok := centers["tool:"+v2RoutingTargetKey(target)]; ok {
				edges = append(edges, v2RouteGraphEdge{from: from, to: to})
			}
		}
	}
	rows := int(math.Ceil(float64(max(1, len(toolMembers))) / float64(toolCols)))
	maxY := toolStartY + float32(rows)*(toolH+itemGap) + 35
	return nodes, edges, image.Rect(0, 0, int(widest), int(maxY))
}

func v2WorkspaceViewLabel(hierarchyFound bool) string {
	if !hierarchyFound {
		return "View: Sources"
	}
	if v2RouteWorkspace.view == "capabilities" {
		return "View: Capabilities"
	}
	return "View: Sources"
}

func v2WorkspaceSelectDefault(nodes []v2RouteGraphNode, states map[string]v2RouteToolState) {
	for _, n := range nodes {
		if n.key == v2RouteWorkspace.selected {
			return
		}
	}
	for _, n := range nodes {
		if n.targetKey != "" && states[n.targetKey].agent != "" {
			v2RouteWorkspace.selected = n.key
			return
		}
	}
	for _, n := range nodes {
		if n.targetKey != "" {
			v2RouteWorkspace.selected = n.key
			return
		}
	}
	v2RouteWorkspace.selected = "catalog"
}

func v2WorkspaceGraph(u *v2DesktopUI, nodes []v2RouteGraphNode, edges []v2RouteGraphEdge, bounds image.Rectangle, hierarchyFound bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(compactCard(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.fit, "Fit")),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }),
					layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.zoomOut, "−")),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(5)}.Layout(gtx) }),
					layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.zoomIn, "+")),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }),
					layout.Rigid(secondaryButton(u.th, &v2RouteWorkspace.viewNext, v2WorkspaceViewLabel(hierarchyFound))),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.E.Layout(gtx, faintCaption(u.th, "Drag to pan · wheel to zoom · click nodes"))
					}),
				)
			})),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(8)}.Layout(gtx) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return v2WorkspaceCanvas(u, gtx, nodes, edges, bounds) }),
		)
	}
}

func v2WorkspaceCanvas(u *v2DesktopUI, gtx layout.Context, nodes []v2RouteGraphNode, edges []v2RouteGraphEdge, bounds image.Rectangle) layout.Dimensions {
	size := gtx.Constraints.Max
	if size.X <= 0 || size.Y <= 0 {
		return layout.Dimensions{}
	}
	defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(unit.Dp(12))).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, uiSurface)
	if v2RouteWorkspace.zoom == 0 {
		v2RouteWorkspace.zoom = 1
	}
	if v2RouteWorkspace.needsFit {
		v2WorkspaceFit(gtx, size, bounds)
		v2RouteWorkspace.needsFit = false
	}
	v2WorkspacePointerEvents(gtx)
	density, scale := float32(gtx.Dp(unit.Dp(1))), float32(gtx.Dp(unit.Dp(1)))*v2RouteWorkspace.zoom
	toScreen := func(p f32.Point) f32.Point {
		return f32.Pt(v2RouteWorkspace.pan.X+p.X*scale, v2RouteWorkspace.pan.Y+p.Y*scale)
	}
	for _, edge := range edges {
		from, to := toScreen(edge.from), toScreen(edge.to)
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(from)
		mid := (from.X + to.X) / 2
		p.CubeTo(f32.Pt(mid, from.Y), f32.Pt(mid, to.Y), to)
		stack := clip.Stroke{Path: p.End(), Width: max(float32(1), density*1.2)}.Op().Push(gtx.Ops)
		paint.Fill(gtx.Ops, uiFaint)
		stack.Pop()
	}
	hits := make([]v2RouteGraphHit, 0, len(nodes))
	for _, n := range nodes {
		x, y := int(math.Round(float64(v2RouteWorkspace.pan.X+n.x*scale))), int(math.Round(float64(v2RouteWorkspace.pan.Y+n.y*scale)))
		w, h := max(2, int(math.Round(float64(n.w*scale)))), max(2, int(math.Round(float64(n.h*scale))))
		r := image.Rect(x, y, x+w, y+h)
		if r.Max.X < 0 || r.Max.Y < 0 || r.Min.X > size.X || r.Min.Y > size.Y {
			continue
		}
		v2WorkspaceDrawNode(u, gtx, n, image.Pt(x, y), image.Pt(w, h))
		hits = append(hits, v2RouteGraphHit{key: n.key, targetKey: n.targetKey, rect: r})
	}
	v2RouteWorkspace.hits = hits
	event.Op(gtx.Ops, &v2RouteWorkspace)
	pointer.CursorGrab.Add(gtx.Ops)
	return layout.Dimensions{Size: size}
}

func v2WorkspaceFit(gtx layout.Context, size image.Point, bounds image.Rectangle) {
	density, padding := float32(gtx.Dp(unit.Dp(1))), float32(gtx.Dp(unit.Dp(28)))
	w, h := max(float32(1), float32(bounds.Dx())), max(float32(1), float32(bounds.Dy()))
	z := min((float32(size.X)-padding*2)/(w*density), (float32(size.Y)-padding*2)/(h*density))
	v2RouteWorkspace.zoom = max(float32(.3), min(float32(1.25), z))
	v2RouteWorkspace.pan = f32.Pt((float32(size.X)-w*density*v2RouteWorkspace.zoom)/2, (float32(size.Y)-h*density*v2RouteWorkspace.zoom)/2)
}

func v2WorkspacePointerEvents(gtx layout.Context) {
	filter := pointer.Filter{Target: &v2RouteWorkspace, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Scroll, ScrollX: pointer.ScrollRange{Min: -10000, Max: 10000}, ScrollY: pointer.ScrollRange{Min: -10000, Max: 10000}}
	for {
		ev, ok := gtx.Event(filter)
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Scroll:
			old := v2RouteWorkspace.zoom
			factor := float32(math.Exp(float64(-e.Scroll.Y) * .0018))
			next := max(float32(.3), min(float32(2.2), old*factor))
			if next != old {
				ratio := next / old
				a := e.Position
				v2RouteWorkspace.pan = f32.Pt(a.X-(a.X-v2RouteWorkspace.pan.X)*ratio, a.Y-(a.Y-v2RouteWorkspace.pan.Y)*ratio)
				v2RouteWorkspace.zoom = next
			}
		case pointer.Press:
			if e.Source == pointer.Mouse && !e.Buttons.Contain(pointer.ButtonPrimary) && !e.Buttons.Contain(pointer.ButtonTertiary) {
				continue
			}
			v2RouteWorkspace.dragging, v2RouteWorkspace.pointerID, v2RouteWorkspace.last, v2RouteWorkspace.press, v2RouteWorkspace.moved = true, e.PointerID, e.Position, e.Position, false
			gtx.Execute(pointer.GrabCmd{Tag: &v2RouteWorkspace, ID: e.PointerID})
		case pointer.Drag:
			if !v2RouteWorkspace.dragging || e.PointerID != v2RouteWorkspace.pointerID {
				continue
			}
			d := e.Position.Sub(v2RouteWorkspace.last)
			v2RouteWorkspace.pan = v2RouteWorkspace.pan.Add(d)
			v2RouteWorkspace.last = e.Position
			m := e.Position.Sub(v2RouteWorkspace.press)
			if m.X*m.X+m.Y*m.Y > 16 {
				v2RouteWorkspace.moved = true
			}
		case pointer.Release:
			if !v2RouteWorkspace.dragging || e.PointerID != v2RouteWorkspace.pointerID {
				continue
			}
			if !v2RouteWorkspace.moved {
				p := image.Pt(int(math.Round(float64(e.Position.X))), int(math.Round(float64(e.Position.Y))))
				for i := len(v2RouteWorkspace.hits) - 1; i >= 0; i-- {
					if p.In(v2RouteWorkspace.hits[i].rect) {
						v2RouteWorkspace.selected = v2RouteWorkspace.hits[i].key
						break
					}
				}
			}
			v2RouteWorkspace.dragging = false
		case pointer.Cancel:
			v2RouteWorkspace.dragging = false
		}
	}
}

func v2WorkspaceDrawNode(u *v2DesktopUI, gtx layout.Context, n v2RouteGraphNode, offset, size image.Point) {
	stack := op.Offset(offset).Push(gtx.Ops)
	defer stack.Pop()
	bg := uiSurfaceRaised
	if n.kind == "server" {
		bg = uiSidebar
	}
	if n.key == v2RouteWorkspace.selected {
		bg = uiAccentSoft
	}
	if strings.Contains(n.status, "AGENT") {
		bg = uiWarningSoft
	}
	if strings.Contains(n.status, "REVIEW") {
		bg = uiDangerSoft
	}
	defer clip.UniformRRect(image.Rectangle{Max: size}, max(2, gtx.Dp(unit.Dp(9)))).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, bg)
	if v2RouteWorkspace.zoom < .43 {
		return
	}
	ng := gtx
	ng.Constraints = layout.Exact(size)
	layout.Inset{Top: unit.Dp(9), Bottom: unit.Dp(7), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(ng, func(gtx layout.Context) layout.Dimensions {
		title := material.Body1(u.th, n.title)
		title.Color, title.TextSize = uiText, unit.Sp(max(float32(8), 14*v2RouteWorkspace.zoom))
		status := material.Caption(u.th, n.status)
		status.Color, status.TextSize = v2WorkspaceNodeStatusColor(n.status), unit.Sp(max(float32(7), 10*v2RouteWorkspace.zoom))
		children := []layout.FlexChild{layout.Rigid(title.Layout)}
		if v2RouteWorkspace.zoom >= .72 && n.subtitle != "" {
			sub := material.Caption(u.th, n.subtitle)
			sub.Color, sub.TextSize = uiFaint, unit.Sp(max(float32(7), 9*v2RouteWorkspace.zoom))
			children = append(children, layout.Rigid(sub.Layout))
		}
		children = append(children, layout.Rigid(status.Layout))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

func v2WorkspaceNodeStatusColor(status string) color.NRGBA {
	if strings.Contains(status, "AGENT") {
		return uiWarning
	}
	if strings.Contains(status, "REVIEW") {
		return uiDanger
	}
	if status == "PREFERRED" {
		return uiAccent
	}
	if status == "READY" || status == "ACTIVE" {
		return uiSuccess
	}
	return uiMuted
}
