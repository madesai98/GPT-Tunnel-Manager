package v2state

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func TestInitializeCreatesProtectedLocalCapabilityOutsideConfigAndKeepsPortStable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	configStore := v2config.NewStore(root)
	configStore.AllocatePort = func() (int, error) { return 43140, nil }
	secretStore := newMemorySecretStore()
	tracker := mustTracker(t, routingstate.NewMemoryBackend(routingstate.Snapshot{}))
	coordinator := mustCoordinator(t, configStore, secretStore, tracker)

	manager, _, _, err := coordinator.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.LocalManager.AccessProtectionEnabled {
		t.Fatal("local Manager access protection must default enabled")
	}
	capability, err := secretStore.Get(ctx, v2config.LocalManagerCapabilitySecretRef)
	if err != nil || len(capability) == 0 {
		t.Fatalf("local Manager capability was not persisted in the secret store: len=%d err=%v", len(capability), err)
	}
	managerJSON, err := os.ReadFile(configStore.ManagerPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(managerJSON), string(capability)) {
		t.Fatal("raw local Manager capability leaked into manager.json")
	}
	if strings.Contains(string(managerJSON), v2config.LocalManagerCapabilitySecretRef) {
		t.Fatal("local Manager capability storage reference should remain an internal fixed secret path, not config data")
	}
	firstHash, ok := coordinator.CurrentRoutingStateHash()
	if !ok {
		t.Fatal("coordinator should be routable after initialization")
	}

	configStore.AllocatePort = func() (int, error) {
		t.Fatal("persisted Manager port must not be reallocated")
		return 0, nil
	}
	reloaded := mustCoordinator(t, configStore, secretStore, tracker)
	manager2, _, _, err := reloaded.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if manager2.LocalManager.Port != manager.LocalManager.Port {
		t.Fatalf("Manager port changed across reload: %d != %d", manager2.LocalManager.Port, manager.LocalManager.Port)
	}
	secondHash, ok := reloaded.CurrentRoutingStateHash()
	if !ok || secondHash != firstHash {
		t.Fatalf("routing hash changed across no-op reload: %q != %q", secondHash, firstHash)
	}
}

func TestPersistedRoutingConfigMutationStaysFailClosedWhenReconcileFails(t *testing.T) {
	ctx := context.Background()
	configStore := v2config.NewStore(t.TempDir())
	configStore.AllocatePort = func() (int, error) { return 43141, nil }
	secretStore := newMemorySecretStore()
	backend := &flakyRoutingBackend{}
	tracker := mustTracker(t, backend)
	coordinator := mustCoordinator(t, configStore, secretStore, tracker)
	if _, _, _, err := coordinator.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	oldHash, _ := coordinator.CurrentRoutingStateHash()

	serversConfig := v2config.DefaultServersConfig()
	serversConfig.Servers = []v2config.ServerEntry{stdioServer("srv_11111111111111111111111111111111")}
	backend.setFailStore(true)
	if _, err := coordinator.SaveServers(ctx, serversConfig); err == nil {
		t.Fatal("expected routing-state persistence failure")
	}
	if coordinator.GenerationCurrent(oldHash) {
		t.Fatal("old generation remained usable after config persisted but routing reconciliation failed")
	}
	_, persistedServers, err := configStore.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedServers.Servers) != 1 {
		t.Fatalf("routing-affecting config write did not persist before injected reconciliation failure: %#v", persistedServers)
	}

	backend.setFailStore(false)
	restarted := mustCoordinator(t, configStore, secretStore, tracker)
	if _, _, _, err := restarted.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	newHash, ok := restarted.CurrentRoutingStateHash()
	if !ok || newHash == oldHash {
		t.Fatalf("startup did not reconcile persisted config mutation: old=%q new=%q", oldHash, newHash)
	}
	if restarted.GenerationCurrent(oldHash) {
		t.Fatal("pre-crash generation was accepted after startup reconciliation")
	}
	if !restarted.GenerationCurrent(newHash) {
		t.Fatal("reconciled current generation hash was not accepted")
	}
}

func TestStartupDetectsRoutingSecretReplacementThatPrecededCrash(t *testing.T) {
	ctx := context.Background()
	configStore := v2config.NewStore(t.TempDir())
	configStore.AllocatePort = func() (int, error) { return 43142, nil }
	_, serversConfig, err := configStore.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	secretRef := "secret://servers/static/api-key"
	serversConfig.Servers = []v2config.ServerEntry{staticHTTPServer("srv_22222222222222222222222222222222", secretRef)}
	if err := configStore.SaveServers(serversConfig); err != nil {
		t.Fatal(err)
	}
	secretStore := newMemorySecretStore()
	if err := secretStore.Put(ctx, secretRef, []byte("first-secret")); err != nil {
		t.Fatal(err)
	}
	tracker := mustTracker(t, routingstate.NewMemoryBackend(routingstate.Snapshot{}))
	first := mustCoordinator(t, configStore, secretStore, tracker)
	if _, _, _, err := first.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	oldHash, _ := first.CurrentRoutingStateHash()

	// Simulate a crash after the secret store was updated but before the old
	// process could reconcile its routing-state metadata.
	if err := secretStore.Put(ctx, secretRef, []byte("second-secret")); err != nil {
		t.Fatal(err)
	}
	restarted := mustCoordinator(t, configStore, secretStore, tracker)
	if _, _, _, err := restarted.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	newHash, ok := restarted.CurrentRoutingStateHash()
	if !ok || newHash == oldHash {
		t.Fatalf("routing secret replacement did not alter startup hash: old=%q new=%q", oldHash, newHash)
	}
	if restarted.GenerationCurrent(oldHash) {
		t.Fatal("old generation survived a routing-secret replacement across restart")
	}
}

