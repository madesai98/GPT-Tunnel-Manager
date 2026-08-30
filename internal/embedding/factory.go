package embedding

import (
	"net/http"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

// NewProvider keeps the existing app construction signature, but secrets are
// deliberately ignored: local embeddings never read an API credential.
func NewProvider(config v2config.EmbeddingConfig, _ secrets.Store, client *http.Client) (Provider, error) {
	root, err := StorageRoot()
	if err != nil {
		return nil, err
	}
	return NewLocalGGUF(LocalOptions{Root: root, Config: config, Client: client})
}

func NewConfiguredQueryCache(config v2config.IndexConfig) (*QueryCache, error) {
	return NewQueryCache(config.QueryEmbeddingCacheEntries)
}
