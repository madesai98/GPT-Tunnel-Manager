package mcpmanager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/discovery"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionrouter"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestV2ManagerModernContractAndWorkflow(t *testing.T) {
	fixture := newPhase10Fixture(t, false)
	session := connectPhase10Modern(t, fixture.endpoint)
	defer session.Close()

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list Manager tools: %v", err)
	}
	assertPhase10ToolContract(t, listed.Tools)

	refresh := callPhase10Tool(t, session, "index_refresh", map[string]any{})
	var refreshOut indexRefreshOutput
	decodePhase10Structured(t, refresh, &refreshOut)
	if refreshOut.Error != nil || refreshOut.Result == nil || refreshOut.Result.Status.StagingGenerationID == "" {
		t.Fatalf("index_refresh output = %#v", refreshOut)
	}

	toolBatchResult := callPhase10Tool(t, session, "index_get_enrichment_batch", map[string]any{
		"kind":  string(catalog.BatchToolEnrichment),
		"limit": 1,
	})
	var toolBatchOut indexGetBatchOutput
	decodePhase10Structured(t, toolBatchResult, &toolBatchOut)
	if toolBatchOut.Error != nil || len(toolBatchOut.Batches) != 1 {
		t.Fatalf("tool enrichment batch output = %#v", toolBatchOut)
	}
	var toolRequest enrichment.ToolBatchRequest
	if err := json.Unmarshal(toolBatchOut.Batches[0].RequestJSON, &toolRequest); err != nil {
		t.Fatalf("decode tool enrichment request: %v", err)
	}
	if len(toolRequest.Items) != 1 || toolRequest.Items[0].Tool.ToolName != "echo" {
		t.Fatalf("tool enrichment request = %#v", toolRequest)
	}
	toolResponse := enrichment.ToolBatchResponse{}
	for _, item := range toolRequest.Items {
		toolResponse.Items = append(toolResponse.Items, enrichment.ToolEnrichmentResult{
			MemberKey: item.Tool.MemberKey,
			Guidance: enrichment.ToolGuidance{
				Purpose:      "Echo supplied text and expose a follow-up resource.",
				UseWhen:      []string{"The caller needs deterministic echo behavior."},
				Capabilities: []string{"echo"},
			},
		})
	}
	submitTool := callPhase10Tool(t, session, "index_submit_enrichment_batch", map[string]any{
		"batch_id": toolBatchOut.Batches[0].ID,
		"response": toolResponse,
	})
	var submitToolOut indexSubmitBatchOutput
	decodePhase10Structured(t, submitTool, &submitToolOut)
	if submitToolOut.Error != nil || submitToolOut.Batch == nil {
		t.Fatalf("tool enrichment submission = %#v", submitToolOut)
	}

	capabilityBatchResult := callPhase10Tool(t, session, "index_get_enrichment_batch", map[string]any{
		"kind":  string(catalog.BatchCapabilityReconciliation),
		"limit": 1,
	})
	var capabilityBatchOut indexGetBatchOutput
	decodePhase10Structured(t, capabilityBatchResult, &capabilityBatchOut)
	if capabilityBatchOut.Error != nil || len(capabilityBatchOut.Batches) != 1 {
		t.Fatalf("capability batch output = %#v", capabilityBatchOut)
	}
	var capabilityRequest enrichment.CapabilityBatchRequest
	if err := json.Unmarshal(capabilityBatchOut.Batches[0].RequestJSON, &capabilityRequest); err != nil {
		t.Fatalf("decode capability request: %v", err)
	}
	members := make([]string, 0, len(capabilityRequest.Items))
	for _, item := range capabilityRequest.Items {
		members = append(members, item.Tool.MemberKey)
	}
	capabilityResponse := enrichment.CapabilityBatchResponse{Hierarchy: enrichment.CapabilityHierarchy{
		Protocol: enrichment.CapabilityProtocolVersion,
		Capabilities: []enrichment.CapabilityNode{{
			ID:          "echo",
			Name:        "Echo",
			Description: "Deterministic echo operations.",
			ToolMembers: members,
		}},
	}}
	submitCapability := callPhase10Tool(t, session, "index_submit_enrichment_batch", map[string]any{
		"batch_id": capabilityBatchOut.Batches[0].ID,
		"response": capabilityResponse,
	})
	var submitCapabilityOut indexSubmitBatchOutput
	decodePhase10Structured(t, submitCapability, &submitCapabilityOut)
	if submitCapabilityOut.Error != nil || submitCapabilityOut.Batch == nil {
		t.Fatalf("capability submission = %#v", submitCapabilityOut)
	}

	commit := callPhase10Tool(t, session, "index_commit", map[string]any{})
	var commitOut indexCommitOutput
	decodePhase10Structured(t, commit, &commitOut)
	if commitOut.Error != nil || commitOut.Result == nil || !commitOut.Result.Status.Ready || commitOut.Result.GenerationID == "" {
		t.Fatalf("index_commit output = %#v", commitOut)
	}

	search := callPhase10Tool(t, session, "search_tools", discovery.SearchInput{Query: "echo", Limit: 5})
	var searchOut searchToolsOutput
	decodePhase10Structured(t, search, &searchOut)
	if searchOut.Error != nil || len(searchOut.Results) != 1 {
		t.Fatalf("search_tools output = %#v", searchOut)
	}
	if searchOut.Results[0].ToolName != "echo" || searchOut.Results[0].ExecutorClass != toolcontract.ExecutorReadOnlyClosed {
		t.Fatalf("search result = %#v", searchOut.Results[0])
	}

	get := callPhase10Tool(t, session, "get_tool", discovery.GetToolInput{ToolRef: searchOut.Results[0].ToolRef})
	var getOut getToolOutput
	decodePhase10Structured(t, get, &getOut)
	if getOut.Error != nil || getOut.ExecutionHandle == "" || getOut.Authoritative == nil {
		t.Fatalf("get_tool output = %#v", getOut)
	}
	if getOut.Authoritative.ExecutorClass != toolcontract.ExecutorReadOnlyClosed {
		t.Fatalf("executor class = %q", getOut.Authoritative.ExecutorClass)
	}

	executed := callPhase10Tool(t, session, string(toolcontract.ExecutorReadOnlyClosed), executionrouter.Input{
		ExecutionHandle: getOut.ExecutionHandle,
		ToolName:        "echo",
		Arguments:       map[string]any{"text": "hello"},
	})
	if executed.IsError {
		t.Fatalf("executor returned tool error: %#v", executed.StructuredContent)
	}
	if len(executed.Content) != 3 {
		t.Fatalf("executor content blocks = %d, want 3", len(executed.Content))
	}
	if got := executed.Content[0].(*mcp.TextContent).Text; got != "executed" {
		t.Fatalf("executor text = %q", got)
	}
	link, ok := executed.Content[1].(*mcp.ResourceLink)
	if !ok {
		t.Fatalf("content[1] type = %T, want ResourceLink", executed.Content[1])
	}
	if !strings.HasPrefix(link.URI, managerResourceScheme+"://resource/") || strings.Contains(link.URI, "test://phase10/resource") {
		t.Fatalf("resource link was not rewritten opaquely: %q", link.URI)
	}
	embedded, ok := executed.Content[2].(*mcp.EmbeddedResource)
	if !ok || embedded.Resource == nil || embedded.Resource.URI != "test://phase10/embedded" {
		t.Fatalf("embedded resource changed = %#v", executed.Content[2])
	}
	resource, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: link.URI})
	if err != nil {
		t.Fatalf("read proxied resource: %v", err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0].Text != "resource payload" || resource.Contents[0].URI != "test://phase10/resource" {
		t.Fatalf("proxied resource = %#v", resource)
	}
}

