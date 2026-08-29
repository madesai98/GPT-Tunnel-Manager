package routingprefs

import (
	"context"
	"errors"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

func TestPreferenceRevisionPrecedenceConflictsAndReconciliation(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	if _, err := cat.DB().ExecContext(ctx, `UPDATE routing_state SET routing_state_hash = 'sha256:routing' WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(cat)
	if err != nil {
		t.Fatal(err)
	}

	profileWrite, err := store.PutProfile(ctx, 0, Profile{Name: "coding", Description: "coding work"})
	if err != nil || !profileWrite.Changed || profileWrite.PreferenceRevision != 1 {
		t.Fatalf("profile write = %#v, %v", profileWrite, err)
	}
	if repeat, err := store.PutProfile(ctx, 0, Profile{ID: profileWrite.ID, Name: "coding", Description: "coding work"}); err != nil || repeat.Changed || repeat.PreferenceRevision != 1 {
		t.Fatalf("idempotent profile repeat = %#v, %v", repeat, err)
	}
	if _, err := store.PutProfile(ctx, 0, Profile{ID: profileWrite.ID, Name: "coding", Description: "changed"}); !errors.Is(err, ErrPreferenceConflict) {
		t.Fatalf("stale profile write = %v", err)
	}

	sources := []catalog.SourceToolRecord{
		{ServerID: "srv", ToolName: "a", SourceFingerprint: "ignored-a", ContractJSON: []byte(`{"name":"a","description":"alpha","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true,"openWorldHint":false}}`)},
		{ServerID: "srv", ToolName: "b", SourceFingerprint: "ignored-b", ContractJSON: []byte(`{"name":"b","description":"beta","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true,"openWorldHint":false}}`)},
	}
	assumptions, err := CurrentAssumptions(sources)
	if err != nil {
		t.Fatal(err)
	}
	a := Target{ServerID: "srv", ToolName: "a", AssumptionFingerprint: assumptions[TargetMapKey("srv", "a")]}
	b := Target{ServerID: "srv", ToolName: "b", AssumptionFingerprint: assumptions[TargetMapKey("srv", "b")]}

	global, err := store.PutRule(ctx, 1, RuleSpec{Specificity: SpecificityServer, SubjectKey: "srv", Preferred: []Target{a}})
	if err != nil || global.PreferenceRevision != 2 || global.NeedsReview {
		t.Fatalf("global preference = %#v, %v", global, err)
	}
	profileRule, err := store.PutRule(ctx, 2, RuleSpec{ProfileID: profileWrite.ID, Specificity: SpecificityConditionalTool, SubjectKey: "code-search", Condition: "when looking for code", Preferred: []Target{b}})
	if err != nil || profileRule.PreferenceRevision != 3 || profileRule.NeedsReview {
		t.Fatalf("profile preference = %#v, %v", profileRule, err)
	}
	effective, err := store.EffectiveRules(ctx, profileWrite.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective) != 2 || effective[0].ID != profileRule.ID || effective[1].ID != global.ID {
		t.Fatalf("effective precedence = %#v", effective)
	}

	firstConflict, err := store.PutRule(ctx, 3, RuleSpec{Specificity: SpecificityToolSet, SubjectKey: "searchers", Preferred: []Target{a}})
	if err != nil || firstConflict.NeedsReview {
		t.Fatalf("first conflict slot = %#v, %v", firstConflict, err)
	}
	secondConflict, err := store.PutRule(ctx, 4, RuleSpec{Specificity: SpecificityToolSet, SubjectKey: "searchers", Preferred: []Target{b}})
	if err != nil || !secondConflict.NeedsReview || secondConflict.PreferenceRevision != 5 {
		t.Fatalf("second conflict slot = %#v, %v", secondConflict, err)
	}
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]ReviewState{}
	for _, rule := range rules {
		states[rule.ID] = rule.ReviewState
	}
	if states[firstConflict.ID] != ReviewNeedsReview || states[secondConflict.ID] != ReviewNeedsReview {
		t.Fatalf("equal-scope conflicts were not both marked needs_review: %#v", states)
	}

	changedSources := append([]catalog.SourceToolRecord(nil), sources...)
	changedSources[0].ContractJSON = []byte(`{"name":"a","description":"alpha changed","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true,"openWorldHint":false}}`)
	changedAssumptions, err := CurrentAssumptions(changedSources)
	if err != nil {
		t.Fatal(err)
	}
	stale, revision, err := store.ReconcileTargets(ctx, changedAssumptions)
	if err != nil {
		t.Fatal(err)
	}
	if revision != 6 || len(stale) != 1 || stale[0] != global.ID {
		t.Fatalf("reconcile = stale:%v revision:%d", stale, revision)
	}
	var routingHash string
	if err := cat.DB().QueryRowContext(ctx, `SELECT routing_state_hash FROM routing_state WHERE singleton = 1`).Scan(&routingHash); err != nil {
		t.Fatal(err)
	}
	if routingHash != "sha256:routing" {
		t.Fatalf("preference writes changed routing state hash to %q", routingHash)
	}
}

func TestPreferenceAssumptionTracksSemanticSourceAndExecutorClass(t *testing.T) {
	base := catalog.SourceToolRecord{ServerID: "srv", ToolName: "tool", ContractJSON: []byte(`{"name":"tool","description":"read data","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true,"openWorldHint":false},"_meta":{"x":1}}`)}
	metadataOnly := base
	metadataOnly.ContractJSON = []byte(`{"name":"tool","description":"read data","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true,"openWorldHint":false},"_meta":{"x":2}}`)
	reclassified := base
	reclassified.ContractJSON = []byte(`{"name":"tool","description":"read data","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":false,"destructiveHint":true,"openWorldHint":false}}`)
	baseMap, err := CurrentAssumptions([]catalog.SourceToolRecord{base})
	if err != nil {
		t.Fatal(err)
	}
	metadataMap, err := CurrentAssumptions([]catalog.SourceToolRecord{metadataOnly})
	if err != nil {
		t.Fatal(err)
	}
	reclassifiedMap, err := CurrentAssumptions([]catalog.SourceToolRecord{reclassified})
	if err != nil {
		t.Fatal(err)
	}
	key := TargetMapKey("srv", "tool")
	if baseMap[key] != metadataMap[key] {
		t.Fatal("_meta-only change altered preference assumption")
	}
	if baseMap[key] == reclassifiedMap[key] {
		t.Fatal("executor reclassification did not alter preference assumption")
	}
	class, err := toolcontract.ExecutorClassForJSON(base.ContractJSON)
	if err != nil || class != toolcontract.ExecutorReadOnlyClosed {
		t.Fatalf("base class = %s, %v", class, err)
	}
}
