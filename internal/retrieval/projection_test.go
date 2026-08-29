package retrieval

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProjectToolSeparatesSourceDescriptionAndInputSchema(t *testing.T) {
	first := &mcp.Tool{
		Name:        "weather_lookup",
		Description: "Find the current weather",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "City name"},
			},
		},
	}
	second := &mcp.Tool{
		Name:        "weather_lookup",
		Description: "Find the current weather",
		InputSchema: map[string]any{
			"properties": map[string]any{
				"city": map[string]any{"description": "City name", "type": "string"},
			},
			"type": "object",
		},
	}
	firstProjection, err := ProjectTool(first)
	if err != nil {
		t.Fatal(err)
	}
	secondProjection, err := ProjectTool(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstProjection.SourceDescription.Fingerprint != secondProjection.SourceDescription.Fingerprint || firstProjection.InputSchema.Fingerprint != secondProjection.InputSchema.Fingerprint {
		t.Fatalf("equivalent tool projections differ:\n%#v\n%#v", firstProjection, secondProjection)
	}
	if !strings.Contains(firstProjection.SourceDescription.Text, "weather_lookup") || !strings.Contains(firstProjection.SourceDescription.Text, "current weather") {
		t.Fatalf("description projection = %q", firstProjection.SourceDescription.Text)
	}
	if !strings.Contains(firstProjection.InputSchema.Text, "city") || !strings.Contains(firstProjection.Lexical.Text, "input_schema") {
		t.Fatalf("schema/lexical projection = %#v", firstProjection)
	}
}

func TestProjectToolDoesNotDereferenceRemoteSchemaReferences(t *testing.T) {
	tool := &mcp.Tool{
		Name: "remote_ref",
		InputSchema: map[string]any{
			"$ref": "https://example.invalid/schema.json",
		},
	}
	projection, err := ProjectTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projection.InputSchema.Text, "https://example.invalid/schema.json") {
		t.Fatalf("schema projection = %q", projection.InputSchema.Text)
	}
}
