package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

func TestSection18SearchQualityGates(t *testing.T) {
	service, cat, prefs, handles, provider := buildDiscoveryFixture(t)
	ctx := context.Background()

	corpusBody, err := os.ReadFile("testdata/phase7_search_eval.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Critical   []evalCase `json:"critical"`
		General    []evalCase `json:"general"`
		NoMatch    []string   `json:"no_match"`
		Preference []struct {
			Query   string `json:"query"`
			Context string `json:"context"`
			Want    string `json:"want"`
		} `json:"preference"`
	}
	if err := json.Unmarshal(corpusBody, &corpus); err != nil {
		t.Fatal(err)
	}
	critical := corpus.Critical
	criticalCorrect := 0
	for _, test := range critical {
		result, err := service.Search(ctx, SearchInput{Query: test.Query, Limit: 5})
		if err != nil {
			t.Fatalf("critical query %q: %v", test.Query, err)
		}
		if containsTool(result.Results, test.Want) {
			criticalCorrect++
		} else {
			t.Logf("critical top-5 miss: query=%q want=%s got=%v", test.Query, test.Want, resultToolNames(result.Results))
		}
	}
	criticalRate := float64(criticalCorrect) / float64(len(critical))
	if criticalRate != 1 {
		t.Errorf("critical must-route top-5 accuracy %.2f, want 1.00", criticalRate)
	}

	general := corpus.General
	top1Correct, top5Correct := 0, 0
	for _, test := range general {
		result, err := service.Search(ctx, SearchInput{Query: test.Query, Limit: 5})
		if err != nil {
			t.Fatalf("general query %q: %v", test.Query, err)
		}
		if len(result.Results) > 0 && result.Results[0].ToolName == test.Want {
			top1Correct++
		} else {
			t.Logf("general top-1 miss: query=%q want=%s got=%v", test.Query, test.Want, resultToolNames(result.Results))
		}
		if containsTool(result.Results, test.Want) {
			top5Correct++
		} else {
			t.Logf("general top-5 miss: query=%q want=%s got=%v", test.Query, test.Want, resultToolNames(result.Results))
		}
	}
	top1Rate := float64(top1Correct) / float64(len(general))
	top5Rate := float64(top5Correct) / float64(len(general))
	if top1Rate < 0.90 {
		t.Errorf("general top-1 accuracy %.3f, want >= 0.90", top1Rate)
	}
	if top5Rate < 0.98 {
		t.Errorf("general top-5 accuracy %.3f, want >= 0.98", top5Rate)
	}

	falsePositives := 0
	for _, query := range corpus.NoMatch {
		result, err := service.Search(ctx, SearchInput{Query: query, Limit: 5})
		if err != nil {
			t.Fatalf("no-match query %q: %v", query, err)
		}
		if len(result.Results) != 0 {
			falsePositives++
			t.Logf("no-match false positive: query=%q got=%v", query, resultToolNames(result.Results))
		}
	}
	falsePositiveRate := float64(falsePositives) / float64(len(corpus.NoMatch))
	if falsePositiveRate > 0.02 {
		t.Errorf("no-match false-positive rate %.3f, want <= 0.02", falsePositiveRate)
	}

	sources, err := cat.SourceTools(ctx, "gen_phase7")
	if err != nil {
		t.Fatal(err)
	}
	assumptions, err := routingprefs.CurrentAssumptions(sources)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := prefs.PutProfile(ctx, 0, routingprefs.Profile{Name: "evaluation-symbols"})
	if err != nil {
		t.Fatal(err)
	}
	preferred := routingprefs.Target{ServerID: "repo", ToolName: "search_symbols", AssumptionFingerprint: assumptions[routingprefs.TargetMapKey("repo", "search_symbols")]}
	deprioritized := routingprefs.Target{ServerID: "repo", ToolName: "search_code", AssumptionFingerprint: assumptions[routingprefs.TargetMapKey("repo", "search_code")]}
	if _, err := prefs.PutRule(ctx, profile.PreferenceRevision, routingprefs.RuleSpec{
		ProfileID: profile.ID, Specificity: routingprefs.SpecificityConditionalTool, SubjectKey: "code-search", Condition: "code lookup",
		Preferred: []routingprefs.Target{preferred}, Deprioritized: []routingprefs.Target{deprioritized},
	}); err != nil {
		t.Fatal(err)
	}
	profileService, err := NewService(cat, provider, prefs, staticState{snapshot: routingSnapshot()}, handles, Options{})
	if err != nil {
		t.Fatal(err)
	}
	preferenceCorrect := 0
	for _, test := range corpus.Preference {
		result, err := profileService.Search(ctx, SearchInput{Query: test.Query, Context: test.Context, RoutingProfile: profile.ID, Limit: 2})
		if err != nil {
			t.Fatalf("preference query %q: %v", test.Query, err)
		}
		if len(result.Results) > 0 && result.Results[0].ToolName == test.Want {
			preferenceCorrect++
		} else {
			t.Logf("preference miss: query=%q context=%q want=%s got=%v", test.Query, test.Context, test.Want, resultToolNames(result.Results))
		}
	}
	preferenceRate := float64(preferenceCorrect) / float64(len(corpus.Preference))
	if preferenceRate != 1 {
		t.Errorf("explicit Routing Preference adherence %.2f, want 1.00", preferenceRate)
	}

	safetyCases := []struct {
		annotations string
		Want        toolcontract.ExecutorClass
	}{
		{`{"readOnlyHint":true,"openWorldHint":false}`, toolcontract.ExecutorReadOnlyClosed},
		{`{"readOnlyHint":true,"openWorldHint":true}`, toolcontract.ExecutorReadOnlyOpen},
		{`{"readOnlyHint":false,"destructiveHint":false,"idempotentHint":false,"openWorldHint":false}`, toolcontract.ExecutorAdditiveClosed},
		{`{"readOnlyHint":false,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false}`, toolcontract.ExecutorAdditiveClosedIdempotent},
		{`{"readOnlyHint":false,"destructiveHint":false,"idempotentHint":false,"openWorldHint":true}`, toolcontract.ExecutorAdditiveOpen},
		{`{"readOnlyHint":false,"destructiveHint":false,"idempotentHint":true,"openWorldHint":true}`, toolcontract.ExecutorAdditiveOpenIdempotent},
		{`{"readOnlyHint":false,"destructiveHint":true,"idempotentHint":false,"openWorldHint":false}`, toolcontract.ExecutorDestructiveClosed},
		{`{"readOnlyHint":false,"destructiveHint":true,"idempotentHint":true,"openWorldHint":false}`, toolcontract.ExecutorDestructiveClosedIdempotent},
		{`{"readOnlyHint":false,"destructiveHint":true,"idempotentHint":false,"openWorldHint":true}`, toolcontract.ExecutorDestructiveOpen},
		{`{"readOnlyHint":false,"destructiveHint":true,"idempotentHint":true,"openWorldHint":true}`, toolcontract.ExecutorDestructiveOpenIdempotent},
	}
	for index, test := range safetyCases {
		contract := []byte(fmt.Sprintf(`{"name":"safety_%d","inputSchema":{"type":"object"},"annotations":%s}`, index, test.annotations))
		got, err := toolcontract.ExecutorClassForJSON(contract)
		if err != nil {
			t.Fatalf("safety case %d: %v", index, err)
		}
		if got != test.Want {
			t.Errorf("safety case %d = %s, want %s", index, got, test.Want)
		}
	}

	t.Logf("Phase 7 quality gates: critical top5 %.1f%%; general top1 %.1f%%; general top5 %.1f%%; no-match FP %.1f%%; explicit preferences %.1f%%; executor mapping 100%%",
		criticalRate*100, top1Rate*100, top5Rate*100, falsePositiveRate*100, preferenceRate*100)
}

type evalCase struct {
	Query string `json:"query"`
	Want  string `json:"want"`
}

func containsTool(results []SearchResult, name string) bool {
	for _, result := range results {
		if result.ToolName == name {
			return true
		}
	}
	return false
}

func resultToolNames(results []SearchResult) []string {
	names := make([]string, 0, len(results))
	for _, result := range results {
		names = append(names, result.ToolName)
	}
	return names
}

func routingSnapshot() routingstate.Snapshot {
	return routingstate.Snapshot{RoutingStateHash: testRoutingHash}
}
