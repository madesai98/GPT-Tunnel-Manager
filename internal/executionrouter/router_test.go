package executionrouter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testServerID    = "srv_80000000000000000000000000000001"
	testRoutingHash = "sha256:phase8-routing"
)

type fakeSession struct {
	snapshot downstream.ToolSnapshot
	result   *mcp.CallToolResult
	err      error
	calls    int
	last     *mcp.CallToolParams
}

func (s *fakeSession) InitialTools() downstream.ToolSnapshot { return s.snapshot.Clone() }
func (s *fakeSession) CallTool(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	s.calls++
	s.last = params
	return s.result, s.err
}

type fixture struct {
	catalog *catalog.Catalog
	tracker *routingstate.Tracker
	handles *executionhandle.Manager
	service *Service
	session *fakeSession
	tool    *mcp.Tool
	handle  string
	class   toolcontract.ExecutorClass
}

func newFixture(t *testing.T, tool *mcp.Tool, session *fakeSession, maxResultBytes int) *fixture {
	t.Helper()
	ctx := context.Background()
	c, err := catalog.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	tracker, err := routingstate.NewTracker(routingstate.NewMemoryBackend(routingstate.Snapshot{RoutingStateHash: testRoutingHash}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateStaging(ctx, catalog.GenerationSpec{ID: "gen_phase8", RoutingStateHash: testRoutingHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceServer(ctx, "gen_phase8", sourceServerEntry("http://127.0.0.1/unused")); err != nil {
		t.Fatal(err)
	}
	fingerprint, _, err := toolcontract.FingerprintTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireSourceTool(ctx, "gen_phase8", testServerID, tool.Name, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceTool(ctx, "gen_phase8", testServerID, tool, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Promote(ctx, "gen_phase8", testRoutingHash); err != nil {
		t.Fatal(err)
	}
	class, err := toolcontract.ExecutorClassForTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	if session == nil {
		session = &fakeSession{snapshot: snapshotFor(t, tool), result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}}
	}
	handles, err := executionhandle.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	handle, err := handles.Mint(executionhandle.Claims{
		GenerationID:      "gen_phase8",
		ServerID:          testServerID,
		ToolName:          tool.Name,
		SourceFingerprint: fingerprint,
		ExecutorClass:     string(class),
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(c, tracker, handles, SessionProviderFunc(func(_ context.Context, serverID string) (Session, error) {
		if serverID != testServerID {
			return nil, errors.New("unexpected server id")
		}
		return session, nil
	}), Options{MaxResultBytes: maxResultBytes})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{catalog: c, tracker: tracker, handles: handles, service: service, session: session, tool: tool, handle: handle, class: class}
}

func TestExecuteValidHandleDispatchesExactlyOnce(t *testing.T) {
	f := newFixture(t, toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema()), nil, 0)
	result, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, ToolName: "display-only", Arguments: map[string]any{"text": "hello"}})
	if failure != nil {
		t.Fatalf("Execute failure = %#v", failure)
	}
	if result != f.session.result {
		t.Fatal("router did not preserve the downstream result object")
	}
	if f.session.calls != 1 {
		t.Fatalf("downstream calls = %d, want 1", f.session.calls)
	}
	if f.session.last.Name != f.tool.Name || f.session.last.Arguments["text"] != "hello" {
		t.Fatalf("downstream params = %#v", f.session.last)
	}
}

func TestExecutionHandleAuthorityFailuresDoNotDispatch(t *testing.T) {
	baseTool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema())

	t.Run("hmac tamper", func(t *testing.T) {
		f := newFixture(t, baseTool, nil, 0)
		_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle + "x", Arguments: map[string]any{"text": "x"}})
		assertFailure(t, failure, CodeInvalidHandle, OutcomeNotStarted, false)
		assertNoCalls(t, f.session)
	})

	t.Run("process restart invalidates", func(t *testing.T) {
		f := newFixture(t, baseTool, nil, 0)
		restarted, err := executionhandle.NewManager()
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewService(f.catalog, f.tracker, restarted, SessionProviderFunc(func(context.Context, string) (Session, error) { return f.session, nil }), Options{})
		if err != nil {
			t.Fatal(err)
		}
		_, failure := service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
		if failure == nil || (failure.Code != CodeInvalidHandle && failure.Code != CodeStaleHandle) || failure.Outcome != OutcomeNotStarted {
			t.Fatalf("restart failure = %#v", failure)
		}
		assertNoCalls(t, f.session)
	})

	t.Run("stale generation", func(t *testing.T) {
		f := newFixture(t, baseTool, nil, 0)
		activateReplacement(t, f.catalog, baseTool)
		_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
		assertFailure(t, failure, CodeStaleHandle, OutcomeNotStarted, false)
		assertNoCalls(t, f.session)
	})

	t.Run("source fingerprint mismatch", func(t *testing.T) {
		f := newFixture(t, baseTool, nil, 0)
		handle, err := f.handles.Mint(executionhandle.Claims{GenerationID: "gen_phase8", ServerID: testServerID, ToolName: baseTool.Name, SourceFingerprint: "sha256:not-authoritative", ExecutorClass: string(f.class)})
		if err != nil {
			t.Fatal(err)
		}
		_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: handle, Arguments: map[string]any{"text": "x"}})
		assertFailure(t, failure, CodeStaleHandle, OutcomeNotStarted, false)
		assertNoCalls(t, f.session)
	})

	t.Run("handle executor class mismatch", func(t *testing.T) {
		f := newFixture(t, baseTool, nil, 0)
		fingerprint, _, err := toolcontract.FingerprintTool(baseTool)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := f.handles.Mint(executionhandle.Claims{GenerationID: "gen_phase8", ServerID: testServerID, ToolName: baseTool.Name, SourceFingerprint: fingerprint, ExecutorClass: string(toolcontract.ExecutorReadOnlyOpen)})
		if err != nil {
			t.Fatal(err)
		}
		_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: handle, Arguments: map[string]any{"text": "x"}})
		assertFailure(t, failure, CodeStaleHandle, OutcomeNotStarted, false)
		assertNoCalls(t, f.session)
	})

	t.Run("wrong Manager executor", func(t *testing.T) {
		f := newFixture(t, baseTool, nil, 0)
		_, failure := f.service.Execute(context.Background(), toolcontract.ExecutorReadOnlyOpen, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
		assertFailure(t, failure, CodeExecutorMismatch, OutcomeNotStarted, false)
		assertNoCalls(t, f.session)
	})
}

