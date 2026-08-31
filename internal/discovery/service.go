package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

const (
	defaultLimit               = 10
	maximumLimit               = 50
	defaultCandidateDepth      = 50
	defaultMinVectorSimilarity = 0.48
	defaultMinLexicalCoverage  = 0.50
	maxPreferenceAdjustment    = 0.012
)

type RoutingStateReader interface {
	Snapshot(context.Context) (routingstate.Snapshot, error)
}

type Options struct {
	DefaultProfile      string
	QueryCache          *embedding.QueryCache
	CandidateDepth      int
	RRFK                int
	MinVectorSimilarity float64
	MinLexicalCoverage  float64
}

type Service struct {
	catalog     *catalog.Catalog
	retrieval   *retrieval.CatalogStore
	provider    embedding.Provider
	queryCache  *embedding.QueryCache
	preferences *routingprefs.Store
	state       RoutingStateReader
	handles     *executionhandle.Manager
	options     Options
}

func NewService(c *catalog.Catalog, provider embedding.Provider, preferences *routingprefs.Store, state RoutingStateReader, handles *executionhandle.Manager, options Options) (*Service, error) {
	if c == nil {
		return nil, errors.New("catalog is required")
	}
	if provider == nil {
		return nil, errors.New("embedding provider is required")
	}
	if preferences == nil {
		return nil, errors.New("routing preference store is required")
	}
	if state == nil {
		return nil, errors.New("routing state reader is required")
	}
	if handles == nil {
		return nil, errors.New("execution handle manager is required")
	}
	store, err := retrieval.NewCatalogStore(c)
	if err != nil {
		return nil, err
	}
	if options.CandidateDepth <= 0 {
		options.CandidateDepth = defaultCandidateDepth
	}
	if options.RRFK <= 0 {
		options.RRFK = defaultRRFK
	}
	if options.MinVectorSimilarity <= 0 {
		options.MinVectorSimilarity = defaultMinVectorSimilarity
	}
	if options.MinLexicalCoverage <= 0 {
		options.MinLexicalCoverage = defaultMinLexicalCoverage
	}
	return &Service{
		catalog:     c,
		retrieval:   store,
		provider:    provider,
		queryCache:  options.QueryCache,
		preferences: preferences,
		state:       state,
		handles:     handles,
		options:     options,
	}, nil
}

type toolPresentation struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type candidate struct {
	key              string
	source           catalog.SourceToolRecord
	presentation     toolPresentation
	server           catalog.SourceServerContract
	guidance         *enrichment.ToolGuidance
	capabilities     []CapabilitySummary
	fusedScore       float64
	preferenceAdjust float64
	maxVectorScore   float64
	lexicalCoverage  float64
}

type scoredKey struct {
	Key   string
	Score float64
}

