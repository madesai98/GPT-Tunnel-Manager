package embedding

import (
	"context"
	"sync"
	"testing"
)

type countingProvider struct {
	mu       sync.Mutex
	identity Identity
	calls    int
}

func (p *countingProvider) Identity() Identity { return p.identity }

func (p *countingProvider) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	vectors := make([][]float32, len(inputs))
	for index := range inputs {
		vectors[index] = []float32{1, float32(index + 1)}
	}
	return vectors, nil
}

func (p *countingProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func testIdentity(model string) Identity {
	return Identity{Provider: "test", BaseURL: "https://example.invalid/v1", Model: model, Protocol: IdentityVersion}
}

func TestQueryCacheHitEvictionAndProviderIdentity(t *testing.T) {
	cache, err := NewQueryCache(2)
	if err != nil {
		t.Fatal(err)
	}
	providerA := &countingProvider{identity: testIdentity("model-a")}
	if _, err := EmbedQuery(context.Background(), providerA, cache, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := EmbedQuery(context.Background(), providerA, cache, "alpha"); err != nil {
		t.Fatal(err)
	}
	if providerA.CallCount() != 1 {
		t.Fatalf("same query call count = %d, want 1", providerA.CallCount())
	}
	if _, err := EmbedQuery(context.Background(), providerA, cache, "beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := EmbedQuery(context.Background(), providerA, cache, "gamma"); err != nil {
		t.Fatal(err)
	}
	if _, err := EmbedQuery(context.Background(), providerA, cache, "alpha"); err != nil {
		t.Fatal(err)
	}
	if providerA.CallCount() != 4 {
		t.Fatalf("eviction call count = %d, want 4", providerA.CallCount())
	}

	providerB := &countingProvider{identity: testIdentity("model-b")}
	if _, err := EmbedQuery(context.Background(), providerB, cache, "gamma"); err != nil {
		t.Fatal(err)
	}
	if providerB.CallCount() != 1 {
		t.Fatalf("different identity reused cache; calls = %d", providerB.CallCount())
	}
}

func TestQueryCacheIsMemoryOnlyAcrossReconstruction(t *testing.T) {
	provider := &countingProvider{identity: testIdentity("model")}
	cache, err := NewQueryCache(4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EmbedQuery(context.Background(), provider, cache, "ephemeral query"); err != nil {
		t.Fatal(err)
	}
	cache, err = NewQueryCache(4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EmbedQuery(context.Background(), provider, cache, "ephemeral query"); err != nil {
		t.Fatal(err)
	}
	if provider.CallCount() != 2 {
		t.Fatalf("new cache unexpectedly retained prior query; calls = %d", provider.CallCount())
	}
}

func TestQueryCacheConcurrentAccess(t *testing.T) {
	cache, err := NewQueryCache(16)
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity("model")
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 100; iteration++ {
				query := string(rune('a' + (worker+iteration)%8))
				cache.Put(identity, query, []float32{1, float32(worker + 1)})
				_, _ = cache.Get(identity, query)
			}
		}(worker)
	}
	wg.Wait()
}
