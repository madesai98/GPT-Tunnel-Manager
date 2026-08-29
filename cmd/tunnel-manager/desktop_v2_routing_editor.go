//go:build !nogui

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
)

type v2RoutingTargetChoice struct {
	preferred     widget.Bool
	deprioritized widget.Bool
}

type v2RoutingRuleActions struct {
	confirm widget.Clickable
	remove  widget.Clickable
}

type v2RoutingProfileActions struct {
	makeDefault widget.Clickable
	remove      widget.Clickable
}

var v2RoutingEditorState struct {
	profileName        widget.Editor
	profileDescription widget.Editor
	addProfile         widget.Clickable
	clearDefault       widget.Clickable

	ruleProfileIndex int
	ruleProfileNext  widget.Clickable
	specificity      routingprefs.Specificity
	specificityNext  widget.Clickable
	subject          widget.Editor
	condition        widget.Editor
	addRule          widget.Clickable

	targetChoices  map[string]*v2RoutingTargetChoice
	ruleActions    map[string]*v2RoutingRuleActions
	profileActions map[string]*v2RoutingProfileActions
	scroll         layout.List
	initialized    bool
}

func ensureV2RoutingEditor() {
	if v2RoutingEditorState.initialized {
		return
	}
	v2RoutingEditorState.profileName.SingleLine = true
	v2RoutingEditorState.profileDescription.SingleLine = true
	v2RoutingEditorState.subject.SingleLine = true
	v2RoutingEditorState.condition.SingleLine = true
	v2RoutingEditorState.specificity = routingprefs.SpecificityToolSet
	v2RoutingEditorState.targetChoices = make(map[string]*v2RoutingTargetChoice)
	v2RoutingEditorState.ruleActions = make(map[string]*v2RoutingRuleActions)
	v2RoutingEditorState.profileActions = make(map[string]*v2RoutingProfileActions)
	v2RoutingEditorState.initialized = true
}

func v2RoutingProfileID(profiles []routingprefs.Profile) string {
	if v2RoutingEditorState.ruleProfileIndex <= 0 || v2RoutingEditorState.ruleProfileIndex > len(profiles) {
		return ""
	}
	return profiles[v2RoutingEditorState.ruleProfileIndex-1].ID
}

func v2RoutingProfileLabel(profiles []routingprefs.Profile) string {
	if v2RoutingEditorState.ruleProfileIndex <= 0 || v2RoutingEditorState.ruleProfileIndex > len(profiles) {
		return "Global"
	}
	return profiles[v2RoutingEditorState.ruleProfileIndex-1].Name
}

func v2RoutingTargetKey(target coreapp.V2RoutingTarget) string {
	return target.ServerID + "\x00" + target.ToolName
}

