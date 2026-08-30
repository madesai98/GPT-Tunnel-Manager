package v2config

import "testing"

func TestDefaultEmbeddingConfigIsLocalAndPinned(t *testing.T) {
	cfg := DefaultEmbeddingConfig()
	if cfg.Provider != EmbeddingProviderLocalGGUF {
		t.Fatalf("provider = %q", cfg.Provider)
	}
	if cfg.Model != DefaultEmbeddingModelID || len(cfg.Models) != 1 {
		t.Fatalf("default embedding config = %#v", cfg)
	}
	model := cfg.Models[0]
	if model.ID != DefaultEmbeddingModelID || model.Dimensions != 384 || model.Pooling != "cls" {
		t.Fatalf("default model = %#v", model)
	}
	if model.SHA256 == "" || model.DownloadURL == "" {
		t.Fatalf("default model must be download-pinned: %#v", model)
	}
	if cfg.Runtime.Release != DefaultLlamaCppRelease || cfg.Runtime.BinaryPath != "" {
		t.Fatalf("runtime = %#v", cfg.Runtime)
	}
	if cfg.BaseURL != "" || cfg.CredentialRef != "" || cfg.Dimensions != nil {
		t.Fatalf("default local config contains legacy online fields: %#v", cfg)
	}
}

func TestReleasedOpenAIConfigMigratesToLocalDefaultForInference(t *testing.T) {
	legacy := EmbeddingConfig{
		Provider:      EmbeddingProviderOpenAICompatible,
		BaseURL:       "https://api.openai.com/v1",
		Model:         "text-embedding-3-small",
		CredentialRef: EmbeddingCredentialRef,
	}
	if err := validateEmbedding(legacy); err != nil {
		t.Fatalf("released legacy config must remain readable: %v", err)
	}
	effective := EffectiveEmbeddingConfig(legacy)
	if effective.Provider != EmbeddingProviderLocalGGUF || effective.Model != DefaultEmbeddingModelID {
		t.Fatalf("effective config = %#v", effective)
	}
	if effective.BaseURL != "" || effective.CredentialRef != "" {
		t.Fatalf("legacy online fields survived inference migration: %#v", effective)
	}
}

func TestLocalEmbeddingModelsRequireChecksum(t *testing.T) {
	cfg := DefaultManagerConfig(43127)
	cfg.Embedding.Models[0].SHA256 = ""
	if err := ValidateManager(cfg); err == nil {
		t.Fatal("expected missing local model checksum to be rejected")
	}
}
