package v2state

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

const (
	RoutingFingerprintKeySecretRef = "secret://manager/routing-fingerprint-key/default"
	oauthIdentitySecretPrefix       = "secret://routing/oauth-identity/"
)

// ServerMutationCoordinator reserves runtime-affecting Server Entry changes
// before they are persisted. Phase 9's router-native lifecycle uses this seam
// to fail edits/disable/delete with server_busy while use leases are active and
// to prevent a new acquisition from racing between the busy check and commit.
type ServerMutationCoordinator interface {
	PrepareServerMutation(current, next v2config.ServersConfig) error
	CommitServerMutation(next v2config.ServersConfig)
	AbortServerMutation()
}

// Coordinator is the Phase 2 config/secret/routing-state consistency boundary.
// Any routing-affecting mutation marks the live generation gate unready before
// the durable write and only re-enables it after the deterministic current hash
// has been reconciled. Startup performs the same reconciliation, so a crash
// after config/secret persistence but before routing-state persistence remains
// fail closed.
type Coordinator struct {
	mu sync.RWMutex

	config  *v2config.Store
	secrets secrets.Store
	tracker *routingstate.Tracker

	manager         v2config.ManagerConfig
	servers         v2config.ServersConfig
	fingerprintKey  []byte
	currentHash     string
	initialized     bool
	ready           bool
	serverMutations ServerMutationCoordinator
}

func New(configStore *v2config.Store, secretStore secrets.Store, tracker *routingstate.Tracker) (*Coordinator, error) {
	if configStore == nil {
		return nil, errors.New("v2 config store is required")
	}
	if secretStore == nil {
		return nil, errors.New("secret store is required")
	}
	if tracker == nil {
		return nil, errors.New("routing state tracker is required")
	}
	return &Coordinator{config: configStore, secrets: secretStore, tracker: tracker}, nil
}

// SetServerMutationCoordinator installs the runtime mutation guard used by
// SaveServers. It is intentionally a narrow interface so v2state does not own
// lifecycle implementation details or introduce a package cycle.
func (c *Coordinator) SetServerMutationCoordinator(coordinator ServerMutationCoordinator) {
	c.mu.Lock()
	c.serverMutations = coordinator
	c.mu.Unlock()
}

func (c *Coordinator) Initialize(ctx context.Context) (v2config.ManagerConfig, v2config.ServersConfig, routingstate.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ready = false

	manager, servers, err := c.config.LoadOrCreate()
	if err != nil {
		return v2config.ManagerConfig{}, v2config.ServersConfig{}, routingstate.Snapshot{}, err
	}
	key, err := c.ensureRoutingFingerprintKey(ctx)
	if err != nil {
		return manager, servers, routingstate.Snapshot{}, err
	}
	c.fingerprintKey = append(c.fingerprintKey[:0], key...)
	if manager.LocalManager.AccessProtectionEnabled {
		if _, err := c.ensureLocalManagerCapability(ctx); err != nil {
			return manager, servers, routingstate.Snapshot{}, err
		}
	}

	hash, err := c.computeHashLocked(ctx, manager, servers)
	if err != nil {
		return manager, servers, routingstate.Snapshot{}, err
	}
	state, _, err := c.tracker.Reconcile(ctx, hash)
	if err != nil {
		return manager, servers, routingstate.Snapshot{}, err
	}
	c.manager = manager
	c.servers = servers
	c.currentHash = hash
	c.initialized = true
	c.ready = true
	return manager, servers, state, nil
}

// GenerationCurrent is the fail-closed generation gate. Future catalog/router
// code must require this check (plus catalog integrity) before treating a
// generation as routable.
func (c *Coordinator) GenerationCurrent(generationRoutingStateHash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.initialized && c.ready && generationRoutingStateHash != "" && generationRoutingStateHash == c.currentHash
}

func (c *Coordinator) CurrentRoutingStateHash() (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.initialized || !c.ready {
		return "", false
	}
	return c.currentHash, true
}

