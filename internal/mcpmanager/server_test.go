package mcpmanager

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKExposesExactlyFourTools(t *testing.T) {
	s := startTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "manager-mcp-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL()}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) != 4 {
		t.Fatalf("got %d tools", len(listed.Tools))
	}

	want := map[string]bool{"get_status": true, "start": true, "restart": true, "shutdown": true}
	for _, tool := range listed.Tools {
		if !want[tool.Name] {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		delete(want, tool.Name)
		if tool.Title == "" {
			t.Fatalf("tool %q is missing title", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Fatalf("tool %q is missing input schema", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Fatalf("tool %q is missing output schema", tool.Name)
		}
		if tool.Annotations == nil {
			t.Fatalf("tool %q is missing annotations", tool.Name)
		}
		if tool.Name == "get_status" && !tool.Annotations.ReadOnlyHint {
			t.Fatal("get_status must be annotated read-only")
		}
		if tool.Name != "get_status" && tool.Annotations.ReadOnlyHint {
			t.Fatalf("mutation tool %q must not be annotated read-only", tool.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing tools %v", want)
	}
}

func TestRejectsBrowserOrigin(t *testing.T) {
	s := startTestServer(t)

	req, err := http.NewRequest(http.MethodPost, s.URL(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got status %d", resp.StatusCode)
	}
}

func TestStatelessTransportDoesNotOfferSessionGET(t *testing.T) {
	s := startTestServer(t)

	resp, err := http.Get(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Mcp-Session-Id"); got != "" {
		t.Fatalf("unexpected session id %q", got)
	}
}

func TestTypedToolErrorsRemainToolResults(t *testing.T) {
	s := startTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "manager-mcp-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL()}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call get_status: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected missing test registry to be returned as a tool error")
	}
	if result.StructuredContent == nil {
		t.Fatal("expected structured error content")
	}
}

func startTestServer(t *testing.T) *Server {
	t.Helper()
	s := New(nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return s
}
