package routingstate

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

const FingerprintAlgorithm = "hmac-sha256"

type Snapshot struct {
	RoutingRevision    uint64 `json:"routing_revision"`
	RoutingStateHash   string `json:"routing_state_hash"`
	PreferenceRevision uint64 `json:"preference_revision"`
}

type Backend interface {
	Load(context.Context) (Snapshot, error)
	Store(context.Context, Snapshot) error
}

type Tracker struct {
	mu      sync.Mutex
	backend Backend
}

func NewTracker(backend Backend) (*Tracker, error) {
	if backend == nil {
		return nil, errors.New("routing state backend is required")
	}
	return &Tracker{backend: backend}, nil
}

func (t *Tracker) Snapshot(ctx context.Context) (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.backend.Load(ctx)
}

func (t *Tracker) Reconcile(ctx context.Context, currentHash string) (Snapshot, bool, error) {
	if currentHash == "" {
		return Snapshot{}, false, errors.New("current routing-state hash is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state, err := t.backend.Load(ctx)
	if err != nil {
		return Snapshot{}, false, err
	}
	if state.RoutingStateHash == currentHash {
		return state, false, nil
	}
	state.RoutingRevision++
	state.RoutingStateHash = currentHash
	if err := t.backend.Store(ctx, state); err != nil {
		return Snapshot{}, false, err
	}
	return state, true, nil
}

func (t *Tracker) AdvanceRoutingRevision(ctx context.Context) (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state, err := t.backend.Load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	state.RoutingRevision++
	if err := t.backend.Store(ctx, state); err != nil {
		return Snapshot{}, err
	}
	return state, nil
}

func (t *Tracker) AdvancePreferenceRevision(ctx context.Context) (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state, err := t.backend.Load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	state.PreferenceRevision++
	if err := t.backend.Store(ctx, state); err != nil {
		return Snapshot{}, err
	}
	return state, nil
}

type RoutingMaterial struct {
	Embedding                      EmbeddingMaterial      `json:"embedding"`
	Servers                        []v2config.ServerEntry `json:"servers"`
	SecretFingerprints             map[string]string      `json:"secret_fingerprints,omitempty"`
	CredentialIdentityFingerprints map[string]string      `json:"credential_identity_fingerprints,omitempty"`
}

type EmbeddingMaterial struct {
	Provider          v2config.EmbeddingProvider `json:"provider"`
	Model             string                     `json:"model"`
	ModelSHA256       string                     `json:"model_sha256"`
	Dimensions        int                        `json:"dimensions"`
	Pooling           string                     `json:"pooling"`
	RuntimeRelease    string                     `json:"runtime_release"`
	RuntimeBinaryPath string                     `json:"runtime_binary_path,omitempty"`
}

// ConfigMaterial includes the complete selected local embedding identity. As a
// result, selecting a different model, quantization, pooling mode, dimension,
// or runtime invalidates the active semantic generation and forces a rebuild.
func ConfigMaterial(manager v2config.ManagerConfig, servers v2config.ServersConfig) RoutingMaterial {
	entries := append([]v2config.ServerEntry(nil), servers.Servers...)
	embeddingConfig := v2config.EffectiveEmbeddingConfig(manager.Embedding)
	model, _ := embeddingConfig.SelectedModel()
	return RoutingMaterial{
		Embedding: EmbeddingMaterial{
			Provider:          embeddingConfig.Provider,
			Model:             model.ID,
			ModelSHA256:       model.SHA256,
			Dimensions:        model.Dimensions,
			Pooling:           model.Pooling,
			RuntimeRelease:    embeddingConfig.Runtime.Release,
			RuntimeBinaryPath: embeddingConfig.Runtime.BinaryPath,
		},
		Servers: entries,
	}
}

func ComputeHash(material RoutingMaterial) (string, error) {
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal routing state: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func FingerprintSecret(installationKey, secret []byte) (string, error) {
	if len(installationKey) < 32 {
		return "", errors.New("installation fingerprint key must be at least 32 bytes")
	}
	mac := hmac.New(sha256.New, installationKey)
	_, _ = mac.Write(secret)
	return FingerprintAlgorithm + ":" + hex.EncodeToString(mac.Sum(nil)), nil
}

type MemoryBackend struct {
	mu    sync.Mutex
	state Snapshot
}

func NewMemoryBackend(initial Snapshot) *MemoryBackend {
	return &MemoryBackend{state: initial}
}

func (b *MemoryBackend) Load(context.Context) (Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state, nil
}

func (b *MemoryBackend) Store(_ context.Context, state Snapshot) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = state
	return nil
}