func TestV2ManagerLegacyUsesSameLogicalContract(t *testing.T) {
	fixture := newPhase10Fixture(t, false)
	client := fixture.http.Client()

	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"phase10-legacy","version":"1"}}}`
	response, body := phase10POST(t, client, fixture.endpoint, initialize, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("legacy initialize status = %d body=%s", response.StatusCode, body)
	}
	sessionID := response.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("legacy initialize did not establish a stateful MCP session")
	}
	var initEnvelope struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &initEnvelope); err != nil {
		t.Fatalf("decode legacy initialize: %v", err)
	}
	if initEnvelope.Result.ProtocolVersion != "2025-11-25" {
		t.Fatalf("legacy protocol = %q", initEnvelope.Result.ProtocolVersion)
	}

	headers := http.Header{
		"Mcp-Session-Id":       []string{sessionID},
		"Mcp-Protocol-Version": []string{"2025-11-25"},
	}
	initialized := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	response, body = phase10POST(t, client, fixture.endpoint, initialized, headers)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("legacy initialized status = %d body=%s", response.StatusCode, body)
	}

	listRequest := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	response, body = phase10POST(t, client, fixture.endpoint, listRequest, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("legacy tools/list status = %d body=%s", response.StatusCode, body)
	}
	var listEnvelope struct {
		Result struct {
			Tools []*mcp.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &listEnvelope); err != nil {
		t.Fatalf("decode legacy tools/list: %v", err)
	}
	assertPhase10ToolContract(t, listEnvelope.Result.Tools)
}

func TestV2ManagerLocalProtectionAndOriginRejection(t *testing.T) {
	fixture := newPhase10Fixture(t, true)
	client := fixture.http.Client()
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"phase10-security","version":"1"}}}`

	response, body := phase10POST(t, client, fixture.endpoint, initialize, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unprotected request status = %d body=%s", response.StatusCode, body)
	}
	capability, err := fixture.secrets.Get(t.Context(), v2config.LocalManagerCapabilitySecretRef)
	if err != nil {
		t.Fatalf("load generated local capability: %v", err)
	}
	authorized := http.Header{"Authorization": []string{"Bearer " + string(capability)}}
	response, body = phase10POST(t, client, fixture.endpoint, initialize, authorized)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authorized request status = %d body=%s", response.StatusCode, body)
	}
	browser := authorized.Clone()
	browser.Set("Origin", "https://example.com")
	response, body = phase10POST(t, client, fixture.endpoint, initialize, browser)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("browser-origin request status = %d body=%s", response.StatusCode, body)
	}
}

