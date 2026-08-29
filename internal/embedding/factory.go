package embedding

import (
	"net/http"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func NewProvider(config v2config.EmbeddingConfig, secretStore secrets.Store, client *http.Client) (Provider, error) {
	return NewOpenAICompatible(OpenAICompatibleOptions{
		Config:  config,
		Secrets: secretStore,
		Client:  client,
	})
}

func NewConfiguredQueryCache(config v2config.IndexConfig) (*QueryCache, error) {
	return NewQueryCache(config.QueryEmbeddingCacheEntries)
}
