package downstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolListChangedRefreshesDynamicInventoryWithoutInvalidatingSession(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "dynamic-tool-test", Version: "1"}, nil)
	server.AddTool(dynamicTestTool("alpha"), dynamicTestHandler("alpha"))

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()

	factory, err := NewFactory(Options{Secrets: newTestSecretStore()})
	if err != nil {
		t.Fatal(err)
	}
	entry := externalHTTPServer("srv_40000000000000000000000000000002", httpServer.URL)
	session, err := factory.Connect(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())

	if !session.SupportsToolListChanged() {
		t.Fatal("dynamic test server did not advertise tools/list_changed support")
	}
	if got := session.CurrentTools(); len(got.Tools) != 1 || got.Tools[0].Name != "alpha" {
		t.Fatalf("initial tools = %#v, want alpha", got.Tools)
	}

	server.AddTool(dynamicTestTool("beta"), dynamicTestHandler("beta"))

	select {
	case <-session.ToolChanges():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for refreshed dynamic tool inventory")
	}

	current := session.CurrentTools()
	if len(current.Tools) != 2 {
		t.Fatalf("refreshed tool count = %d, want 2", len(current.Tools))
	}
	if session.ToolContractChanged() {
		t.Fatal("tools/list_changed incorrectly invalidated the healthy downstream session")
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "beta", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(beta) after tools/list_changed: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("beta result content length = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "beta" {
		t.Fatalf("beta result = %#v, want beta", result.Content[0])
	}
}

func dynamicTestTool(name string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: "dynamic test tool " + name,
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func dynamicTestHandler(text string) mcp.ToolHandler {
	return func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}
