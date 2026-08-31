package indexing

import (
	"context"
	"fmt"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func (s *Service) applyDirtyStatus(ctx context.Context, status Status) (Status, error) {
	partitions, err := s.catalog.DirtyPartitions(ctx)
	if err != nil {
		return Status{}, err
	}
	if len(partitions) == 0 {
		return status, nil
	}
	status.Ready = false
	for _, partition := range partitions {
		blocker := fmt.Sprintf("dirty routing partition %q: %s", partition.PartitionKey, partition.Reason)
		if partition.ObservedFingerprint != "" {
			blocker += fmt.Sprintf(" (observed fingerprint %s)", partition.ObservedFingerprint)
		}
		status.PromotionBlockers = append(status.PromotionBlockers, blocker)
	}
	status.NextAction = "Routing source state changed after the current index build. Call index_refresh before processing more enrichment or calling index_commit. Refresh preserves accepted enrichment when authoritative source contracts are unchanged and creates a fresh staging generation only when they actually changed."
	return status, nil
}

// refreshLocked reconciles both the immutable generation build and the global
// dirty-partition invalidation gate. The caller must hold Service.mu.
func (s *Service) refreshLocked(ctx context.Context, hash string) (RefreshResult, error) {
	if _, err := s.catalog.ReconcileStaging(ctx, hash); err != nil {
		return RefreshResult{}, err
	}

	// Snapshot the exact dirty markers before acquiring downstream sources. A
	// concurrent MarkDirty replaces marked_at/reason/fingerprint; the final
	// compare-and-clear then leaves that newer invalidation intact.
	dirtyBefore, err := s.catalog.DirtyPartitions(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	snapshots, err := s.acquireIndexSnapshots(ctx)
	if err != nil {
		return RefreshResult{}, err
	}

	generation, err := s.stagingForHash(ctx, hash)
	if err != nil {
		generation, err = s.catalog.CreateStaging(ctx, catalog.GenerationSpec{RoutingStateHash: hash})
		if err != nil {
			return RefreshResult{}, err
		}
	} else {
		changed, compareErr := s.stagingSourcesChanged(ctx, generation.ID, snapshots)
		if compareErr != nil {
			return RefreshResult{}, compareErr
		}
		if changed {
			// Enrichment batches are immutable and may already be accepted. A
			// changed authoritative source set therefore needs a new generation;
			// content-addressed artifacts for unaffected tools are still reused by
			// PrepareToolEnrichment.
			if err := s.catalog.SupersedeStaging(ctx, generation.ID); err != nil {
				return RefreshResult{}, err
			}
			generation, err = s.catalog.CreateStaging(ctx, catalog.GenerationSpec{RoutingStateHash: hash})
			if err != nil {
				return RefreshResult{}, err
			}
		}
	}

	if err := s.populateBaseIndexFromSnapshots(ctx, generation.ID, snapshots); err != nil {
		return RefreshResult{}, err
	}
	if _, err := s.enrichment.PrepareToolEnrichment(ctx, generation.ID); err != nil {
		return RefreshResult{}, err
	}
	if err := s.reconcileObservedDirtyPartitions(ctx, dirtyBefore, snapshots); err != nil {
		return RefreshResult{}, err
	}
	status, err := s.Status(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{Status: status}, nil
}

func (s *Service) acquireIndexSnapshots(ctx context.Context) (map[string]downstream.ToolSnapshot, error) {
	snapshots := make(map[string]downstream.ToolSnapshot)
	for _, entry := range s.servers.Servers {
		if entry.Mode == v2config.ModeDisabled {
			continue
		}
		lease, err := s.lifecycle.Acquire(ctx, entry.ID)
		if err != nil {
			if strings.Contains(err.Error(), routedlifecycle.ErrManualServerStopped.Error()) {
				return nil, &Error{Code: CodeManualServerStoppedForIndex, Message: fmt.Sprintf("manual server %s is stopped and must be started before indexing", entry.ID), cause: err}
			}
			return nil, err
		}
		snapshots[entry.ID] = lease.InitialTools()
		lease.Release()
	}
	return snapshots, nil
}

func (s *Service) stagingSourcesChanged(ctx context.Context, generationID string, snapshots map[string]downstream.ToolSnapshot) (bool, error) {
	existing, err := s.catalog.SourceTools(ctx, generationID)
	if err != nil {
		return false, err
	}
	// A just-created/unpopulated staging generation has nothing to invalidate.
	if len(existing) == 0 {
		return false, nil
	}
	stored := make(map[string]string, len(existing))
	for _, source := range existing {
		stored[enrichment.MemberKey(source.ServerID, source.ToolName)] = source.SourceFingerprint
	}
	current, err := s.currentSourceFingerprints(snapshots)
	if err != nil {
		return false, err
	}
	if len(stored) != len(current) {
		return true, nil
	}
	for memberKey, fingerprint := range stored {
		if current[memberKey] != fingerprint {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) currentSourceFingerprints(snapshots map[string]downstream.ToolSnapshot) (map[string]string, error) {
	current := make(map[string]string)
	for _, entry := range s.servers.Servers {
		if entry.Mode == v2config.ModeDisabled {
			continue
		}
		snapshot, ok := snapshots[entry.ID]
		if !ok {
			return nil, fmt.Errorf("missing authoritative tool snapshot for server %s", entry.ID)
		}
		for _, tool := range snapshot.Tools {
			if !entry.ToolExposed(tool.Name) {
				continue
			}
			fingerprint, _, err := toolcontract.FingerprintTool(tool)
			if err != nil {
				return nil, err
			}
			current[enrichment.MemberKey(entry.ID, tool.Name)] = fingerprint
		}
	}
	return current, nil
}

func (s *Service) populateBaseIndexFromSnapshots(ctx context.Context, generationID string, snapshots map[string]downstream.ToolSnapshot) error {
	for _, entry := range s.servers.Servers {
		if entry.Mode == v2config.ModeDisabled {
			continue
		}
		snapshot, ok := snapshots[entry.ID]
		if !ok {
			return fmt.Errorf("missing authoritative tool snapshot for server %s", entry.ID)
		}
		if _, err := s.catalog.PutSourceServer(ctx, generationID, entry); err != nil {
			return err
		}
		for _, tool := range snapshot.Tools {
			if !entry.ToolExposed(tool.Name) {
				continue
			}
			fingerprint, _, err := toolcontract.FingerprintTool(tool)
			if err != nil {
				return err
			}
			if err := s.catalog.RequireSourceTool(ctx, generationID, entry.ID, tool.Name, fingerprint); err != nil {
				return err
			}
			if _, err := s.catalog.PutSourceTool(ctx, generationID, entry.ID, tool, true); err != nil {
				return err
			}
			projections, err := retrieval.ProjectTool(tool)
			if err != nil {
				return err
			}
			memberKey := enrichment.MemberKey(entry.ID, tool.Name)
			if err := s.retrieval.RequireLexical(ctx, generationID, memberKey, projections.Lexical); err != nil {
				return err
			}
			if _, err := s.retrieval.StoreLexical(ctx, generationID, memberKey, projections.Lexical); err != nil {
				return err
			}
			if err := s.retrieval.EnsureEmbedding(ctx, generationID, retrieval.RoleSourceDescriptionVector, memberKey, s.embedding, projections.SourceDescription); err != nil {
				return err
			}
			if err := s.retrieval.EnsureEmbedding(ctx, generationID, retrieval.RoleInputSchemaVector, memberKey, s.embedding, projections.InputSchema); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) reconcileObservedDirtyPartitions(ctx context.Context, observed []catalog.DirtyPartition, snapshots map[string]downstream.ToolSnapshot) error {
	configured := make(map[string]v2config.ServerEntry, len(s.servers.Servers))
	for _, entry := range s.servers.Servers {
		configured[entry.ID] = entry
	}
	for _, partition := range observed {
		if !strings.HasPrefix(partition.PartitionKey, "server:") {
			continue
		}
		serverID := strings.TrimPrefix(partition.PartitionKey, "server:")
		entry, exists := configured[serverID]
		if exists && entry.Mode != v2config.ModeDisabled {
			if _, rebuilt := snapshots[serverID]; !rebuilt {
				continue
			}
		}
		if _, err := s.catalog.ClearDirtyIfUnchanged(ctx, partition); err != nil {
			return err
		}
	}
	return nil
}
