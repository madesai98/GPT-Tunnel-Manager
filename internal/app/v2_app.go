package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/discovery"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpmanager"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2state"
)

const (
	serverStaticSecretSuffix = "/auth/static"
	serverEnvSecretPrefix    = "/env/"
)

// V2ManagerSnapshot is the native-facing Manager state. It intentionally does
// not expose the local capability or any credential value.
type V2ManagerSnapshot struct {
	MCPURL                  string
	Running                 bool
	AccessProtectionEnabled bool
	ManagerTunnelEnabled    bool
	ManagerTunnelID         string
}

// V2PreferenceSnapshot is an optimistic-concurrency snapshot suitable for the
// native editor. Every write must send PreferenceRevision back unchanged.
type V2PreferenceSnapshot struct {
	PreferenceRevision uint64
	Profiles           []routingprefs.Profile
	Rules              []routingprefs.Rule
}

// V2App is the Phase 11 native/headless facade over the canonical v2 backend.
// It coexists with the old App only until Phase 12 retires v1 packages.
type V2App struct {
	mu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	root   string

	configStore *v2config.Store
	secrets     secrets.Store
	catalog     *catalog.Catalog
	tracker     *routingstate.Tracker
	state       *v2state.Coordinator
	routing     *v2RoutingAdapter
	product     *v2ProductRuntime

	manager v2config.ManagerConfig
	servers v2config.ServersConfig

	embedding   *liveEmbeddingProvider
	enrichment  *enrichment.Coordinator
	preferences *routingprefs.Store
	handles     *executionhandle.Manager
	lifecycle   *routedlifecycle.Service
	indexing    *indexing.Service
	discovery   *discovery.Service
	mcp         *mcpmanager.V2Server

	started bool
}

// NewV2App performs the clean v2 cutover without interpreting legacy v1 data,
// then constructs the same Phase 2-10 subsystem graph used by the canonical
// Manager MCP. Network listeners are not started until Start is called.
func NewV2App(parent context.Context, root string) (*V2App, error) {
	if parent == nil {
		parent = context.Background()
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("portable root is required")
	}
	ctx, cancel := context.WithCancel(parent)
	a := &V2App{ctx: ctx, cancel: cancel, root: root, configStore: v2config.NewStore(root), secrets: secrets.New(root)}
	fail := func(err error) (*V2App, error) {
		cancel()
		if a.product != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = a.product.close(cleanupCtx)
			cleanupCancel()
		}
		if a.catalog != nil {
			_ = a.catalog.Close()
		}
		return nil, err
	}

	if err := ensureV2ConfigLayout(a.configStore); err != nil {
		return fail(err)
	}
	c, err := catalog.Open(ctx, root)
	if err != nil {
		return fail(err)
	}
	a.catalog = c
	backend, err := routingstate.NewSQLiteBackend(c.DB())
	if err != nil {
		return fail(err)
	}
	tracker, err := routingstate.NewTracker(backend)
	if err != nil {
		return fail(err)
	}
	a.tracker = tracker
	state, err := v2state.New(a.configStore, a.secrets, tracker)
	if err != nil {
		return fail(err)
	}
	a.state = state
	manager, serversConfig, _, err := state.Initialize(ctx)
	if err != nil {
		return fail(err)
	}
	manager.ManagerTunnel.RuntimeCredentialRef = v2config.ManagerRuntimeCredentialRef
	a.manager = cloneManagerConfig(manager)
	a.servers = cloneServersConfig(serversConfig)
	a.routing = &v2RoutingAdapter{tracker: tracker, coordinator: state}
	product, err := newV2ProductRuntime(root, manager)
	if err != nil {
		return fail(err)
	}
	a.product = product

	provider, err := embedding.NewProvider(manager.Embedding, a.secrets, nil)
	if err != nil {
		return fail(fmt.Errorf("configure embedding provider: %w", err))
	}
	liveProvider, err := newLiveEmbeddingProvider(provider)
	if err != nil {
		return fail(err)
	}
	a.embedding = liveProvider
	coordinator, err := enrichment.NewCoordinator(c, liveProvider, enrichment.Options{})
	if err != nil {
		return fail(err)
	}
	a.enrichment = coordinator
	prefs, err := routingprefs.NewStore(c)
	if err != nil {
		return fail(err)
	}
	a.preferences = prefs
	handles, err := executionhandle.NewManager()
	if err != nil {
		return fail(err)
	}
	a.handles = handles

	factory, err := downstream.NewFactory(downstream.Options{
		Secrets: a.secrets,
		Log: func(line downstream.LogLine) {
			if a.product != nil {
				a.product.logDownstream(line.ServerID, line.Stream, line.Text)
			}
		},
		OnToolContractChanged: func(serverID string) {
			if err := c.MarkDirty(context.Background(), "server:"+serverID, "live downstream tool contract changed", ""); err == nil {
				_, _ = tracker.AdvanceRoutingRevision(context.Background())
			}
		},
	})
	if err != nil {
		return fail(err)
	}
	lifecycleService, err := routedlifecycle.New(ctx, manager, serversConfig, routedlifecycle.ConnectWithFactory(factory), routedlifecycle.Options{})
	if err != nil {
		return fail(err)
	}
	a.lifecycle = lifecycleService
	state.SetServerMutationCoordinator(lifecycleService)
	indexService, err := indexing.NewService(c, coordinator, liveProvider, lifecycleService, a.routing, serversConfig)
	if err != nil {
		return fail(err)
	}
	a.indexing = indexService
	if err := a.rebuildManagerLocked(ctx, manager); err != nil {
		return fail(err)
	}
	return a, nil
}

