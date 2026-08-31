//go:build !nogui

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
)

type v2BackgroundTask struct {
	Key       string
	Label     string
	Detail    string
	StartedAt time.Time
}

type v2RoutingPrepared struct {
	Status           indexing.Status
	Prefs            coreapp.V2PreferenceSnapshot
	Targets          []coreapp.V2RoutingTarget
	ToolBatches      []catalog.EnrichmentBatch
	CapBatches       []catalog.EnrichmentBatch
	Reviews          []catalog.EnrichmentBatch
	Hierarchy        enrichment.CapabilityHierarchy
	HierarchyUsable  bool
	ServerNames      map[string]string
	States           map[string]v2RouteToolState
	CapabilityGroups []v2RoutingExplorerGroup
	ServerGroups     []v2RoutingExplorerGroup
	Counts           v2RoutingExplorerCounts
	Revision         uint64
	UpdatedAt        time.Time
}

type v2UIAsyncState struct {
	mu sync.Mutex

	tasks       map[string]v2BackgroundTask
	uiCallbacks []func()

	routingWorkerOnce  sync.Once
	routingWake        chan struct{}
	routing            v2RoutingPrepared
	routingStarted     bool
	routingLoaded      bool
	routingLoading     bool
	routingDirty       bool
	routingError       string
	routingAttemptedAt time.Time

	filterKey string
	filtered  []coreapp.V2RoutingTarget
}

var v2UIAsyncStates sync.Map

func v2AsyncStateFor(u *v2DesktopUI) *v2UIAsyncState {
	if value, ok := v2UIAsyncStates.Load(u); ok {
		return value.(*v2UIAsyncState)
	}
	state := &v2UIAsyncState{
		tasks:       make(map[string]v2BackgroundTask),
		routingWake: make(chan struct{}, 1),
	}
	actual, _ := v2UIAsyncStates.LoadOrStore(u, state)
	return actual.(*v2UIAsyncState)
}

func v2DefaultTaskDetail(label string) string {
	lower := strings.ToLower(label)
	switch {
	case strings.Contains(lower, "index") || strings.Contains(lower, "routing"):
		return "Routing work is running on a background worker; the current UI remains usable."
	case strings.Contains(lower, "download") || strings.Contains(lower, "update") || strings.Contains(lower, "export"):
		return "Network or disk work is running in the background."
	case strings.Contains(lower, "starting") || strings.Contains(lower, "stopping") || strings.Contains(lower, "restarting") || strings.Contains(lower, "oauth"):
		return "The downstream runtime operation is running in the background."
	default:
		return "Running in the background."
	}
}

func (u *v2DesktopUI) runTask(key, label, detail string, fn func() error) bool {
	if strings.TrimSpace(key) == "" {
		key = strings.ToLower(strings.TrimSpace(label))
	}
	if strings.TrimSpace(detail) == "" {
		detail = v2DefaultTaskDetail(label)
	}

	u.mu.RLock()
	exiting := u.exiting
	u.mu.RUnlock()
	if exiting {
		return false
	}

	state := v2AsyncStateFor(u)
	state.mu.Lock()
	if running, exists := state.tasks[key]; exists {
		state.mu.Unlock()
		u.setMessage("Already running: " + running.Label)
		return false
	}
	state.tasks[key] = v2BackgroundTask{Key: key, Label: label, Detail: detail, StartedAt: time.Now()}
	active := len(state.tasks)
	state.mu.Unlock()

	u.mu.Lock()
	u.busy = active > 0
	u.message = label
	u.mu.Unlock()
	u.invalidate()

	go func() {
		err := fn()

		state.mu.Lock()
		delete(state.tasks, key)
		remaining := len(state.tasks)
		state.mu.Unlock()

		u.mu.Lock()
		u.busy = remaining > 0
		switch {
		case err != nil:
			u.message = label + ": " + err.Error()
		case remaining > 0:
			u.message = fmt.Sprintf("%d background task(s) still running", remaining)
		default:
			u.message = "Done: " + label
		}
		u.mu.Unlock()

		// If the routing workspace has been opened, actions that might change
		// the catalog invalidate its prepared snapshot. Do not start the heavy
		// routing worker just because a user performed an unrelated action on
		// another page.
		v2RefreshRoutingSnapshotIfStarted(u)
		u.invalidate()
	}()
	return true
}

func (u *v2DesktopUI) taskActive(key string) bool {
	state := v2AsyncStateFor(u)
	state.mu.Lock()
	defer state.mu.Unlock()
	_, ok := state.tasks[key]
	return ok
}

func (u *v2DesktopUI) activeTasks() []v2BackgroundTask {
	state := v2AsyncStateFor(u)
	state.mu.Lock()
	defer state.mu.Unlock()
	tasks := make([]v2BackgroundTask, 0, len(state.tasks))
	for _, task := range state.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].StartedAt.Before(tasks[j].StartedAt) })
	return tasks
}

