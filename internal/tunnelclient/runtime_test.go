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

func TestJoinCommand(t *testing.T) {
	got := JoinCommand([]string{"python", "a b.py", "--x=1"})
	if got != "python \"a b.py\" --x=1" {
		t.Fatalf("%q", got)
	}
}

func TestMeaningfulActivity(t *testing.T) {
	if !meaningfulActivity(`{"component":"dispatcher","rpc_method":"tools/call"}`) {
		t.Fatal("tools/call should count")
	}
	if meaningfulActivity(`{"component":"dispatcher","rpc_method":"initialize"}`) {
		t.Fatal("initialize should not count")
	}
}

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

func TestWaitReadyReturnsWhenTunnelClientExits(t *testing.T) {
	process := &timeoutProcess{done: make(chan struct{}), err: errors.New("stdio MCP command exited")}
	close(process.done)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err := waitReady(ctx, filepath.Join(t.TempDir(), "missing-health.url"), process, true)
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

	health, err := waitReady(ctx, urlFile, process, true)
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

	_, err := waitReady(ctx, urlFile, process, true)
	if err == nil || !strings.Contains(err.Error(), "no successful control-plane poll observed") {
		t.Fatalf("err=%v", err)
	}
}
