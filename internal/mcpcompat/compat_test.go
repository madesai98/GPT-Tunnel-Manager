package mcpcompat_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpcompat"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

const (
	modernProtocol = "2026-07-28"
	legacyProtocol = "2025-11-25"
)

func TestModernProtocolPaginationAndListChanges(t *testing.T) {
	changed := make(chan struct{}, 1)
	server := mcp.NewServer(testImplementation("catalog-server"), &mcp.ServerOptions{PageSize: 1})
	for _, name := range []string{"alpha", "beta", "gamma"} {
		addTextTool(server, name, name)
	}

	client := mcp.NewClient(testImplementation("catalog-client"), &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case changed <- struct{}{}:
			default:
			}
		},
	})
	session := connectInMemory(t, server, client)
	if got := session.InitializeResult().ProtocolVersion; got != modernProtocol {
		t.Fatalf("protocol = %q, want %q", got, modernProtocol)
	}

	var names []string
	for tool, err := range session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatalf("iterate tools: %v", err)
		}
		names = append(names, tool.Name)
	}
	if want := []string{"alpha", "beta", "gamma"}; !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}

	addTextTool(server, "delta", "delta")
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tools list change notification")
	}
}

func TestModernMultiRoundTripInputRequired(t *testing.T) {
	var callbacks atomic.Int32
	server := mcp.NewServer(testImplementation("mrtr-server"), nil)
	server.AddTool(&mcp.Tool{Name: "approve", InputSchema: objectSchema()}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if len(request.Params.InputResponses) == 0 {
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{
					"confirmation": &mcp.ElicitParams{Message: "Approve?"},
				},
				RequestState: "approval-state",
			}, nil
		}
		if request.Params.RequestState != "approval-state" {
			return nil, fmt.Errorf("request state = %q", request.Params.RequestState)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "approved"}}}, nil
	})

	client := mcp.NewClient(testImplementation("mrtr-client"), &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			callbacks.Add(1)
			return &mcp.ElicitResult{Action: "accept"}, nil
		},
	})
	session := connectInMemory(t, server, client)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "approve"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("elicitation callbacks = %d, want 1", callbacks.Load())
	}
	if got := result.Content[0].(*mcp.TextContent).Text; got != "approved" {
		t.Fatalf("result = %q, want approved", got)
	}
}

func TestLegacyStatefulCallbacksAndModernStatelessShareOneURL(t *testing.T) {
	server := mcp.NewServer(testImplementation("hybrid-server"), nil)
	server.AddTool(&mcp.Tool{Name: "legacy_callbacks", InputSchema: objectSchema()}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		elicited, err := request.Session.Elicit(ctx, &mcp.ElicitParams{Message: "legacy callback"})
		if err != nil {
			return nil, fmt.Errorf("elicitation callback: %w", err)
		}
		sampled, err := request.Session.CreateMessage(ctx, &mcp.CreateMessageParams{MaxTokens: 32})
		if err != nil {
			return nil, fmt.Errorf("sampling callback: %w", err)
		}
		roots, err := request.Session.ListRoots(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("roots callback: %w", err)
		}
		if len(roots.Roots) != 1 {
			return nil, fmt.Errorf("roots callback returned %d roots", len(roots.Roots))
		}
		sampledText, ok := sampled.Content.(*mcp.TextContent)
		if !ok {
			return nil, fmt.Errorf("sampling content type = %T", sampled.Content)
		}
		text := strings.Join([]string{elicited.Action, sampledText.Text, roots.Roots[0].URI}, "|")
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	})

	modern := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	legacy := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		JSONResponse: true,
	})
	testServer := httptest.NewServer(hybridHandler(modern, legacy))
	t.Cleanup(testServer.Close)

	modernClient := mcp.NewClient(testImplementation("modern-upstream-client"), nil)
	modernSession, err := modernClient.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: testServer.URL}, nil)
	if err != nil {
		t.Fatalf("connect modern client: %v", err)
	}
	t.Cleanup(func() { _ = modernSession.Close() })
	if got := modernSession.InitializeResult().ProtocolVersion; got != modernProtocol {
		t.Fatalf("modern protocol = %q, want %q", got, modernProtocol)
	}

	var elicitationCallbacks atomic.Int32
	var samplingCallbacks atomic.Int32
	legacyClient := mcp.NewClient(testImplementation("legacy-upstream-client"), &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			elicitationCallbacks.Add(1)
			return &mcp.ElicitResult{Action: "accept"}, nil
		},
		CreateMessageHandler: func(context.Context, *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			samplingCallbacks.Add(1)
			return &mcp.CreateMessageResult{
				Model:   "phase1-model",
				Role:    "assistant",
				Content: &mcp.TextContent{Text: "sampled"},
			}, nil
		},
	})
	legacyClient.AddRoots(&mcp.Root{URI: "file:///phase1", Name: "phase1"})
	legacyHTTPClient := &http.Client{Transport: headerRoundTripper{
		base:   http.DefaultTransport,
		header: http.Header{"X-GTM-Force-Legacy": []string{"1"}},
	}}
	legacySession, err := legacyClient.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   testServer.URL,
		HTTPClient: legacyHTTPClient,
	}, nil)
	if err != nil {
		t.Fatalf("connect legacy client: %v", err)
	}
	t.Cleanup(func() { _ = legacySession.Close() })
	if got := legacySession.InitializeResult().ProtocolVersion; got != legacyProtocol {
		t.Fatalf("legacy protocol = %q, want %q", got, legacyProtocol)
	}

	result, err := legacySession.CallTool(t.Context(), &mcp.CallToolParams{Name: "legacy_callbacks"})
	if err != nil {
		t.Fatalf("legacy callback tool: %v", err)
	}
	if elicitationCallbacks.Load() != 1 {
		t.Fatalf("legacy elicitation callbacks = %d, want 1", elicitationCallbacks.Load())
	}
	if samplingCallbacks.Load() != 1 {
		t.Fatalf("legacy sampling callbacks = %d, want 1", samplingCallbacks.Load())
	}
	if got, want := result.Content[0].(*mcp.TextContent).Text, "accept|sampled|file:///phase1"; got != want {
		t.Fatalf("legacy callback result = %q, want %q", got, want)
	}
}