func TestOperationalCredentialRotationDoesNotStaleRouting(t *testing.T) {
	ctx := context.Background()
	configStore := v2config.NewStore(t.TempDir())
	configStore.AllocatePort = func() (int, error) { return 43143, nil }
	secretStore := newMemorySecretStore()
	tracker := mustTracker(t, routingstate.NewMemoryBackend(routingstate.Snapshot{}))
	coordinator := mustCoordinator(t, configStore, secretStore, tracker)
	if _, _, initialState, err := coordinator.Initialize(ctx); err != nil {
		t.Fatal(err)
	} else if initialState.RoutingRevision != 1 {
		t.Fatalf("initial routing revision = %d, want 1", initialState.RoutingRevision)
	}
	initialHash, _ := coordinator.CurrentRoutingStateHash()

	for _, mutation := range []struct {
		ref   string
		value string
	}{
		{v2config.EmbeddingCredentialRef, "embedding-key-rotated"},
		{v2config.ManagerRuntimeCredentialRef, "manager-tunnel-key-rotated"},
		{"secret://oauth/access/srv_example", "routine-access-token-refresh"},
	} {
		state, err := coordinator.PutSecret(ctx, mutation.ref, []byte(mutation.value))
		if err != nil {
			t.Fatalf("PutSecret(%s): %v", mutation.ref, err)
		}
		if state.RoutingRevision != 1 {
			t.Fatalf("operational credential %s advanced routing revision to %d", mutation.ref, state.RoutingRevision)
		}
		currentHash, ok := coordinator.CurrentRoutingStateHash()
		if !ok || currentHash != initialHash {
			t.Fatalf("operational credential %s changed routing hash: %q != %q", mutation.ref, currentHash, initialHash)
		}
	}
}

func TestOAuthIdentityChangesStaleRoutingButTokenRotationDoesNot(t *testing.T) {
	ctx := context.Background()
	configStore := v2config.NewStore(t.TempDir())
	configStore.AllocatePort = func() (int, error) { return 43144, nil }
	_, serversConfig, err := configStore.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	serverID := "srv_33333333333333333333333333333333"
	serversConfig.Servers = []v2config.ServerEntry{oauthHTTPServer(serverID)}
	if err := configStore.SaveServers(serversConfig); err != nil {
		t.Fatal(err)
	}
	secretStore := newMemorySecretStore()
	tracker := mustTracker(t, routingstate.NewMemoryBackend(routingstate.Snapshot{}))
	coordinator := mustCoordinator(t, configStore, secretStore, tracker)
	if _, _, _, err := coordinator.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	missingIdentityHash, _ := coordinator.CurrentRoutingStateHash()

	if _, err := coordinator.PutSecret(ctx, "secret://oauth/access/"+serverID, []byte("access-token-a")); err != nil {
		t.Fatal(err)
	}
	if hash, _ := coordinator.CurrentRoutingStateHash(); hash != missingIdentityHash {
		t.Fatal("routine OAuth access-token storage changed routing hash")
	}
	state, err := coordinator.SetOAuthCredentialIdentity(ctx, serverID, []byte("account=user-a;scopes=read"))
	if err != nil {
		t.Fatal(err)
	}
	identityAHash, _ := coordinator.CurrentRoutingStateHash()
	if identityAHash == missingIdentityHash || state.RoutingRevision != 2 {
		t.Fatalf("first OAuth identity did not stale routing: revision=%d old=%q new=%q", state.RoutingRevision, missingIdentityHash, identityAHash)
	}
	storedIdentity, err := secretStore.Get(ctx, oauthIdentityRef(serverID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedIdentity), "user-a") || !strings.HasPrefix(string(storedIdentity), routingstate.FingerprintAlgorithm+":") {
		t.Fatalf("OAuth identity persistence must contain only keyed fingerprint material, got %q", storedIdentity)
	}

	if _, err := coordinator.PutSecret(ctx, "secret://oauth/access/"+serverID, []byte("access-token-b")); err != nil {
		t.Fatal(err)
	}
	if hash, _ := coordinator.CurrentRoutingStateHash(); hash != identityAHash {
		t.Fatal("routine OAuth token refresh changed routing hash after identity was established")
	}
	state, err = coordinator.SetOAuthCredentialIdentity(ctx, serverID, []byte("account=user-a;scopes=read"))
	if err != nil {
		t.Fatal(err)
	}
	if state.RoutingRevision != 2 {
		t.Fatalf("identical OAuth identity write advanced routing revision to %d", state.RoutingRevision)
	}
	state, err = coordinator.SetOAuthCredentialIdentity(ctx, serverID, []byte("account=user-b;scopes=read"))
	if err != nil {
		t.Fatal(err)
	}
	identityBHash, _ := coordinator.CurrentRoutingStateHash()
	if state.RoutingRevision != 3 || identityBHash == identityAHash {
		t.Fatalf("OAuth account change did not stale routing: revision=%d a=%q b=%q", state.RoutingRevision, identityAHash, identityBHash)
	}
}

