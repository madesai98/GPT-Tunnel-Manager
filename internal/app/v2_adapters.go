package app

import (
	"context"
	"errors"
	"sync"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2state"
)

// liveEmbeddingProvider keeps the semantic services stable while allowing the
// native application to replace a validated provider after an explicit user
// configuration change. No embedding request is made by Swap.
type liveEmbeddingProvider struct {
	mu       sync.RWMutex
	provider embedding.Provider
}

func newLiveEmbeddingProvider(provider embedding.Provider) (*liveEmbeddingProvider, error) {
	if provider == nil {
		return nil, errors.New("embedding provider is required")
	}
	return &liveEmbeddingProvider{provider: provider}, nil
}

func (p *liveEmbeddingProvider) Identity() embedding.Identity {
	p.mu.RLock()
	provider := p.provider
	p.mu.RUnlock()
	return provider.Identity()
}

func (p *liveEmbeddingProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	p.mu.RLock()
	provider := p.provider
	p.mu.RUnlock()
	return provider.Embed(ctx, inputs)
}

func (p *liveEmbeddingProvider) Swap(provider embedding.Provider) error {
	if provider == nil {
		return errors.New("embedding provider is required")
	}
	p.mu.Lock()
	p.provider = provider
	p.mu.Unlock()
	return nil
}

// v2RoutingAdapter deliberately combines the durable revision tracker with the
// v2 configuration coordinator's current-hash readiness gate. The existing
// router/indexing contracts stay authoritative.
type v2RoutingAdapter struct {
	tracker     *routingstate.Tracker
	coordinator *v2state.Coordinator
}

func (r *v2RoutingAdapter) Snapshot(ctx context.Context) (routingstate.Snapshot, error) {
	return r.tracker.Snapshot(ctx)
}

func (r *v2RoutingAdapter) AdvanceRoutingRevision(ctx context.Context) (routingstate.Snapshot, error) {
	return r.tracker.AdvanceRoutingRevision(ctx)
}

func (r *v2RoutingAdapter) CurrentRoutingStateHash() (string, bool) {
	return r.coordinator.CurrentRoutingStateHash()
}
