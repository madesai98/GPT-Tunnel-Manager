//go:build !nogui

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
)

type v2ReviewActions struct {
	preferenceIDs widget.Editor
	neutral       widget.Clickable
	preference    widget.Clickable
}

var v2IndexReviewState struct {
	scroll  layout.List
	actions map[string]*v2ReviewActions
}

func ensureV2ReviewActions(batchID string) *v2ReviewActions {
	if v2IndexReviewState.actions == nil {
		v2IndexReviewState.actions = make(map[string]*v2ReviewActions)
	}
	actions := v2IndexReviewState.actions[batchID]
	if actions == nil {
		actions = &v2ReviewActions{}
		actions.preferenceIDs.SingleLine = true
		v2IndexReviewState.actions[batchID] = actions
	}
	return actions
}

func v2BatchSummary(batch catalog.EnrichmentBatch) string {
	switch batch.Kind {
	case catalog.BatchToolEnrichment:
		var request enrichment.ToolBatchRequest
		if json.Unmarshal(batch.RequestJSON, &request) == nil {
			return fmt.Sprintf("Tool enrichment · %d tool item(s)", len(request.Items))
		}
		return "Tool enrichment"
	case catalog.BatchCapabilityReconciliation:
		var request enrichment.CapabilityBatchRequest
		if json.Unmarshal(batch.RequestJSON, &request) == nil {
			return fmt.Sprintf("Capability reconciliation · %d enriched tool item(s)", len(request.Items))
		}
		return "Capability reconciliation"
	case catalog.BatchAmbiguityReview:
		var request enrichment.AmbiguityReviewRequest
		if json.Unmarshal(batch.RequestJSON, &request) == nil {
			return request.Proposal.Summary
		}
		return "Ambiguity review"
	default:
		return string(batch.Kind)
	}
}

func v2IndexReviewPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	for u.indexRefresh.Clicked(gtx) {
		u.async("refreshing index", func() error { _, err := u.core.IndexRefresh(context.Background()); return err })
	}
	for u.indexCommit.Clicked(gtx) {
		u.async("committing index", func() error { _, err := u.core.IndexCommit(context.Background()); return err })
	}
	ctx := context.Background()
	status, err := u.core.IndexStatus(ctx)
	if err != nil {
		return card(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, err.Error())(gtx) })(gtx)
	}
	toolBatches, toolErr := u.core.PendingEnrichment(ctx, catalog.BatchToolEnrichment, 100)
	capabilityBatches, capabilityErr := u.core.PendingEnrichment(ctx, catalog.BatchCapabilityReconciliation, 100)
	reviews, reviewErr := u.core.PendingEnrichment(ctx, catalog.BatchAmbiguityReview, 100)
	if toolErr != nil {
		toolBatches = nil
	}
	if capabilityErr != nil {
		capabilityBatches = nil
	}
	if reviewErr != nil {
		reviews = nil
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			v2IndexReviewState.scroll.Axis = layout.Vertical
			return v2IndexReviewState.scroll.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				children := []layout.FlexChild{
					layout.Rigid(card(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(sectionTitle(u.th, "Routing catalog")),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, mutedCaption(u.th, fmt.Sprintf("Active generation: %s", status.ActiveGenerationID))) }),
							layout.Rigid(mutedCaption(u.th, fmt.Sprintf("Staging generation: %s", status.StagingGenerationID))),
							layout.Rigid(mutedCaption(u.th, fmt.Sprintf("Ready: %t · pending required: %d · open reviews: %d · accepted required: %d", status.Ready, status.PendingRequired, status.OpenReviews, status.AcceptedRequired))),
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
					})),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
					layout.Rigid(card(func(gtx layout.Context) layout.Dimensions {
						rows := []layout.FlexChild{
							layout.Rigid(sectionTitle(u.th, fmt.Sprintf("Agent enrichment blockers (%d)", len(toolBatches)+len(capabilityBatches)))),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(4)}.Layout(gtx, mutedCaption(u.th, "Required enrichment batches are intentionally completed by connected agents through the Manager MCP batch APIs. They block promotion until accepted.")) }),
						}
						for _, batch := range append(toolBatches, capabilityBatches...) {
							b := batch
							rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(mutedCaption(u.th, v2BatchSummary(b))),
										layout.Rigid(faintCaption(u.th, b.ID)),
									)
								}))
							}))
						}
						if len(toolBatches)+len(capabilityBatches) == 0 {
							rows = append(rows, layout.Rigid(mutedCaption(u.th, "No required enrichment blockers.")))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
					})),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
					layout.Rigid(card(func(gtx layout.Context) layout.Dimensions {
						rows := []layout.FlexChild{layout.Rigid(sectionTitle(u.th, fmt.Sprintf("Ambiguity Reviews (%d)", len(reviews))))}
						for _, batch := range reviews {
							b := batch
							actions := ensureV2ReviewActions(b.ID)
							var request enrichment.AmbiguityReviewRequest
							_ = json.Unmarshal(b.RequestJSON, &request)
							for actions.neutral.Clicked(gtx) {
								id := b.ID
								u.async("resolving ambiguity review", func() error {
									_, err := u.core.SubmitEnrichment(context.Background(), id, enrichment.AmbiguityReviewResponse{Resolution: enrichment.AmbiguityNeutral})
									return err
								})
							}
							for actions.preference.Clicked(gtx) {
								id := b.ID
								ids := strings.FieldsFunc(actions.preferenceIDs.Text(), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
								u.async("resolving ambiguity review with preferences", func() error {
									_, err := u.core.SubmitEnrichment(context.Background(), id, enrichment.AmbiguityReviewResponse{Resolution: enrichment.AmbiguityPreference, PreferenceIDs: ids})
									return err
								})
							}
							rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions {
									children := []layout.FlexChild{
										layout.Rigid(mutedCaption(u.th, v2BatchSummary(b))),
									}
									if len(request.Proposal.CompetingTools) != 0 {
										children = append(children, layout.Rigid(faintCaption(u.th, "Competing: "+strings.Join(request.Proposal.CompetingTools, ", "))))
									}
									children = append(children,
										layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &actions.preferenceIDs, "Preference IDs, separated by commas (optional)")) }),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
												return layout.Flex{}.Layout(gtx,
													layout.Rigid(secondaryButton(u.th, &actions.neutral, "Resolve Neutral")),
													layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(7)}.Layout(gtx) }),
													layout.Rigid(primaryButton(u.th, &actions.preference, "Resolve With Preferences")),
												)
											})
										}),
									)
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
								}))
							}))
						}
						if len(reviews) == 0 {
							rows = append(rows, layout.Rigid(mutedCaption(u.th, "No open Ambiguity Reviews.")))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
					})),
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
			})
		}),
	)
}