func (s *Service) Search(ctx context.Context, input SearchInput) (SearchOutput, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return SearchOutput{}, fmt.Errorf("%w: query is required", ErrInvalidSearchRequest)
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maximumLimit {
		return SearchOutput{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidSearchRequest, maximumLimit)
	}
	generation, err := s.currentGeneration(ctx)
	if err != nil {
		return SearchOutput{}, err
	}
	profile, profileID, err := s.resolveProfile(ctx, strings.TrimSpace(input.RoutingProfile))
	if err != nil {
		return SearchOutput{}, err
	}
	preferenceRevision, err := s.preferences.Revision(ctx)
	if err != nil {
		return SearchOutput{}, err
	}

	sources, err := s.catalog.SourceTools(ctx, generation.ID)
	if err != nil {
		return SearchOutput{}, err
	}
	if len(sources) == 0 {
		return SearchOutput{GenerationID: generation.ID, EffectiveProfile: profile, PreferenceRevision: preferenceRevision, Results: []SearchResult{}}, nil
	}
	availableByKey := make(map[string]bool, len(sources))
	unavailableCount := 0
	for _, source := range sources {
		available, known, err := s.catalog.ToolAvailability(ctx, source.ServerID, source.ToolName, source.SourceFingerprint)
		if err != nil {
			return SearchOutput{}, err
		}
		key := enrichment.MemberKey(source.ServerID, source.ToolName)
		availableByKey[key] = !known || available
		if known && !available {
			unavailableCount++
		}
	}
	if unavailableCount == len(sources) {
		return SearchOutput{GenerationID: generation.ID, EffectiveProfile: profile, PreferenceRevision: preferenceRevision, Results: []SearchResult{}}, nil
	}
	guidance, err := s.loadGuidance(ctx, generation.ID)
	if err != nil {
		return SearchOutput{}, err
	}
	hierarchy, err := s.loadCapabilityHierarchy(ctx, generation.ID)
	if err != nil {
		return SearchOutput{}, err
	}

	lexicalIndex, err := s.retrieval.LoadLexicalIndex(ctx, generation.ID)
	if err != nil {
		return SearchOutput{}, err
	}
	sourceIndex, err := s.retrieval.LoadVectorIndex(ctx, generation.ID, retrieval.RoleSourceDescriptionVector)
	if err != nil {
		return SearchOutput{}, err
	}
	schemaIndex, err := s.retrieval.LoadVectorIndex(ctx, generation.ID, retrieval.RoleInputSchemaVector)
	if err != nil {
		return SearchOutput{}, err
	}
	enrichedIndex, err := s.retrieval.LoadVectorIndex(ctx, generation.ID, enrichment.RoleEnrichedEmbedding)
	if err != nil {
		return SearchOutput{}, err
	}
	if lexicalIndex.Len() != len(sources) || sourceIndex.Len() != len(sources) || schemaIndex.Len() != len(sources) || enrichedIndex.Len() != len(sources) {
		return SearchOutput{}, ErrIndexRequired
	}

	queryVector, err := embedding.EmbedQuery(ctx, s.provider, s.queryCache, query)
	if err != nil {
		return SearchOutput{}, fmt.Errorf("embed search query: %w", err)
	}
	depth := s.options.CandidateDepth
	if minimum := limit + unavailableCount; depth < minimum {
		depth = minimum
	}
	if depth > len(sources) {
		depth = len(sources)
	}
	lexicalResults, err := lexicalIndex.Search(query, depth)
	if err != nil {
		return SearchOutput{}, err
	}
	sourceResults, err := sourceIndex.Search(queryVector, depth)
	if err != nil {
		return SearchOutput{}, err
	}
	schemaResults, err := schemaIndex.Search(queryVector, depth)
	if err != nil {
		return SearchOutput{}, err
	}
	enrichedResults, err := enrichedIndex.Search(queryVector, depth)
	if err != nil {
		return SearchOutput{}, err
	}
	capabilityResults := capabilityVectorRanking(queryVector, hierarchy, enrichedIndex)
	if len(capabilityResults) > depth {
		capabilityResults = capabilityResults[:depth]
	}

	facets := []RankedFacet{
		{Name: "lexical", Weight: 1, Keys: lexicalKeys(lexicalResults)},
		{Name: "source_description", Weight: 1, Keys: vectorKeys(sourceResults)},
		{Name: "input_schema", Weight: 1, Keys: vectorKeys(schemaResults)},
		{Name: "enrichment", Weight: 1, Keys: vectorKeys(enrichedResults)},
		{Name: "capability", Weight: 1, Keys: scoredKeys(capabilityResults)},
	}
	fused, err := reciprocalRankFusion(facets, s.options.RRFK)
	if err != nil {
		return SearchOutput{}, err
	}

	sourceByKey := make(map[string]catalog.SourceToolRecord, len(sources))
	serverByID := make(map[string]catalog.SourceServerContract)
	for _, source := range sources {
		key := enrichment.MemberKey(source.ServerID, source.ToolName)
		sourceByKey[key] = source
		if _, exists := serverByID[source.ServerID]; !exists {
			server, err := s.catalog.SourceServer(ctx, generation.ID, source.ServerID)
			if err != nil {
				return SearchOutput{}, fmt.Errorf("load source server %s: %w", source.ServerID, err)
			}
			serverByID[source.ServerID] = server.Contract
		}
	}
	vectorEvidence := make(map[string]float64, len(sources))
	collectVectorEvidence(vectorEvidence, sourceResults)
	collectVectorEvidence(vectorEvidence, schemaResults)
	collectVectorEvidence(vectorEvidence, enrichedResults)
	for _, result := range capabilityResults {
		if result.Score > vectorEvidence[result.Key] {
			vectorEvidence[result.Key] = result.Score
		}
	}
	capabilityByMember := capabilitySummaries(hierarchy)

	candidates := make([]candidate, 0, len(fused))
	for _, item := range fused {
		if !availableByKey[item.Key] {
			continue
		}
		source, ok := sourceByKey[item.Key]
		if !ok {
			return SearchOutput{}, fmt.Errorf("%w: fused result %s has no authoritative source", ErrIndexRequired, item.Key)
		}
		presentation, err := parsePresentation(source)
		if err != nil {
			return SearchOutput{}, err
		}
		text := presentation.Name + "\n" + presentation.Title + "\n" + presentation.Description
		if itemGuidance := guidance[item.Key]; itemGuidance != nil {
			body, _ := json.Marshal(itemGuidance)
			text += "\n" + string(body)
		}
		for _, capability := range capabilityByMember[item.Key] {
			text += "\n" + capability.Path + "\n" + capability.Description
		}
		coverage := lexicalCoverage(query, text)
		maxVector := vectorEvidence[item.Key]
		if maxVector < s.options.MinVectorSimilarity && coverage < s.options.MinLexicalCoverage {
			continue
		}
		candidates = append(candidates, candidate{
			key:             item.Key,
			source:          source,
			presentation:    presentation,
			server:          serverByID[source.ServerID],
			guidance:        guidance[item.Key],
			capabilities:    capabilityByMember[item.Key],
			fusedScore:      item.Score,
			maxVectorScore:  maxVector,
			lexicalCoverage: coverage,
		})
	}

	rules, err := s.preferences.EffectiveRules(ctx, profileID)
	if err != nil {
		return SearchOutput{}, err
	}
	preferenceContext := query
	if strings.TrimSpace(input.Context) != "" {
		preferenceContext += "\n" + input.Context
	}
	for index := range candidates {
		candidates[index].preferenceAdjust = preferenceAdjustment(rules, candidates[index], preferenceContext)
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].fusedScore + candidates[i].preferenceAdjust
		right := candidates[j].fusedScore + candidates[j].preferenceAdjust
		if left == right {
			if candidates[i].fusedScore == candidates[j].fusedScore {
				return candidates[i].key < candidates[j].key
			}
			return candidates[i].fusedScore > candidates[j].fusedScore
		}
		return left > right
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	results := make([]SearchResult, 0, len(candidates))
	for _, item := range candidates {
		class, err := toolcontract.ExecutorClassForJSON(item.source.ContractJSON)
		if err != nil {
			return SearchOutput{}, err
		}
		toolRef, err := encodeToolReference(toolReferenceClaims{
			GenerationID:      generation.ID,
			ServerID:          item.source.ServerID,
			ToolName:          item.source.ToolName,
			SourceFingerprint: item.source.SourceFingerprint,
		})
		if err != nil {
			return SearchOutput{}, err
		}
		summary := item.presentation.Description
		if item.guidance != nil && strings.TrimSpace(item.guidance.Purpose) != "" {
			summary = item.guidance.Purpose
		}
		results = append(results, SearchResult{
			ToolRef:       toolRef,
			ServerName:    item.server.Name,
			ToolName:      item.source.ToolName,
			Title:         item.presentation.Title,
			Summary:       summary,
			ExecutorClass: class,
		})
	}
	return SearchOutput{GenerationID: generation.ID, EffectiveProfile: profile, PreferenceRevision: preferenceRevision, Results: results}, nil
}

