package embedding

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
)

type QueryCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*list.Element
	order    *list.List
}

type cacheEntry struct {
	key    string
	vector []float32
}

func NewQueryCache(capacity int) (*QueryCache, error) {
	if capacity < 0 {
		return nil, errors.New("query embedding cache capacity cannot be negative")
	}
	return &QueryCache{
		capacity: capacity,
		entries:  make(map[string]*list.Element, capacity),
		order:    list.New(),
	}, nil
}

func queryCacheKey(identity Identity, query string) string {
	digest := sha256.Sum256([]byte(query))
	return identity.Fingerprint() + ":" + hex.EncodeToString(digest[:])
}

func (c *QueryCache) Get(identity Identity, query string) ([]float32, bool) {
	if c == nil || c.capacity == 0 {
		return nil, false
	}
	key := queryCacheKey(identity, query)
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	entry := element.Value.(*cacheEntry)
	return append([]float32(nil), entry.vector...), true
}

func (c *QueryCache) Put(identity Identity, query string, vector []float32) {
	if c == nil || c.capacity == 0 {
		return
	}
	key := queryCacheKey(identity, query)
	copyVector := append([]float32(nil), vector...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		element.Value.(*cacheEntry).vector = copyVector
		c.order.MoveToFront(element)
		return
	}
	element := c.order.PushFront(&cacheEntry{key: key, vector: copyVector})
	c.entries[key] = element
	if c.order.Len() <= c.capacity {
		return
	}
	oldest := c.order.Back()
	if oldest != nil {
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*cacheEntry).key)
	}
}

func EmbedQuery(ctx context.Context, provider Provider, cache *QueryCache, query string) ([]float32, error) {
	if provider == nil {
		return nil, errors.New("embedding provider is required")
	}
	identity := provider.Identity()
	if cache != nil {
		if vector, ok := cache.Get(identity, query); ok {
			return vector, nil
		}
	}
	vectors, err := provider.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, errors.New("query embedding provider returned unexpected vector count")
	}
	expected := 0
	if identity.Dimensions != nil {
		expected = *identity.Dimensions
	}
	if err := ValidateVector(vectors[0], expected); err != nil {
		return nil, err
	}
	if cache != nil {
		cache.Put(identity, query, vectors[0])
	}
	return append([]float32(nil), vectors[0]...), nil
}
