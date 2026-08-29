package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
)

func (c *Coordinator) PrepareToolEnrichment(ctx context.Context, generationID string) (catalog.EnrichmentBatchCounts, error) {
	generation, err := c.catalog.Generation(ctx, generationID)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if generation.Status != catalog.GenerationStaging {
		return catalog.EnrichmentBatchCounts{}, catalog.ErrGenerationNotStaging
	}
	batchCount, err := c.catalog.EnrichmentBatchCount(ctx, generationID, catalog.BatchToolEnrichment)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if batchCount != 0 {
		if err := c.repairAcceptedBatches(ctx, generationID, catalog.BatchToolEnrichment); err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		return c.catalog.EnrichmentBatchCounts(ctx, generationID)
	}
	sources, err := c.catalog.SourceTools(ctx, generationID)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	capabilityGateKey, capabilityGateFingerprint := capabilityReconciliationGate(sources)
	if err := c.catalog.RequireDependency(ctx, generationID, capabilityGateKey, capabilityGateFingerprint); err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	vectorIndex, err := c.retrieval.LoadVectorIndex(ctx, generationID, retrieval.RoleSourceDescriptionVector)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if vectorIndex.Len() != len(sources) {
		return catalog.EnrichmentBatchCounts{}, fmt.Errorf("%w: source-description vector count %d does not match source tool count %d", ErrEnrichmentPrerequisite, vectorIndex.Len(), len(sources))
	}
	byMember := make(map[string]catalog.SourceToolRecord, len(sources))
	for _, source := range sources {
		byMember[MemberKey(source.ServerID, source.ToolName)] = source
	}
	for _, key := range vectorIndex.Keys() {
		if _, ok := byMember[key]; !ok {
			return catalog.EnrichmentBatchCounts{}, fmt.Errorf("%w: vector member %q has no authoritative source tool", ErrEnrichmentPrerequisite, key)
		}
	}
	var pending []ToolWork
	for _, source := range sources {
		memberKey := MemberKey(source.ServerID, source.ToolName)
		neighbors, err := vectorIndex.Neighbors(memberKey, c.options.NeighborhoodSize)
		if err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		work := ToolWork{Tool: toolRef(source)}
		for index, result := range neighbors {
			neighbor, ok := byMember[result.Key]
			if !ok {
				return catalog.EnrichmentBatchCounts{}, fmt.Errorf("%w: semantic neighbor %q has no authoritative source tool", ErrEnrichmentPrerequisite, result.Key)
			}
			work.Neighbors = append(work.Neighbors, NeighborRef{
				Rank:              index + 1,
				MemberKey:         result.Key,
				ServerID:          neighbor.ServerID,
				ToolName:          neighbor.ToolName,
				SourceFingerprint: neighbor.SourceFingerprint,
				Contract:          append(json.RawMessage(nil), neighbor.ContractJSON...),
			})
		}
		contextFingerprint, err := neighborhoodFingerprint(work)
		if err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		work.NeighborhoodContextFingerprint = contextFingerprint
		if err := c.catalog.SetNeighborhoodContext(ctx, generationID, memberKey, contextFingerprint); err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		spec := toolArtifactSpec(work)
		if _, err := c.catalog.RequireArtifact(ctx, generationID, spec); err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		embeddingGateKey, embeddingGateFingerprint := enrichedEmbeddingGate(work, c.embedding.Identity())
		if err := c.catalog.RequireDependency(ctx, generationID, embeddingGateKey, embeddingGateFingerprint); err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		reused, err := c.catalog.FindReusableArtifact(ctx, spec.Kind, spec.Dependencies, spec.ContextFingerprint)
		if err == nil {
			if err := c.catalog.FulfillArtifact(ctx, generationID, spec, reused.Key); err != nil {
				return catalog.EnrichmentBatchCounts{}, err
			}
			if err := c.ensureEnrichedEmbedding(ctx, generationID, work, reused.Payload); err != nil {
				return catalog.EnrichmentBatchCounts{}, err
			}
			if err := c.catalog.SatisfyDependency(ctx, generationID, embeddingGateKey, embeddingGateFingerprint, true); err != nil {
				return catalog.EnrichmentBatchCounts{}, err
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return catalog.EnrichmentBatchCounts{}, err
		}
		pending = append(pending, work)
	}
	var specs []catalog.EnrichmentBatchSpec
	for start, ordinal := 0, 0; start < len(pending); ordinal++ {
		end := start + c.options.ToolBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		var body []byte
		for {
			request := ToolBatchRequest{Protocol: ToolEnrichmentProtocolVersion, Items: append([]ToolWork(nil), pending[start:end]...)}
			encoded, err := json.Marshal(request)
			if err != nil {
				return catalog.EnrichmentBatchCounts{}, err
			}
			if len(encoded) <= c.options.MaxToolBatchBytes {
				body = encoded
				break
			}
			if end-start == 1 {
				return catalog.EnrichmentBatchCounts{}, fmt.Errorf("tool enrichment work item %s is %d bytes, exceeding batch bound %d", pending[start].Tool.MemberKey, len(encoded), c.options.MaxToolBatchBytes)
			}
			end--
		}
		batchKey := fmt.Sprintf("tool:%06d", ordinal)
		specs = append(specs, catalog.EnrichmentBatchSpec{
			ID:           deterministicBatchID(generationID, catalog.BatchToolEnrichment, batchKey, body),
			GenerationID: generationID,
			Kind:         catalog.BatchToolEnrichment,
			BatchKey:     batchKey,
			Required:     true,
			RequestJSON:  body,
		})
		start = end
	}
	if err := c.catalog.PutEnrichmentBatches(ctx, specs); err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	return c.catalog.EnrichmentBatchCounts(ctx, generationID)
}

func (c *Coordinator) PrepareCapabilityReconciliation(ctx context.Context, generationID string) (catalog.EnrichmentBatchCounts, error) {
	generation, err := c.catalog.Generation(ctx, generationID)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if generation.Status != catalog.GenerationStaging {
		return catalog.EnrichmentBatchCounts{}, catalog.ErrGenerationNotStaging
	}
	count, err := c.catalog.EnrichmentBatchCount(ctx, generationID, catalog.BatchCapabilityReconciliation)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if count != 0 {
		if err := c.repairAcceptedBatches(ctx, generationID, catalog.BatchCapabilityReconciliation); err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		return c.catalog.EnrichmentBatchCounts(ctx, generationID)
	}
	pendingTool, err := c.catalog.PendingEnrichmentBatches(ctx, generationID, catalog.BatchToolEnrichment, 1)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if len(pendingTool) != 0 {
		return catalog.EnrichmentBatchCounts{}, fmt.Errorf("%w: tool enrichment batches remain pending", ErrEnrichmentPrerequisite)
	}
	sources, err := c.catalog.SourceTools(ctx, generationID)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	capabilityGateKey, capabilityGateFingerprint := capabilityReconciliationGate(sources)
	if err := c.catalog.RequireDependency(ctx, generationID, capabilityGateKey, capabilityGateFingerprint); err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if len(sources) > c.options.MaxReconciliationTools {
		return catalog.EnrichmentBatchCounts{}, fmt.Errorf("capability reconciliation tool count %d exceeds configured bound %d", len(sources), c.options.MaxReconciliationTools)
	}
	artifacts, err := c.catalog.GenerationArtifacts(ctx, generationID, RoleToolEnrichment)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if len(artifacts) != len(sources) {
		return catalog.EnrichmentBatchCounts{}, fmt.Errorf("%w: tool enrichment artifact count %d does not match source tool count %d", ErrEnrichmentPrerequisite, len(artifacts), len(sources))
	}
	artifactByMember := make(map[string]catalog.Artifact, len(artifacts))
	for _, record := range artifacts {
		artifactByMember[record.MemberKey] = record.Artifact
	}
	request := CapabilityBatchRequest{Protocol: CapabilityProtocolVersion}
	dependencies := make([]catalog.ArtifactDependency, 0, len(sources)+1)
	dependencies = append(dependencies, catalog.ArtifactDependency{Key: "capability.protocol", Fingerprint: fingerprintText(CapabilityProtocolVersion)})
	for _, source := range sources {
		memberKey := MemberKey(source.ServerID, source.ToolName)
		artifact, ok := artifactByMember[memberKey]
		if !ok {
			return catalog.EnrichmentBatchCounts{}, fmt.Errorf("%w: missing tool enrichment for %s", ErrEnrichmentPrerequisite, memberKey)
		}
		request.Items = append(request.Items, CapabilityInputItem{Tool: toolRef(source), Enrichment: append(json.RawMessage(nil), artifact.Payload...)})
		dependencies = append(dependencies, catalog.ArtifactDependency{Key: "tool-enrichment:" + memberKey, Fingerprint: artifact.ContentFingerprint})
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	if len(requestBody) > c.options.MaxReconciliationBytes {
		return catalog.EnrichmentBatchCounts{}, fmt.Errorf("capability reconciliation request is %d bytes, exceeding bound %d", len(requestBody), c.options.MaxReconciliationBytes)
	}
	contextFingerprint, err := canonicalJSONFingerprint(requestBody)
	if err != nil {
		return catalog.EnrichmentBatchCounts{}, fmt.Errorf("canonicalize capability reconciliation identity: %w", err)
	}
	artifactSpec := catalog.RequiredArtifactSpec{Role: RoleCapabilityHierarchy, MemberKey: "global", Kind: CapabilityHierarchyArtifactKind, Dependencies: dependencies, ContextFingerprint: contextFingerprint}
	if _, err := c.catalog.RequireArtifact(ctx, generationID, artifactSpec); err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	batchKey := "global"
	batchSpec := catalog.EnrichmentBatchSpec{
		ID:           deterministicBatchID(generationID, catalog.BatchCapabilityReconciliation, batchKey, requestBody),
		GenerationID: generationID,
		Kind:         catalog.BatchCapabilityReconciliation,
		BatchKey:     batchKey,
		Required:     true,
		RequestJSON:  requestBody,
	}
	if err := c.catalog.PutEnrichmentBatches(ctx, []catalog.EnrichmentBatchSpec{batchSpec}); err != nil {
		return catalog.EnrichmentBatchCounts{}, err
	}
	reused, err := c.catalog.FindReusableArtifact(ctx, artifactSpec.Kind, artifactSpec.Dependencies, artifactSpec.ContextFingerprint)
	if err == nil {
		var hierarchy CapabilityHierarchy
		if err := json.Unmarshal(reused.Payload, &hierarchy); err != nil {
			return catalog.EnrichmentBatchCounts{}, fmt.Errorf("decode reusable capability hierarchy: %w", err)
		}
		responseBody, _ := json.Marshal(CapabilityBatchResponse{Hierarchy: hierarchy})
		if _, err := c.catalog.AcceptEnrichmentBatch(ctx, batchSpec.ID, responseBody); err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		if err := c.catalog.FulfillArtifact(ctx, generationID, artifactSpec, reused.Key); err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
		if err := c.catalog.SatisfyDependency(ctx, generationID, capabilityGateKey, capabilityGateFingerprint, true); err != nil {
			return catalog.EnrichmentBatchCounts{}, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return catalog.EnrichmentBatchCounts{}, err
	}
	return c.catalog.EnrichmentBatchCounts(ctx, generationID)
}

func (c *Coordinator) GetBatch(ctx context.Context, generationID string, kind catalog.EnrichmentBatchKind) (catalog.EnrichmentBatch, bool, error) {
	batches, err := c.catalog.PendingEnrichmentBatches(ctx, generationID, kind, 1)
	if err != nil {
		return catalog.EnrichmentBatch{}, false, err
	}
	if len(batches) == 0 {
		return catalog.EnrichmentBatch{}, false, nil
	}
	return batches[0], true, nil
}
