package mcpmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/buildinfo"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/discovery"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const discoveryInstructions = "Discover indexed downstream tools and inspect authoritative invocation contracts before execution."

type searchToolsOutput struct {
	GenerationID       string                      `json:"generation_id,omitempty"`
	EffectiveProfile   *discovery.EffectiveProfile `json:"effective_profile,omitempty"`
	PreferenceRevision uint64                      `json:"preference_revision,omitempty"`
	Results            []discovery.SearchResult    `json:"results,omitempty"`
	Error              *toolError                  `json:"error,omitempty"`
}

// authoritativeToolOutput is the MCP-facing projection of the authoritative
// catalog record. discovery.AuthoritativeTool intentionally keeps the original
// downstream Tool contract as json.RawMessage so catalog/discovery code can
// preserve the exact source JSON. Reflecting RawMessage through the typed MCP
// output-schema generator describes it as []byte, though the wire value is a
// JSON object. Decode only at the upstream boundary so the advertised schema
// and actual JSON value agree without weakening authoritative storage fidelity.
type authoritativeToolOutput struct {
	Server             catalog.SourceServerContract `json:"server"`
	InvocationIdentity discovery.InvocationIdentity  `json:"invocation_identity"`
	SourceFingerprint  string                        `json:"source_fingerprint"`
	ExecutorClass      toolcontract.ExecutorClass    `json:"executor_class"`
	Tool               any                           `json:"tool" jsonschema:"Exact authoritative downstream MCP Tool contract as a JSON value."`
}

type getToolOutput struct {
	GenerationID    string                       `json:"generation_id,omitempty"`
	Authoritative   *authoritativeToolOutput     `json:"authoritative,omitempty"`
	Derived         *discovery.DerivedTool       `json:"derived,omitempty"`
	HumanIdentity   *discovery.HumanToolIdentity `json:"human_identity,omitempty"`
	ExecutionHandle string                       `json:"execution_handle,omitempty"`
	Error           *toolError                   `json:"error,omitempty"`
}

// NewDiscoveryServer composes the Phase 7 discovery/detail portion of the v2
// Manager MCP contract. The complete fixed 19-tool upstream surface is composed
// in Phase 10; this constructor deliberately does not stub or pull forward
// indexing, preference-write, execution, lifecycle, callback, or Task routing.
func NewDiscoveryServer(service *discovery.Service) *Server {
	s := &Server{}
	s.accepting.Store(true)
	s.mcp = mcp.NewServer(
		&mcp.Implementation{Name: "gpt-tunnel-manager", Version: buildinfo.Version},
		&mcp.ServerOptions{Instructions: discoveryInstructions, Capabilities: &mcp.ServerCapabilities{}},
	)
	registerV2DiscoveryTools(s.mcp, service)
	return s
}

func registerV2DiscoveryTools(server *mcp.Server, service *discovery.Service) {
	closedWorld := false
	openWorld := true
	nondestructive := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_tools",
		Title:       "Search indexed tools",
		Description: "Search the committed active Tool Catalog using deterministic multi-facet retrieval and optional Routing Profile context. Returns compact Tool References for get_tool.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Search indexed tools",
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &nondestructive,
			OpenWorldHint:   &openWorld,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input discovery.SearchInput) (*mcp.CallToolResult, searchToolsOutput, error) {
		if service == nil {
			return discoverySearchFailure(errors.New("manager_discovery_unavailable"))
		}
		result, err := service.Search(ctx, input)
		if err != nil {
			return discoverySearchFailure(err)
		}
		return nil, searchToolsOutput{
			GenerationID: result.GenerationID, EffectiveProfile: result.EffectiveProfile,
			PreferenceRevision: result.PreferenceRevision, Results: result.Results,
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_tool",
		Title:       "Get indexed tool detail",
		Description: "Resolve a Tool Reference against the current committed generation, return authoritative source contract data separately from derived guidance, and mint a process-epoch-bound Execution Handle.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Get indexed tool detail",
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &nondestructive,
			OpenWorldHint:   &closedWorld,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input discovery.GetToolInput) (*mcp.CallToolResult, getToolOutput, error) {
		if service == nil {
			return discoveryGetFailure(errors.New("manager_discovery_unavailable"))
		}
		result, err := service.GetTool(ctx, input)
		if err != nil {
			return discoveryGetFailure(err)
		}
		authoritative, err := projectAuthoritativeTool(result.Authoritative)
		if err != nil {
			return discoveryGetFailure(err)
		}
		return nil, getToolOutput{
			GenerationID: result.GenerationID, Authoritative: &authoritative,
			Derived: &result.Derived, HumanIdentity: &result.HumanIdentity, ExecutionHandle: result.ExecutionHandle,
		}, nil
	})
}

func projectAuthoritativeTool(source discovery.AuthoritativeTool) (authoritativeToolOutput, error) {
	if len(source.Tool) == 0 {
		return authoritativeToolOutput{}, errors.New("authoritative downstream tool contract is empty")
	}
	var tool any
	if err := json.Unmarshal(source.Tool, &tool); err != nil {
		return authoritativeToolOutput{}, fmt.Errorf("decode authoritative downstream tool contract: %w", err)
	}
	return authoritativeToolOutput{
		Server:             source.Server,
		InvocationIdentity: source.InvocationIdentity,
		SourceFingerprint:  source.SourceFingerprint,
		ExecutorClass:      source.ExecutorClass,
		Tool:               tool,
	}, nil
}

func discoverySearchFailure(err error) (*mcp.CallToolResult, searchToolsOutput, error) {
	return &mcp.CallToolResult{IsError: true}, searchToolsOutput{Error: stableDiscoveryError(err)}, nil
}

func discoveryGetFailure(err error) (*mcp.CallToolResult, getToolOutput, error) {
	return &mcp.CallToolResult{IsError: true}, getToolOutput{Error: stableDiscoveryError(err)}, nil
}

func stableDiscoveryError(err error) *toolError {
	result := &toolError{Code: "operation_failed", Message: err.Error(), Retryable: true}
	switch {
	case errors.Is(err, discovery.ErrIndexRequired):
		result.Code = "index_required"
		result.Retryable = false
	case errors.Is(err, discovery.ErrRoutingProfileNotFound):
		result.Code = "routing_profile_not_found"
		result.Retryable = false
	case errors.Is(err, discovery.ErrInvalidToolReference):
		result.Code = "invalid_tool_reference"
		result.Retryable = false
	case errors.Is(err, discovery.ErrInvalidSearchRequest):
		result.Code = "invalid_request"
		result.Retryable = false
	case err.Error() == "manager_discovery_unavailable":
		result.Code = "manager_unavailable"
	}
	return result
}