func (s *Service) GetTool(ctx context.Context, input GetToolInput) (GetToolOutput, error) {
	claims, err := decodeToolReference(strings.TrimSpace(input.ToolRef))
	if err != nil {
		return GetToolOutput{}, err
	}
	generation, err := s.currentGeneration(ctx)
	if err != nil {
		return GetToolOutput{}, err
	}
	if claims.GenerationID != generation.ID {
		return GetToolOutput{}, ErrInvalidToolReference
	}
	sources, err := s.catalog.SourceTools(ctx, generation.ID)
	if err != nil {
		return GetToolOutput{}, err
	}
	var source *catalog.SourceToolRecord
	for index := range sources {
		if sources[index].ServerID == claims.ServerID && sources[index].ToolName == claims.ToolName {
			source = &sources[index]
			break
		}
	}
	if source == nil || source.SourceFingerprint != claims.SourceFingerprint {
		return GetToolOutput{}, ErrInvalidToolReference
	}
	available, known, err := s.catalog.ToolAvailability(ctx, source.ServerID, source.ToolName, source.SourceFingerprint)
	if err != nil {
		return GetToolOutput{}, err
	}
	if known && !available {
		return GetToolOutput{}, ErrInvalidToolReference
	}
	server, err := s.catalog.SourceServer(ctx, generation.ID, source.ServerID)
	if err != nil {
		return GetToolOutput{}, err
	}
	presentation, err := parsePresentation(*source)
	if err != nil {
		return GetToolOutput{}, err
	}
	class, err := toolcontract.ExecutorClassForJSON(source.ContractJSON)
	if err != nil {
		return GetToolOutput{}, err
	}
	guidance, err := s.loadGuidance(ctx, generation.ID)
	if err != nil {
		return GetToolOutput{}, err
	}
	hierarchy, err := s.loadCapabilityHierarchy(ctx, generation.ID)
	if err != nil {
		return GetToolOutput{}, err
	}
	memberKey := enrichment.MemberKey(source.ServerID, source.ToolName)
	capabilities := capabilitySummaries(hierarchy)[memberKey]
	handle, err := s.handles.Mint(executionhandle.Claims{
		GenerationID:      generation.ID,
		ServerID:          source.ServerID,
		ToolName:          source.ToolName,
		SourceFingerprint: source.SourceFingerprint,
		ExecutorClass:     string(class),
	})
	if err != nil {
		return GetToolOutput{}, err
	}
	title := presentation.Title
	displayName := presentation.Name
	if title != "" {
		displayName = title
	}
	return GetToolOutput{
		GenerationID: generation.ID,
		Authoritative: AuthoritativeTool{
			Server:             server.Contract,
			InvocationIdentity: InvocationIdentity{ServerID: source.ServerID, ToolName: source.ToolName},
			SourceFingerprint:  source.SourceFingerprint,
			ExecutorClass:      class,
			Tool:               append(json.RawMessage(nil), source.ContractJSON...),
		},
		Derived: DerivedTool{SemanticGuidance: guidance[memberKey], Capabilities: capabilities},
		HumanIdentity: HumanToolIdentity{
			ServerName: server.Contract.Name,
			ToolName:   source.ToolName,
			Title:      presentation.Title,
			Display:    server.Contract.Name + " / " + displayName,
		},
		ExecutionHandle: handle,
	}, nil
}