func v2RoutingEditorPage(u *v2DesktopUI, gtx layout.Context) layout.Dimensions {
	ensureV2RoutingEditor()
	ctx := context.Background()
	prefs, err := u.core.RoutingPreferences(ctx)
	if err != nil {
		return card(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, err.Error())(gtx) })(gtx)
	}
	targets, err := u.core.RoutingTargets(ctx)
	if err != nil {
		return card(func(gtx layout.Context) layout.Dimensions { return mutedCaption(u.th, err.Error())(gtx) })(gtx)
	}
	sort.Slice(prefs.Profiles, func(i, j int) bool { return prefs.Profiles[i].Name < prefs.Profiles[j].Name })
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ServerID == targets[j].ServerID {
			return targets[i].ToolName < targets[j].ToolName
		}
		return targets[i].ServerID < targets[j].ServerID
	})
	if v2RoutingEditorState.ruleProfileIndex > len(prefs.Profiles) {
		v2RoutingEditorState.ruleProfileIndex = 0
	}

	for v2RoutingEditorState.addProfile.Clicked(gtx) {
		name := strings.TrimSpace(v2RoutingEditorState.profileName.Text())
		description := strings.TrimSpace(v2RoutingEditorState.profileDescription.Text())
		expected := prefs.PreferenceRevision
		u.async("creating routing profile", func() error {
			_, err := u.core.PutRoutingProfile(context.Background(), expected, routingprefs.Profile{Name: name, Description: description})
			if err == nil {
				v2RoutingEditorState.profileName.SetText("")
				v2RoutingEditorState.profileDescription.SetText("")
			}
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
		v2RoutingEditorState.ruleProfileIndex++
		if v2RoutingEditorState.ruleProfileIndex > len(prefs.Profiles) {
			v2RoutingEditorState.ruleProfileIndex = 0
		}
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
	for v2RoutingEditorState.addRule.Clicked(gtx) {
		expected := prefs.PreferenceRevision
		spec := routingprefs.RuleSpec{
			ProfileID:   v2RoutingProfileID(prefs.Profiles),
			Specificity: v2RoutingEditorState.specificity,
			SubjectKey:  strings.TrimSpace(v2RoutingEditorState.subject.Text()),
			Condition:   strings.TrimSpace(v2RoutingEditorState.condition.Text()),
		}
		for _, target := range targets {
			choice := v2RoutingEditorState.targetChoices[v2RoutingTargetKey(target)]
			if choice == nil {
				continue
			}
			value := routingprefs.Target{ServerID: target.ServerID, ToolName: target.ToolName, AssumptionFingerprint: target.AssumptionFingerprint}
			if choice.preferred.Value {
				spec.Preferred = append(spec.Preferred, value)
			}
			if choice.deprioritized.Value {
				spec.Deprioritized = append(spec.Deprioritized, value)
			}
		}
		u.async("saving routing preference", func() error {
			_, err := u.core.PutRoutingRule(context.Background(), expected, spec)
			if err == nil {
				for _, choice := range v2RoutingEditorState.targetChoices {
					choice.preferred.Value = false
					choice.deprioritized.Value = false
				}
				v2RoutingEditorState.subject.SetText("")
				v2RoutingEditorState.condition.SetText("")
			}
			return err
		})
	}

	manager := u.core.ManagerConfig()
	v2RoutingEditorState.scroll.Axis = layout.Vertical
	return v2RoutingEditorState.scroll.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(v2RoutingProfilesCard(u, prefs, manager.Routing.DefaultProfile)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
			layout.Rigid(v2RoutingAddRuleCard(u, prefs, targets)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(12)}.Layout(gtx) }),
			layout.Rigid(v2RoutingRulesCard(u, prefs)),
		)
	})
}

func v2RoutingProfilesCard(u *v2DesktopUI, prefs coreapp.V2PreferenceSnapshot, defaultProfile string) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		rows := []layout.FlexChild{
			layout.Rigid(sectionTitle(u.th, fmt.Sprintf("Profiles · revision %d", prefs.PreferenceRevision))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2RoutingEditorState.profileName, "New profile name"))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, editorSurface(u.th, &v2RoutingEditorState.profileDescription, "Description (optional)"))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, primaryButton(u.th, &v2RoutingEditorState.addProfile, "Add Profile"))
			}),
		}
		defaultText := "Default profile: global"
		if defaultProfile != "" {
			defaultText = "Default profile: " + defaultProfile
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, mutedCaption(u.th, defaultText)),
					layout.Rigid(secondaryButton(u.th, &v2RoutingEditorState.clearDefault, "Use Global Default")),
				)
			})
		}))
		for _, profile := range prefs.Profiles {
			profile := profile
			actions := v2RoutingEditorState.profileActions[profile.ID]
			if actions == nil {
				actions = &v2RoutingProfileActions{}
				v2RoutingEditorState.profileActions[profile.ID] = actions
			}
			for actions.makeDefault.Clicked(gtx) {
				id := profile.ID
				u.async("setting default routing profile", func() error {
					cfg := u.core.ManagerConfig()
					cfg.Routing.DefaultProfile = id
					return u.core.SaveManager(context.Background(), cfg)
				})
			}
			for actions.remove.Clicked(gtx) {
				id, expected := profile.ID, prefs.PreferenceRevision
				u.async("deleting routing profile", func() error {
					_, err := u.core.DeleteRoutingProfile(context.Background(), expected, id)
					return err
				})
			}
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, mutedCaption(u.th, profile.Name+" — "+profile.Description)),
						layout.Rigid(secondaryButton(u.th, &actions.makeDefault, "Make Default")),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }),
						layout.Rigid(dangerButton(u.th, &actions.remove, "Delete")),
					)
				}))
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