func TestSafeArgumentValidation(t *testing.T) {
	tests := []struct {
		name      string
		schema    map[string]any
		arguments map[string]any
		wantCode  string
	}{
		{name: "valid nested and array", schema: map[string]any{"type": "object", "properties": map[string]any{"items": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []any{"name"}}}}, "required": []any{"items"}}, arguments: map[string]any{"items": []any{map[string]any{"name": "ok"}}}},
		{name: "missing required", schema: basicSchema(), arguments: map[string]any{}, wantCode: CodeInvalidArguments},
		{name: "primitive type mismatch", schema: basicSchema(), arguments: map[string]any{"text": 42}, wantCode: CodeInvalidArguments},
		{name: "local defs ref", schema: map[string]any{"type": "object", "$defs": map[string]any{"payload": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []any{"name"}}}, "properties": map[string]any{"payload": map[string]any{"$ref": "#/$defs/payload"}}, "required": []any{"payload"}}, arguments: map[string]any{"payload": map[string]any{"name": "local"}}},
		{name: "http ref rejected", schema: map[string]any{"type": "object", "properties": map[string]any{"payload": map[string]any{"$ref": "https://example.invalid/schema.json"}}}, arguments: map[string]any{"payload": map[string]any{}}, wantCode: CodeUnsupportedSchema},
		{name: "file ref rejected", schema: map[string]any{"type": "object", "properties": map[string]any{"payload": map[string]any{"$ref": "file:///etc/passwd"}}}, arguments: map[string]any{"payload": map[string]any{}}, wantCode: CodeUnsupportedSchema},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, toolForClass(t, toolcontract.ExecutorReadOnlyClosed, tt.schema), nil, 0)
			_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: tt.arguments})
			if tt.wantCode == "" {
				if failure != nil {
					t.Fatalf("Execute failure = %#v", failure)
				}
				if f.session.calls != 1 {
					t.Fatalf("downstream calls = %d, want 1", f.session.calls)
				}
				return
			}
			assertFailure(t, failure, tt.wantCode, OutcomeNotStarted, false)
			assertNoCalls(t, f.session)
		})
	}
}

