package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

const (
	OpenAICompatibleProtocol = "openai-embeddings/v1"
	defaultBatchSize         = 128
	maxBatchSize             = 2048
	maxResponseBytes         = 64 << 20
)

type OpenAICompatibleOptions struct {
	Config    v2config.EmbeddingConfig
	Secrets   secrets.Store
	Client    *http.Client
	BatchSize int
}

type OpenAICompatible struct {
	identity      Identity
	credentialRef string
	secrets       secrets.Store
	client        *http.Client
	endpoint      string
	batchSize     int
}

func NewOpenAICompatible(options OpenAICompatibleOptions) (*OpenAICompatible, error) {
	config := options.Config
	if config.Provider != v2config.EmbeddingProviderOpenAICompatible {
		return nil, fmt.Errorf("unsupported embedding provider %q", config.Provider)
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("embedding model is required")
	}
	if options.Secrets == nil {
		return nil, errors.New("embedding secret store is required")
	}
	if err := secrets.ValidateRef(config.CredentialRef); err != nil {
		return nil, fmt.Errorf("embedding credential reference: %w", err)
	}
	baseURL, err := normalizeBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	batchSize := options.BatchSize
	if batchSize == 0 {
		batchSize = defaultBatchSize
	}
	if batchSize < 1 || batchSize > maxBatchSize {
		return nil, fmt.Errorf("embedding batch size must be between 1 and %d", maxBatchSize)
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	identity := Identity{
		Provider:   string(config.Provider),
		BaseURL:    baseURL,
		Model:      config.Model,
		Dimensions: cloneInt(config.Dimensions),
		Protocol:   OpenAICompatibleProtocol,
	}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	return &OpenAICompatible{
		identity:      identity,
		credentialRef: config.CredentialRef,
		secrets:       options.Secrets,
		client:        client,
		endpoint:      strings.TrimRight(baseURL, "/") + "/embeddings",
		batchSize:     batchSize,
	}, nil
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("embedding base URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse embedding base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("embedding base URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("embedding base URL must include a host")
	}
	if parsed.User != nil {
		return "", errors.New("embedding base URL may not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("embedding base URL may not contain a query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (p *OpenAICompatible) Identity() Identity {
	identity := p.identity
	identity.Dimensions = cloneInt(identity.Dimensions)
	return identity
}

func (p *OpenAICompatible) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if p == nil {
		return nil, errors.New("embedding provider is nil")
	}
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	credential, err := p.secrets.Get(ctx, p.credentialRef)
	if err != nil {
		return nil, fmt.Errorf("resolve embedding credential: %w", err)
	}
	if len(credential) == 0 {
		return nil, errors.New("embedding credential is empty")
	}
	defer zeroBytes(credential)

	result := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += p.batchSize {
		end := start + p.batchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		batch, err := p.embedBatch(ctx, inputs[start:end], credential)
		if err != nil {
			return nil, err
		}
		result = append(result, batch...)
	}
	return result, nil
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

type openAIEmbeddingRequest struct {
	Input      []string `json:"input"`
	Model      string   `json:"model"`
	Dimensions *int     `json:"dimensions,omitempty"`
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     *int      `json:"index"`
	} `json:"data"`
}

func (p *OpenAICompatible) embedBatch(ctx context.Context, inputs []string, credential []byte) ([][]float32, error) {
	body, err := json.Marshal(openAIEmbeddingRequest{
		Input:      inputs,
		Model:      p.identity.Model,
		Dimensions: cloneInt(p.identity.Dimensions),
	})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(credential))
	response, err := p.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("embedding request failed with HTTP %s", response.Status)
	}
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return nil, errors.New("embedding response exceeds size limit")
	}
	var decoded openAIEmbeddingResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(decoded.Data) != len(inputs) {
		return nil, fmt.Errorf("embedding response count mismatch: got %d, want %d", len(decoded.Data), len(inputs))
	}
	ordered := make([][]float32, len(inputs))
	seen := make([]bool, len(inputs))
	expectedDimensions := 0
	if p.identity.Dimensions != nil {
		expectedDimensions = *p.identity.Dimensions
	}
	for _, item := range decoded.Data {
		if item.Index == nil {
			return nil, errors.New("embedding response item is missing index")
		}
		index := *item.Index
		if index < 0 || index >= len(inputs) {
			return nil, fmt.Errorf("embedding response index %d is out of range", index)
		}
		if seen[index] {
			return nil, fmt.Errorf("embedding response contains duplicate index %d", index)
		}
		if expectedDimensions == 0 {
			expectedDimensions = len(item.Embedding)
		}
		if err := ValidateVector(item.Embedding, expectedDimensions); err != nil {
			return nil, fmt.Errorf("embedding response item %d: %w", index, err)
		}
		seen[index] = true
		ordered[index] = append([]float32(nil), item.Embedding...)
	}
	return ordered, nil
}
