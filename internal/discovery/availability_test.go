package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTemporaryToolAvailabilityDoesNotRequireReindex(t *testing.T) {
	service, cat, _, _, _ := buildDiscoveryFixture(t)
	ctx := context.Background()

	baseline, err := service.Search(ctx, SearchInput{Query: "weather forecast and rain", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Results) == 0 || baseline.Results[0].ToolName != "forecast" {
		t.Fatalf("baseline results = %#v", baseline.Results)
	}
	ref := baseline.Results[0].ToolRef

	change, err := cat.ObserveServerTools(ctx, "weather", nil)
	if err != nil {
		t.Fatal(err)
	}
	if change.SemanticChanged || !change.AvailabilityChanged {
		t.Fatalf("disappearance = %#v", change)
	}
	hidden, err := service.Search(ctx, SearchInput{Query: "weather forecast and rain", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range hidden.Results {
		if result.ToolName == "forecast" {
			t.Fatalf("unavailable forecast remained discoverable: %#v", hidden.Results)
		}
	}
	if _, err := service.GetTool(ctx, GetToolInput{ToolRef: ref}); !errors.Is(err, ErrInvalidToolReference) {
		t.Fatalf("unavailable tool get error = %v", err)
	}

	sources, err := cat.SourceTools(ctx, "gen_phase7")
	if err != nil {
		t.Fatal(err)
	}
	var forecast *mcp.Tool
	for _, source := range sources {
		if source.ServerID != "weather" || source.ToolName != "forecast" {
			continue
		}
		var tool mcp.Tool
		if err := json.Unmarshal(source.ContractJSON, &tool); err != nil {
			t.Fatal(err)
		}
		forecast = &tool
		break
	}
	if forecast == nil {
		t.Fatal("fixture forecast source not found")
	}
	change, err = cat.ObserveServerTools(ctx, "weather", []*mcp.Tool{forecast})
	if err != nil {
		t.Fatal(err)
	}
	if change.SemanticChanged || !change.AvailabilityChanged {
		t.Fatalf("reappearance = %#v", change)
	}
	restored, err := service.Search(ctx, SearchInput{Query: "weather forecast and rain", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Results) == 0 || restored.Results[0].ToolName != "forecast" {
		t.Fatalf("reappeared unchanged tool not restored: %#v", restored.Results)
	}
}