func TestLiveContractDriftMarksDirtyAndAdvancesRoutingRevision(t *testing.T) {
	tool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema())
	drifted := *tool
	drifted.Description = "changed after indexing"
	session := &fakeSession{snapshot: snapshotFor(t, &drifted), result: &mcp.CallToolResult{}}
	f := newFixture(t, tool, session, 0)

	_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
	assertFailure(t, failure, CodeIndexRequired, OutcomeNotStarted, false)
	assertNoCalls(t, session)
	partitions, err := f.catalog.DirtyPartitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 1 || partitions[0].PartitionKey != "server:"+testServerID {
		t.Fatalf("dirty partitions = %#v", partitions)
	}
	state, err := f.tracker.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.RoutingRevision != 1 {
		t.Fatalf("routing revision = %d, want 1", state.RoutingRevision)
	}
}

func TestCallTimeContractDriftIsNotDispatchedAgain(t *testing.T) {
	tool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema())
	session := &fakeSession{snapshot: snapshotFor(t, tool), err: downstream.ErrToolContractChanged}
	f := newFixture(t, tool, session, 0)
	_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
	assertFailure(t, failure, CodeIndexRequired, OutcomeNotStarted, false)
	if session.calls != 1 {
		t.Fatalf("session CallTool invocations = %d, want exactly 1", session.calls)
	}
}

func TestDownstreamUnavailableDoesNotStaleGeneration(t *testing.T) {
	f := newFixture(t, toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema()), nil, 0)
	service, err := NewService(f.catalog, f.tracker, f.handles, SessionProviderFunc(func(context.Context, string) (Session, error) {
		return nil, downstream.ErrDownstreamUnavailable
	}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, failure := service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
	assertFailure(t, failure, CodeDownstreamUnavailable, OutcomeNotStarted, true)
	partitions, err := f.catalog.DirtyPartitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 0 {
		t.Fatalf("availability failure dirtied catalog: %#v", partitions)
	}
	state, err := f.tracker.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := f.catalog.ActiveCurrent(context.Background(), state.RoutingStateHash)
	if err != nil || !current {
		t.Fatalf("availability failure staled generation: current=%v err=%v", current, err)
	}
}

func TestAmbiguousCallFailureIsOutcomeUnknownAndNeverReplayed(t *testing.T) {
	tool := toolForClass(t, toolcontract.ExecutorAdditiveOpenIdempotent, basicSchema())
	session := &fakeSession{snapshot: snapshotFor(t, tool), err: errors.New("connection reset after request write")}
	f := newFixture(t, tool, session, 0)
	_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "write"}})
	assertFailure(t, failure, CodeDownstreamCallFailed, OutcomeUnknown, false)
	if session.calls != 1 {
		t.Fatalf("ambiguous idempotent call was replayed: calls=%d", session.calls)
	}
}