func TestExternalHTTPModernOAuth(t *testing.T) {
	server := mcp.NewServer(testImplementation("oauth-server"), nil)
	addTextTool(server, "oauth_check", "authorized")
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})

	const token = "phase1-oauth-token"
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", `Bearer realm="phase1"`)
			http.Error(w, "authorization required", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, request)
	}))
	t.Cleanup(httpServer.Close)

	oauthHandler := &fakeOAuthHandler{token: token}
	client := mcp.NewClient(testImplementation("oauth-client"), nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:     httpServer.URL,
		OAuthHandler: oauthHandler,
	}, nil)
	if err != nil {
		t.Fatalf("connect OAuth client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if got := session.InitializeResult().ProtocolVersion; got != modernProtocol {
		t.Fatalf("protocol = %q, want %q", got, modernProtocol)
	}
	if oauthHandler.authorizeCalls.Load() != 1 {
		t.Fatalf("Authorize calls = %d, want 1", oauthHandler.authorizeCalls.Load())
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "oauth_check"})
	if err != nil {
		t.Fatalf("OAuth tool call: %v", err)
	}
	if got := result.Content[0].(*mcp.TextContent).Text; got != "authorized" {
		t.Fatalf("OAuth tool result = %q, want authorized", got)
	}
}

func TestResultFidelityAndResourceFollowup(t *testing.T) {
	server := mcp.NewServer(testImplementation("fidelity-server"), nil)
	server.AddResource(&mcp.Resource{
		URI:      "test://resource/followup",
		Name:     "followup",
		MIMEType: "text/plain",
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      "test://resource/followup",
			MIMEType: "text/plain",
			Text:     "resource payload",
		}}}, nil
	})

	server.AddTool(&mcp.Tool{Name: "fidelity", InputSchema: objectSchema()}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "text"},
				&mcp.ImageContent{Data: []byte{1, 2, 3}, MIMEType: "image/png"},
				&mcp.AudioContent{Data: []byte{4, 5, 6}, MIMEType: "audio/wav"},
				&mcp.ResourceLink{URI: "test://resource/followup", Name: "followup", MIMEType: "text/plain"},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "test://embedded", MIMEType: "text/plain", Text: "embedded payload"}},
			},
			StructuredContent: map[string]any{"kind": "structured", "ok": true},
			IsError:           true,
		}, nil
	})

	client := mcp.NewClient(testImplementation("fidelity-client"), nil)
	session := connectInMemory(t, server, client)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "fidelity"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("IsError = false, want true")
	}
	if len(result.Content) != 5 {
		t.Fatalf("content blocks = %d, want 5", len(result.Content))
	}
	if _, ok := result.Content[1].(*mcp.ImageContent); !ok {
		t.Fatalf("content[1] type = %T, want *mcp.ImageContent", result.Content[1])
	}
	if _, ok := result.Content[2].(*mcp.AudioContent); !ok {
		t.Fatalf("content[2] type = %T, want *mcp.AudioContent", result.Content[2])
	}
	link, ok := result.Content[3].(*mcp.ResourceLink)
	if !ok {
		t.Fatalf("content[3] type = %T, want *mcp.ResourceLink", result.Content[3])
	}
	if _, ok := result.Content[4].(*mcp.EmbeddedResource); !ok {
		t.Fatalf("content[4] type = %T, want *mcp.EmbeddedResource", result.Content[4])
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["kind"] != "structured" || structured["ok"] != true {
		t.Fatalf("structured content = %#v", result.StructuredContent)
	}

	resource, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: link.URI})
	if err != nil {
		t.Fatalf("ReadResource followup: %v", err)
	}
	if got := resource.Contents[0].Text; got != "resource payload" {
		t.Fatalf("resource payload = %q", got)
	}
}