func (c *Coordinator) SaveManager(ctx context.Context, manager v2config.ManagerConfig) (routingstate.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireInitializedLocked(); err != nil {
		return routingstate.Snapshot{}, err
	}
	if err := v2config.ValidateManager(manager); err != nil {
		return routingstate.Snapshot{}, err
	}
	if manager.LocalManager.AccessProtectionEnabled {
		if _, err := c.ensureLocalManagerCapability(ctx); err != nil {
			return routingstate.Snapshot{}, err
		}
	}
	candidateHash, err := c.computeHashLocked(ctx, manager, c.servers)
	if err != nil {
		return routingstate.Snapshot{}, err
	}
	if candidateHash != c.currentHash {
		c.ready = false
	}
	if err := c.config.SaveManager(manager); err != nil {
		return routingstate.Snapshot{}, err
	}
	return c.reconcilePersistedLocked(ctx)
}

func (c *Coordinator) SaveServers(ctx context.Context, serversConfig v2config.ServersConfig) (routingstate.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireInitializedLocked(); err != nil {
		return routingstate.Snapshot{}, err
	}
	if err := v2config.ValidateServers(serversConfig); err != nil {
		return routingstate.Snapshot{}, err
	}

	guard := c.serverMutations
	prepared := false
	abortPrepared := func() {
		if prepared && guard != nil {
			guard.AbortServerMutation()
			prepared = false
		}
	}
	if guard != nil {
		if err := guard.PrepareServerMutation(c.servers, serversConfig); err != nil {
			return routingstate.Snapshot{}, err
		}
		prepared = true
	}

	candidateHash, err := c.computeHashLocked(ctx, c.manager, serversConfig)
	if err != nil {
		abortPrepared()
		return routingstate.Snapshot{}, err
	}
	if candidateHash != c.currentHash {
		c.ready = false
	}
	if err := c.config.SaveServers(serversConfig); err != nil {
		abortPrepared()
		return routingstate.Snapshot{}, err
	}
	if guard != nil {
		guard.CommitServerMutation(serversConfig)
		prepared = false
	}
	return c.reconcilePersistedLocked(ctx)
}

// PutSecret automatically distinguishes routing-relevant configured secret
// references from operational credentials. Static downstream credentials and
// environment-secret changes invalidate routing; embedding, Manager Tunnel, and
// routine OAuth token rotation do not.
func (c *Coordinator) PutSecret(ctx context.Context, ref string, value []byte) (routingstate.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireInitializedLocked(); err != nil {
		return routingstate.Snapshot{}, err
	}
	if ref == RoutingFingerprintKeySecretRef || strings.HasPrefix(ref, oauthIdentitySecretPrefix) {
		return routingstate.Snapshot{}, errors.New("reserved routing-state secret reference")
	}
	routingRelevant := isRoutingSecretRef(c.servers, ref)
	if routingRelevant {
		c.ready = false
	}
	if err := c.secrets.Put(ctx, ref, value); err != nil {
		return routingstate.Snapshot{}, err
	}
	if !routingRelevant {
		state, err := c.tracker.Snapshot(ctx)
		return state, err
	}
	return c.reconcilePersistedLocked(ctx)
}

func (c *Coordinator) DeleteSecret(ctx context.Context, ref string) (routingstate.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireInitializedLocked(); err != nil {
		return routingstate.Snapshot{}, err
	}
	if ref == RoutingFingerprintKeySecretRef || strings.HasPrefix(ref, oauthIdentitySecretPrefix) {
		return routingstate.Snapshot{}, errors.New("reserved routing-state secret reference")
	}
	if ref == v2config.LocalManagerCapabilitySecretRef && c.manager.LocalManager.AccessProtectionEnabled {
		return routingstate.Snapshot{}, errors.New("cannot delete local Manager capability while access protection is enabled")
	}
	routingRelevant := isRoutingSecretRef(c.servers, ref)
	if routingRelevant {
		c.ready = false
	}
	if err := c.secrets.Delete(ctx, ref); err != nil {
		return routingstate.Snapshot{}, err
	}
	if !routingRelevant {
		state, err := c.tracker.Snapshot(ctx)
		return state, err
	}
	return c.reconcilePersistedLocked(ctx)
}

