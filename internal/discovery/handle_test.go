package discovery

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

func TestExecutionHandleRestartInvalidatesGetToolHandle(t *testing.T) {
	service, cat, prefs, _, provider := buildDiscoveryFixture(t)
	ctx := context.Background()
	search, err := service.Search(ctx, SearchInput{Query: "read filesystem file"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.GetTool(ctx, GetToolInput{ToolRef: search.Results[0].ToolRef})
	if err != nil {
		t.Fatal(err)
	}
	restartedHandles, err := executionhandle.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartedHandles.Validate(detail.ExecutionHandle); err == nil {
		t.Fatal("manager restart accepted a pre-restart execution handle")
	}
	restartedService, err := NewService(cat, provider, prefs, staticState{routingstate.Snapshot{RoutingStateHash: testRoutingHash}}, restartedHandles, Options{})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := restartedService.GetTool(ctx, GetToolInput{ToolRef: search.Results[0].ToolRef})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartedHandles.Validate(replacement.ExecutionHandle); err != nil {
		t.Fatalf("replacement handle invalid: %v", err)
	}
}

func TestFixtureExecutorClassIsAuthoritative(t *testing.T) {
	service, _, _, _, _ := buildDiscoveryFixture(t)
	result, err := service.Search(context.Background(), SearchInput{Query: "weather"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) == 0 || result.Results[0].ExecutorClass != toolcontract.ExecutorReadOnlyClosed {
		t.Fatalf("executor class = %#v", result.Results)
	}
}

func TestToolReferenceCannotCrossGeneration(t *testing.T) {
	service, _, _, _, _ := buildDiscoveryFixture(t)
	result, err := service.Search(context.Background(), SearchInput{Query: "weather"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := decodeToolReference(result.Results[0].ToolRef)
	if err != nil {
		t.Fatal(err)
	}
	claims.GenerationID = "another-generation"
	ref, err := encodeToolReference(claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetTool(context.Background(), GetToolInput{ToolRef: ref}); !errors.Is(err, ErrInvalidToolReference) {
		t.Fatalf("cross-generation reference error = %v", err)
	}
}

func TestFixtureHasExpectedServerNames(t *testing.T) {
	service, _, _, _, _ := buildDiscoveryFixture(t)
	result, err := service.Search(context.Background(), SearchInput{Query: "email inbox"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) == 0 || result.Results[0].ServerName != "Mail" {
		t.Fatalf("server context = %#v", result.Results)
	}
}

func ExampleSearchOutput() {
	fmt.Println("search_tools returns compact references; get_tool returns the authoritative contract and a handle")
	// Output: search_tools returns compact references; get_tool returns the authoritative contract and a handle
}
