package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if identity.Protocol != LocalGGUFProtocol {
		t.Fatalf("protocol = %q", identity.Protocol)
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

func TestLocalServerArgsUseModelContextAndLargePhysicalBatch(t *testing.T) {
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
	if _, ok := values["--ctx-size"]; ok {
		t.Fatalf("local embedding runtime must use the model-native context size; args=%v", args)
	}
	for _, flag := range []string{"--batch-size", "--ubatch-size"} {
		if values[flag] != want {
			t.Fatalf("%s = %q, want %q; args=%v", flag, values[flag], want, args)
		}
	}
}

func TestLocalEmbeddingChunksInputsBeyondRuntimeContext(t *testing.T) {
	maxEmbeddedContentTokens := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/props":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"default_generation_settings": map[string]any{"n_ctx": 8},
			})
		case "/tokenize":
			var request struct {
				Content    string `json:"content"`
				AddSpecial bool   `json:"add_special"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fields := strings.Fields(request.Content)
			tokens := make([]int, 0, len(fields)+2)
			if request.AddSpecial {
				tokens = append(tokens, 101)
			}
			for index, field := range fields {
				value, err := strconv.Atoi(field)
				if err != nil {
					value = index + 1
				}
				tokens = append(tokens, value)
			}
			if request.AddSpecial {
				tokens = append(tokens, 102)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tokens": tokens})
		case "/detokenize":
			var request struct {
				Tokens []int `json:"tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			parts := make([]string, len(request.Tokens))
			for i, token := range request.Tokens {
				parts[i] = strconv.Itoa(token)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": strings.Join(parts, " ")})
		case "/v1/embeddings":
			var request struct {
				Input []string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data := make([]map[string]any, len(request.Input))
			for i, input := range request.Input {
				fields := strings.Fields(input)
				if len(fields) > maxEmbeddedContentTokens {
					maxEmbeddedContentTokens = len(fields)
				}
				if len(fields)+2 > 8 {
					http.Error(w, "input exceeds model context", http.StatusBadRequest)
					return
				}
				vector := []float32{1, 0}
				if len(fields) > 0 {
					first, _ := strconv.Atoi(fields[0])
					if first >= 7 {
						vector = []float32{0, 1}
					}
				}
				data[i] = map[string]any{"index": i, "embedding": vector}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := v2config.DefaultEmbeddingConfig()
	cfg.Models[0].Dimensions = 2
	provider, err := NewLocalGGUF(LocalOptions{Root: t.TempDir(), Config: cfg, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := provider.embedAtEndpoint(context.Background(), server.URL, []string{"1 2 3 4 5 6 7 8 9 10"})
	if err != nil {
		t.Fatal(err)
	}
	if maxEmbeddedContentTokens > 6 {
		t.Fatalf("embedded chunk contained %d content tokens, want <= 6", maxEmbeddedContentTokens)
	}
	if len(vectors) != 1 || len(vectors[0]) != 2 {
		t.Fatalf("vectors = %#v", vectors)
	}
	want0 := 6 / math.Sqrt(52)
	want1 := 4 / math.Sqrt(52)
	if math.Abs(float64(vectors[0][0])-want0) > 1e-5 || math.Abs(float64(vectors[0][1])-want1) > 1e-5 {
		t.Fatalf("chunk aggregate = %v, want approximately [%f %f]", vectors[0], want0, want1)
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
