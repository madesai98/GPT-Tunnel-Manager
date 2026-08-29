package enrichment

import (
	"encoding/json"
	"errors"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
)

const (
	ToolEnrichmentArtifactKind      = "semantic.tool-enrichment/v1"
	CapabilityHierarchyArtifactKind = "semantic.capability-hierarchy/v1"
	RoleToolEnrichment              = "semantic.tool_enrichment"
	RoleCapabilityHierarchy         = "semantic.capability_hierarchy"
	RoleEnrichedEmbedding           = "embedding.enriched"
	ToolEnrichmentProtocolVersion   = "tool-enrichment/v1"
	CapabilityProtocolVersion       = "capability-reconciliation/v1"
	AmbiguityReviewProtocolVersion  = "ambiguity-review/v1"
	EnrichedProjectionVersion       = "enriched-routing-guidance/v1"
	NeighborhoodAlgorithmVersion    = "semantic-neighborhood/exact-cosine-source-description/v1"
)

var ErrEnrichmentPrerequisite = errors.New("enrichment_prerequisite_incomplete")

type Options struct {
	NeighborhoodSize       int
	ToolBatchSize          int
	MaxToolBatchBytes      int
	MaxReconciliationTools int
	MaxReconciliationBytes int
}

func (o Options) withDefaults() Options {
	if o.NeighborhoodSize <= 0 {
		o.NeighborhoodSize = 8
	}
	if o.ToolBatchSize <= 0 {
		o.ToolBatchSize = 8
	}
	if o.MaxToolBatchBytes <= 0 {
		o.MaxToolBatchBytes = 256 * 1024
	}
	if o.MaxReconciliationTools <= 0 {
		o.MaxReconciliationTools = 2000
	}
	if o.MaxReconciliationBytes <= 0 {
		o.MaxReconciliationBytes = 4 * 1024 * 1024
	}
	return o
}

type Coordinator struct {
	catalog   *catalog.Catalog
	retrieval *retrieval.CatalogStore
	embedding embedding.Provider
	options   Options
}

func NewCoordinator(c *catalog.Catalog, provider embedding.Provider, options Options) (*Coordinator, error) {
	if c == nil {
		return nil, errors.New("catalog is required")
	}
	if provider == nil {
		return nil, errors.New("embedding provider is required")
	}
	store, err := retrieval.NewCatalogStore(c)
	if err != nil {
		return nil, err
	}
	return &Coordinator{catalog: c, retrieval: store, embedding: provider, options: options.withDefaults()}, nil
}

type ToolRef struct {
	MemberKey         string          `json:"member_key"`
	ServerID          string          `json:"server_id"`
	ToolName          string          `json:"tool_name"`
	SourceFingerprint string          `json:"source_fingerprint"`
	Contract          json.RawMessage `json:"contract"`
}

type NeighborRef struct {
	Rank              int             `json:"rank"`
	MemberKey         string          `json:"member_key"`
	ServerID          string          `json:"server_id"`
	ToolName          string          `json:"tool_name"`
	SourceFingerprint string          `json:"source_fingerprint"`
	Contract          json.RawMessage `json:"contract"`
}

type ToolWork struct {
	Tool                           ToolRef       `json:"tool"`
	Neighbors                      []NeighborRef `json:"neighbors"`
	NeighborhoodContextFingerprint string        `json:"neighborhood_context_fingerprint"`
}

type ToolBatchRequest struct {
	Protocol string     `json:"protocol"`
	Items    []ToolWork `json:"items"`
}

type ToolGuidance struct {
	Purpose              string            `json:"purpose"`
	UseWhen              []string          `json:"use_when,omitempty"`
	AvoidWhen            []string          `json:"avoid_when,omitempty"`
	Examples             []string          `json:"examples,omitempty"`
	ArgumentGuidance     map[string]string `json:"argument_guidance,omitempty"`
	Preconditions        []string          `json:"preconditions,omitempty"`
	OutputInterpretation string            `json:"output_interpretation,omitempty"`
	Alternatives         []string          `json:"alternatives,omitempty"`
	Capabilities         []string          `json:"capabilities,omitempty"`
}

type ToolEnrichmentResult struct {
	MemberKey string       `json:"member_key"`
	Guidance  ToolGuidance `json:"guidance"`
}

type ToolBatchResponse struct {
	Items []ToolEnrichmentResult `json:"items"`
}

type CapabilityInputItem struct {
	Tool       ToolRef         `json:"tool"`
	Enrichment json.RawMessage `json:"enrichment"`
}

type CapabilityBatchRequest struct {
	Protocol string                `json:"protocol"`
	Items    []CapabilityInputItem `json:"items"`
}

type CapabilityNode struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	ToolMembers []string `json:"tool_members,omitempty"`
}

type CapabilityHierarchy struct {
	Protocol     string           `json:"protocol"`
	Capabilities []CapabilityNode `json:"capabilities"`
}

type ToolProsCons struct {
	Pros []string `json:"pros,omitempty"`
	Cons []string `json:"cons,omitempty"`
}

type AmbiguityProposal struct {
	Summary             string                  `json:"summary"`
	CompetingTools      []string                `json:"competing_tools"`
	ProsCons            map[string]ToolProsCons `json:"pros_cons,omitempty"`
	ConditionalUseCases []string                `json:"conditional_use_cases,omitempty"`
	SuggestedOptions    []string                `json:"suggested_options,omitempty"`
}

type CapabilityBatchResponse struct {
	Hierarchy   CapabilityHierarchy `json:"hierarchy"`
	Ambiguities []AmbiguityProposal `json:"ambiguities,omitempty"`
}

type AmbiguityReviewRequest struct {
	Protocol   string            `json:"protocol"`
	Proposal   AmbiguityProposal `json:"proposal"`
	Generation string            `json:"generation_id"`
}

type AmbiguityResolution string

const (
	AmbiguityNeutral    AmbiguityResolution = "neutral"
	AmbiguityPreference AmbiguityResolution = "preference"
)

type AmbiguityReviewResponse struct {
	Resolution    AmbiguityResolution `json:"resolution"`
	PreferenceIDs []string            `json:"preference_ids,omitempty"`
}

type SubmitResult struct {
	Batch      catalog.EnrichmentBatch
	Idempotent bool
}

func MemberKey(serverID, toolName string) string { return serverID + "/" + toolName }
