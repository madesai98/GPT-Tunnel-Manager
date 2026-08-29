package executionrouter

import (
	"context"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

func TestExecuteRequiresValidCurrentGeneration(t *testing.T) {
	ctx := context.Background()
	c, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tracker, err := routingstate.NewTracker(routingstate.NewMemoryBackend(routingstate.Snapshot{RoutingStateHash: testRoutingHash}))
	if err != nil {
		t.Fatal(err)
	}
	handles, err := executionhandle.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	handle, err := handles.Mint(executionhandle.Claims{
		GenerationID:      "gen_missing",
		ServerID:          testServerID,
		ToolName:          "echo",
		SourceFingerprint: "sha256:missing",
		ExecutorClass:     string(toolcontract.ExecutorReadOnlyClosed),
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(c, tracker, handles, SessionProviderFunc(func(context.Context, string) (Session, error) {
		t.Fatal("downstream session acquisition must not occur without a valid current generation")
		return nil, nil
	}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, failure := service.Execute(ctx, toolcontract.ExecutorReadOnlyClosed, Input{ExecutionHandle: handle, Arguments: map[string]any{"text": "x"}})
	assertFailure(t, failure, CodeIndexRequired, OutcomeNotStarted, false)
}

func TestMalformedInputSchemaFailsClosedBeforeDispatch(t *testing.T) {
	tool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, map[string]any{
		"type": 42,
	})
	f := newFixture(t, tool, nil, 0)
	_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
	assertFailure(t, failure, CodeUnsupportedSchema, OutcomeNotStarted, false)
	assertNoCalls(t, f.session)
}