func TestResultFidelityAndSizeSemantics(t *testing.T) {
	t.Run("mixed result passes through exactly", func(t *testing.T) {
		tool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema())
		original := mixedResult()
		session := &fakeSession{snapshot: snapshotFor(t, tool), result: original}
		f := newFixture(t, tool, session, 1<<20)
		result, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
		if failure != nil {
			t.Fatalf("Execute failure = %#v", failure)
		}
		if result != original {
			t.Fatal("router reconstructed the downstream CallToolResult")
		}
		if !result.IsError || len(result.Content) != 5 {
			t.Fatalf("result lost fidelity: %#v", result)
		}
		if _, ok := result.Content[1].(*mcp.ImageContent); !ok {
			t.Fatalf("image content type = %T", result.Content[1])
		}
		if _, ok := result.Content[2].(*mcp.AudioContent); !ok {
			t.Fatalf("audio content type = %T", result.Content[2])
		}
		if _, ok := result.Content[3].(*mcp.ResourceLink); !ok {
			t.Fatalf("resource link type = %T", result.Content[3])
		}
		if _, ok := result.Content[4].(*mcp.EmbeddedResource); !ok {
			t.Fatalf("embedded resource type = %T", result.Content[4])
		}
	})

	t.Run("too large is completed and non-retryable", func(t *testing.T) {
		tool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema())
		session := &fakeSession{snapshot: snapshotFor(t, tool), result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("x", 512)}}}}
		f := newFixture(t, tool, session, 64)
		_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
		assertFailure(t, failure, CodeResultTooLarge, OutcomeCompleted, false)
		if session.calls != 1 {
			t.Fatalf("downstream calls = %d, want 1", session.calls)
		}
	})

	t.Run("invalid post-call result is completed", func(t *testing.T) {
		tool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema())
		original := &mcp.CallToolResult{StructuredContent: map[string]any{"invalid": func() {}}, IsError: true}
		session := &fakeSession{snapshot: snapshotFor(t, tool), result: original}
		f := newFixture(t, tool, session, 1<<20)
		_, failure := f.service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
		assertFailure(t, failure, CodeDownstreamResultInvalid, OutcomeCompleted, false)
		if failure.OriginalResult != original || failure.DownstreamIsError == nil || !*failure.DownstreamIsError {
			t.Fatalf("invalid result context was not preserved: %#v", failure)
		}
	})
}

func TestRepresentativeDirectDownstreamRoundTripWithoutResultLoss(t *testing.T) {
	tool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema())
	downstreamServer := mcp.NewServer(&mcp.Implementation{Name: "phase8-fidelity", Version: "1"}, nil)
	downstreamServer.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mixedResult(), nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return downstreamServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	t.Cleanup(httpServer.Close)

	factory, err := downstream.NewFactory(downstream.Options{Secrets: newMemorySecretStore()})
	if err != nil {
		t.Fatal(err)
	}
	entry := sourceServerEntry(httpServer.URL)
	session, err := factory.Connect(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	f := newFixture(t, tool, nil, 1<<20)
	service, err := NewService(f.catalog, f.tracker, f.handles, SessionProviderFunc(func(context.Context, string) (Session, error) { return session, nil }), Options{MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	result, failure := service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
	if failure != nil {
		t.Fatalf("Execute failure = %#v", failure)
	}
	if !result.IsError || len(result.Content) != 5 {
		t.Fatalf("direct downstream result = %#v", result)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["kind"] != "structured" || structured["ok"] != true {
		t.Fatalf("structured content = %#v", result.StructuredContent)
	}
	if _, ok := result.Content[1].(*mcp.ImageContent); !ok {
		t.Fatalf("image round-trip type = %T", result.Content[1])
	}
	if _, ok := result.Content[2].(*mcp.AudioContent); !ok {
		t.Fatalf("audio round-trip type = %T", result.Content[2])
	}
	if _, ok := result.Content[3].(*mcp.ResourceLink); !ok {
		t.Fatalf("resource-link round-trip type = %T", result.Content[3])
	}
	if _, ok := result.Content[4].(*mcp.EmbeddedResource); !ok {
		t.Fatalf("embedded-resource round-trip type = %T", result.Content[4])
	}
}

func basicSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []any{"text"}}
}

func toolForClass(t *testing.T, class toolcontract.ExecutorClass, schema map[string]any) *mcp.Tool {
	t.Helper()
	readOnly, destructive, idempotent, openWorld := false, true, false, true
	switch class {
	case toolcontract.ExecutorReadOnlyClosed:
		readOnly, destructive, idempotent, openWorld = true, false, true, false
	case toolcontract.ExecutorReadOnlyOpen:
		readOnly, destructive, idempotent, openWorld = true, false, true, true
	case toolcontract.ExecutorAdditiveClosed:
		readOnly, destructive, idempotent, openWorld = false, false, false, false
	case toolcontract.ExecutorAdditiveClosedIdempotent:
		readOnly, destructive, idempotent, openWorld = false, false, true, false
	case toolcontract.ExecutorAdditiveOpen:
		readOnly, destructive, idempotent, openWorld = false, false, false, true
	case toolcontract.ExecutorAdditiveOpenIdempotent:
		readOnly, destructive, idempotent, openWorld = false, false, true, true
	case toolcontract.ExecutorDestructiveClosed:
		readOnly, destructive, idempotent, openWorld = false, true, false, false
	case toolcontract.ExecutorDestructiveClosedIdempotent:
		readOnly, destructive, idempotent, openWorld = false, true, true, false
	case toolcontract.ExecutorDestructiveOpen:
		readOnly, destructive, idempotent, openWorld = false, true, false, true
	case toolcontract.ExecutorDestructiveOpenIdempotent:
		readOnly, destructive, idempotent, openWorld = false, true, true, true
	default:
		t.Fatalf("unknown executor class %q", class)
	}
	return &mcp.Tool{
		Name:        "echo",
		Description: "phase 8 execution test tool",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: &openWorld},
	}
}