func (s *Service) currentGeneration(ctx context.Context) (catalog.Generation, error) {
	state, err := s.state.Snapshot(ctx)
	if err != nil {
		return catalog.Generation{}, err
	}
	if strings.TrimSpace(state.RoutingStateHash) == "" {
		return catalog.Generation{}, ErrIndexRequired
	}
	generation, current, err := s.catalog.ActiveCurrent(ctx, state.RoutingStateHash)
	if err != nil {
		return catalog.Generation{}, err
	}
	if !current {
		return catalog.Generation{}, ErrIndexRequired
	}
	return generation, nil
}

func (s *Service) resolveProfile(ctx context.Context, requested string) (*EffectiveProfile, string, error) {
	selector := requested
	if selector == "" {
		selector = strings.TrimSpace(s.options.DefaultProfile)
	}
	if selector == "" {
		return nil, "", nil
	}
	profiles, err := s.preferences.ListProfiles(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, profile := range profiles {
		if profile.ID == selector {
			return &EffectiveProfile{ID: profile.ID, Name: profile.Name}, profile.ID, nil
		}
	}
	for _, profile := range profiles {
		if profile.Name == selector {
			return &EffectiveProfile{ID: profile.ID, Name: profile.Name}, profile.ID, nil
		}
	}
	return nil, "", ErrRoutingProfileNotFound
}

func (s *Service) loadGuidance(ctx context.Context, generationID string) (map[string]*enrichment.ToolGuidance, error) {
	artifacts, err := s.catalog.GenerationArtifacts(ctx, generationID, enrichment.RoleToolEnrichment)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*enrichment.ToolGuidance, len(artifacts))
	for _, record := range artifacts {
		var guidance enrichment.ToolGuidance
		if err := json.Unmarshal(record.Artifact.Payload, &guidance); err != nil {
			return nil, fmt.Errorf("decode semantic guidance %s: %w", record.MemberKey, err)
		}
		copyGuidance := guidance
		result[record.MemberKey] = &copyGuidance
	}
	return result, nil
}

func (s *Service) loadCapabilityHierarchy(ctx context.Context, generationID string) (enrichment.CapabilityHierarchy, error) {
	artifacts, err := s.catalog.GenerationArtifacts(ctx, generationID, enrichment.RoleCapabilityHierarchy)
	if err != nil {
		return enrichment.CapabilityHierarchy{}, err
	}
	if len(artifacts) != 1 || artifacts[0].MemberKey != "global" {
		return enrichment.CapabilityHierarchy{}, ErrIndexRequired
	}
	var hierarchy enrichment.CapabilityHierarchy
	if err := json.Unmarshal(artifacts[0].Artifact.Payload, &hierarchy); err != nil {
		return enrichment.CapabilityHierarchy{}, fmt.Errorf("decode capability hierarchy: %w", err)
	}
	return hierarchy, nil
}

func parsePresentation(source catalog.SourceToolRecord) (toolPresentation, error) {
	var presentation toolPresentation
	if err := json.Unmarshal(source.ContractJSON, &presentation); err != nil {
		return toolPresentation{}, fmt.Errorf("decode source tool %s/%s: %w", source.ServerID, source.ToolName, err)
	}
	if strings.TrimSpace(presentation.Name) == "" || presentation.Name != source.ToolName {
		return toolPresentation{}, fmt.Errorf("%w: source invocation identity mismatch for %s/%s", ErrIndexRequired, source.ServerID, source.ToolName)
	}
	return presentation, nil
}