func assertPhase10ToolContract(t *testing.T, tools []*mcp.Tool) {
	t.Helper()
	want := map[string]bool{
		"index_status": true,
		"index_refresh": true,
		"index_get_enrichment_batch": true,
		"index_submit_enrichment_batch": true,
		"index_commit": true,
		"search_tools": true,
		"get_tool": true,
		"get_routing_preferences": true,
		"set_routing_preferences": true,
		string(toolcontract.ExecutorReadOnlyClosed): true,
		string(toolcontract.ExecutorReadOnlyOpen): true,
		string(toolcontract.ExecutorAdditiveClosed): true,
		string(toolcontract.ExecutorAdditiveClosedIdempotent): true,
		string(toolcontract.ExecutorAdditiveOpen): true,
		string(toolcontract.ExecutorAdditiveOpenIdempotent): true,
		string(toolcontract.ExecutorDestructiveClosed): true,
		string(toolcontract.ExecutorDestructiveClosedIdempotent): true,
		string(toolcontract.ExecutorDestructiveOpen): true,
		string(toolcontract.ExecutorDestructiveOpenIdempotent): true,
	}
	if len(tools) != len(want) {
		t.Fatalf("Manager tool count = %d, want %d", len(tools), len(want))
	}
	byName := make(map[string]*mcp.Tool, len(tools))
	for _, tool := range tools {
		if tool == nil || !want[tool.Name] {
			t.Fatalf("unexpected Manager tool %#v", tool)
		}
		if tool.Title == "" || tool.InputSchema == nil || tool.Annotations == nil {
			t.Fatalf("tool %q has incomplete contract: %#v", tool.Name, tool)
		}
		byName[tool.Name] = tool
		delete(want, tool.Name)
		if strings.HasPrefix(tool.Name, "call_") {
			class, err := toolcontract.ExecutorClassForTool(tool)
			if err != nil {
				t.Fatalf("normalize executor %q: %v", tool.Name, err)
			}
			if class != toolcontract.ExecutorClass(tool.Name) {
				t.Fatalf("executor %q annotations normalize to %q", tool.Name, class)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing Manager tools %v", want)
	}
	assertPhase10Annotations(t, byName["get_routing_preferences"], true, false, true, false)
	assertPhase10Annotations(t, byName["set_routing_preferences"], false, true, true, false)
	assertPhase10Annotations(t, byName["index_status"], true, false, true, false)
	assertPhase10Annotations(t, byName["index_refresh"], false, false, true, true)
	assertPhase10Annotations(t, byName["index_get_enrichment_batch"], true, false, true, false)
	assertPhase10Annotations(t, byName["index_submit_enrichment_batch"], false, false, true, false)
	assertPhase10Annotations(t, byName["index_commit"], false, false, false, false)
	assertPhase10Annotations(t, byName["search_tools"], true, false, true, true)
	assertPhase10Annotations(t, byName["get_tool"], true, false, true, false)
}

func assertPhase10Annotations(t *testing.T, tool *mcp.Tool, readOnly, destructive, idempotent, openWorld bool) {
	t.Helper()
	if tool == nil || tool.Annotations == nil {
		t.Fatal("tool annotations are unavailable")
	}
	annotations := tool.Annotations
	if annotations.ReadOnlyHint != readOnly || annotations.IdempotentHint != idempotent {
		t.Fatalf("tool %q annotations = %#v", tool.Name, annotations)
	}
	if annotations.DestructiveHint == nil || *annotations.DestructiveHint != destructive {
		t.Fatalf("tool %q destructive annotation = %#v", tool.Name, annotations.DestructiveHint)
	}
	if annotations.OpenWorldHint == nil || *annotations.OpenWorldHint != openWorld {
		t.Fatalf("tool %q open-world annotation = %#v", tool.Name, annotations.OpenWorldHint)
	}
}

func callPhase10Tool(t *testing.T, session *mcp.ClientSession, name string, arguments any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result == nil {
		t.Fatalf("call %s returned nil result", name)
	}
	if result.IsError {
		t.Fatalf("call %s returned tool error: %#v", name, result.StructuredContent)
	}
	return result
}

func decodePhase10Structured(t *testing.T, result *mcp.CallToolResult, out any) {
	t.Helper()
	body, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode structured content %s: %v", body, err)
	}
}

func connectPhase10Modern(t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "phase10-modern", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatalf("connect modern Manager client: %v", err)
	}
	if initialized := session.InitializeResult(); initialized == nil || initialized.ProtocolVersion != modernProtocolVersion {
		t.Fatalf("modern protocol = %#v", initialized)
	}
	return session
}

