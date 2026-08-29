package v2state

import (
	"context"
	"errors"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

type fakeServerMutationCoordinator struct {
	prepareErr error
	prepares   int
	commits    int
	aborts     int
	current    v2config.ServersConfig
	next       v2config.ServersConfig
}

func (f *fakeServerMutationCoordinator) PrepareServerMutation(current, next v2config.ServersConfig) error {
	f.prepares++
	f.current = current
	f.next = next
	return f.prepareErr
}

func (f *fakeServerMutationCoordinator) CommitServerMutation(next v2config.ServersConfig) {
	f.commits++
	f.next = next
}

func (f *fakeServerMutationCoordinator) AbortServerMutation() { f.aborts++ }

func TestSaveServersMutationReservationPrecedesPersistence(t *testing.T) {
	ctx := context.Background()
	store := v2config.NewStore(t.TempDir())
	store.AllocatePort = func() (int, error) { return 43160, nil }
	secretStore := newMemorySecretStore()
	tracker := mustTracker(t, routingstate.NewMemoryBackend(routingstate.Snapshot{}))
	coordinator := mustCoordinator(t, store, secretStore, tracker)
	if _, _, _, err := coordinator.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	serverID := "srv_dddddddddddddddddddddddddddddddd"
	current := v2config.DefaultServersConfig()
	current.Servers = []v2config.ServerEntry{stdioServer(serverID)}
	if _, err := coordinator.SaveServers(ctx, current); err != nil {
		t.Fatal(err)
	}
	oldHash, _ := coordinator.CurrentRoutingStateHash()

	guard := &fakeServerMutationCoordinator{prepareErr: errors.New("server_busy")}
	coordinator.SetServerMutationCoordinator(guard)
	next := current
	next.Servers = append([]v2config.ServerEntry(nil), current.Servers...)
	next.Servers[0].Name = "blocked-edit"
	if _, err := coordinator.SaveServers(ctx, next); err == nil || err.Error() != "server_busy" {
		t.Fatalf("SaveServers busy reservation error = %v", err)
	}
	if guard.prepares != 1 || guard.commits != 0 || guard.aborts != 0 {
		t.Fatalf("guard calls after rejected mutation: prepare=%d commit=%d abort=%d", guard.prepares, guard.commits, guard.aborts)
	}
	_, persisted, err := store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Servers) != 1 || persisted.Servers[0].Name == "blocked-edit" {
		t.Fatalf("server_busy mutation persisted despite reservation failure: %#v", persisted.Servers)
	}
	if hash, ok := coordinator.CurrentRoutingStateHash(); !ok || hash != oldHash {
		t.Fatalf("server_busy mutation changed routing state: hash=%q ok=%v old=%q", hash, ok, oldHash)
	}

	guard.prepareErr = nil
	if _, err := coordinator.SaveServers(ctx, next); err != nil {
		t.Fatal(err)
	}
	if guard.prepares != 2 || guard.commits != 1 || guard.aborts != 0 {
		t.Fatalf("guard calls after committed mutation: prepare=%d commit=%d abort=%d", guard.prepares, guard.commits, guard.aborts)
	}
	_, persisted, err = store.LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Servers[0].Name != "blocked-edit" {
		t.Fatalf("accepted mutation did not persist: %#v", persisted.Servers[0])
	}
}