func TestCancellationReachesDownstreamHandler(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	server := mcp.NewServer(testImplementation("cancel-server"), nil)
	server.AddTool(&mcp.Tool{Name: "block", InputSchema: objectSchema()}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	client := mcp.NewClient(testImplementation("cancel-client"), nil)
	session := connectInMemory(t, server, client)

	ctx, cancel := context.WithCancel(t.Context())
	callDone := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "block"})
		callDone <- err
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool handler did not start")
	}
	cancel()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("downstream handler did not observe cancellation")
	}
	select {
	case err := <-callDone:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("CallTool cancellation error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled tool call did not return")
	}
}

func TestTasksExtensionWireAndManagementMethods(t *testing.T) {
	rawTask := json.RawMessage(`{
		"resultType":"task",
		"taskId":"task-123",
		"status":"working",
		"createdAt":"2026-08-28T00:00:00Z",
		"lastUpdatedAt":"2026-08-28T00:00:01Z",
		"ttlMs":60000,
		"pollIntervalMs":1000
	}`)
	tool, task, err := mcpcompat.DecodeToolOrTaskResult(rawTask)
	if err != nil {
		t.Fatalf("decode task result: %v", err)
	}
	if tool != nil || task == nil || task.TaskID != "task-123" || task.Status != mcpcompat.TaskWorking {
		t.Fatalf("decoded task = %#v, tool = %#v", task, tool)
	}

	server := mcp.NewServer(testImplementation("task-server"), nil)
	if err := mcp.AddReceivingCustomMethod(server, "tasks/get", func(context.Context, *mcp.ServerSession, *mcpcompat.GetTaskParams) (*mcpcompat.GetTaskResult, error) {
		return &mcpcompat.GetTaskResult{
			ResultType: "complete",
			Task: mcpcompat.Task{
				TaskID:        "task-123",
				Status:        mcpcompat.TaskCompleted,
				CreatedAt:     "2026-08-28T00:00:00Z",
				LastUpdatedAt: "2026-08-28T00:00:02Z",
			},
			Result: json.RawMessage(`{"content":[{"type":"text","text":"done"}],"isError":false}`),
		}, nil
	}); err != nil {
		t.Fatalf("register tasks/get server method: %v", err)
	}
	if err := mcp.AddReceivingCustomMethod(server, "tasks/update", func(context.Context, *mcp.ServerSession, *mcpcompat.UpdateTaskParams) (*mcpcompat.UpdateTaskResult, error) {
		return &mcpcompat.UpdateTaskResult{ResultType: "complete"}, nil
	}); err != nil {
		t.Fatalf("register tasks/update server method: %v", err)
	}
	if err := mcp.AddReceivingCustomMethod(server, "tasks/cancel", func(context.Context, *mcp.ServerSession, *mcpcompat.CancelTaskParams) (*mcpcompat.CancelTaskResult, error) {
		return &mcpcompat.CancelTaskResult{ResultType: "complete"}, nil
	}); err != nil {
		t.Fatalf("register tasks/cancel server method: %v", err)
	}

	client := mcp.NewClient(testImplementation("task-client"), nil)
	if err := mcpcompat.RegisterTaskMethods(client); err != nil {
		t.Fatalf("register task client methods: %v", err)
	}
	session := connectInMemory(t, server, client)
	state, err := mcpcompat.GetTask(t.Context(), session, "task-123")
	if err != nil {
		t.Fatalf("tasks/get: %v", err)
	}
	if state.Status != mcpcompat.TaskCompleted || string(state.Result) == "" {
		t.Fatalf("tasks/get state = %#v", state)
	}
	if _, err := mcpcompat.UpdateTask(t.Context(), session, "task-123", mcp.InputResponseMap{}); err != nil {
		t.Fatalf("tasks/update: %v", err)
	}
	if _, err := mcpcompat.CancelTask(t.Context(), session, "task-123"); err != nil {
		t.Fatalf("tasks/cancel: %v", err)
	}

	shadowClient := mcp.NewClient(testImplementation("task-shadow-client"), nil)
	if err := mcp.AddSendingCustomMethod[*mcpcompat.GetTaskParams, *mcpcompat.GetTaskResult](shadowClient, "tools/call"); err == nil {
		t.Fatal("expected SDK to reject custom tools/call registration")
	}
}