func ensureV2ConfigLayout(store *v2config.Store) error {
	_, _, err := store.LoadOrCreate()
	if err == nil {
		return nil
	}
	managerExists := pathExists(store.ManagerPath())
	serversExists := pathExists(store.ServersPath())
	if managerExists || serversExists {
		return err
	}
	// A legacy release leaves config/ (and commonly data/) present. Move those
	// directories aside opaquely, then initialize v2 from defaults.
	if _, statErr := os.Stat(store.Root + string(os.PathSeparator) + "config"); statErr == nil {
		if _, cutoverErr := store.CutoverOpaqueLegacy(); cutoverErr != nil {
			return cutoverErr
		}
		_, _, loadErr := store.LoadOrCreate()
		return loadErr
	}
	return err
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (a *V2App) rebuildManagerLocked(ctx context.Context, manager v2config.ManagerConfig) error {
	queryCache, err := embedding.NewConfiguredQueryCache(manager.Index)
	if err != nil {
		return err
	}
	discoveryService, err := discovery.NewService(a.catalog, a.embedding, a.preferences, a.routing, a.handles, discovery.Options{
		DefaultProfile: manager.Routing.DefaultProfile,
		QueryCache:     queryCache,
	})
	if err != nil {
		return err
	}
	server, err := mcpmanager.NewV2Server(ctx, mcpmanager.V2ServerOptions{
		Manager: manager, Catalog: a.catalog, Secrets: a.secrets, Lifecycle: a.lifecycle,
		Indexing: a.indexing, Discovery: discoveryService, Preferences: a.preferences,
		RoutingState: a.routing, Handles: a.handles,
	})
	if err != nil {
		return err
	}
	a.discovery = discoveryService
	a.mcp = server
	return nil
}

func (a *V2App) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil
	}
	if err := a.mcp.Start(); err != nil {
		a.mu.Unlock()
		return err
	}
	a.started = true
	lifecycleService := a.lifecycle
	product := a.product
	a.mu.Unlock()
	// A downstream outage must not take the Manager itself offline. Always On
	// maintenance remains owned by routedlifecycle and can recover independently.
	_ = lifecycleService.StartAlwaysOn(ctx)
	if product != nil {
		product.start(a)
	}
	return nil
}

