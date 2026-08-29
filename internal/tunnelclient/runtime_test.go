package tunnelclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type timeoutProcess struct {
	grace time.Duration
	done  chan struct{}
	err   error
}

func (p *timeoutProcess) Done() <-chan struct{} { return p.done }
func (p *timeoutProcess) Err() error            { return p.err }
func (p *timeoutProcess) Stop(_ context.Context, grace time.Duration) error {
	p.grace = grace
	return nil
}

func TestRuntimeStopHonorsConfiguredShutdownTimeout(t *testing.T) {
	process := &timeoutProcess{done: make(chan struct{})}
	runtime := &Runtime{p: process, shutdownTimeout: 37 * time.Second}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if process.grace != 37*time.Second {
		t.Fatalf("grace=%s, want 37s", process.grace)
	}
}

func TestReadinessTimeoutIncludesFirstLongPollGrace(t *testing.T) {
	got := readinessTimeout(30 * time.Second)
	want := 40 * time.Second
	if got != want {
		t.Fatalf("readiness timeout=%s, want %s", got, want)
	}
}

func TestRunArgsKeepManagerCapabilityOutOfArgv(t *testing.T) {
	const authorization = "Bearer local-manager-capability"
	args, env, err := runArgsEnv(RunSpec{
		TunnelID:        "tunnel_0123456789abcdef0123456789abcdef",
		APIKey:          "runtime-key",
		MCPURL:          "http://127.0.0.1:12345/mcp",
		MCPAuthorization: authorization,
	}, "/tmp/health.url")
	if err != nil {
		t.Fatal(err)
	}
	joinedArgs := strings.Join(args, "\x00")
	if strings.Contains(joinedArgs, authorization) || strings.Contains(joinedArgs, "local-manager-capability") {
		t.Fatalf("Manager capability leaked into argv: %q", joinedArgs)
	}
	if !strings.Contains(joinedArgs, "Authorization: env:"+managerMCPAuthorizationEnv) {
		t.Fatalf("argv does not contain environment-backed Authorization header: %q", joinedArgs)
	}
	joinedEnv := strings.Join(env, "\x00")
	if !strings.Contains(joinedEnv, managerMCPAuthorizationEnv+"="+authorization) {
		t.Fatalf("Manager authorization missing from child environment: %q", joinedEnv)
	}
}

func TestRunArgsRejectManagerAuthorizationLineBreaks(t *testing.T) {
	_, _, err := runArgsEnv(RunSpec{MCPAuthorization: "Bearer value\r\nX-Evil: injected"}, "health.url")
	if err == nil {
		t.Fatal("expected Manager authorization with line breaks to be rejected")
	}
}

func TestWaitReadyReturnsWhenTunnelClientExits(t *testing.T) {
	process := &timeoutProcess{done: make(chan struct{}), err: errors.New("Manager MCP target exited")}
	close(process.done)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err := waitReady(ctx, filepath.Join(t.TempDir(), "missing-health.url"), process)
	if err == nil || !strings.Contains(err.Error(), "tunnel-client exited during startup") {
		t.Fatalf("err=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("startup failure took %s; expected immediate process-exit detection", elapsed)
	}
}

func TestWaitReadyRequiresSuccessfulControlPlanePoll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
		case "/metrics":
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "%s %d\n", controlPlanePollMetric, time.Now().Unix())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	urlFile := filepath.Join(t.TempDir(), "health.url")
	if err := os.WriteFile(urlFile, []byte(server.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	process := &timeoutProcess{done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	health, err := waitReady(ctx, urlFile, process)
	if err != nil {
		t.Fatal(err)
	}
	if health != server.URL {
		t.Fatalf("health=%q, want %q", health, server.URL)
	}
}

func TestWaitReadyRejectsLocalReadyWithoutControlPlanePoll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
		case "/metrics":
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "%s 0\n", controlPlanePollMetric)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	urlFile := filepath.Join(t.TempDir(), "health.url")
	if err := os.WriteFile(urlFile, []byte(server.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	process := &timeoutProcess{done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer cancel()

	_, err := waitReady(ctx, urlFile, process)
	if err == nil || !strings.Contains(err.Error(), "no successful control-plane poll observed") {
		t.Fatalf("err=%v", err)
	}
}