func phase10POST(t *testing.T, client *http.Client, endpoint, body string, headers http.Header) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, payload
}

type phase10Fixture struct {
	endpoint  string
	http      *httptest.Server
	server    *V2Server
	catalog   *catalog.Catalog
	lifecycle *routedlifecycle.Service
	secrets   *phase10Secrets
}

func newPhase10Fixture(t *testing.T, protection bool) *phase10Fixture {
	t.Helper()
	manager := v2config.DefaultManagerConfig(39091)
	manager.LocalManager.AccessProtectionEnabled = protection
	manager.ManagedDefaults.IdleTimeoutSeconds = 3600

	closedWorld := false
	nondestructive := false
	tool := &mcp.Tool{
		Name:        "echo",
		Title:       "Echo",
		Description: "Echo supplied text and return a follow-up resource link.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{
			Title:           "Echo",
			ReadOnlyHint:    true,
			DestructiveHint: &nondestructive,
			IdempotentHint:  true,
			OpenWorldHint:   &closedWorld,
		},
	}
	fingerprint, canonical, err := toolcontract.FingerprintTools([]*mcp.Tool{tool})
	if err != nil {
		t.Fatalf("fingerprint fake downstream: %v", err)
	}
	runtime := &phase10Runtime{
		snapshot: downstream.ToolSnapshot{Tools: canonical, Fingerprint: fingerprint},
		done:     make(chan struct{}),
	}
	servers := v2config.ServersConfig{SchemaVersion: v2config.SchemaVersion, Servers: []v2config.ServerEntry{{
		ID:   "phase10-server",
		Name: "Phase 10 Server",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type:  v2config.TransportStdio,
			Stdio: &v2config.StdioTransport{Executable: "phase10-fake"},
		},
	}}}
	if err := v2config.ValidateManager(manager); err != nil {
		t.Fatalf("validate test Manager config: %v", err)
	}
	if err := v2config.ValidateServers(servers); err != nil {
		t.Fatalf("validate test server config: %v", err)
	}

	c, err := catalog.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	provider := phase10Embedding{}
	coordinator, err := enrichment.NewCoordinator(c, provider, enrichment.Options{})
	if err != nil {
		t.Fatalf("create enrichment coordinator: %v", err)
	}
	lifecycle, err := routedlifecycle.New(t.Context(), manager, servers, func(context.Context, v2config.ServerEntry) (routedlifecycle.RuntimeSession, error) {
		return runtime, nil
	}, routedlifecycle.Options{})
	if err != nil {
		t.Fatalf("create routed lifecycle: %v", err)
	}
	routingHash, err := routingstate.ComputeHash(routingstate.ConfigMaterial(manager, servers))
	if err != nil {
		t.Fatalf("compute routing hash: %v", err)
	}
	routing := newPhase10Routing(t, routingHash)
	indexService, err := indexing.NewService(c, coordinator, provider, lifecycle, routing, servers)
	if err != nil {
		t.Fatalf("create indexing service: %v", err)
	}
	preferences, err := routingprefs.NewStore(c)
	if err != nil {
		t.Fatalf("create preference store: %v", err)
	}
	handles, err := executionhandle.NewManager()
	if err != nil {
		t.Fatalf("create handle manager: %v", err)
	}
	discoveryService, err := discovery.NewService(c, provider, preferences, routing, handles, discovery.Options{})
	if err != nil {
		t.Fatalf("create discovery service: %v", err)
	}
	secretStore := newPhase10Secrets()
	server, err := NewV2Server(t.Context(), V2ServerOptions{
		Manager:     manager,
		Catalog:     c,
		Secrets:     secretStore,
		Lifecycle:   lifecycle,
		Indexing:    indexService,
		Discovery:   discoveryService,
		Preferences: preferences,
		RoutingState: routing,
		Handles:      handles,
	})
	if err != nil {
		t.Fatalf("create v2 Manager server: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	fixture := &phase10Fixture{
		endpoint:  httpServer.URL + managerMCPPath,
		http:      httpServer,
		server:    server,
		catalog:   c,
		lifecycle: lifecycle,
		secrets:   secretStore,
	}
	t.Cleanup(func() {
		httpServer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = lifecycle.Close(ctx)
		_ = c.Close()
	})
	return fixture
}

