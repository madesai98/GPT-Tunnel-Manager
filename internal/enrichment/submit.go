package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

func (c *Coordinator) SubmitBatch(ctx context.Context, batchID string, responseJSON []byte) (SubmitResult, error) {
	batch, err := c.catalog.EnrichmentBatch(ctx, batchID)
	if err != nil {
		return SubmitResult{}, err
	}
	switch batch.Kind {
	case catalog.BatchToolEnrichment:
		return c.submitToolBatch(ctx, batch, responseJSON)
	case catalog.BatchCapabilityReconciliation:
		return c.submitCapabilityBatch(ctx, batch, responseJSON)
	case catalog.BatchAmbiguityReview:
		return c.submitAmbiguityReview(ctx, batch, responseJSON)
	default:
		return SubmitResult{}, fmt.Errorf("unsupported enrichment batch kind %q", batch.Kind)
	}
}

func (c *Coordinator) submitToolBatch(ctx context.Context, batch catalog.EnrichmentBatch, responseJSON []byte) (SubmitResult, error) {
	var request ToolBatchRequest
	if err := json.Unmarshal(batch.RequestJSON, &request); err != nil {
		return SubmitResult{}, fmt.Errorf("decode tool enrichment batch request: %w", err)
	}
	var response ToolBatchResponse
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		return SubmitResult{}, fmt.Errorf("decode tool enrichment batch response: %w", err)
	}
	workByMember := make(map[string]ToolWork, len(request.Items))
	for _, work := range request.Items {
		workByMember[work.Tool.MemberKey] = work
	}
	results := make(map[string]ToolEnrichmentResult, len(response.Items))
	for _, item := range response.Items {
		if _, ok := workByMember[item.MemberKey]; !ok {
			return SubmitResult{}, fmt.Errorf("tool enrichment response contains unknown member %q", item.MemberKey)
		}
		if _, duplicate := results[item.MemberKey]; duplicate {
			return SubmitResult{}, fmt.Errorf("tool enrichment response repeats member %q", item.MemberKey)
		}
		if strings.TrimSpace(item.Guidance.Purpose) == "" {
			return SubmitResult{}, fmt.Errorf("tool enrichment %s is missing purpose", item.MemberKey)
		}
		results[item.MemberKey] = item
	}
	if len(results) != len(workByMember) {
		return SubmitResult{}, fmt.Errorf("tool enrichment response has %d item(s), want %d", len(results), len(workByMember))
	}
	allSources, err := c.catalog.SourceTools(ctx, batch.GenerationID)
	if err != nil {
		return SubmitResult{}, err
	}
	knownMembers := make(map[string]struct{}, len(allSources))
	for _, source := range allSources {
		knownMembers[MemberKey(source.ServerID, source.ToolName)] = struct{}{}
	}
	for _, item := range response.Items {
		for _, alternative := range item.Guidance.Alternatives {
			if _, ok := knownMembers[alternative]; !ok {
				return SubmitResult{}, fmt.Errorf("tool enrichment %s references unknown alternative %q", item.MemberKey, alternative)
			}
		}
	}
	canonicalResponse, err := json.Marshal(response)
	if err != nil {
		return SubmitResult{}, err
	}
	accepted, err := c.catalog.AcceptEnrichmentBatch(ctx, batch.ID, canonicalResponse)
	if err != nil {
		return SubmitResult{}, err
	}
	if accepted.Idempotent {
		generation, err := c.catalog.Generation(ctx, batch.GenerationID)
		if err != nil {
			return SubmitResult{}, err
		}
		if generation.Status != catalog.GenerationStaging {
			return SubmitResult{Batch: accepted.Batch, Idempotent: true}, nil
		}
	}
	for _, work := range request.Items {
		item := results[work.Tool.MemberKey]
		payload, err := json.Marshal(item.Guidance)
		if err != nil {
			return SubmitResult{}, err
		}
		spec := toolArtifactSpec(work)
		artifact, err := c.catalog.PutArtifact(ctx, catalog.ArtifactSpec{Kind: spec.Kind, Payload: payload, Dependencies: spec.Dependencies, ContextFingerprint: spec.ContextFingerprint})
		if err != nil {
			return SubmitResult{}, err
		}
		if err := c.catalog.FulfillArtifact(ctx, batch.GenerationID, spec, artifact.Key); err != nil {
			return SubmitResult{}, err
		}
		if err := c.ensureEnrichedEmbedding(ctx, batch.GenerationID, work, payload); err != nil {
			return SubmitResult{}, err
		}
		gateKey, gateFingerprint := enrichedEmbeddingGate(work, c.embedding.Identity())
		if err := c.catalog.SatisfyDependency(ctx, batch.GenerationID, gateKey, gateFingerprint, true); err != nil {
			return SubmitResult{}, err
		}
	}
	return SubmitResult{Batch: accepted.Batch, Idempotent: accepted.Idempotent}, nil
}

