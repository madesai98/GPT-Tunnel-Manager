package routingstate

import (
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func TestChangingSelectedEmbeddingModelInvalidatesRoutingHash(t *testing.T) {
	manager := v2config.DefaultManagerConfig(43127)
	servers := v2config.DefaultServersConfig()
	before, err := ComputeHash(ConfigMaterial(manager, servers))
	if err != nil {
		t.Fatal(err)
	}
	manager.Embedding.Models = append(manager.Embedding.Models, v2config.EmbeddingModel{
		ID:          "custom-embed-q8",
		Name:        "Custom Embed Q8",
		DownloadURL: "https://example.com/custom.gguf",
		FileName:    "custom.gguf",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Dimensions:  768,
		Pooling:     "mean",
	})
	manager.Embedding.Model = "custom-embed-q8"
	after, err := ComputeHash(ConfigMaterial(manager, servers))
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("selected embedding model change did not invalidate routing hash")
	}
}

func TestLegacyOnlineEmbeddingHashResolvesToLocalDefault(t *testing.T) {
	legacy := v2config.DefaultManagerConfig(43127)
	legacy.Embedding = v2config.EmbeddingConfig{
		Provider:      v2config.EmbeddingProviderOpenAICompatible,
		BaseURL:       "https://api.openai.com/v1",
		Model:         "text-embedding-3-small",
		CredentialRef: v2config.EmbeddingCredentialRef,
	}
	local := v2config.DefaultManagerConfig(43127)
	legacyHash, err := ComputeHash(ConfigMaterial(legacy, v2config.DefaultServersConfig()))
	if err != nil {
		t.Fatal(err)
	}
	localHash, err := ComputeHash(ConfigMaterial(local, v2config.DefaultServersConfig()))
	if err != nil {
		t.Fatal(err)
	}
	if legacyHash != localHash {
		t.Fatalf("legacy inference migration must use local default identity: %s != %s", legacyHash, localHash)
	}
}
