package mcpmanager

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type getRoutingPreferencesInput struct {
	ProfileID     string `json:"profile_id,omitempty" jsonschema:"Optional Routing Profile ID. When set with effective_only, return active Global rules plus active rules from this profile in deterministic precedence order."`
	EffectiveOnly bool   `json:"effective_only,omitempty" jsonschema:"Return only active rules effective for profile_id instead of every stored rule, including needs-review entries."`
}

type getRoutingPreferencesOutput struct {
	PreferenceRevision uint64                 `json:"preference_revision"`
	Profiles           []routingprefs.Profile `json:"profiles,omitempty"`
	Rules              []routingprefs.Rule    `json:"rules,omitempty"`
	Error              *toolError             `json:"error,omitempty"`
}

type routingPreferenceOperation string

const (
	preferencePutRule      routingPreferenceOperation = "put_rule"
	preferenceConfirmRule  routingPreferenceOperation = "confirm_rule"
	preferenceDeleteRule   routingPreferenceOperation = "delete_rule"
	preferencePutProfile   routingPreferenceOperation = "put_profile"
	preferenceDeleteProfile routingPreferenceOperation = "delete_profile"
)

type routingProfileInput struct {
	ID          string `json:"profile_id,omitempty" jsonschema:"Stable profile ID when updating an existing profile. Omit when creating a profile so the canonical identity is derived from its name."`
	Name        string `json:"name" jsonschema:"Unique human-readable Routing Profile name."`
	Description string `json:"description,omitempty" jsonschema:"Optional human-readable profile description."`
}

type setRoutingPreferencesInput struct {
	ExpectedPreferenceRevision uint64                     `json:"expected_preference_revision" jsonschema:"Optimistic preference revision. Stale writes return preference_conflict. Identical writes are no-ops even when this revision is stale."`
	Operation                  routingPreferenceOperation `json:"operation" jsonschema:"Mutation: put_rule, confirm_rule, delete_rule, put_profile, or delete_profile."`
	Rule                       *routingprefs.RuleSpec      `json:"rule,omitempty" jsonschema:"Required for put_rule. Targets use immutable server/tool identities and assumption fingerprints."`
	PreferenceID               string                     `json:"preference_id,omitempty" jsonschema:"Required for confirm_rule or delete_rule."`
	Profile                    *routingProfileInput       `json:"profile,omitempty" jsonschema:"Required for put_profile."`
	ProfileID                   string                     `json:"profile_id,omitempty" jsonschema:"Required for delete_profile."`
}

type setRoutingPreferencesOutput struct {
	Result *routingprefs.WriteResult `json:"result,omitempty"`
	Error  *toolError                `json:"error,omitempty"`
}

