package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	processutil "github.com/madesai98/GPT-Tunnel-Manager/internal/process"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

const (
	LocalGGUFProtocol              = "llama.cpp-v1-embeddings/local-chunked-v2"
	localEmbeddingBatchTokenLimit = 2048
)

type LocalOptions struct {
	Root   string
	Config v2config.EmbeddingConfig
	Client *http.Client
}

type LocalGGUF struct {
	mu sync.Mutex

	root     string
	config   v2config.EmbeddingConfig
	model    v2config.EmbeddingModel
	identity Identity
	client   *http.Client

	cmd      *exec.Cmd
	endpoint string
}

type embeddingSegment struct {
	inputIndex    int
	text          string
	contentTokens int
	requestTokens int
}

func NewLocalGGUF(options LocalOptions) (*LocalGGUF, error) {
	if strings.TrimSpace(options.Root) == "" {
		return nil, errors.New("embedding root is required")
	}
	config := v2config.EffectiveEmbeddingConfig(options.Config)
	if config.Provider != v2config.EmbeddingProviderLocalGGUF {
		return nil, fmt.Errorf("unsupported embedding provider %q", config.Provider)
	}
	model, ok := config.SelectedModel()
	if !ok {
		return nil, fmt.Errorf("embedding model %q is not configured", config.Model)
	}
	dimensions := model.Dimensions
	runtimeIdentity := "llama.cpp:" + config.Runtime.Release
	if config.Runtime.BinaryPath != "" {
		runtimeIdentity = "llama.cpp:custom:" + filepath.Clean(config.Runtime.BinaryPath)
	}
	identity := Identity{
		Provider:    string(config.Provider),
		Model:       model.ID,
		ModelSHA256: model.SHA256,
		Dimensions:  &dimensions,
		Pooling:     model.Pooling,
		Runtime:     runtimeIdentity,
		Protocol:    LocalGGUFProtocol,
	}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &LocalGGUF{root: options.Root, config: config, model: model, identity: identity, client: client}, nil
}

func (p *LocalGGUF) Identity() Identity {
	identity := p.identity
	identity.Dimensions = cloneInt(identity.Dimensions)
	return identity
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (p *LocalGGUF) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if p == nil {
		return nil, errors.New("embedding provider is nil")
	}
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	endpoint, err := p.ensureServer(ctx)
	if err != nil {
		return nil, err
	}
	return p.embedAtEndpoint(ctx, endpoint, inputs)
}

func (p *LocalGGUF) embedAtEndpoint(ctx context.Context, endpoint string, inputs []string) ([][]float32, error) {
	segments, segmentCounts, err := p.prepareEmbeddingSegments(ctx, endpoint, inputs)
	if err != nil {
		return nil, err
	}
	results := make([][]float32, len(inputs))
	aggregates := make([][]float64, len(inputs))

	for _, batch := range embeddingSegmentBatches(segments, localEmbeddingBatchTokenLimit) {
		texts := make([]string, len(batch))
		for i, segment := range batch {
			texts[i] = segment.text
		}
		vectors, err := p.embedBatch(ctx, endpoint, texts)
		if err != nil {
			return nil, err
		}
		for i, segment := range batch {
			vector := vectors[i]
			if segmentCounts[segment.inputIndex] == 1 {
				results[segment.inputIndex] = append([]float32(nil), vector...)
				continue
			}
			if aggregates[segment.inputIndex] == nil {
				aggregates[segment.inputIndex] = make([]float64, p.model.Dimensions)
			}
			weight := float64(segment.contentTokens)
			if weight <= 0 {
				weight = 1
			}
			for dimension, value := range vector {
				aggregates[segment.inputIndex][dimension] += float64(value) * weight
			}
		}
	}

	for inputIndex := range inputs {
		if segmentCounts[inputIndex] == 1 {
			if err := ValidateVector(results[inputIndex], p.model.Dimensions); err != nil {
				return nil, fmt.Errorf("local embedding result %d: %w", inputIndex, err)
			}
			continue
		}
		aggregate := aggregates[inputIndex]
		if len(aggregate) != p.model.Dimensions {
			return nil, fmt.Errorf("local embedding result %d has no aggregate", inputIndex)
		}
		var magnitude float64
		for _, value := range aggregate {
			magnitude += value * value
		}
		if magnitude == 0 {
			return nil, fmt.Errorf("local embedding result %d: %w", inputIndex, ErrZeroVector)
		}
		scale := 1 / math.Sqrt(magnitude)
		vector := make([]float32, len(aggregate))
		for dimension, value := range aggregate {
			vector[dimension] = float32(value * scale)
		}
		if err := ValidateVector(vector, p.model.Dimensions); err != nil {
			return nil, fmt.Errorf("local embedding result %d: %w", inputIndex, err)
		}
		results[inputIndex] = vector
	}
	return results, nil
}

