package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func (a *V2App) EffectiveEmbeddingConfig() v2config.EmbeddingConfig {
	return v2config.EffectiveEmbeddingConfig(a.ManagerConfig().Embedding)
}

func (a *V2App) EmbeddingModels() []v2config.EmbeddingModel {
	cfg := a.EffectiveEmbeddingConfig()
	return append([]v2config.EmbeddingModel(nil), cfg.Models...)
}

func (a *V2App) EmbeddingModelInstalled(modelID string) bool {
	root, err := embedding.StorageRoot()
	if err != nil {
		return false
	}
	for _, model := range a.EmbeddingModels() {
		if model.ID == modelID {
			return embedding.ModelInstalled(root, model)
		}
	}
	return false
}

func (a *V2App) InstallEmbeddingModel(ctx context.Context, modelID string) error {
	root, err := embedding.StorageRoot()
	if err != nil {
		return err
	}
	cfg := a.EffectiveEmbeddingConfig()
	return embedding.Install(ctx, root, cfg, modelID, nil)
}

func (a *V2App) SelectEmbeddingModel(ctx context.Context, modelID string) error {
	manager := a.ManagerConfig()
	wasLegacy := manager.Embedding.Provider != v2config.EmbeddingProviderLocalGGUF
	manager.Embedding = v2config.EffectiveEmbeddingConfig(manager.Embedding)
	found := false
	for _, model := range manager.Embedding.Models {
		if model.ID == modelID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("embedding model %q is not configured", modelID)
	}
	if manager.Embedding.Model == modelID && !wasLegacy {
		return nil
	}
	manager.Embedding.Model = modelID
	return a.SaveManager(ctx, manager)
}

// AddEmbeddingModel adds a reusable local GGUF definition. It does not select
// the model, so existing indexes remain valid until the user explicitly changes
// the selected model.
func (a *V2App) AddEmbeddingModel(ctx context.Context, model v2config.EmbeddingModel) error {
	manager := a.ManagerConfig()
	manager.Embedding = v2config.EffectiveEmbeddingConfig(manager.Embedding)
	for _, existing := range manager.Embedding.Models {
		if existing.ID == model.ID {
			return fmt.Errorf("embedding model id %q already exists", model.ID)
		}
	}
	manager.Embedding.Models = append(manager.Embedding.Models, model)
	return a.SaveManager(ctx, manager)
}

func (a *V2App) RemoveEmbeddingModel(ctx context.Context, modelID string) error {
	manager := a.ManagerConfig()
	manager.Embedding = v2config.EffectiveEmbeddingConfig(manager.Embedding)
	if manager.Embedding.Model == modelID {
		return errors.New("cannot remove the selected embedding model")
	}
	out := manager.Embedding.Models[:0]
	found := false
	for _, model := range manager.Embedding.Models {
		if model.ID == modelID {
			found = true
			continue
		}
		out = append(out, model)
	}
	if !found {
		return nil
	}
	manager.Embedding.Models = out
	return a.SaveManager(ctx, manager)
}