func (c *Coordinator) submitCapabilityBatch(ctx context.Context, batch catalog.EnrichmentBatch, responseJSON []byte) (SubmitResult, error) {
	var request CapabilityBatchRequest
	if err := json.Unmarshal(batch.RequestJSON, &request); err != nil {
		return SubmitResult{}, fmt.Errorf("decode capability batch request: %w", err)
	}
	var response CapabilityBatchResponse
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		return SubmitResult{}, fmt.Errorf("decode capability batch response: %w", err)
	}
	known := make(map[string]struct{}, len(request.Items))
	for _, item := range request.Items {
		known[item.Tool.MemberKey] = struct{}{}
	}
	if err := validateHierarchy(response.Hierarchy, known); err != nil {
		return SubmitResult{}, err
	}
	for _, proposal := range response.Ambiguities {
		if err := validateAmbiguity(proposal, known); err != nil {
			return SubmitResult{}, err
		}
	}
	canonicalResponse, err := json.Marshal(response)
	if err != nil {
		return SubmitResult{}, err
	}
	accepted, err := c.catalog.AcceptEnrichmentBatch(ctx, batch.ID, canonicalResponse)
	if err != nil {
		return SubmitResult{}, err
	}
	if accepted.Idempotent {
		generation, err := c.catalog.Generation(ctx, batch.GenerationID)
		if err != nil {
			return SubmitResult{}, err
		}
		if generation.Status != catalog.GenerationStaging {
			return SubmitResult{Batch: accepted.Batch, Idempotent: true}, nil
		}
	}
	dependencies := make([]catalog.ArtifactDependency, 0, len(request.Items)+1)
	dependencies = append(dependencies, catalog.ArtifactDependency{Key: "capability.protocol", Fingerprint: fingerprintText(CapabilityProtocolVersion)})
	artifacts, err := c.catalog.GenerationArtifacts(ctx, batch.GenerationID, RoleToolEnrichment)
	if err != nil {
		return SubmitResult{}, err
	}
	artifactByMember := make(map[string]catalog.Artifact, len(artifacts))
	for _, record := range artifacts {
		artifactByMember[record.MemberKey] = record.Artifact
	}
	for _, item := range request.Items {
		artifact, ok := artifactByMember[item.Tool.MemberKey]
		if !ok {
			return SubmitResult{}, fmt.Errorf("%w: missing tool enrichment for %s", ErrEnrichmentPrerequisite, item.Tool.MemberKey)
		}
		dependencies = append(dependencies, catalog.ArtifactDependency{Key: "tool-enrichment:" + item.Tool.MemberKey, Fingerprint: artifact.ContentFingerprint})
	}
	requestIdentity, err := json.Marshal(request)
	if err != nil {
		return SubmitResult{}, err
	}
	contextFingerprint := toolcontract.FingerprintJSON(requestIdentity)
	spec := catalog.RequiredArtifactSpec{Role: RoleCapabilityHierarchy, MemberKey: "global", Kind: CapabilityHierarchyArtifactKind, Dependencies: dependencies, ContextFingerprint: contextFingerprint}
	hierarchyPayload, err := json.Marshal(response.Hierarchy)
	if err != nil {
		return SubmitResult{}, err
	}
	if err := c.createAmbiguityBatches(ctx, batch.GenerationID, response.Ambiguities); err != nil {
		return SubmitResult{}, err
	}
	artifact, err := c.catalog.PutArtifact(ctx, catalog.ArtifactSpec{Kind: spec.Kind, Payload: hierarchyPayload, Dependencies: spec.Dependencies, ContextFingerprint: spec.ContextFingerprint})
	if err != nil {
		return SubmitResult{}, err
	}
	if err := c.catalog.FulfillArtifact(ctx, batch.GenerationID, spec, artifact.Key); err != nil {
		return SubmitResult{}, err
	}
	sources, err := c.catalog.SourceTools(ctx, batch.GenerationID)
	if err != nil {
		return SubmitResult{}, err
	}
	gateKey, gateFingerprint := capabilityReconciliationGate(sources)
	if err := c.catalog.SatisfyDependency(ctx, batch.GenerationID, gateKey, gateFingerprint, true); err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{Batch: accepted.Batch, Idempotent: accepted.Idempotent}, nil
}