type phase10Embedding struct{}

func (phase10Embedding) Identity() embedding.Identity {
	return embedding.Identity{Provider: "phase10-test", BaseURL: "https://example.invalid/v1", Model: "phase10", Protocol: "phase10/v1"}
}

func (phase10Embedding) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(inputs))
	for index := range inputs {
		vectors[index] = []float32{1, 1, 1}
	}
	return vectors, nil
}

type phase10Runtime struct {
	snapshot  downstream.ToolSnapshot
	done      chan struct{}
	closeOnce sync.Once
}

func (r *phase10Runtime) InitialTools() downstream.ToolSnapshot { return r.snapshot.Clone() }

func (r *phase10Runtime) CallTool(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if params == nil || params.Name != "echo" {
		return nil, errors.New("unexpected fake downstream tool call")
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "executed"},
			&mcp.ResourceLink{URI: "test://phase10/resource", Name: "follow-up", MIMEType: "text/plain"},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "test://phase10/embedded", MIMEType: "text/plain", Text: "embedded payload"}},
		},
		StructuredContent: map[string]any{"ok": true},
	}, nil
}

func (r *phase10Runtime) ReadResource(_ context.Context, uri string) (*mcp.ReadResourceResult, error) {
	if uri != "test://phase10/resource" {
		return nil, errors.New("unexpected fake downstream resource URI")
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/plain", Text: "resource payload"}}}, nil
}

func (r *phase10Runtime) Close(context.Context) error {
	r.closeOnce.Do(func() { close(r.done) })
	return nil
}

func (r *phase10Runtime) Done() <-chan struct{} { return r.done }

type phase10Routing struct {
	tracker *routingstate.Tracker
	hash    string
}

func newPhase10Routing(t *testing.T, hash string) *phase10Routing {
	t.Helper()
	tracker, err := routingstate.NewTracker(routingstate.NewMemoryBackend(routingstate.Snapshot{
		RoutingRevision:  1,
		RoutingStateHash: hash,
	}))
	if err != nil {
		t.Fatalf("create routing tracker: %v", err)
	}
	return &phase10Routing{tracker: tracker, hash: hash}
}

func (s *phase10Routing) Snapshot(ctx context.Context) (routingstate.Snapshot, error) {
	return s.tracker.Snapshot(ctx)
}

func (s *phase10Routing) AdvanceRoutingRevision(ctx context.Context) (routingstate.Snapshot, error) {
	return s.tracker.AdvanceRoutingRevision(ctx)
}

func (s *phase10Routing) CurrentRoutingStateHash() (string, bool) { return s.hash, s.hash != "" }

type phase10Secrets struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newPhase10Secrets() *phase10Secrets { return &phase10Secrets{values: make(map[string][]byte)} }

func (s *phase10Secrets) Put(_ context.Context, ref string, value []byte) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	s.values[ref] = append([]byte(nil), value...)
	s.mu.Unlock()
	return nil
}

func (s *phase10Secrets) Get(_ context.Context, ref string) ([]byte, error) {
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

func (s *phase10Secrets) Delete(_ context.Context, ref string) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.values, ref)
	s.mu.Unlock()
	return nil
}
