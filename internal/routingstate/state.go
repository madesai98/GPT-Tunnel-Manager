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

// Reconcile compares a freshly computed routing-state hash with the persisted
// diagnostic state. Call it after every routing-relevant config/secret change
// and during startup before any generation is considered routable. This makes
// the deterministic hash, rather than mutation timing, the correctness proof.
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
	Provider   v2config.EmbeddingProvider `json:"provider"`
	BaseURL    string                     `json:"base_url"`
	Model      string                     `json:"model"`
	Dimensions *int                       `json:"dimensions,omitempty"`
}

// ConfigMaterial deliberately excludes local Manager access settings, Manager
// Tunnel credentials, UI/logging settings, query-cache sizing, and the default
// Routing Profile. Those are operational or preference-overlay state rather
// than semantic source correctness. Downstream entries are retained in full;
// later catalog phases may narrow dirty partitions while preserving this global
// fail-closed hash.
func ConfigMaterial(manager v2config.ManagerConfig, servers v2config.ServersConfig) RoutingMaterial {
	entries := append([]v2config.ServerEntry(nil), servers.Servers...)
	return RoutingMaterial{
		Embedding: EmbeddingMaterial{
			Provider:   manager.Embedding.Provider,
			BaseURL:    manager.Embedding.BaseURL,
			Model:      manager.Embedding.Model,
			Dimensions: cloneInt(manager.Embedding.Dimensions),
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

// FingerprintSecret returns a keyed fingerprint suitable for inclusion in
// RoutingMaterial. The installation key must be independent of the secret being
// fingerprinted. Raw secrets and unkeyed hashes must never be persisted.
func FingerprintSecret(installationKey, secret []byte) (string, error) {
	if len(installationKey) < 32 {
		return "", errors.New("installation fingerprint key must be at least 32 bytes")
	}
	mac := hmac.New(sha256.New, installationKey)
	_, _ = mac.Write(secret)
	return FingerprintAlgorithm + ":" + hex.EncodeToString(mac.Sum(nil)), nil
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// MemoryBackend is useful before the SQLite catalog backend lands and in tests.
// Release routing state will use the Phase 4 SQLite implementation.
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
