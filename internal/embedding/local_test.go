package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func TestLocalProviderIdentityUsesPinnedModelAndRuntime(t *testing.T) {
	cfg := v2config.DefaultEmbeddingConfig()
	provider, err := NewLocalGGUF(LocalOptions{Root: t.TempDir(), Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	identity := provider.Identity()
	if identity.Provider != string(v2config.EmbeddingProviderLocalGGUF) {
		t.Fatalf("provider = %q", identity.Provider)
	}
	if identity.Model != v2config.DefaultEmbeddingModelID {
		t.Fatalf("model = %q", identity.Model)
	}
	if identity.ModelSHA256 != cfg.Models[0].SHA256 {
		t.Fatalf("model sha = %q", identity.ModelSHA256)
	}
	if identity.Dimensions == nil || *identity.Dimensions != 384 {
		t.Fatalf("dimensions = %#v", identity.Dimensions)
	}
	if identity.Pooling != "cls" || identity.Runtime != "llama.cpp:"+v2config.DefaultLlamaCppRelease {
		t.Fatalf("identity = %#v", identity)
	}
	if identity.BaseURL != "" {
		t.Fatalf("local identity unexpectedly contains base URL %q", identity.BaseURL)
	}
}

func TestLocalProviderDoesNotFallBackToNetworkWhenModelMissing(t *testing.T) {
	provider, err := NewLocalGGUF(LocalOptions{Root: t.TempDir(), Config: v2config.DefaultEmbeddingConfig()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Embed(context.Background(), []string{"query"})
	if !errors.Is(err, ErrModelNotInstalled) {
		t.Fatalf("error = %v, want ErrModelNotInstalled", err)
	}
}

func TestLocalServerArgsKeepEmbeddingBatchLimitsCoherent(t *testing.T) {
	cfg := v2config.DefaultEmbeddingConfig()
	model, ok := cfg.SelectedModel()
	if !ok {
		t.Fatal("default embedding model missing")
	}
	args := localServerArgs("model.gguf", model, 43210)
	want := strconv.Itoa(localEmbeddingBatchTokenLimit)
	values := make(map[string]string)
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--ctx-size", "--batch-size", "--ubatch-size":
			values[args[i]] = args[i+1]
		}
	}
	for _, flag := range []string{"--ctx-size", "--batch-size", "--ubatch-size"} {
		if values[flag] != want {
			t.Fatalf("%s = %q, want %q; args=%v", flag, values[flag], want, args)
		}
	}
}

func TestDownloadVerifiedRejectsChangedBytes(t *testing.T) {
	body := []byte("local embedding artifact")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "artifact.bin")
	wrong := sha256.Sum256([]byte("different"))
	err := downloadVerified(context.Background(), server.Client(), server.URL, destination, hex.EncodeToString(wrong[:]))
	if err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("checksum failure must not install destination, stat err = %v", statErr)
	}
}

func TestDownloadVerifiedInstallsMatchingBytes(t *testing.T) {
	body := []byte("verified local embedding artifact")
	sum := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "artifact.bin")
	if err := downloadVerified(context.Background(), server.Client(), server.URL, destination, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != string(body) {
		t.Fatalf("installed bytes = %q", installed)
	}
}