// SetOAuthCredentialIdentity records only an installation-keyed fingerprint of
// stable account/scope identity. Access/refresh tokens remain operational
// secrets and can rotate without changing routing state.
func (c *Coordinator) SetOAuthCredentialIdentity(ctx context.Context, serverID string, identityMaterial []byte) (routingstate.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireInitializedLocked(); err != nil {
		return routingstate.Snapshot{}, err
	}
	if !oauthConfigured(c.servers, serverID) {
		return routingstate.Snapshot{}, fmt.Errorf("server %q is not configured for downstream OAuth", serverID)
	}
	fingerprint, err := routingstate.FingerprintSecret(c.fingerprintKey, identityMaterial)
	if err != nil {
		return routingstate.Snapshot{}, err
	}
	c.ready = false
	if err := c.secrets.Put(ctx, oauthIdentityRef(serverID), []byte(fingerprint)); err != nil {
		return routingstate.Snapshot{}, err
	}
	return c.reconcilePersistedLocked(ctx)
}

func (c *Coordinator) ClearOAuthCredentialIdentity(ctx context.Context, serverID string) (routingstate.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.requireInitializedLocked(); err != nil {
		return routingstate.Snapshot{}, err
	}
	if !oauthConfigured(c.servers, serverID) {
		return routingstate.Snapshot{}, fmt.Errorf("server %q is not configured for downstream OAuth", serverID)
	}
	c.ready = false
	if err := c.secrets.Delete(ctx, oauthIdentityRef(serverID)); err != nil {
		return routingstate.Snapshot{}, err
	}
	return c.reconcilePersistedLocked(ctx)
}

func (c *Coordinator) reconcilePersistedLocked(ctx context.Context) (routingstate.Snapshot, error) {
	c.ready = false
	manager, serversConfig, err := c.config.LoadOrCreate()
	if err != nil {
		return routingstate.Snapshot{}, err
	}
	hash, err := c.computeHashLocked(ctx, manager, serversConfig)
	if err != nil {
		return routingstate.Snapshot{}, err
	}
	state, _, err := c.tracker.Reconcile(ctx, hash)
	if err != nil {
		return routingstate.Snapshot{}, err
	}
	c.manager = manager
	c.servers = serversConfig
	c.currentHash = hash
	c.ready = true
	return state, nil
}

func (c *Coordinator) computeHashLocked(ctx context.Context, manager v2config.ManagerConfig, serversConfig v2config.ServersConfig) (string, error) {
	if len(c.fingerprintKey) < 32 {
		return "", errors.New("routing fingerprint key is unavailable")
	}
	material := routingstate.ConfigMaterial(manager, serversConfig)
	refs := routingSecretRefs(serversConfig)
	if len(refs) > 0 {
		material.SecretFingerprints = make(map[string]string, len(refs))
		for ref := range refs {
			value, err := c.secrets.Get(ctx, ref)
			if errors.Is(err, secrets.ErrNotFound) {
				material.SecretFingerprints[ref] = "missing"
				continue
			}
			if err != nil {
				return "", fmt.Errorf("resolve routing secret %s: %w", ref, err)
			}
			fingerprint, err := routingstate.FingerprintSecret(c.fingerprintKey, value)
			if err != nil {
				return "", err
			}
			material.SecretFingerprints[ref] = fingerprint
		}
	}
	for _, server := range serversConfig.Servers {
		if !serverUsesOAuth(server) {
			continue
		}
		if material.CredentialIdentityFingerprints == nil {
			material.CredentialIdentityFingerprints = make(map[string]string)
		}
		value, err := c.secrets.Get(ctx, oauthIdentityRef(server.ID))
		if errors.Is(err, secrets.ErrNotFound) {
			material.CredentialIdentityFingerprints[server.ID] = "missing"
			continue
		}
		if err != nil {
			return "", fmt.Errorf("resolve OAuth identity fingerprint for %s: %w", server.ID, err)
		}
		fingerprint := string(value)
		if !strings.HasPrefix(fingerprint, routingstate.FingerprintAlgorithm+":") {
			return "", fmt.Errorf("invalid OAuth identity fingerprint for %s", server.ID)
		}
		material.CredentialIdentityFingerprints[server.ID] = fingerprint
	}
	return routingstate.ComputeHash(material)
}