// postUI schedules a mutation of Gio/widget state for the next frame. Worker
// goroutines should use this instead of touching editors or page state directly.
func (u *v2DesktopUI) postUI(fn func()) {
	if fn == nil {
		return
	}
	state := v2AsyncStateFor(u)
	state.mu.Lock()
	state.uiCallbacks = append(state.uiCallbacks, fn)
	state.mu.Unlock()
	u.invalidate()
}

func (u *v2DesktopUI) drainUI() {
	state := v2AsyncStateFor(u)
	state.mu.Lock()
	callbacks := append([]func(){}, state.uiCallbacks...)
	state.uiCallbacks = nil
	state.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func v2TaskElapsed(start time.Time) string {
	elapsed := time.Since(start)
	if elapsed < time.Second {
		return "<1s"
	}
	if elapsed < time.Minute {
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	}
	minutes := int(elapsed.Minutes())
	seconds := int(elapsed.Seconds()) % 60
	return fmt.Sprintf("%dm %02ds", minutes, seconds)
}

func (u *v2DesktopUI) activityPanel(gtx layout.Context) layout.Dimensions {
	tasks := u.activeTasks()
	if len(tasks) == 0 {
		return layout.Dimensions{}
	}

	shown := len(tasks)
	if shown > 3 {
		shown = 3
	}
	return layout.Inset{Bottom: unit.Dp(12)}.Layout(gtx, fillSurface(uiAccentSoft, unit.Dp(12), layout.UniformInset(unit.Dp(12)), func(gtx layout.Context) layout.Dimensions {
		rows := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(pill(u.th, "WORKING", uiSurfaceRaised, uiAccent)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
					layout.Flexed(1, mutedCaption(u.th, fmt.Sprintf("%d background task(s) · UI stays responsive", len(tasks)))),
				)
			}),
		}
		for i := 0; i < shown; i++ {
			task := tasks[i]
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					name := material.Body2(u.th, task.Label)
					name.Color = uiText
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(name.Layout),
								layout.Rigid(faintCaption(u.th, task.Detail)),
							)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(10)}.Layout(gtx) }),
						layout.Rigid(pill(u.th, v2TaskElapsed(task.StartedAt), uiSurfaceRaised, uiMuted)),
					)
				})
			}))
		}
		if len(tasks) > shown {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, faintCaption(u.th, fmt.Sprintf("+%d more background task(s)", len(tasks)-shown)))
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	}))
}

func v2EnsureRoutingSnapshot(u *v2DesktopUI) {
	state := v2AsyncStateFor(u)
	state.routingWorkerOnce.Do(func() { go v2RoutingCacheLoop(u, state) })

	now := time.Now()
	state.mu.Lock()
	state.routingStarted = true
	fresh := state.routingLoaded && now.Sub(state.routing.UpdatedAt) < 5*time.Second
	retryBackoff := !state.routingAttemptedAt.IsZero() && now.Sub(state.routingAttemptedAt) < 2*time.Second
	if fresh || state.routingLoading || retryBackoff {
		state.mu.Unlock()
		return
	}
	state.routingLoading = true
	state.routingAttemptedAt = now
	state.mu.Unlock()

	select {
	case state.routingWake <- struct{}{}:
	default:
	}
}

func v2RefreshRoutingSnapshotIfStarted(u *v2DesktopUI) {
	state := v2AsyncStateFor(u)
	state.mu.Lock()
	started := state.routingStarted
	state.mu.Unlock()
	if started {
		v2RefreshRoutingSnapshot(u)
	}
}

func v2RefreshRoutingSnapshot(u *v2DesktopUI) {
	state := v2AsyncStateFor(u)
	state.routingWorkerOnce.Do(func() { go v2RoutingCacheLoop(u, state) })

	state.mu.Lock()
	state.routingStarted = true
	if state.routingLoading {
		state.routingDirty = true
		state.mu.Unlock()
		return
	}
	state.routingLoading = true
	state.routingAttemptedAt = time.Now()
	state.mu.Unlock()

	select {
	case state.routingWake <- struct{}{}:
	default:
	}
}

func v2RoutingSnapshotFor(u *v2DesktopUI) (v2RoutingPrepared, bool, bool, string) {
	state := v2AsyncStateFor(u)
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.routing, state.routingLoaded, state.routingLoading, state.routingError
}

