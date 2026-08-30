package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const LocalGGUFProtocol = "llama.cpp-v1-embeddings/local"

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
	args := []string{"-m", modelPath, "--embedding", "--pooling", p.model.Pooling, "--embd-normalize", "2", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--parallel", "1", "--sleep-idle-seconds", "60", "--log-disable"}
	cmd := processutil.ConfigureCommand(exec.Command(binary, args...))
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
