//go:build !nogui

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
)

func v2WorkspaceAgentQueue(u *v2DesktopUI, toolBatches, capBatches []catalog.EnrichmentBatch) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		batches := append(append([]catalog.EnrichmentBatch{}, toolBatches...), capBatches...)
		rows := []layout.FlexChild{layout.Rigid(sectionTitle(u.th, fmt.Sprintf("Agent work (%d)", len(batches))))}
		if len(batches) == 0 {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, pill(u.th, "No agent blockers", uiSuccessSoft, uiSuccess))
			}))
		} else {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(5)}.Layout(gtx, mutedCaption(u.th, "The connected agent completes these through Manager MCP. You do not need to copy batch IDs or fill in enrichment manually."))
			}))
		}
		for _, batch := range batches {
			kind, tools := "Tool enrichment", []string{}
			if batch.Kind == catalog.BatchToolEnrichment {
				var r enrichment.ToolBatchRequest
				if json.Unmarshal(batch.RequestJSON, &r) == nil {
					for _, item := range r.Items {
						tools = append(tools, item.Tool.ToolName)
					}
				}
			} else {
				kind = "Capability grouping"
				var r enrichment.CapabilityBatchRequest
				if json.Unmarshal(batch.RequestJSON, &r) == nil {
					for _, item := range r.Items {
						tools = append(tools, item.Tool.ToolName)
					}
				}
			}
			text := strings.Join(tools, ", ")
			if len(text) > 110 {
				text = text[:110] + "…"
			}
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(pill(u.th, "AGENT", uiWarningSoft, uiWarning)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(7)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("%s · %d tools", kind, len(tools))))
						}))
					}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, faintCaption(u.th, text))
					}))
				}))
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

func v2WorkspaceReviews(u *v2DesktopUI, prefs coreapp.V2PreferenceSnapshot, reviews []catalog.EnrichmentBatch) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		rows := []layout.FlexChild{layout.Rigid(sectionTitle(u.th, fmt.Sprintf("Human reviews (%d)", len(reviews)))), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(5)}.Layout(gtx, mutedCaption(u.th, "Optional: ambiguity reviews never block promotion."))
		})}
		if len(reviews) == 0 {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, pill(u.th, "Nothing to review", uiSuccessSoft, uiSuccess))
			}))
		}
		for _, batch := range reviews {
			b, actions := batch, ensureV2ReviewActions(batch.ID)
			var request enrichment.AmbiguityReviewRequest
			_ = json.Unmarshal(b.RequestJSON, &request)
			matching := v2WorkspaceMatchingPreferences(request.Proposal.CompetingTools, prefs.Rules)
			for actions.neutral.Clicked(gtx) {
				id := b.ID
				u.async("resolving ambiguity review", func() error {
					_, err := u.core.SubmitEnrichment(context.Background(), id, enrichment.AmbiguityReviewResponse{Resolution: enrichment.AmbiguityNeutral})
					return err
				})
			}
			for actions.preference.Clicked(gtx) {
				if len(matching) > 0 {
					id, ids := b.ID, append([]string{}, matching...)
					u.async("resolving ambiguity review with preferences", func() error {
						_, err := u.core.SubmitEnrichment(context.Background(), id, enrichment.AmbiguityReviewResponse{Resolution: enrichment.AmbiguityPreference, PreferenceIDs: ids})
						return err
					})
				}
			}
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions {
					children := []layout.FlexChild{layout.Rigid(mutedCaption(u.th, request.Proposal.Summary)), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, faintCaption(u.th, "Competing: "+strings.Join(request.Proposal.CompetingTools, ", ")))
					}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							buttons := []layout.FlexChild{layout.Rigid(secondaryButton(u.th, &actions.neutral, "Keep neutral"))}
							if len(matching) > 0 {
								buttons = append(buttons, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(5)}.Layout(gtx) }), layout.Rigid(primaryButton(u.th, &actions.preference, fmt.Sprintf("Use %d preference(s)", len(matching)))))
							}
							return layout.Flex{}.Layout(gtx, buttons...)
						})
					})}
					if len(matching) == 0 {
						children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(5)}.Layout(gtx, faintCaption(u.th, "Create a matching preference first if you want a non-neutral resolution."))
						}))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
				}))
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

func v2WorkspaceMatchingPreferences(competing []string, rules []routingprefs.Rule) []string {
	set := map[string]bool{}
	for _, member := range competing {
		if slash := strings.Index(member, "/"); slash >= 0 {
			set[member[:slash]+"\x00"+member[slash+1:]] = true
		}
	}
	var ids []string
	for _, rule := range rules {
		if rule.ReviewState != routingprefs.ReviewActive {
			continue
		}
		matched := false
		for _, target := range append(append([]routingprefs.Target{}, rule.Spec.Preferred...), rule.Spec.Deprioritized...) {
			if set[target.ServerID+"\x00"+target.ToolName] {
				matched = true
				break
			}
		}
		if matched {
			ids = append(ids, rule.ID)
		}
	}
	return ids
}