func (p *LocalGGUF) prepareEmbeddingSegments(ctx context.Context, endpoint string, inputs []string) ([]embeddingSegment, []int, error) {
	contextTokens, err := p.runtimeContextTokens(ctx, endpoint)
	if err != nil {
		return nil, nil, err
	}
	sequenceLimit := contextTokens
	if sequenceLimit > localEmbeddingBatchTokenLimit {
		sequenceLimit = localEmbeddingBatchTokenLimit
	}
	if sequenceLimit <= 0 {
		return nil, nil, errors.New("local embedding runtime reported an invalid context size")
	}

	probePlain, err := p.tokenize(ctx, endpoint, "context", false)
	if err != nil {
		return nil, nil, err
	}
	probeSpecial, err := p.tokenize(ctx, endpoint, "context", true)
	if err != nil {
		return nil, nil, err
	}
	specialOverhead := len(probeSpecial) - len(probePlain)
	if specialOverhead < 0 {
		specialOverhead = 0
	}
	maxContentTokens := sequenceLimit - specialOverhead
	if maxContentTokens <= 0 {
		return nil, nil, fmt.Errorf("local embedding context size %d is too small for tokenizer special tokens", sequenceLimit)
	}

	segments := make([]embeddingSegment, 0, len(inputs))
	segmentCounts := make([]int, len(inputs))
	for inputIndex, input := range inputs {
		plainTokens, err := p.tokenize(ctx, endpoint, input, false)
		if err != nil {
			return nil, nil, fmt.Errorf("tokenize local embedding input %d: %w", inputIndex, err)
		}
		fullTokens, err := p.tokenize(ctx, endpoint, input, true)
		if err != nil {
			return nil, nil, fmt.Errorf("tokenize local embedding input %d with special tokens: %w", inputIndex, err)
		}
		if len(fullTokens) <= sequenceLimit {
			segments = append(segments, embeddingSegment{
				inputIndex:    inputIndex,
				text:          input,
				contentTokens: maxInt(1, len(plainTokens)),
				requestTokens: maxInt(1, len(fullTokens)),
			})
			segmentCounts[inputIndex] = 1
			continue
		}
		if len(plainTokens) == 0 {
			return nil, nil, fmt.Errorf("local embedding input %d exceeds context but tokenizer returned no content tokens", inputIndex)
		}

		for start := 0; start < len(plainTokens); {
			end := start + maxContentTokens
			if end > len(plainTokens) {
				end = len(plainTokens)
			}
			var (
				text      string
				retokens  []int
				fitErr    error
			)
			for end > start {
				text, fitErr = p.detokenize(ctx, endpoint, plainTokens[start:end])
				if fitErr != nil {
					return nil, nil, fmt.Errorf("detokenize local embedding input %d: %w", inputIndex, fitErr)
				}
				retokens, fitErr = p.tokenize(ctx, endpoint, text, true)
				if fitErr != nil {
					return nil, nil, fmt.Errorf("verify local embedding chunk %d: %w", inputIndex, fitErr)
				}
				if len(retokens) <= sequenceLimit {
					break
				}
				overflow := len(retokens) - sequenceLimit
				end -= maxInt(1, overflow)
			}
			if end <= start {
				return nil, nil, fmt.Errorf("local embedding input %d cannot be chunked to context size %d", inputIndex, sequenceLimit)
			}
			segments = append(segments, embeddingSegment{
				inputIndex:    inputIndex,
				text:          text,
				contentTokens: end - start,
				requestTokens: maxInt(1, len(retokens)),
			})
			segmentCounts[inputIndex]++
			start = end
		}
	}
	return segments, segmentCounts, nil
}

