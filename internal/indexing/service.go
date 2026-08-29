package indexing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

const CodeManualServerStoppedForIndex = "manual_server_stopped_for_index"

type RoutingState interface {
	CurrentRoutingStateHash() (string, bool)
}

type Lifecycle interface {
	Acquire(context.Context, string) (*routedlifecycle.UseLease, error)
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type Status struct {
	RoutingStateHash   string `json:"routing_state_hash,omitempty"`
	ActiveGenerationID string `json:"active_generation_id,omitempty"`
	StagingGenerationID string `json:"staging_generation_id,omitempty"`
	Ready              bool   `json:"ready"`
	PendingRequired    int    `json:"pending_required"`
	OpenReviews        int    `json:"open_reviews"`
	AcceptedRequired   int    `json:"accepted_required"`
}

type RefreshResult struct {
	Status Status `json:"status"`
}

type CommitResult struct {
	GenerationID string `json:"generation_id"`
	Status       Status `json:"status"`
}

type Service struct {
	catalog    *catalog.Catalog
	enrichment *enrichment.Coordinator
	retrieval  *retrieval.CatalogStore
	embedding  embedding.Provider
	lifecycle  Lifecycle
	routing    RoutingState
	servers    v2config.ServersConfig

	mu sync.Mutex
}

func NewService(c *catalog.Catalog, coordinator *enrichment.Coordinator, provider embedding.Provider, lifecycle Lifecycle, routing RoutingState, servers v2config.ServersConfig) (*Service, error) {
	if c == nil {
		return nil, errors.New("catalog is required")
	}
	if coordinator == nil {
		return nil, errors.New("enrichment coordinator is required")
	}
	if provider == nil {
		return nil, errors.New("embedding provider is required")
	}
	if lifecycle == nil {
		return nil, errors.New("routed lifecycle is required")
	}
	if routing == nil {
		return nil, errors.New("routing state is required")
	}
	if err := v2config.ValidateServers(servers); err != nil {
		return nil, err
	}
	store, err := retrieval.NewCatalogStore(c)
	if err != nil {
		return nil, err
	}
	return &Service{catalog: c, enrichment: coordinator, retrieval: store, embedding: provider, lifecycle: lifecycle, routing: routing, servers: servers}, nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	hash, ready := s.routing.CurrentRoutingStateHash()
	status := Status{RoutingStateHash: hash}
	if !ready || hash == "" {
		return status, nil
	}
	if active, err := s.catalog.ActiveGeneration(ctx); err == nil {
		status.ActiveGenerationID = active.ID
		status.Ready = active.RoutingStateHash == hash
	} else if !errors.Is(err, catalog.ErrGenerationNotFound) {
		return Status{}, err
	}
	staging, err := s.stagingForHash(ctx, hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Status{}, err
	}
	if err == nil {
		status.StagingGenerationID = staging.ID
		counts, err := s.catalog.EnrichmentBatchCounts(ctx, staging.ID)
		if err != nil {
			return Status{}, err
		}
		status.PendingRequired = counts.PendingRequired
		status.OpenReviews = counts.PendingOptional
		status.AcceptedRequired = counts.AcceptedRequired
	}
	return status, nil
}

func (s *Service) Refresh(ctx context.Context) (RefreshResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash, ready := s.routing.CurrentRoutingStateHash()
	if !ready || hash == "" {
		return RefreshResult{}, &Error{Code: "index_required", Message: "routing state is not ready for indexing"}
	}
	if _, err := s.catalog.ReconcileStaging(ctx, hash); err != nil {
		return RefreshResult{}, err
	}
	generation, err := s.stagingForHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		generation, err = s.catalog.CreateStaging(ctx, catalog.GenerationSpec{RoutingStateHash: hash})
	}
	if err != nil {
		return RefreshResult{}, err
	}
	if err := s.populateBaseIndex(ctx, generation.ID); err != nil {
		return RefreshResult{}, err
	}
	if _, err := s.enrichment.PrepareToolEnrichment(ctx, generation.ID); err != nil {
		return RefreshResult{}, err
	}
	status, err := s.Status(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{Status: status}, nil
}

func (s *Service) GetBatch(ctx context.Context, kind catalog.EnrichmentBatchKind, limit int) ([]catalog.EnrichmentBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	generation, err := s.currentStaging(ctx)
	if err != nil {
		return nil, err
	}
	switch kind {
	case catalog.BatchToolEnrichment:
		if _, err := s.enrichment.PrepareToolEnrichment(ctx, generation.ID); err != nil {
			return nil, err
		}
	case catalog.BatchCapabilityReconciliation:
		if _, err := s.enrichment.PrepareCapabilityReconciliation(ctx, generation.ID); err != nil {
			return nil, err
		}
	case catalog.BatchAmbiguityReview:
		// Ambiguity Reviews are generated by an accepted capability batch and
		// remain retrievable after commit; while staging, ordinary pending lookup
		// is sufficient.
	default:
		return nil, fmt.Errorf("unsupported enrichment batch kind %q", kind)
	}
	if limit <= 0 {
		limit = 1
	}
	return s.catalog.PendingEnrichmentBatches(ctx, generation.ID, kind, limit)
}

func (s *Service) SubmitBatch(ctx context.Context, batchID string, response any) (enrichment.SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := json.Marshal(response)
	if err != nil {
		return enrichment.SubmitResult{}, fmt.Errorf("encode enrichment response: %w", err)
	}
	result, err := s.enrichment.SubmitBatch(ctx, batchID, body)
	if err != nil {
		return enrichment.SubmitResult{}, err
	}
	if result.Batch.Kind == catalog.BatchToolEnrichment {
		pending, err := s.catalog.PendingEnrichmentBatches(ctx, result.Batch.GenerationID, catalog.BatchToolEnrichment, 1)
		if err != nil {
			return enrichment.SubmitResult{}, err
		}
		if len(pending) == 0 {
			if _, err := s.enrichment.PrepareCapabilityReconciliation(ctx, result.Batch.GenerationID); err != nil {
				return enrichment.SubmitResult{}, err
			}
		}
	}
	return result, nil
}

func (s *Service) Commit(ctx context.Context) (CommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	generation, err := s.currentStaging(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	if _, err := s.enrichment.PrepareCapabilityReconciliation(ctx, generation.ID); err != nil {
		return CommitResult{}, err
	}
	counts, err := s.catalog.EnrichmentBatchCounts(ctx, generation.ID)
	if err != nil {
		return CommitResult{}, err
	}
	if counts.PendingRequired != 0 {
		return CommitResult{}, &Error{Code: "required_enrichment_pending", Message: fmt.Sprintf("%d required enrichment batch(es) remain pending", counts.PendingRequired)}
	}
	hash, ready := s.routing.CurrentRoutingStateHash()
	if !ready || hash == "" {
		return CommitResult{}, &Error{Code: "index_required", Message: "routing state changed before index commit"}
	}
	if err := s.catalog.Promote(ctx, generation.ID, hash); err != nil {
		return CommitResult{}, err
	}
	status, err := s.Status(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{GenerationID: generation.ID, Status: status}, nil
}

func (s *Service) populateBaseIndex(ctx context.Context, generationID string) error {
	for _, entry := range s.servers.Servers {
		if entry.Mode == v2config.ModeDisabled {
			continue
		}
		lease, err := s.lifecycle.Acquire(ctx, entry.ID)
		if err != nil {
			if errors.Is(err, routedlifecycle.ErrManualServerStopped) {
				return &Error{Code: CodeManualServerStoppedForIndex, Message: fmt.Sprintf("manual server %s is stopped and must be started before indexing", entry.ID), cause: err}
			}
			return err
		}
		snapshot := lease.InitialTools()
		lease.Release()
		if _, err := s.catalog.PutSourceServer(ctx, generationID, entry); err != nil {
			return err
		}
		for _, tool := range snapshot.Tools {
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

func (s *Service) currentStaging(ctx context.Context) (catalog.Generation, error) {
	hash, ready := s.routing.CurrentRoutingStateHash()
	if !ready || hash == "" {
		return catalog.Generation{}, &Error{Code: "index_required", Message: "routing state is not ready"}
	}
	generation, err := s.stagingForHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Generation{}, &Error{Code: "index_refresh_required", Message: "no staging index generation exists; call index_refresh first"}
	}
	return generation, err
}

func (s *Service) stagingForHash(ctx context.Context, hash string) (catalog.Generation, error) {
	var id string
	err := s.catalog.DB().QueryRowContext(ctx, `
		SELECT generation_id FROM generations
		WHERE status = 'staging' AND routing_state_hash = ?
		ORDER BY created_at_unix_ms DESC, generation_id DESC LIMIT 1
	`, hash).Scan(&id)
	if err != nil {
		return catalog.Generation{}, err
	}
	return s.catalog.Generation(ctx, id)
}
