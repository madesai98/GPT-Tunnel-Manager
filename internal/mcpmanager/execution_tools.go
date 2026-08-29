package mcpmanager

import (
	"context"
	"encoding/json"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/buildinfo"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/discovery"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionrouter"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const routerInstructions = "Discover indexed downstream tools, inspect authoritative contracts, then execute only through the permission-class tool authorized by the returned execution handle."

type executorDefinition struct {
	Class       toolcontract.ExecutorClass
	Title       string
	Description string
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

var executorDefinitions = []executorDefinition{
	{toolcontract.ExecutorReadOnlyClosed, "Call read-only closed-world tool", "Execute a read-only downstream tool that does not interact with the open world.", true, false, false, false},
	{toolcontract.ExecutorReadOnlyOpen, "Call read-only open-world tool", "Execute a read-only downstream tool that may interact with the open world.", true, false, false, true},
	{toolcontract.ExecutorAdditiveClosed, "Call additive closed-world tool", "Execute a non-destructive, non-idempotent downstream tool that does not interact with the open world.", false, false, false, false},
	{toolcontract.ExecutorAdditiveClosedIdempotent, "Call idempotent additive closed-world tool", "Execute a non-destructive, idempotent downstream tool that does not interact with the open world.", false, false, true, false},
	{toolcontract.ExecutorAdditiveOpen, "Call additive open-world tool", "Execute a non-destructive, non-idempotent downstream tool that may interact with the open world.", false, false, false, true},
	{toolcontract.ExecutorAdditiveOpenIdempotent, "Call idempotent additive open-world tool", "Execute a non-destructive, idempotent downstream tool that may interact with the open world.", false, false, true, true},
	{toolcontract.ExecutorDestructiveClosed, "Call destructive closed-world tool", "Execute a destructive, non-idempotent downstream tool that does not interact with the open world.", false, true, false, false},
	{toolcontract.ExecutorDestructiveClosedIdempotent, "Call idempotent destructive closed-world tool", "Execute a destructive, idempotent downstream tool that does not interact with the open world.", false, true, true, false},
	{toolcontract.ExecutorDestructiveOpen, "Call destructive open-world tool", "Execute a destructive, non-idempotent downstream tool that may interact with the open world.", false, true, false, true},
	{toolcontract.ExecutorDestructiveOpenIdempotent, "Call idempotent destructive open-world tool", "Execute a destructive, idempotent downstream tool that may interact with the open world.", false, true, true, true},
}

// NewRouterServer composes the Phase 7 discovery/detail tools with the Phase 8
// permission-preserving execution tools. Phase 10's canonical server composes
// these same registration helpers with the lifecycle-aware continuation path.
func NewRouterServer(discoveryService *discovery.Service, executionService *executionrouter.Service) *Server {
	s := &Server{}
	s.accepting.Store(true)
	s.mcp = mcp.NewServer(
		&mcp.Implementation{Name: "gpt-tunnel-manager", Version: buildinfo.Version},
		&mcp.ServerOptions{Instructions: routerInstructions, Capabilities: &mcp.ServerCapabilities{}},
	)
	registerV2DiscoveryTools(s.mcp, discoveryService)
	registerV2ExecutionTools(s.mcp, executionService)
	return s
}

func registerV2ExecutionTools(server *mcp.Server, service *executionrouter.Service) {
	for _, definition := range executorDefinitions {
		definition := definition
		openWorld := definition.OpenWorld
		var destructiveHint *bool
		if !definition.ReadOnly {
			destructive := definition.Destructive
			destructiveHint = &destructive
		}
		mcp.AddTool(server, &mcp.Tool{
			Name:        string(definition.Class),
			Title:       definition.Title,
			Description: definition.Description + " Requires an authenticated execution_handle returned by get_tool; caller-supplied routing identifiers are never authoritative.",
			Annotations: &mcp.ToolAnnotations{
				Title:           definition.Title,
				ReadOnlyHint:    definition.ReadOnly,
				DestructiveHint: destructiveHint,
				IdempotentHint:  definition.Idempotent,
				OpenWorldHint:   &openWorld,
			},
		}, func(ctx context.Context, req *mcp.CallToolRequest, input executionrouter.Input) (*mcp.CallToolResult, any, error) {
			if service == nil {
				failure := &executionrouter.ExecutionError{
					Code:      executionrouter.CodeManagerUnavailable,
					Message:   "manager execution router is unavailable",
					Outcome:   executionrouter.OutcomeNotStarted,
					Retryable: true,
				}
				return executionFailureResult(failure), nil, nil
			}
			if req != nil && req.Session != nil {
				ctx = downstream.WithLegacyCallbackTarget(ctx, newUpstreamCallbackTarget(req.Session))
			}
			result, failure := service.Execute(ctx, definition.Class, input)
			if failure != nil {
				return executionFailureResult(failure), nil, nil
			}
			// Return the downstream CallToolResult itself with an any output type.
			// The MCP SDK therefore does not synthesize or flatten content and all
			// text/image/audio/resource/structured/isError/input-required fields remain intact.
			return result, nil, nil
		})
	}
}

type executionFailureBody struct {
	Error executionFailureDetails `json:"error"`
}

type executionFailureDetails struct {
	Code              string                  `json:"code"`
	Message           string                  `json:"message"`
	Outcome           executionrouter.Outcome `json:"outcome"`
	Retryable         bool                    `json:"retryable"`
	DownstreamIsError *bool                   `json:"downstream_is_error,omitempty"`
}

func executionFailureResult(failure *executionrouter.ExecutionError) *mcp.CallToolResult {
	body := executionFailureBody{Error: executionFailureDetails{
		Code:              failure.Code,
		Message:           failure.Message,
		Outcome:           failure.Outcome,
		Retryable:         failure.Retryable,
		DownstreamIsError: failure.DownstreamIsError,
	}}
	encoded, err := json.Marshal(body)
	if err != nil {
		encoded = []byte(`{"error":{"code":"manager_unavailable","message":"failed to encode execution error","outcome":"not_started","retryable":true}}`)
	}
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		StructuredContent: json.RawMessage(encoded),
	}
}