func snapshotFor(t *testing.T, tools ...*mcp.Tool) downstream.ToolSnapshot {
	t.Helper()
	fingerprint, canonical, err := toolcontract.FingerprintTools(tools)
	if err != nil {
		t.Fatal(err)
	}
	return downstream.ToolSnapshot{Tools: canonical, Fingerprint: fingerprint}
}

func sourceServerEntry(endpoint string) v2config.ServerEntry {
	return v2config.ServerEntry{
		ID: testServerID, Name: "phase8", Mode: v2config.ModeAlwaysOn,
		Transport: v2config.TransportConfig{Type: v2config.TransportExternalHTTP, ExternalHTTP: &v2config.ExternalHTTPTransport{URL: endpoint, Auth: v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthNone}}},
		Runtime: v2config.RuntimeConfig{StartupTimeoutSeconds: 10, ShutdownTimeoutSeconds: 3},
	}
}

func activateReplacement(t *testing.T, c *catalog.Catalog, tool *mcp.Tool) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.CreateStaging(ctx, catalog.GenerationSpec{ID: "gen_phase8_replacement", RoutingStateHash: testRoutingHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceServer(ctx, "gen_phase8_replacement", sourceServerEntry("http://127.0.0.1/unused")); err != nil {
		t.Fatal(err)
	}
	fingerprint, _, err := toolcontract.FingerprintTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireSourceTool(ctx, "gen_phase8_replacement", testServerID, tool.Name, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSourceTool(ctx, "gen_phase8_replacement", testServerID, tool, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Promote(ctx, "gen_phase8_replacement", testRoutingHash); err != nil {
		t.Fatal(err)
	}
}

func mixedResult() *mcp.CallToolResult {
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
	}
}

func assertFailure(t *testing.T, failure *ExecutionError, code string, outcome Outcome, retryable bool) {
	t.Helper()
	if failure == nil || failure.Code != code || failure.Outcome != outcome || failure.Retryable != retryable {
		t.Fatalf("failure = %#v, want code=%q outcome=%q retryable=%v", failure, code, outcome, retryable)
	}
}

func assertNoCalls(t *testing.T, session *fakeSession) {
	t.Helper()
	if session.calls != 0 {
		t.Fatalf("downstream calls = %d, want 0", session.calls)
	}
}

type memorySecretStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newMemorySecretStore() *memorySecretStore { return &memorySecretStore{values: map[string][]byte{}} }
func (s *memorySecretStore) Put(_ context.Context, ref string, value []byte) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[ref] = append([]byte(nil), value...)
	return nil
}
func (s *memorySecretStore) Get(_ context.Context, ref string) ([]byte, error) {
	if err := secrets.ValidateRef(ref); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[ref]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}
func (s *memorySecretStore) Delete(_ context.Context, ref string) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, ref)
	return nil
}
