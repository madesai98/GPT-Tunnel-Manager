package routingstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func TestFingerprintSecretIsKeyed(t *testing.T) {
	secret := []byte("same-secret")
	keyA := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	keyB := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	fingerprintA, err := FingerprintSecret(keyA, secret)
	if err != nil {
		t.Fatal(err)
	}
	fingerprintB, err := FingerprintSecret(keyB, secret)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprintA == fingerprintB {
		t.Fatal("fingerprints with different installation keys must differ")
	}
	unkeyed := sha256.Sum256(secret)
	if fingerprintA == "sha256:"+hex.EncodeToString(unkeyed[:]) || fingerprintA == hex.EncodeToString(unkeyed[:]) {
		t.Fatal("routing secret fingerprint must not equal an unkeyed SHA-256 hash")
	}
}

func TestComputeHashIsDeterministicAcrossMapOrder(t *testing.T) {
	manager := v2config.DefaultManagerConfig(43127)
	servers := v2config.DefaultServersConfig()
	first := ConfigMaterial(manager, servers)
	first.SecretFingerprints = map[string]string{"b": "two", "a": "one"}
	second := ConfigMaterial(manager, servers)
	second.SecretFingerprints = map[string]string{"a": "one", "b": "two"}
	hashA, err := ComputeHash(first)
	if err != nil {
		t.Fatal(err)
	}
	hashB, err := ComputeHash(second)
	if err != nil {
		t.Fatal(err)
	}
	if hashA != hashB {
		t.Fatalf("deterministic hashes differ: %s != %s", hashA, hashB)
	}
}

func TestPreferenceAndOperationalSettingsDoNotChangeRoutingHash(t *testing.T) {
	manager := v2config.DefaultManagerConfig(43127)
	servers := v2config.DefaultServersConfig()
	baseline, err := ComputeHash(ConfigMaterial(manager, servers))
	if err != nil {
		t.Fatal(err)
	}

	manager.Routing.DefaultProfile = "work"
	manager.LocalManager.AccessProtectionEnabled = false
	manager.ManagerTunnel.RuntimeCredentialRef = "secret://different-manager-tunnel-key"
	manager.Logging.DisplayLevel = "debug"
	changed, err := ComputeHash(ConfigMaterial(manager, servers))
	if err != nil {
		t.Fatal(err)
	}
	if baseline != changed {
		t.Fatalf("preference/operational settings changed routing hash: %s != %s", baseline, changed)
	}
}

func TestRoutingRelevantServerChangeChangesHash(t *testing.T) {
	manager := v2config.DefaultManagerConfig(43127)
	entry := v2config.ServerEntry{
		ID:   "srv_0123456789abcdef0123456789abcdef",
		Name: "example",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type:  v2config.TransportStdio,
			Stdio: &v2config.StdioTransport{Executable: "example-mcp"},
		},
	}
	servers := v2config.ServersConfig{SchemaVersion: 2, Servers: []v2config.ServerEntry{entry}}
	before, err := ComputeHash(ConfigMaterial(manager, servers))
	if err != nil {
		t.Fatal(err)
	}
	servers.Servers[0].Environment.Values = map[string]string{"MODE": "different"}
	after, err := ComputeHash(ConfigMaterial(manager, servers))
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("routing-relevant server environment change did not alter routing hash")
	}
}

func TestTrackerReconcileAndPreferenceRevisionsStaySeparate(t *testing.T) {
	tracker, err := NewTracker(NewMemoryBackend(Snapshot{}))
	if err != nil {
		t.Fatal(err)
	}
	state, changed, err := tracker.Reconcile(context.Background(), "sha256:first")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || state.RoutingRevision != 1 || state.RoutingStateHash != "sha256:first" {
		t.Fatalf("first reconcile = %#v changed=%v", state, changed)
	}
	state, changed, err = tracker.Reconcile(context.Background(), "sha256:first")
	if err != nil {
		t.Fatal(err)
	}
	if changed || state.RoutingRevision != 1 {
		t.Fatalf("idempotent reconcile = %#v changed=%v", state, changed)
	}

	state, err = tracker.AdvancePreferenceRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.PreferenceRevision != 1 || state.RoutingRevision != 1 || state.RoutingStateHash != "sha256:first" {
		t.Fatalf("preference increment leaked into routing state: %#v", state)
	}

	state, changed, err = tracker.Reconcile(context.Background(), "sha256:second")
	if err != nil {
		t.Fatal(err)
	}
	if !changed || state.RoutingRevision != 2 || state.PreferenceRevision != 1 {
		t.Fatalf("second reconcile = %#v changed=%v", state, changed)
	}
}

func TestShortFingerprintKeyRejected(t *testing.T) {
	if _, err := FingerprintSecret([]byte("short"), []byte("secret")); err == nil {
		t.Fatal("expected short installation key to be rejected")
	}
}
