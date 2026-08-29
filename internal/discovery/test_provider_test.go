package discovery

import (
	"context"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
)

const testRoutingHash = "sha256:phase7-routing"

type staticState struct{ snapshot routingstate.Snapshot }

func (s staticState) Snapshot(context.Context) (routingstate.Snapshot, error) { return s.snapshot, nil }

type semanticProvider struct{ identity embedding.Identity }

func newSemanticProvider() *semanticProvider {
	dimensions := 8
	return &semanticProvider{identity: embedding.Identity{
		Provider: "test", BaseURL: "https://embedding.invalid/v1", Model: "phase7", Dimensions: &dimensions, Protocol: embedding.IdentityVersion,
	}}
}

func (p *semanticProvider) Identity() embedding.Identity { return p.identity }

func (p *semanticProvider) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	vectors := make([][]float32, len(inputs))
	for index, input := range inputs {
		text := strings.ToLower(input)
		dimension := 6
		switch {
		case containsAny(text, "symbol", "symbols", "function", "functions", "type", "types", "named"):
			dimension = 7
		case containsAny(text, "issue", "ticket", "bug"):
			dimension = 1
		case containsAny(text, "code", "repository", "repositories", "repo"):
			dimension = 0
		case containsAny(text, "calendar", "event", "meeting", "schedule"):
			dimension = 2
		case containsAny(text, "email", "mail", "message", "inbox"):
			dimension = 3
		case containsAny(text, "weather", "forecast", "rain", "temperature"):
			dimension = 4
		case containsAny(text, "file", "filesystem", "path", "document"):
			dimension = 5
		}
		vectors[index] = basisVector(8, dimension)
	}
	return vectors, nil
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

type fixtureTool struct {
	serverID    string
	serverName  string
	name        string
	title       string
	description string
	guidance    enrichment.ToolGuidance
	dimension   int
}

func phase7Tools() []fixtureTool {
	return []fixtureTool{
		{serverID: "repo", serverName: "Repository", name: "search_code", title: "Search code", description: "Search source code in repositories.", guidance: enrichment.ToolGuidance{Purpose: "Find source code in repositories.", UseWhen: []string{"Locate code by concept or text."}, Examples: []string{"Find authentication code."}, Capabilities: []string{"code search"}}, dimension: 0},
		{serverID: "repo", serverName: "Repository", name: "search_symbols", title: "Search symbols", description: "Search source code in repositories.", guidance: enrichment.ToolGuidance{Purpose: "Find named code symbols in repositories.", UseWhen: []string{"Locate functions, types, or symbols."}, Examples: []string{"Find the Router type."}, Capabilities: []string{"code search"}}, dimension: 0},
		{serverID: "repo", serverName: "Repository", name: "create_issue", title: "Create issue", description: "Create a repository issue for a bug or task.", guidance: enrichment.ToolGuidance{Purpose: "Create an issue or bug ticket.", Examples: []string{"Open a bug report."}, Capabilities: []string{"issue tracking"}}, dimension: 1},
		{serverID: "calendar", serverName: "Calendar", name: "create_event", title: "Create event", description: "Create a calendar event or meeting.", guidance: enrichment.ToolGuidance{Purpose: "Schedule a calendar event or meeting.", Capabilities: []string{"calendar"}}, dimension: 2},
		{serverID: "mail", serverName: "Mail", name: "search_messages", title: "Search messages", description: "Search email messages in the inbox.", guidance: enrichment.ToolGuidance{Purpose: "Find email and inbox messages.", Capabilities: []string{"email search"}}, dimension: 3},
		{serverID: "weather", serverName: "Weather", name: "forecast", title: "Weather forecast", description: "Get a weather forecast including rain and temperature.", guidance: enrichment.ToolGuidance{Purpose: "Read weather forecasts and temperature.", Capabilities: []string{"weather"}}, dimension: 4},
		{serverID: "files", serverName: "Files", name: "read_file", title: "Read file", description: "Read a file from a filesystem path.", guidance: enrichment.ToolGuidance{Purpose: "Read a local file by filesystem path.", Capabilities: []string{"file read"}}, dimension: 5},
	}
}

func basisVector(size, dimension int) []float32 {
	vector := make([]float32, size)
	vector[dimension] = 1
	return vector
}
