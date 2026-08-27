package tunnelclient

import (
	"context"
	"errors"
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

func TestWaitReadyReturnsWhenTunnelClientExits(t *testing.T) {
	process := &timeoutProcess{done: make(chan struct{}), err: errors.New("stdio MCP command exited")}
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
