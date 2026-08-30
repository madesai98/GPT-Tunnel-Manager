package embedding

import (
	"net/http"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

// NewProvider keeps the existing construction signature for non-app callers.
// Secrets are deliberately ignored: local embeddings never read an API
// credential. The native app uses NewProviderAtRoot so managed assets remain
// inside its protected portable data root.
func NewProvider(config v2config.EmbeddingConfig, _ secrets.Store, client *http.Client) (Provider, error) {
	root, err := StorageRoot()
	if err != nil {
		return nil, err
	}
	return NewProviderAtRoot(config, root, client)
}

func NewProviderAtRoot(config v2config.EmbeddingConfig, root string, client *http.Client) (Provider, error) {
	return NewLocalGGUF(LocalOptions{Root: root, Config: config, Client: client})
}

func NewConfiguredQueryCache(config v2config.IndexConfig) (*QueryCache, error) {
	return NewQueryCache(config.QueryEmbeddingCacheEntries)
}
