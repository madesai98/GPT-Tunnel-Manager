package tunnelclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	proc "github.com/madesai98/GPT-Tunnel-Manager/internal/process"
)

type RunSpec struct {
	Binary              string
	TunnelID            string
	APIKey              string
	MCPURL              string
	MCPCommand          []string
	Dir                 string
	Env                 map[string]string
	HealthDir           string
	StartupTimeout      time.Duration
	ShutdownTimeout     time.Duration
	TelemetryCompatible bool
	OnLog               func(stream, line string)
}

type managedProcess interface {
	Done() <-chan struct{}
	Err() error
	Stop(context.Context, time.Duration) error
}

type Runtime struct {
	p               managedProcess
	healthURL       string
	activity        chan time.Time
	tracking        bool
	shutdownTimeout time.Duration
	done            chan struct{}
	mu              sync.RWMutex
	err             error
}

func (r *Runtime) Done() <-chan struct{}      { return r.done }
func (r *Runtime) Err() error                 { r.mu.RLock(); defer r.mu.RUnlock(); return r.err }
func (r *Runtime) Activity() <-chan time.Time { return r.activity }
func (r *Runtime) ActivityTracking() bool     { return r.tracking }
func (r *Runtime) HealthURL() string          { return r.healthURL }
func (r *Runtime) Stop(ctx context.Context) error {
	return r.p.Stop(ctx, r.shutdownTimeout)
}

var simpleArg = regexp.MustCompile(`^[A-Za-z0-9_./:\\=-]+$`)

func JoinCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if simpleArg.MatchString(a) && a != "" {
			parts[i] = a
			continue
		}
		a = strings.ReplaceAll(a, "\\", "\\\\")
		a = strings.ReplaceAll(a, "\"", "\\\"")
		parts[i] = "\"" + a + "\""
	}
	return strings.Join(parts, " ")
}

func Start(ctx context.Context, s RunSpec) (*Runtime, error) {
	if s.Binary == "" || s.TunnelID == "" || s.APIKey == "" {
		return nil, errors.New("tunnel-client binary, tunnel id, and runtime API key are required")
	}
	if (s.MCPURL == "") == (len(s.MCPCommand) == 0) {
		return nil, errors.New("exactly one MCP target is required")
	}
	if s.StartupTimeout <= 0 {
		s.StartupTimeout = 30 * time.Second
	}
	if s.ShutdownTimeout <= 0 {
		s.ShutdownTimeout = 10 * time.Second
	}
	if s.HealthDir == "" {
		s.HealthDir = os.TempDir()
	}
	if err := os.MkdirAll(s.HealthDir, 0o700); err != nil {
		return nil, err
	}
	urlFile := filepath.Join(s.HealthDir, fmt.Sprintf("health-%d.url", time.Now().UnixNano()))
	args := []string{
		"run",
		"--control-plane.tunnel-id", s.TunnelID,
		"--health.listen-addr", "127.0.0.1:0",
		"--health.url-file", urlFile,
		"--log.format", "json",
		"--log.level", "debug",
	}
	if s.MCPURL != "" {
		args = append(args, "--mcp.server-url", s.MCPURL)
	} else {
		args = append(args, "--mcp.command", JoinCommand(s.MCPCommand))
	}
	env := []string{"CONTROL_PLANE_API_KEY=" + s.APIKey}
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}
	r := &Runtime{
		activity:        make(chan time.Time, 64),
		tracking:        s.TelemetryCompatible,
		shutdownTimeout: s.ShutdownTimeout,
		done:            make(chan struct{}),
	}
	p, err := proc.Start(proc.Spec{Executable: s.Binary, Args: args, Dir: s.Dir, Env: env}, func(line proc.Line) {
		if s.OnLog != nil {
			s.OnLog(line.Stream, line.Text)
		}
		if s.TelemetryCompatible && meaningfulActivity(line.Text) {
			select {
			case r.activity <- time.Now().UTC():
			default:
			}
		}
	})
	if err != nil {
		return nil, err
	}
	r.p = p
	readyCtx, cancel := context.WithTimeout(ctx, s.StartupTimeout)
	defer cancel()
	health, err := waitReady(readyCtx, urlFile)
	if err != nil {
		stopCtx, c := context.WithTimeout(context.Background(), s.ShutdownTimeout)
		_ = p.Stop(stopCtx, s.ShutdownTimeout)
		c()
		return nil, err
	}
	r.healthURL = health
	go func() {
		<-p.Done()
		r.mu.Lock()
		r.err = p.Err()
		r.mu.Unlock()
		close(r.done)
		close(r.activity)
		_ = os.Remove(urlFile)
	}()
	return r, nil
}

func waitReady(ctx context.Context, urlFile string) (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	var health string
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("tunnel-client readiness timeout: %w", ctx.Err())
		case <-tick.C:
			if health == "" {
				if b, err := os.ReadFile(urlFile); err == nil {
					health = strings.TrimSpace(string(b))
				}
			}
			if health != "" {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(health, "/")+"/readyz", nil)
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						return health, nil
					}
				}
			}
		}
	}
}

var ignored = map[string]bool{
	"initialize":                true,
	"notifications/initialized": true,
	"ping":                      true,
	"notifications/cancelled":   true,
}

func meaningfulActivity(line string) bool {
	var m map[string]any
	if json.Unmarshal([]byte(line), &m) != nil {
		return false
	}
	component, _ := m["component"].(string)
	if component != "dispatcher" {
		return false
	}
	method, _ := m["rpc_method"].(string)
	if method == "" {
		method, _ = m["request_method"].(string)
	}
	if method == "" || ignored[method] || strings.HasPrefix(method, "notifications/") {
		return false
	}
	return true
}

func TelemetryCompatible(version string) bool {
	return version == "v0.0.13" || strings.HasPrefix(version, "v0.0.13+")
}
