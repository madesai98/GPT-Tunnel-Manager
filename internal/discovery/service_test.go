package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
)

func TestSearchGetToolAndExecutionHandle(t *testing.T) {
	service, _, _, handles, _ := buildDiscoveryFixture(t)
	ctx := context.Background()
	search, err := service.Search(ctx, SearchInput{Query: "weather forecast and rain", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) == 0 || search.Results[0].ToolName != "forecast" {
		t.Fatalf("search results = %#v", search.Results)
	}
	if search.Results[0].ToolRef == "" {
		t.Fatal("search result omitted tool reference")
	}
	detail, err := service.GetTool(ctx, GetToolInput{ToolRef: search.Results[0].ToolRef})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Authoritative.InvocationIdentity.ServerID != "weather" || detail.Authoritative.InvocationIdentity.ToolName != "forecast" {
		t.Fatalf("authoritative identity = %#v", detail.Authoritative.InvocationIdentity)
	}
	if detail.Derived.SemanticGuidance == nil || !strings.Contains(detail.Derived.SemanticGuidance.Purpose, "weather") {
		t.Fatalf("derived guidance = %#v", detail.Derived.SemanticGuidance)
	}
	if detail.ExecutionHandle == "" || detail.HumanIdentity.Display == "" {
		t.Fatalf("detail output = %#v", detail)
	}
	claims, err := handles.Validate(detail.ExecutionHandle)
	if err != nil {
		t.Fatal(err)
	}
	if claims.GenerationID != "gen_phase7" || claims.ServerID != "weather" || claims.ToolName != "forecast" || claims.SourceFingerprint != detail.Authoritative.SourceFingerprint || claims.ExecutorClass != string(detail.Authoritative.ExecutorClass) {
		t.Fatalf("handle claims = %#v", claims)
	}
	var sourceContract map[string]any
	if err := json.Unmarshal(detail.Authoritative.Tool, &sourceContract); err != nil {
		t.Fatal(err)
	}
	if sourceContract["description"] != "Get a weather forecast including rain and temperature." {
		t.Fatalf("authoritative description was rewritten: %#v", sourceContract["description"])
	}
}

func TestSearchCanReturnZeroResultsAndFailsClosedWithoutCurrentIndex(t *testing.T) {
	service, cat, prefs, handles, provider := buildDiscoveryFixture(t)
	ctx := context.Background()
	noMatch, err := service.Search(ctx, SearchInput{Query: "quasar neutron transmutation lattice", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(noMatch.Results) != 0 {
		t.Fatalf("no-match query returned %#v", noMatch.Results)
	}
	stale, err := NewService(cat, provider, prefs, staticState{routingstate.Snapshot{RoutingStateHash: "sha256:different"}}, handles, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Search(ctx, SearchInput{Query: "weather"}); !errors.Is(err, ErrIndexRequired) {
		t.Fatalf("stale index search error = %v", err)
	}
}

func TestExplicitProfilePrecedenceAndMissingProfile(t *testing.T) {
	service, cat, prefs, handles, provider := buildDiscoveryFixture(t)
	ctx := context.Background()
	sources, err := cat.SourceTools(ctx, "gen_phase7")
	if err != nil {
		t.Fatal(err)
	}
	assumptions, err := routingprefs.CurrentAssumptions(sources)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := prefs.PutProfile(ctx, 0, routingprefs.Profile{Name: "symbols-first", Description: "Prefer symbol search for code lookup"})
	if err != nil {
		t.Fatal(err)
	}
	preferred := routingprefs.Target{ServerID: "repo", ToolName: "search_symbols", AssumptionFingerprint: assumptions[routingprefs.TargetMapKey("repo", "search_symbols")]}
	deprioritized := routingprefs.Target{ServerID: "repo", ToolName: "search_code", AssumptionFingerprint: assumptions[routingprefs.TargetMapKey("repo", "search_code")]}
	if _, err := prefs.PutRule(ctx, profile.PreferenceRevision, routingprefs.RuleSpec{
		ProfileID: profile.ID, Specificity: routingprefs.SpecificityConditionalTool, SubjectKey: "code-search", Condition: "looking for code",
		Preferred: []routingprefs.Target{preferred}, Deprioritized: []routingprefs.Target{deprioritized},
	}); err != nil {
		t.Fatal(err)
	}
	profileService, err := NewService(cat, provider, prefs, staticState{routingstate.Snapshot{RoutingStateHash: testRoutingHash}}, handles, Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := profileService.Search(ctx, SearchInput{Query: "search source code repositories", Context: "looking for code", RoutingProfile: "symbols-first", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) < 2 || result.Results[0].ToolName != "search_symbols" {
		t.Fatalf("profile did not win ranking: %#v", result.Results)
	}
	if result.EffectiveProfile == nil || result.EffectiveProfile.ID != profile.ID {
		t.Fatalf("effective profile = %#v", result.EffectiveProfile)
	}
	if _, err := profileService.Search(ctx, SearchInput{Query: "code", RoutingProfile: "missing-profile"}); !errors.Is(err, ErrRoutingProfileNotFound) {
		t.Fatalf("missing explicit profile error = %v", err)
	}
}

func TestDefaultProfileIsUsedOnlyWhenNoExplicitProfile(t *testing.T) {
	_, cat, prefs, handles, provider := buildDiscoveryFixture(t)
	ctx := context.Background()
	profile, err := prefs.PutProfile(ctx, 0, routingprefs.Profile{Name: "default-routing"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(cat, provider, prefs, staticState{routingstate.Snapshot{RoutingStateHash: testRoutingHash}}, handles, Options{DefaultProfile: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(ctx, SearchInput{Query: "calendar meeting"})
	if err != nil {
		t.Fatal(err)
	}
	if result.EffectiveProfile == nil || result.EffectiveProfile.ID != profile.ID {
		t.Fatalf("default profile = %#v", result.EffectiveProfile)
	}
	if _, err := service.Search(ctx, SearchInput{Query: "calendar", RoutingProfile: "does-not-exist"}); !errors.Is(err, ErrRoutingProfileNotFound) {
		t.Fatalf("explicit missing profile silently fell back: %v", err)
	}
}
