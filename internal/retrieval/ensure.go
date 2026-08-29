package retrieval

import (
	"context"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
)

// EnsureEmbedding attaches a content-addressed reusable embedding when one is
// available, otherwise obtains exactly one new vector from the configured
// provider and stores it through the existing artifact substrate.
func (s *CatalogStore) EnsureEmbedding(ctx context.Context, generationID, role, memberKey string, provider embedding.Provider, projection Projection) error {
	if provider == nil {
		return fmt.Errorf("embedding provider is required")
	}
	identity := provider.Identity()
	if err := s.RequireEmbedding(ctx, generationID, role, memberKey, identity, projection); err != nil {
		return err
	}
	_, artifactKey, reused, err := s.ReuseEmbedding(ctx, role, memberKey, identity, projection)
	if err != nil {
		return err
	}
	if reused {
		return s.catalog.FulfillArtifact(ctx, generationID, embeddingSpec(role, memberKey, identity, projection), artifactKey)
	}
	vectors, err := provider.Embed(ctx, []string{projection.Text})
	if err != nil {
		return err
	}
	if len(vectors) != 1 {
		return fmt.Errorf("embedding provider returned %d vectors for one input", len(vectors))
	}
	_, err = s.StoreEmbedding(ctx, generationID, role, memberKey, identity, projection, vectors[0])
	return err
}
