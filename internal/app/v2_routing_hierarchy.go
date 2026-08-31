package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
)

// RoutingCapabilityHierarchy returns the accepted semantic capability hierarchy
// for the generation currently shown by the routing editor. The staging
// generation wins while an index is being rebuilt; otherwise the active
// generation is used. A false found value means capability reconciliation has
// not produced a hierarchy yet.
func (a *V2App) RoutingCapabilityHierarchy(ctx context.Context) (hierarchy enrichment.CapabilityHierarchy, found bool, err error) {
	status, err := a.indexing.Status(ctx)
	if err != nil {
		return enrichment.CapabilityHierarchy{}, false, err
	}
	generationID := status.StagingGenerationID
	if generationID == "" {
		generationID = status.ActiveGenerationID
	}
	if generationID == "" {
		return enrichment.CapabilityHierarchy{}, false, nil
	}
	artifacts, err := a.catalog.GenerationArtifacts(ctx, generationID, enrichment.RoleCapabilityHierarchy)
	if err != nil {
		return enrichment.CapabilityHierarchy{}, false, err
	}
	for _, record := range artifacts {
		if record.MemberKey != "global" {
			continue
		}
		var value enrichment.CapabilityHierarchy
		if err := json.Unmarshal(record.Artifact.Payload, &value); err != nil {
			return enrichment.CapabilityHierarchy{}, false, fmt.Errorf("decode capability hierarchy: %w", err)
		}
		return value, true, nil
	}
	return enrichment.CapabilityHierarchy{}, false, nil
}
