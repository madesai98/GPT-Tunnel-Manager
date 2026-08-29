package mcpmanager

import (
	"context"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPhase8RouterServerRegistersAllTenExecutorsWithExactClasses(t *testing.T) {
	s := NewRouterServer(nil, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "phase8-registration-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 12 {
		t.Fatalf("Phase 7+8 router tools = %d, want 12", len(listed.Tools))
	}

	wantExecutors := map[toolcontract.ExecutorClass]bool{
		toolcontract.ExecutorReadOnlyClosed:              true,
		toolcontract.ExecutorReadOnlyOpen:                true,
		toolcontract.ExecutorAdditiveClosed:              true,
		toolcontract.ExecutorAdditiveClosedIdempotent:    true,
		toolcontract.ExecutorAdditiveOpen:                true,
		toolcontract.ExecutorAdditiveOpenIdempotent:      true,
		toolcontract.ExecutorDestructiveClosed:           true,
		toolcontract.ExecutorDestructiveClosedIdempotent: true,
		toolcontract.ExecutorDestructiveOpen:             true,
		toolcontract.ExecutorDestructiveOpenIdempotent:   true,
	}
	discoveryTools := map[string]bool{"search_tools": true, "get_tool": true}
	for _, tool := range listed.Tools {
		if discoveryTools[tool.Name] {
			delete(discoveryTools, tool.Name)
			continue
		}
		class := toolcontract.ExecutorClass(tool.Name)
		if !wantExecutors[class] {
			t.Fatalf("unexpected Phase 8 tool %q", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Fatalf("executor %q has no input schema", tool.Name)
		}
		if tool.Annotations == nil {
			t.Fatalf("executor %q has no annotations", tool.Name)
		}
		normalized, err := toolcontract.ExecutorClassForTool(tool)
		if err != nil {
			t.Fatalf("normalize %q annotations: %v", tool.Name, err)
		}
		if normalized != class {
			t.Fatalf("executor %q annotations normalize to %q", tool.Name, normalized)
		}
		delete(wantExecutors, class)
	}
	if len(discoveryTools) != 0 || len(wantExecutors) != 0 {
		t.Fatalf("missing discovery=%v executors=%v", discoveryTools, wantExecutors)
	}
}

func TestPhase8UnavailableRouterReturnsOutcomeAwareToolError(t *testing.T) {
	s := NewRouterServer(nil, nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "phase8-error-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      string(toolcontract.ExecutorReadOnlyClosed),
		Arguments: map[string]any{"execution_handle": "eh1.unavailable.unavailable", "arguments": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("executor call returned protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("unavailable execution service must be a tool error")
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured error type = %T", result.StructuredContent)
	}
	errorBody, ok := structured["error"].(map[string]any)
	if !ok || errorBody["code"] != "manager_unavailable" || errorBody["outcome"] != "not_started" || errorBody["retryable"] != true {
		t.Fatalf("structured error = %#v", result.StructuredContent)
	}
}