func TestDirectStdioCommandTransport(t *testing.T) {
	binary := buildCompatTestServer(t)
	command := exec.Command(binary)
	var stderr bytes.Buffer
	command.Stderr = &stderr

	client := mcp.NewClient(testImplementation("stdio-client"), nil)
	session, err := client.Connect(t.Context(), &mcp.CommandTransport{
		Command:           command,
		TerminateDuration: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("connect stdio server: %v; stderr=%s", err, stderr.String())
	}
	t.Cleanup(func() { _ = session.Close() })
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "stdio-ok"},
	})
	if err != nil {
		t.Fatalf("stdio CallTool: %v; stderr=%s", err, stderr.String())
	}
	if got := result.Content[0].(*mcp.TextContent).Text; got != "stdio-ok" {
		t.Fatalf("stdio result = %q", got)
	}
}

func TestDirectManagedHTTPTransport(t *testing.T) {
	binary := buildCompatTestServer(t)
	command := exec.Command(binary, "-http", "127.0.0.1:0")
	stderr, err := command.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start managed HTTP server: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	})

	ready := make(chan string, 1)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "READY ") {
				ready <- strings.TrimSpace(strings.TrimPrefix(line, "READY "))
				return
			}
		}
		readErr <- scanner.Err()
	}()

	var endpoint string
	select {
	case endpoint = <-ready:
	case err := <-readErr:
		t.Fatalf("managed HTTP server failed before ready: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("managed HTTP server did not become ready")
	}

	client := mcp.NewClient(testImplementation("managed-http-client"), nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatalf("connect managed HTTP server: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "managed-http-ok"},
	})
	if err != nil {
		t.Fatalf("managed HTTP CallTool: %v", err)
	}
	if got := result.Content[0].(*mcp.TextContent).Text; got != "managed-http-ok" {
		t.Fatalf("managed HTTP result = %q", got)
	}
}

func connectInMemory(t *testing.T, server *mcp.Server, client *mcp.Client) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func testImplementation(name string) *mcp.Implementation {
	return &mcp.Implementation{Name: name, Version: "phase1"}
}

func objectSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":true}`)
}

func addTextTool(server *mcp.Server, name, text string) {
	server.AddTool(&mcp.Tool{Name: name, InputSchema: objectSchema()}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	})
}

func hybridHandler(modern, legacy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body []byte
		if request.Method == http.MethodPost && request.Body != nil {
			var err error
			body, err = io.ReadAll(request.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = request.Body.Close()
			request.Body = io.NopCloser(bytes.NewReader(body))
		}
		isDiscover := bytes.Contains(body, []byte(`"method":"server/discover"`))
		forceLegacy := request.Header.Get("X-GTM-Force-Legacy") == "1"
		if forceLegacy && isDiscover {
			http.Error(w, "legacy client forces initialize fallback", http.StatusBadRequest)
			return
		}
		if request.Header.Get("Mcp-Protocol-Version") >= modernProtocol || isDiscover {
			modern.ServeHTTP(w, request)
			return
		}
		legacy.ServeHTTP(w, request)
	})
}

type headerRoundTripper struct {
	base   http.RoundTripper
	header http.Header
}

func (h headerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for key, values := range h.header {
		clone.Header.Del(key)
		for _, value := range values {
			clone.Header.Add(key, value)
		}
	}
	return h.base.RoundTrip(clone)
}

type fakeOAuthHandler struct {
	token          string
	authorized     atomic.Bool
	authorizeCalls atomic.Int32
}

var _ auth.OAuthHandler = (*fakeOAuthHandler)(nil)

func (h *fakeOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	if !h.authorized.Load() {
		return nil, nil
	}
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token, TokenType: "Bearer"}), nil
}

func (h *fakeOAuthHandler) Authorize(_ context.Context, _ *http.Request, response *http.Response) error {
	h.authorizeCalls.Add(1)
	h.authorized.Store(true)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return nil
}

func buildCompatTestServer(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	name := "mcpcompat-testserver"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", binary, "./internal/mcpcompat/testserver")
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build compatibility test server: %v\n%s", err, output)
	}
	return binary
}