func (a *V2App) Close() error {
	if a == nil {
		return nil
	}
	a.cancel()
	a.mu.Lock()
	server := a.mcp
	lifecycleService := a.lifecycle
	c := a.catalog
	product := a.product
	a.started = false
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var joined error
	if product != nil {
		joined = errors.Join(joined, product.close(ctx))
	}
	if server != nil {
		joined = errors.Join(joined, server.Shutdown(ctx))
	}
	if lifecycleService != nil {
		joined = errors.Join(joined, lifecycleService.Close(ctx))
	}
	if c != nil {
		joined = errors.Join(joined, c.Close())
	}
	return joined
}

func (a *V2App) Root() string { return a.root }

func (a *V2App) ManagerConfig() v2config.ManagerConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneManagerConfig(a.manager)
}

func (a *V2App) ServersConfig() v2config.ServersConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneServersConfig(a.servers)
}

func (a *V2App) Entries() []v2config.ServerEntry {
	return a.ServersConfig().Servers
}

func (a *V2App) Snapshots() []routedlifecycle.Snapshot {
	if a == nil || a.lifecycle == nil {
		return nil
	}
	return a.lifecycle.Snapshots()
}

func (a *V2App) KnownServerToolNames(ctx context.Context, serverID string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	set := make(map[string]struct{})
	live := false
	if a != nil && a.lifecycle != nil {
		snapshot, err := a.lifecycle.KnownTools(serverID)
		if err == nil && len(snapshot.Tools) > 0 {
			live = true
			for _, tool := range snapshot.Tools {
				if tool != nil && strings.TrimSpace(tool.Name) != "" {
					set[tool.Name] = struct{}{}
				}
			}
		}
	}
	if !live && a != nil && a.catalog != nil {
		if generation, err := a.catalog.ActiveGeneration(ctx); err == nil {
			rows, queryErr := a.catalog.DB().QueryContext(ctx, `SELECT tool_name FROM source_tools WHERE generation_id = ? AND server_id = ? ORDER BY tool_name`, generation.ID, serverID)
			if queryErr != nil {
				return nil, queryErr
			}
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					rows.Close()
					return nil, err
				}
				set[name] = struct{}{}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
	}
	if !live {
		for _, entry := range a.Entries() {
			if entry.ID != serverID {
				continue
			}
			for _, name := range entry.ToolVisibility.Hidden {
				set[name] = struct{}{}
			}
			break
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (a *V2App) KnownServerToolCounts(ctx context.Context) map[string]int {
	counts := make(map[string]int)
	for _, entry := range a.Entries() {
		names, err := a.KnownServerToolNames(ctx, entry.ID)
		if err == nil {
			counts[entry.ID] = len(names)
		}
	}
	return counts
}


func (a *V2App) ManagerSnapshot() V2ManagerSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	url := ""
	if a.mcp != nil {
		url = a.mcp.URL()
	}
	return V2ManagerSnapshot{
		MCPURL: url, Running: a.started,
		AccessProtectionEnabled: a.manager.LocalManager.AccessProtectionEnabled,
		ManagerTunnelEnabled: a.manager.ManagerTunnel.Enabled,
		ManagerTunnelID: a.manager.ManagerTunnel.TunnelID,
	}
}

func (a *V2App) SaveManager(ctx context.Context, next v2config.ManagerConfig) error {
	next.ManagerTunnel.RuntimeCredentialRef = v2config.ManagerRuntimeCredentialRef
	if err := v2config.ValidateManager(next); err != nil {
		return err
	}
	provider, err := embedding.NewProvider(next.Embedding, a.secrets, nil)
	if err != nil {
		return err
	}
	old := a.ManagerConfig()
	rollback := func() {}
	if a.product != nil {
		rollback, err = a.product.prepareManagerChange(ctx, old, next)
		if err != nil {
			return err
		}
	}

	a.mu.Lock()
	oldServer := a.mcp
	wasStarted := a.started
	if _, err := a.state.SaveManager(ctx, next); err != nil {
		a.mu.Unlock()
		rollback()
		return err
	}
	if err := a.embedding.Swap(provider); err != nil {
		a.mu.Unlock()
		return err
	}
	if wasStarted && oldServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = oldServer.Shutdown(shutdownCtx)
		cancel()
	}
	if err := a.rebuildManagerLocked(ctx, next); err != nil {
		a.started = false
		a.mu.Unlock()
		return err
	}
	if wasStarted {
		if err := a.mcp.Start(); err != nil {
			a.started = false
			a.mu.Unlock()
			return err
		}
	}
	a.manager = cloneManagerConfig(next)
	a.mu.Unlock()
	if a.product != nil {
		a.product.managerChanged(a, old, next)
	}
	return nil
}

func (a *V2App) SetLocalManagerProtection(ctx context.Context, enabled bool) error {
	manager := a.ManagerConfig()
	manager.LocalManager.AccessProtectionEnabled = enabled
	return a.SaveManager(ctx, manager)
}

func (a *V2App) SetEmbedding(ctx context.Context, cfg v2config.EmbeddingConfig, credential []byte) error {
	cfg.CredentialRef = v2config.EmbeddingCredentialRef
	if len(credential) != 0 {
		if _, err := a.state.PutSecret(ctx, cfg.CredentialRef, append([]byte(nil), credential...)); err != nil {
			return err
		}
		if a.product != nil {
			a.product.log.Redactor().Register(credential)
		}
	}
	manager := a.ManagerConfig()
	manager.Embedding = cfg
	return a.SaveManager(ctx, manager)
}

func (a *V2App) EmbeddingCredentialConfigured(ctx context.Context) bool {
	_, err := a.secrets.Get(ctx, v2config.EmbeddingCredentialRef)
	return err == nil
}

func StaticAuthSecretRef(serverID string) string {
	return "secret://servers/" + strings.TrimSpace(serverID) + serverStaticSecretSuffix
}

func EnvironmentSecretRef(serverID, name string) string {
	return "secret://servers/" + strings.TrimSpace(serverID) + serverEnvSecretPrefix + strings.TrimSpace(name)
}

func (a *V2App) PutStaticAuthSecret(ctx context.Context, serverID string, value []byte) (string, error) {
	ref := StaticAuthSecretRef(serverID)
	if strings.TrimSpace(serverID) == "" || len(value) == 0 {
		return "", errors.New("server id and static credential are required")
	}
	_, err := a.state.PutSecret(ctx, ref, append([]byte(nil), value...))
	if err == nil && a.product != nil {
		a.product.log.Redactor().Register(value)
	}
	return ref, err
}

func (a *V2App) PutEnvironmentSecret(ctx context.Context, serverID, name string, value []byte) (string, error) {
	if strings.TrimSpace(serverID) == "" || strings.TrimSpace(name) == "" || len(value) == 0 {
		return "", errors.New("server id, environment name, and secret value are required")
	}
	ref := EnvironmentSecretRef(serverID, name)
	_, err := a.state.PutSecret(ctx, ref, append([]byte(nil), value...))
	if err == nil && a.product != nil {
		a.product.log.Redactor().Register(value)
	}
	return ref, err
}

func (a *V2App) SaveServers(ctx context.Context, next v2config.ServersConfig) error {
	if err := v2config.ValidateServers(next); err != nil {
		return err
	}
	if _, err := a.state.SaveServers(ctx, next); err != nil {
		return err
	}
	if err := a.indexing.SetServers(next); err != nil {
		return err
	}
	a.mu.Lock()
	a.servers = cloneServersConfig(next)
	a.mu.Unlock()
	if a.product != nil {
		a.product.log.Log("info", "Manager", "Configuration", "server configuration saved", map[string]any{"server_count": len(next.Servers)})
	}
	return nil
}

func (a *V2App) SaveServer(ctx context.Context, entry v2config.ServerEntry) error {
	serversConfig := a.ServersConfig()
	replaced := false
	for i := range serversConfig.Servers {
		if serversConfig.Servers[i].ID == entry.ID {
			serversConfig.Servers[i] = cloneServerEntry(entry)
			replaced = true
			break
		}
	}
	if !replaced {
		serversConfig.Servers = append(serversConfig.Servers, cloneServerEntry(entry))
	}
	sort.Slice(serversConfig.Servers, func(i, j int) bool { return serversConfig.Servers[i].ID < serversConfig.Servers[j].ID })
	return a.SaveServers(ctx, serversConfig)
}

func (a *V2App) DeleteServer(ctx context.Context, serverID string) error {
	serverID = strings.TrimSpace(serverID)
	serversConfig := a.ServersConfig()
	out := serversConfig.Servers[:0]
	found := false
	for _, entry := range serversConfig.Servers {
		if entry.ID == serverID {
			found = true
			continue
		}
		out = append(out, entry)
	}
	if !found {
		return nil
	}
	serversConfig.Servers = out
	return a.SaveServers(ctx, serversConfig)
}

func (a *V2App) StartServer(ctx context.Context, serverID string) (routedlifecycle.Snapshot, error) {
	return a.lifecycle.Start(ctx, serverID)
}
func (a *V2App) StopServer(ctx context.Context, serverID string) (routedlifecycle.Snapshot, error) {
	return a.lifecycle.Stop(ctx, serverID)
}
func (a *V2App) RestartServer(ctx context.Context, serverID string) (routedlifecycle.Snapshot, error) {
	return a.lifecycle.Restart(ctx, serverID)
}

func (a *V2App) IndexStatus(ctx context.Context) (indexing.Status, error) {
	return a.indexing.Status(ctx)
}
func (a *V2App) IndexRefresh(ctx context.Context) (indexing.RefreshResult, error) {
	return a.indexing.Refresh(ctx)
}
func (a *V2App) IndexCommit(ctx context.Context) (indexing.CommitResult, error) {
	return a.indexing.Commit(ctx)
}
func (a *V2App) PendingEnrichment(ctx context.Context, kind catalog.EnrichmentBatchKind, limit int) ([]catalog.EnrichmentBatch, error) {
	return a.indexing.GetBatch(ctx, kind, limit)
}
func (a *V2App) SubmitEnrichment(ctx context.Context, batchID string, response any) (enrichment.SubmitResult, error) {
	return a.indexing.SubmitBatch(ctx, batchID, response)
}

func (a *V2App) RoutingPreferences(ctx context.Context) (V2PreferenceSnapshot, error) {
	revision, err := a.preferences.Revision(ctx)
	if err != nil {
		return V2PreferenceSnapshot{}, err
	}
	profiles, err := a.preferences.ListProfiles(ctx)
	if err != nil {
		return V2PreferenceSnapshot{}, err
	}
	rules, err := a.preferences.ListRules(ctx)
	if err != nil {
		return V2PreferenceSnapshot{}, err
	}
	return V2PreferenceSnapshot{PreferenceRevision: revision, Profiles: profiles, Rules: rules}, nil
}
func (a *V2App) PutRoutingProfile(ctx context.Context, expected uint64, profile routingprefs.Profile) (routingprefs.WriteResult, error) {
	return a.preferences.PutProfile(ctx, expected, profile)
}
func (a *V2App) DeleteRoutingProfile(ctx context.Context, expected uint64, profileID string) (routingprefs.WriteResult, error) {
	return a.preferences.DeleteProfile(ctx, expected, profileID)
}
func (a *V2App) PutRoutingRule(ctx context.Context, expected uint64, spec routingprefs.RuleSpec) (routingprefs.WriteResult, error) {
	return a.preferences.PutRule(ctx, expected, spec)
}
func (a *V2App) DeleteRoutingRule(ctx context.Context, expected uint64, preferenceID string) (routingprefs.WriteResult, error) {
	return a.preferences.DeleteRule(ctx, expected, preferenceID)
}
func (a *V2App) ConfirmRoutingRule(ctx context.Context, expected uint64, preferenceID string) (routingprefs.WriteResult, error) {
	return a.preferences.ConfirmRule(ctx, expected, preferenceID)
}

func cloneManagerConfig(value v2config.ManagerConfig) v2config.ManagerConfig {
	return cloneJSON(value)
}
func cloneServersConfig(value v2config.ServersConfig) v2config.ServersConfig {
	return cloneJSON(value)
}
func cloneServerEntry(value v2config.ServerEntry) v2config.ServerEntry {
	return cloneJSON(value)
}
func cloneJSON[T any](value T) T {
	body, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		return value
	}
	return out
}