func registerV2PreferenceTools(server *mcp.Server, store *routingprefs.Store) {
	closedWorld := false
	nondestructive := false
	destructive := true

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_routing_preferences",
		Title:       "Get routing preferences",
		Description: "Read the current preference revision, Routing Profiles, and Global/profile-scoped routing guidance without changing semantic index generations.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Get routing preferences",
			ReadOnlyHint:    true,
			DestructiveHint: &nondestructive,
			IdempotentHint:  true,
			OpenWorldHint:   &closedWorld,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input getRoutingPreferencesInput) (*mcp.CallToolResult, getRoutingPreferencesOutput, error) {
		if store == nil {
			return preferenceGetFailure(errors.New("manager_preferences_unavailable"))
		}
		revision, err := store.Revision(ctx)
		if err != nil {
			return preferenceGetFailure(err)
		}
		profiles, err := store.ListProfiles(ctx)
		if err != nil {
			return preferenceGetFailure(err)
		}
		profileID := strings.TrimSpace(input.ProfileID)
		if profileID != "" && !profileExists(profiles, profileID) {
			return preferenceGetFailure(errors.New("routing_profile_not_found"))
		}
		var rules []routingprefs.Rule
		if input.EffectiveOnly {
			rules, err = store.EffectiveRules(ctx, profileID)
		} else {
			rules, err = store.ListRules(ctx)
		}
		if err != nil {
			return preferenceGetFailure(err)
		}
		return nil, getRoutingPreferencesOutput{PreferenceRevision: revision, Profiles: profiles, Rules: rules}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_routing_preferences",
		Title:       "Set routing preferences",
		Description: "Create, confirm, or delete Global/profile routing guidance and Routing Profiles using canonical identities and optimistic expected_preference_revision semantics. Equal-scope conflicts are marked needs_review rather than resolved newest-wins.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Set routing preferences",
			ReadOnlyHint:    false,
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &closedWorld,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input setRoutingPreferencesInput) (*mcp.CallToolResult, setRoutingPreferencesOutput, error) {
		if store == nil {
			return preferenceSetFailure(errors.New("manager_preferences_unavailable"))
		}
		var (
			result routingprefs.WriteResult
			err    error
		)
		switch input.Operation {
		case preferencePutRule:
			if input.Rule == nil {
				return preferenceSetFailure(errors.New("put_rule requires rule"))
			}
			result, err = store.PutRule(ctx, input.ExpectedPreferenceRevision, *input.Rule)
		case preferenceConfirmRule:
			if strings.TrimSpace(input.PreferenceID) == "" {
				return preferenceSetFailure(errors.New("confirm_rule requires preference_id"))
			}
			result, err = store.ConfirmRule(ctx, input.ExpectedPreferenceRevision, input.PreferenceID)
		case preferenceDeleteRule:
			if strings.TrimSpace(input.PreferenceID) == "" {
				return preferenceSetFailure(errors.New("delete_rule requires preference_id"))
			}
			result, err = store.DeleteRule(ctx, input.ExpectedPreferenceRevision, input.PreferenceID)
		case preferencePutProfile:
			if input.Profile == nil || strings.TrimSpace(input.Profile.Name) == "" {
				return preferenceSetFailure(errors.New("put_profile requires profile.name"))
			}
			result, err = store.PutProfile(ctx, input.ExpectedPreferenceRevision, routingprefs.Profile{
				ID:          input.Profile.ID,
				Name:        input.Profile.Name,
				Description: input.Profile.Description,
			})
		case preferenceDeleteProfile:
			if strings.TrimSpace(input.ProfileID) == "" {
				return preferenceSetFailure(errors.New("delete_profile requires profile_id"))
			}
			result, err = store.DeleteProfile(ctx, input.ExpectedPreferenceRevision, input.ProfileID)
		default:
			return preferenceSetFailure(errors.New("unsupported routing preference operation"))
		}
		if err != nil {
			return preferenceSetFailure(err)
		}
		return nil, setRoutingPreferencesOutput{Result: &result}, nil
	})
}

func profileExists(profiles []routingprefs.Profile, profileID string) bool {
	for _, profile := range profiles {
		if profile.ID == profileID {
			return true
		}
	}
	return false
}

func preferenceGetFailure(err error) (*mcp.CallToolResult, getRoutingPreferencesOutput, error) {
	return &mcp.CallToolResult{IsError: true}, getRoutingPreferencesOutput{Error: stablePreferenceError(err)}, nil
}

func preferenceSetFailure(err error) (*mcp.CallToolResult, setRoutingPreferencesOutput, error) {
	return &mcp.CallToolResult{IsError: true}, setRoutingPreferencesOutput{Error: stablePreferenceError(err)}, nil
}

func stablePreferenceError(err error) *toolError {
	result := &toolError{Code: "invalid_request", Message: err.Error(), Retryable: false}
	var conflict *routingprefs.ConflictError
	switch {
	case errors.As(err, &conflict), errors.Is(err, routingprefs.ErrPreferenceConflict):
		result.Code = "preference_conflict"
	case errors.Is(err, sql.ErrNoRows):
		result.Code = "preference_not_found"
	case strings.Contains(err.Error(), "routing profile") && strings.Contains(err.Error(), "not found"), err.Error() == "routing_profile_not_found":
		result.Code = "routing_profile_not_found"
	case err.Error() == "manager_preferences_unavailable":
		result.Code = "manager_unavailable"
		result.Retryable = true
	}
	return result
}