func embeddingSegmentBatches(segments []embeddingSegment, tokenLimit int) [][]embeddingSegment {
	if len(segments) == 0 {
		return nil
	}
	if tokenLimit <= 0 {
		tokenLimit = localEmbeddingBatchTokenLimit
	}
	batches := make([][]embeddingSegment, 0, 1)
	current := make([]embeddingSegment, 0)
	currentTokens := 0
	for _, segment := range segments {
		cost := maxInt(1, segment.requestTokens)
		if len(current) > 0 && currentTokens+cost > tokenLimit {
			batches = append(batches, current)
			current = make([]embeddingSegment, 0)
			currentTokens = 0
		}
		current = append(current, segment)
		currentTokens += cost
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (p *LocalGGUF) runtimeContextTokens(ctx context.Context, endpoint string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/props", nil)
	if err != nil {
		return 0, fmt.Errorf("create local embedding props request: %w", err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, fmt.Errorf("local embedding props request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return 0, fmt.Errorf("local embedding props returned HTTP %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var decoded struct {
		DefaultGenerationSettings struct {
			ContextTokens int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&decoded); err != nil {
		return 0, fmt.Errorf("decode local embedding props: %w", err)
	}
	if decoded.DefaultGenerationSettings.ContextTokens <= 0 {
		return 0, errors.New("local embedding runtime did not report a positive context size")
	}
	return decoded.DefaultGenerationSettings.ContextTokens, nil
}

func (p *LocalGGUF) tokenize(ctx context.Context, endpoint, content string, addSpecial bool) ([]int, error) {
	body, err := json.Marshal(struct {
		Content    string `json:"content"`
		AddSpecial bool   `json:"add_special"`
	}{Content: content, AddSpecial: addSpecial})
	if err != nil {
		return nil, fmt.Errorf("encode local tokenizer request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/tokenize", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create local tokenizer request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("local tokenizer request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("local tokenizer returned HTTP %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var decoded struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode local tokenizer response: %w", err)
	}
	return decoded.Tokens, nil
}

func (p *LocalGGUF) detokenize(ctx context.Context, endpoint string, tokens []int) (string, error) {
	body, err := json.Marshal(struct {
		Tokens []int `json:"tokens"`
	}{Tokens: tokens})
	if err != nil {
		return "", fmt.Errorf("encode local detokenizer request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/detokenize", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create local detokenizer request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("local detokenizer request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return "", fmt.Errorf("local detokenizer returned HTTP %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var decoded struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode local detokenizer response: %w", err)
	}
	return decoded.Content, nil
}

func (p *LocalGGUF) embedBatch(ctx context.Context, endpoint string, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(struct {
		Input []string `json:"input"`
		Model string   `json:"model"`
	}{Input: inputs, Model: p.model.ID})
	if err != nil {
		return nil, fmt.Errorf("encode local embedding request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create local embedding request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("local embedding request failed: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 64<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(limited, 64<<10))
		return nil, fmt.Errorf("local embedding runtime returned HTTP %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var decoded struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(limited).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode local embedding response: %w", err)
	}
	if len(decoded.Data) != len(inputs) {
		return nil, fmt.Errorf("local embedding response count mismatch: got %d, want %d", len(decoded.Data), len(inputs))
	}
	ordered := make([][]float32, len(inputs))
	seen := make([]bool, len(inputs))
	for _, item := range decoded.Data {
		if item.Index < 0 || item.Index >= len(inputs) {
			return nil, fmt.Errorf("local embedding response index %d is out of range", item.Index)
		}
		if seen[item.Index] {
			return nil, fmt.Errorf("local embedding response contains duplicate index %d", item.Index)
		}
		if err := ValidateVector(item.Embedding, p.model.Dimensions); err != nil {
			return nil, fmt.Errorf("local embedding response item %d: %w", item.Index, err)
		}
		seen[item.Index] = true
		ordered[item.Index] = append([]float32(nil), item.Embedding...)
	}
	return ordered, nil
}

func localServerArgs(modelPath string, model v2config.EmbeddingModel, port int) []string {
	batchTokens := strconv.Itoa(localEmbeddingBatchTokenLimit)
	return []string{
		"-m", modelPath,
		"--embedding",
		"--pooling", model.Pooling,
		"--embd-normalize", "2",
		"--batch-size", batchTokens,
		"--ubatch-size", batchTokens,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--parallel", "1",
		"--sleep-idle-seconds", "60",
		"--log-disable",
	}
}

func (p *LocalGGUF) ensureServer(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil && p.endpoint != "" {
		return p.endpoint, nil
	}
	modelPath := ModelPath(p.root, p.model)
	if _, err := os.Stat(modelPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrModelNotInstalled, p.model.Name)
		}
		return "", fmt.Errorf("inspect local embedding model: %w", err)
	}
	binary, err := RuntimeBinaryPath(p.root, p.config)
	if err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate local embedding port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	cmd := processutil.ConfigureCommand(exec.Command(binary, localServerArgs(modelPath, p.model, port)...))
	cmd.Dir = filepath.Dir(binary)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start local embedding runtime: %w", err)
	}
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return "", ctx.Err()
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/health", nil)
		response, probeErr := p.client.Do(request)
		if probeErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 500 {
				p.cmd = cmd
				p.endpoint = endpoint
				go p.reap(cmd)
				return endpoint, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return "", errors.New("local embedding runtime did not become ready")
}

func (p *LocalGGUF) reap(cmd *exec.Cmd) {
	_ = cmd.Wait()
	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
		p.endpoint = ""
	}
	p.mu.Unlock()
}

func (p *LocalGGUF) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	cmd := p.cmd
	p.cmd = nil
	p.endpoint = ""
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
