package app

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
)

// V2RoutingTarget is a native-editor projection of an authoritative tool target.
// AssumptionFingerprint is derived from the authoritative contract and executor
// class; users never need to construct it manually. A blank fingerprint means
// the tool is visible in the current live downstream contract but has not yet
// been incorporated into the active/staging routing generation.
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

	indexed := make([]V2RoutingTarget, 0)
	if generationID != "" {
		sources, err := a.catalog.SourceTools(ctx, generationID)
		if err != nil {
			if !errors.Is(err, catalog.ErrGenerationNotFound) {
				return nil, err
			}
		} else {
			assumptions, err := routingprefs.CurrentAssumptions(sources)
			if err != nil {
				return nil, err
			}
			indexed = make([]V2RoutingTarget, 0, len(sources))
			for _, source := range sources {
				indexed = append(indexed, V2RoutingTarget{
					ServerID:              source.ServerID,
					ToolName:              source.ToolName,
					AssumptionFingerprint: assumptions[routingprefs.TargetMapKey(source.ServerID, source.ToolName)],
				})
			}
		}
	}

	// The desktop workspace is a live operational view, not only a snapshot of
	// the last committed generation. If a running downstream server publishes a
	// newer tools/list contract, prefer that live membership immediately while
	// preserving fingerprints for tools that are already indexed. Live-only
	// tools carry a blank fingerprint so the UI can show that a refresh is
	// required before routing preferences can safely target them.
	live := make(map[string][]string)
	if a != nil && a.lifecycle != nil {
		for _, entry := range a.Entries() {
			snapshot, err := a.lifecycle.KnownTools(entry.ID)
			if err != nil || len(snapshot.Tools) == 0 {
				continue
			}
			seen := make(map[string]struct{}, len(snapshot.Tools))
			for _, tool := range snapshot.Tools {
				if tool == nil {
					continue
				}
				name := strings.TrimSpace(tool.Name)
				if name == "" {
					continue
				}
				seen[name] = struct{}{}
			}
			if len(seen) == 0 {
				continue
			}
			names := make([]string, 0, len(seen))
			for name := range seen {
				names = append(names, name)
			}
			sort.Strings(names)
			live[entry.ID] = names
		}
	}
	return mergeRoutingTargets(indexed, live), nil
}

func mergeRoutingTargets(indexed []V2RoutingTarget, live map[string][]string) []V2RoutingTarget {
	indexedByKey := make(map[string]V2RoutingTarget, len(indexed))
	for _, target := range indexed {
		indexedByKey[target.ServerID+"\x00"+target.ToolName] = target
	}

	result := make([]V2RoutingTarget, 0, len(indexed))
	for _, target := range indexed {
		if _, hasLive := live[target.ServerID]; !hasLive {
			result = append(result, target)
		}
	}
	for serverID, names := range live {
		for _, name := range names {
			key := serverID + "\x00" + name
			if target, ok := indexedByKey[key]; ok {
				result = append(result, target)
				continue
			}
			result = append(result, V2RoutingTarget{ServerID: serverID, ToolName: name})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ServerID == result[j].ServerID {
			return result[i].ToolName < result[j].ToolName
		}
		return result[i].ServerID < result[j].ServerID
	})
	return result
}