func TestRoutingSecretValuesNeverLeakIntoConfig(t *testing.T) {
	ctx := context.Background()
	configStore := v2config.NewStore(t.TempDir())
	configStore.AllocatePort = func() (int, error) { return 43145, nil }
	secretStore := newMemorySecretStore()
	tracker := mustTracker(t, routingstate.NewMemoryBackend(routingstate.Snapshot{}))
	coordinator := mustCoordinator(t, configStore, secretStore, tracker)
	if _, _, _, err := coordinator.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	secretRef := "secret://servers/leak-test/api-key"
	serversConfig := v2config.DefaultServersConfig()
	serversConfig.Servers = []v2config.ServerEntry{staticHTTPServer("srv_44444444444444444444444444444444", secretRef)}
	if _, err := coordinator.SaveServers(ctx, serversConfig); err != nil {
		t.Fatal(err)
	}
	rawSecret := "raw-secret-must-never-appear-in-config"
	if _, err := coordinator.PutSecret(ctx, secretRef, []byte(rawSecret)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{configStore.ManagerPath(), configStore.ServersPath()} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), rawSecret) {
			t.Fatalf("raw routing secret leaked into %s", path)
		}
	}
	serversBody, err := os.ReadFile(configStore.ServersPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serversBody), secretRef) {
		t.Fatal("servers config should persist only the secret reference")
	}
}

func stdioServer(id string) v2config.ServerEntry {
	return v2config.ServerEntry{
		ID:   id,
		Name: "stdio test",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type:  v2config.TransportStdio,
			Stdio: &v2config.StdioTransport{Executable: "test-mcp"},
		},
	}
}

func staticHTTPServer(id, secretRef string) v2config.ServerEntry {
	return v2config.ServerEntry{
		ID:   id,
		Name: "static auth test",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type: v2config.TransportExternalHTTP,
			ExternalHTTP: &v2config.ExternalHTTPTransport{
				URL: "https://example.test/mcp",
				Auth: v2config.HTTPAuthConfig{
					Mode: v2config.HTTPAuthStatic,
					Static: &v2config.StaticAuthConfig{
						HeaderName: "Authorization",
						Scheme:     "Bearer",
						SecretRef:  secretRef,
					},
				},
			},
		},
	}
}

func oauthHTTPServer(id string) v2config.ServerEntry {
	return v2config.ServerEntry{
		ID:   id,
		Name: "oauth test",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type: v2config.TransportExternalHTTP,
			ExternalHTTP: &v2config.ExternalHTTPTransport{
				URL: "https://example.test/mcp",
				Auth: v2config.HTTPAuthConfig{
					Mode:  v2config.HTTPAuthOAuth,
					OAuth: &v2config.OAuthAuthConfig{Scopes: []string{"read"}},
				},
			},
		},
	}
}

type memorySecretStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{values: make(map[string][]byte)}
}

func (s *memorySecretStore) Put(_ context.Context, ref string, value []byte) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[ref] = append([]byte(nil), value...)
	return nil
}

func (s *memorySecretStore) Get(_ context.Context, ref string) ([]byte, error) {
	if err := secrets.ValidateRef(ref); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[ref]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *memorySecretStore) Delete(_ context.Context, ref string) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, ref)
	return nil
}

type flakyRoutingBackend struct {
	mu        sync.Mutex
	state     routingstate.Snapshot
	failStore bool
}

func (b *flakyRoutingBackend) Load(context.Context) (routingstate.Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state, nil
}

func (b *flakyRoutingBackend) Store(_ context.Context, state routingstate.Snapshot) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failStore {
		return errors.New("injected routing state store failure")
	}
	b.state = state
	return nil
}

func (b *flakyRoutingBackend) setFailStore(value bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failStore = value
}

func mustTracker(t *testing.T, backend routingstate.Backend) *routingstate.Tracker {
	t.Helper()
	tracker, err := routingstate.NewTracker(backend)
	if err != nil {
		t.Fatal(err)
	}
	return tracker
}

func mustCoordinator(t *testing.T, configStore *v2config.Store, secretStore secrets.Store, tracker *routingstate.Tracker) *Coordinator {
	t.Helper()
	coordinator, err := New(configStore, secretStore, tracker)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}
