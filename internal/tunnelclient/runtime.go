package tunnelclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	// Local /readyz only proves the daemon and MCP target are ready. A ChatGPT
	// tunnel is not actually usable until tunnel-client has completed at least
	// one successful control-plane poll, so require that stronger condition for
	// every managed tunnel before publishing it as ready.
	health, err := waitReady(readyCtx, urlFile, p, true)
	if err != nil {
		stopCtx, c := context.WithTimeout(context.Background(), s.ShutdownTimeout)
		_ = p.Stop(stopCtx, s.ShutdownTimeout)
		c()
		_ = os.Remove(urlFile)
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

const (
	controlPlanePollMetric      = "commands_poll_last_successful_timestamp_seconds"
	maxControlPlaneMetricLine   = 1 << 20
	initialMetricScannerBufSize = 64 << 10
)

func waitReady(ctx context.Context, urlFile string, p managedProcess, requireControlPlanePoll bool) (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	var health string
	var lastControlPlaneErr error
	for {
		select {
		case <-ctx.Done():
			if requireControlPlanePoll && lastControlPlaneErr != nil {
				return "", fmt.Errorf("tunnel-client control-plane readiness timeout: %v: %w", lastControlPlaneErr, ctx.Err())
			}
			return "", fmt.Errorf("tunnel-client readiness timeout: %w", ctx.Err())
		case <-p.Done():
			if err := p.Err(); err != nil {
				return "", fmt.Errorf("tunnel-client exited during startup: %w", err)
			}
			return "", errors.New("tunnel-client exited during startup")
		case <-tick.C:
			if health == "" {
				if b, err := os.ReadFile(urlFile); err == nil {
					health = strings.TrimSpace(string(b))
				}
			}
			if health == "" {
				continue
			}

			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(health, "/")+"/readyz", nil)
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				continue
			}
			if !requireControlPlanePoll {
				return health, nil
			}

			ready, err := controlPlanePollReady(ctx, client, health)
			if ready {
				return health, nil
			}
			lastControlPlaneErr = err
		}
	}
}

func controlPlanePollReady(ctx context.Context, client *http.Client, health string) (bool, error) {
	metricsURL := strings.TrimRight(health, "/") + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("metrics endpoint returned %s", resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, initialMetricScannerBufSize), maxControlPlaneMetricLine)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line != controlPlanePollMetric &&
			!strings.HasPrefix(line, controlPlanePollMetric+" ") &&
			!strings.HasPrefix(line, controlPlanePollMetric+"{") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		if value <= 0 {
			return false, errors.New("no successful control-plane poll observed")
		}
		return true, nil
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("scan metrics response: %w", err)
	}
	return false, fmt.Errorf("missing %s metric", controlPlanePollMetric)
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