func v2RoutingAddRuleCard(u *v2DesktopUI, prefs coreapp.V2PreferenceSnapshot, targets []coreapp.V2RoutingTarget) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		rows := []layout.FlexChild{
			layout.Rigid(sectionTitle(u.th, "Add preference")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2RoutingEditorState.ruleProfileNext, "Profile: "+v2RoutingProfileLabel(prefs.Profiles)))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(7)}.Layout(gtx, secondaryButton(u.th, &v2RoutingEditorState.specificityNext, "Specificity: "+strings.ToUpper(string(v2RoutingEditorState.specificity))))
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
		if len(targets) == 0 {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, mutedCaption(u.th, "Build an index first to make authoritative routing targets available."))
			}))
		} else {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, mutedCaption(u.th, "Authoritative targets:"))
			}))
			for _, target := range targets {
				target := target
				key := v2RoutingTargetKey(target)
				choice := v2RoutingEditorState.targetChoices[key]
				if choice == nil {
					choice = &v2RoutingTargetChoice{}
					v2RoutingEditorState.targetChoices[key] = choice
				}
				if choice.preferred.Value {
					choice.deprioritized.Value = false
				}
				if choice.deprioritized.Value {
					choice.preferred.Value = false
				}
				rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, mutedCaption(u.th, target.ServerID+" / "+target.ToolName)),
						layout.Rigid(material.CheckBox(u.th, &choice.preferred, "Preferred").Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
						layout.Rigid(material.CheckBox(u.th, &choice.deprioritized, "Deprioritized").Layout),
					)
				}))
			}
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(10)}.Layout(gtx, primaryButton(u.th, &v2RoutingEditorState.addRule, "Save Preference"))
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

func v2RoutingRulesCard(u *v2DesktopUI, prefs coreapp.V2PreferenceSnapshot) layout.Widget {
	return card(func(gtx layout.Context) layout.Dimensions {
		rows := []layout.FlexChild{layout.Rigid(sectionTitle(u.th, fmt.Sprintf("Preferences (%d)", len(prefs.Rules))))}
		if len(prefs.Rules) == 0 {
			rows = append(rows, layout.Rigid(mutedCaption(u.th, "No routing preferences configured.")))
		}
		for _, rule := range prefs.Rules {
			rule := rule
			actions := v2RoutingEditorState.ruleActions[rule.ID]
			if actions == nil {
				actions = &v2RoutingRuleActions{}
				v2RoutingEditorState.ruleActions[rule.ID] = actions
			}
			for actions.confirm.Clicked(gtx) {
				id, expected := rule.ID, prefs.PreferenceRevision
				u.async("confirming routing preference", func() error {
					_, err := u.core.ConfirmRoutingRule(context.Background(), expected, id)
					return err
				})
			}
			for actions.remove.Clicked(gtx) {
				id, expected := rule.ID, prefs.PreferenceRevision
				u.async("deleting routing preference", func() error {
					_, err := u.core.DeleteRoutingRule(context.Background(), expected, id)
					return err
				})
			}
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, compactCard(func(gtx layout.Context) layout.Dimensions {
					buttons := []layout.FlexChild{
						layout.Flexed(1, mutedCaption(u.th, fmt.Sprintf("%s · %s · %s", rule.Spec.SubjectKey, rule.Spec.Specificity, rule.ReviewState))),
					}
					if rule.ReviewState == routingprefs.ReviewNeedsReview {
						buttons = append(buttons,
							layout.Rigid(secondaryButton(u.th, &actions.confirm, "Confirm")),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(6)}.Layout(gtx) }),
						)
					}
					buttons = append(buttons, layout.Rigid(dangerButton(u.th, &actions.remove, "Delete")))
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, buttons...)
				}))
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}