func v2RoutingCacheLoop(u *v2DesktopUI, state *v2UIAsyncState) {
	for {
		select {
		case <-u.core.Done():
			return
		case <-state.routingWake:
			for {
				prepared, err := v2LoadRoutingPrepared(u)

				state.mu.Lock()
				if err != nil {
					state.routingError = err.Error()
				} else {
					prepared.Revision = state.routing.Revision + 1
					state.routing = prepared
					state.routingLoaded = true
					state.routingError = ""
					state.filterKey = ""
					state.filtered = nil
				}
				rerun := state.routingDirty
				state.routingDirty = false
				if !rerun {
					state.routingLoading = false
				}
				state.mu.Unlock()
				u.invalidate()
				if !rerun {
					break
				}
			}
		}
	}
}

func v2LoadRoutingPrepared(u *v2DesktopUI) (v2RoutingPrepared, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status, err := u.core.IndexStatus(ctx)
	if err != nil {
		return v2RoutingPrepared{}, err
	}
	prefs, err := u.core.RoutingPreferences(ctx)
	if err != nil {
		return v2RoutingPrepared{}, err
	}
	targets, err := u.core.RoutingTargets(ctx)
	if err != nil {
		return v2RoutingPrepared{}, err
	}
	toolBatches, _ := u.core.PendingEnrichment(ctx, catalog.BatchToolEnrichment, 100)
	capBatches, _ := u.core.PendingEnrichment(ctx, catalog.BatchCapabilityReconciliation, 100)
	reviews, _ := u.core.PendingEnrichment(ctx, catalog.BatchAmbiguityReview, 100)
	hierarchy, hierarchyFound, _ := u.core.RoutingCapabilityHierarchy(ctx)

	sort.Slice(prefs.Profiles, func(i, j int) bool { return prefs.Profiles[i].Name < prefs.Profiles[j].Name })
	// Keep the prepared target slice in the same order used by the explorer so
	// search/filter changes are O(n) and do not re-sort thousands of tools on
	// the UI thread.
	sort.Slice(targets, func(i, j int) bool {
		if strings.EqualFold(targets[i].ToolName, targets[j].ToolName) {
			return targets[i].ServerID < targets[j].ServerID
		}
		return strings.ToLower(targets[i].ToolName) < strings.ToLower(targets[j].ToolName)
	})

	serverNames := map[string]string{}
	for _, entry := range u.core.Entries() {
		serverNames[entry.ID] = entry.Name
	}
	states := v2WorkspaceStates(prefs.Rules, toolBatches, capBatches)
	liveDrift := false
	for _, target := range targets {
		if target.AssumptionFingerprint != "" {
			continue
		}
		liveDrift = true
		key := v2RoutingTargetKey(target)
		item := states[key]
		if item.agent == "" && !item.needsReview && item.preference == "" {
			item.preference = "NEW · REFRESH INDEX"
		}
		states[key] = item
	}
	hierarchyUsable := hierarchyFound && !liveDrift

	prepared := v2RoutingPrepared{
		Status:          status,
		Prefs:           prefs,
		Targets:         targets,
		ToolBatches:     toolBatches,
		CapBatches:      capBatches,
		Reviews:         reviews,
		Hierarchy:       hierarchy,
		HierarchyUsable: hierarchyUsable,
		ServerNames:     serverNames,
		States:          states,
		ServerGroups:    v2ExplorerServerGroups(targets, serverNames),
		Counts:          v2ExplorerCounts(targets, states),
		UpdatedAt:       time.Now(),
	}
	if hierarchyUsable {
		prepared.CapabilityGroups = v2ExplorerCapabilityGroups(targets, hierarchy)
	}
	return prepared, nil
}

func v2CachedFilteredTargets(u *v2DesktopUI, prepared v2RoutingPrepared, group v2RoutingExplorerGroup, query string, attentionOnly bool) []coreapp.V2RoutingTarget {
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	key := fmt.Sprintf("%d|%s|%t|%s", prepared.Revision, group.Key, attentionOnly, normalizedQuery)
	state := v2AsyncStateFor(u)
	state.mu.Lock()
	if state.filterKey == key {
		filtered := state.filtered
		state.mu.Unlock()
		return filtered
	}
	state.mu.Unlock()

	filtered := make([]coreapp.V2RoutingTarget, 0, len(prepared.Targets))
	for _, target := range prepared.Targets {
		targetKey := v2RoutingTargetKey(target)
		if group.Key != "all" && !group.Members[targetKey] {
			continue
		}
		item := prepared.States[targetKey]
		if attentionOnly && !v2ExplorerNeedsAttention(target, item) {
			continue
		}
		if normalizedQuery != "" {
			serverName := prepared.ServerNames[target.ServerID]
			haystack := strings.ToLower(target.ToolName + " " + target.ServerID + " " + serverName + " " + item.agent + " " + item.preference)
			if !strings.Contains(haystack, normalizedQuery) {
				continue
			}
		}
		filtered = append(filtered, target)
	}

	state.mu.Lock()
	state.filterKey = key
	state.filtered = filtered
	state.mu.Unlock()
	return filtered
}