func (c *Coordinator) submitAmbiguityReview(ctx context.Context, batch catalog.EnrichmentBatch, responseJSON []byte) (SubmitResult, error) {
	var response AmbiguityReviewResponse
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		return SubmitResult{}, fmt.Errorf("decode ambiguity review response: %w", err)
	}
	switch response.Resolution {
	case AmbiguityNeutral:
		if len(response.PreferenceIDs) != 0 {
			return SubmitResult{}, errors.New("neutral ambiguity resolution cannot reference preferences")
		}
	case AmbiguityPreference:
		if len(response.PreferenceIDs) == 0 {
			return SubmitResult{}, errors.New("preference ambiguity resolution requires at least one persisted preference id")
		}
	default:
		return SubmitResult{}, fmt.Errorf("unsupported ambiguity resolution %q", response.Resolution)
	}
	canonical, err := json.Marshal(response)
	if err != nil {
		return SubmitResult{}, err
	}
	accepted, err := c.catalog.AcceptEnrichmentBatch(ctx, batch.ID, canonical)
	if err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{Batch: accepted.Batch, Idempotent: accepted.Idempotent}, nil
}

func (c *Coordinator) createAmbiguityBatches(ctx context.Context, generationID string, proposals []AmbiguityProposal) error {
	if len(proposals) == 0 {
		return nil
	}
	normalized := append([]AmbiguityProposal(nil), proposals...)
	sort.Slice(normalized, func(i, j int) bool {
		left, _ := json.Marshal(normalized[i])
		right, _ := json.Marshal(normalized[j])
		return string(left) < string(right)
	})
	specs := make([]catalog.EnrichmentBatchSpec, 0, len(normalized))
	for index, proposal := range normalized {
		request := AmbiguityReviewRequest{Protocol: AmbiguityReviewProtocolVersion, Proposal: proposal, Generation: generationID}
		body, err := json.Marshal(request)
		if err != nil {
			return err
		}
		batchKey := fmt.Sprintf("review:%06d:%s", index, shortFingerprint(body))
		specs = append(specs, catalog.EnrichmentBatchSpec{
			ID:           deterministicBatchID(generationID, catalog.BatchAmbiguityReview, batchKey, body),
			GenerationID: generationID,
			Kind:         catalog.BatchAmbiguityReview,
			BatchKey:     batchKey,
			Required:     false,
			RequestJSON:  body,
		})
	}
	return c.catalog.PutEnrichmentBatches(ctx, specs)
}

func (c *Coordinator) ensureEnrichedEmbedding(ctx context.Context, generationID string, work ToolWork, guidancePayload []byte) error {
	projection, err := enrichedProjection(work, guidancePayload)
	if err != nil {
		return err
	}
	identity := c.embedding.Identity()
	if err := c.retrieval.RequireEmbedding(ctx, generationID, RoleEnrichedEmbedding, work.Tool.MemberKey, identity, projection); err != nil {
		return err
	}
	vector, _, reused, err := c.retrieval.ReuseEmbedding(ctx, RoleEnrichedEmbedding, work.Tool.MemberKey, identity, projection)
	if err != nil {
		return err
	}
	if !reused {
		vectors, err := c.embedding.Embed(ctx, []string{projection.Text})
		if err != nil {
			return err
		}
		if len(vectors) != 1 {
			return fmt.Errorf("embedding provider returned %d vectors for one enrichment", len(vectors))
		}
		vector = vectors[0]
	}
	_, err = c.retrieval.StoreEmbedding(ctx, generationID, RoleEnrichedEmbedding, work.Tool.MemberKey, identity, projection, vector)
	return err
}
