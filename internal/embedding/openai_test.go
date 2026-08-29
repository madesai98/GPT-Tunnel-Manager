package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

type memorySecrets struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (m *memorySecrets) Put(_ context.Context, ref string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[ref] = append([]byte(nil), value...)
	return nil
}

func (m *memorySecrets) Get(_ context.Context, ref string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[ref]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (m *memorySecrets) Delete(_ context.Context, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, ref)
	return nil
}

func testSecretStore(token string) *memorySecrets {
	return &memorySecrets{values: map[string][]byte{v2config.EmbeddingCredentialRef: []byte(token)}}
}

func testEmbeddingConfig(baseURL string, dimensions *int) v2config.EmbeddingConfig {
	return v2config.EmbeddingConfig{
		Provider:      v2config.EmbeddingProviderOpenAICompatible,
		BaseURL:       baseURL,
		Model:         "test-model",
		Dimensions:    dimensions,
		CredentialRef: v2config.EmbeddingCredentialRef,
	}
}

func TestOpenAICompatibleBatchesDimensionsAndResponseOrdering(t *testing.T) {
	const token = "super-secret-embedding-token"
	dimensions := 3
	var mu sync.Mutex
	var batches [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("authorization = %q", got)
		}
		var request struct {
			Input      []string `json:"input"`
			Model      string   `json:"model"`
			Dimensions *int     `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || request.Dimensions == nil || *request.Dimensions != dimensions {
			t.Fatalf("request = %#v", request)
		}
		mu.Lock()
		batches = append(batches, append([]string(nil), request.Input...))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if len(request.Input) == 2 {
			fmt.Fprint(w, `{"data":[{"index":1,"embedding":[0,1,0]},{"index":0,"embedding":[1,0,0]}]}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[0,0,1]}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatible(OpenAICompatibleOptions{
		Config:    testEmbeddingConfig(server.URL+"/v1/", &dimensions),
		Secrets:   testSecretStore(token),
		Client:    server.Client(),
		BatchSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := provider.Embed(context.Background(), []string{"one", "two", "three"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 3 || vectors[0][0] != 1 || vectors[1][1] != 1 || vectors[2][2] != 1 {
		t.Fatalf("vectors = %#v", vectors)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 1 {
		t.Fatalf("batches = %#v", batches)
	}
	identity := provider.Identity()
	if identity.BaseURL != server.URL+"/v1" || identity.Model != "test-model" || identity.Protocol != OpenAICompatibleProtocol {
		t.Fatalf("identity = %#v", identity)
	}
	if identity.Fingerprint() == "" {
		t.Fatal("identity fingerprint is empty")
	}
}

func TestOpenAICompatibleRejectsMalformedResponses(t *testing.T) {
	dimensions := 2
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "count mismatch", body: `{"data":[]}`, want: "count mismatch"},
		{name: "missing index", body: `{"data":[{"embedding":[1,0]}]}`, want: "missing index"},
		{name: "duplicate index", body: `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`, want: "duplicate index"},
		{name: "dimension mismatch", body: `{"data":[{"index":0,"embedding":[1,0,0]}]}`, want: "dimension mismatch"},
		{name: "zero vector", body: `{"data":[{"index":0,"embedding":[0,0]}]}`, want: "zero magnitude"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			provider, err := NewOpenAICompatible(OpenAICompatibleOptions{
				Config:  testEmbeddingConfig(server.URL, &dimensions),
				Secrets: testSecretStore("token"),
				Client:  server.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			inputs := []string{"one"}
			if tc.name == "duplicate index" {
				inputs = []string{"one", "two"}
			}
			_, err = provider.Embed(context.Background(), inputs)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestOpenAICompatibleDoesNotLeakCredentialOrErrorBody(t *testing.T) {
	const token = "credential-that-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider echoed "+token, http.StatusUnauthorized)
	}))
	defer server.Close()
	provider, err := NewOpenAICompatible(OpenAICompatibleOptions{
		Config:  testEmbeddingConfig(server.URL, nil),
		Secrets: testSecretStore(token),
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Embed(context.Background(), []string{"query"})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "provider echoed") {
		t.Fatalf("error leaked provider body or credential: %v", err)
	}
}

func TestOpenAICompatibleHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	}))
	defer server.Close()
	provider, err := NewOpenAICompatible(OpenAICompatibleOptions{
		Config:  testEmbeddingConfig(server.URL, nil),
		Secrets: testSecretStore("token"),
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := provider.Embed(ctx, []string{"query"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled request did not return")
	}
}
