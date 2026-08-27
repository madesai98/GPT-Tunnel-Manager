package servers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/tunnelclient"
)

type panicSecretStore struct{}

func (panicSecretStore) Put(context.Context, string, []byte) error { return nil }
func (panicSecretStore) Get(context.Context, string) ([]byte, error) {
	panic("simulated third-party startup panic")
}
func (panicSecretStore) Delete(context.Context, string) error { return nil }

func TestFactoryStartContainsPanic(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tunnel-client")
	if err := os.WriteFile(binary, []byte("stub"), 0o700); err != nil {
		t.Fatal(err)
	}
	factory := &Factory{
		Installer:            &tunnelclient.Installer{},
		Secrets:              panicSecretStore{},
		DefaultCredentialRef: "secret://runtime/default",
		BinaryOverride:       binary,
		Channel:              "stable",
	}
	entry := config.ServerEntry{
		Tunnel: config.TunnelConfig{TunnelID: "tunnel_0123456789abcdef0123456789abcdef"},
		Transport: config.TransportConfig{
			Type:  config.TransportStdio,
			Stdio: &config.StdioTransport{Executable: "npx", Args: []string{"-y", "@zavora-ai/computer-use-mcp@7.1.0"}},
		},
	}
	runtime, err := factory.Start(context.Background(), entry)
	if runtime != nil {
		t.Fatal("runtime should be nil after contained panic")
	}
	if err == nil || !strings.Contains(err.Error(), "server runtime startup panic") {
		t.Fatalf("err=%v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("panic was misreported as cancellation: %v", err)
	}
}
