package app

import (
	"context"
	"errors"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
)

// V2RoutingTarget is a native-editor projection of an authoritative tool target.
// AssumptionFingerprint is derived from the authoritative contract and executor
// class; users never need to construct it manually.
type V2RoutingTarget struct {
	ServerID              string
	ToolName              string
	AssumptionFingerprint string
}

func (a *V2App) RoutingTargets(ctx context.Context) ([]V2RoutingTarget, error) {
	status, err := a.indexing.Status(ctx)
	if err != nil {
		return nil, err
	}
	generationID := status.StagingGenerationID
	if generationID == "" {
		generationID = status.ActiveGenerationID
	}
	if generationID == "" {
		return nil, nil
	}
	sources, err := a.catalog.SourceTools(ctx, generationID)
	if err != nil {
		if errors.Is(err, catalog.ErrGenerationNotFound) {
			return nil, nil
		}
		return nil, err
	}
	assumptions, err := routingprefs.CurrentAssumptions(sources)
	if err != nil {
		return nil, err
	}
	result := make([]V2RoutingTarget, 0, len(sources))
	for _, source := range sources {
		result = append(result, V2RoutingTarget{
			ServerID: source.ServerID,
			ToolName: source.ToolName,
			AssumptionFingerprint: assumptions[routingprefs.TargetMapKey(source.ServerID, source.ToolName)],
		})
	}
	return result, nil
}
