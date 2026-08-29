package discovery

import (
	"encoding/json"
	"errors"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

var (
	ErrIndexRequired          = errors.New("index_required")
	ErrRoutingProfileNotFound = errors.New("routing_profile_not_found")
	ErrInvalidToolReference   = errors.New("invalid_tool_reference")
	ErrInvalidSearchRequest   = errors.New("invalid_search_request")
)

type SearchInput struct {
	Query          string `json:"query"`
	Limit          int    `json:"limit,omitempty"`
	RoutingProfile string `json:"routing_profile,omitempty"`
	Context        string `json:"context,omitempty"`
}

type EffectiveProfile struct {
	ID   string `json:"profile_id"`
	Name string `json:"name"`
}

type SearchResult struct {
	ToolRef       string                     `json:"tool_ref"`
	ServerName    string                     `json:"server_name"`
	ToolName      string                     `json:"tool_name"`
	Title         string                     `json:"title,omitempty"`
	Summary       string                     `json:"summary,omitempty"`
	ExecutorClass toolcontract.ExecutorClass `json:"executor_class"`
}

type SearchOutput struct {
	GenerationID       string            `json:"generation_id"`
	EffectiveProfile   *EffectiveProfile `json:"effective_profile,omitempty"`
	PreferenceRevision uint64            `json:"preference_revision"`
	Results            []SearchResult    `json:"results"`
}

type InvocationIdentity struct {
	ServerID string `json:"server_id"`
	ToolName string `json:"tool_name"`
}

type AuthoritativeTool struct {
	Server             catalog.SourceServerContract `json:"server"`
	InvocationIdentity InvocationIdentity            `json:"invocation_identity"`
	SourceFingerprint  string                        `json:"source_fingerprint"`
	ExecutorClass      toolcontract.ExecutorClass    `json:"executor_class"`
	Tool               json.RawMessage               `json:"tool"`
}

type CapabilitySummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
}

type DerivedTool struct {
	SemanticGuidance *enrichment.ToolGuidance `json:"semantic_guidance,omitempty"`
	Capabilities     []CapabilitySummary       `json:"capabilities,omitempty"`
}

type HumanToolIdentity struct {
	ServerName string `json:"server_name"`
	ToolName   string `json:"tool_name"`
	Title      string `json:"title,omitempty"`
	Display    string `json:"display"`
}

type GetToolInput struct {
	ToolRef string `json:"tool_ref"`
}

type GetToolOutput struct {
	GenerationID    string            `json:"generation_id"`
	Authoritative   AuthoritativeTool `json:"authoritative"`
	Derived         DerivedTool       `json:"derived"`
	HumanIdentity   HumanToolIdentity `json:"human_identity"`
	ExecutionHandle string            `json:"execution_handle"`
}