func (c *Coordinator) ensureRoutingFingerprintKey(ctx context.Context) ([]byte, error) {
	value, err := c.secrets.Get(ctx, RoutingFingerprintKeySecretRef)
	if err == nil {
		if len(value) < 32 {
			return nil, errors.New("stored routing fingerprint key is too short")
		}
		return value, nil
	}
	if !errors.Is(err, secrets.ErrNotFound) {
		return nil, err
	}
	value = make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("generate routing fingerprint key: %w", err)
	}
	if err := c.secrets.Put(ctx, RoutingFingerprintKeySecretRef, value); err != nil {
		return nil, err
	}
	return value, nil
}

func (c *Coordinator) ensureLocalManagerCapability(ctx context.Context) ([]byte, error) {
	value, err := c.secrets.Get(ctx, v2config.LocalManagerCapabilitySecretRef)
	if err == nil {
		if len(value) == 0 {
			return nil, errors.New("stored local Manager capability is empty")
		}
		return value, nil
	}
	if !errors.Is(err, secrets.ErrNotFound) {
		return nil, err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("generate local Manager capability: %w", err)
	}
	value = []byte(base64.RawURLEncoding.EncodeToString(random))
	if err := c.secrets.Put(ctx, v2config.LocalManagerCapabilitySecretRef, value); err != nil {
		return nil, err
	}
	return value, nil
}

func (c *Coordinator) requireInitializedLocked() error {
	if !c.initialized {
		return errors.New("v2 state coordinator is not initialized")
	}
	return nil
}

func routingSecretRefs(serversConfig v2config.ServersConfig) map[string]struct{} {
	refs := make(map[string]struct{})
	for _, server := range serversConfig.Servers {
		for _, ref := range server.Environment.SecretRefs {
			if ref != "" {
				refs[ref] = struct{}{}
			}
		}
		auth := serverHTTPAuth(server)
		if auth != nil && auth.Mode == v2config.HTTPAuthStatic && auth.Static != nil && auth.Static.SecretRef != "" {
			refs[auth.Static.SecretRef] = struct{}{}
		}
	}
	return refs
}

func isRoutingSecretRef(serversConfig v2config.ServersConfig, ref string) bool {
	_, ok := routingSecretRefs(serversConfig)[ref]
	return ok
}

func oauthConfigured(serversConfig v2config.ServersConfig, serverID string) bool {
	for _, server := range serversConfig.Servers {
		if server.ID == serverID {
			return serverUsesOAuth(server)
		}
	}
	return false
}

func serverUsesOAuth(server v2config.ServerEntry) bool {
	auth := serverHTTPAuth(server)
	return auth != nil && auth.Mode == v2config.HTTPAuthOAuth
}

func serverHTTPAuth(server v2config.ServerEntry) *v2config.HTTPAuthConfig {
	switch server.Transport.Type {
	case v2config.TransportManagedHTTP:
		if server.Transport.ManagedHTTP != nil {
			return &server.Transport.ManagedHTTP.Auth
		}
	case v2config.TransportExternalHTTP:
		if server.Transport.ExternalHTTP != nil {
			return &server.Transport.ExternalHTTP.Auth
		}
	}
	return nil
}

func oauthIdentityRef(serverID string) string {
	return oauthIdentitySecretPrefix + serverID
}
