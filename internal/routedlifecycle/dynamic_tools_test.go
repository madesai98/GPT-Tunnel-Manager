package routedlifecycle

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type dynamicToolRuntime struct {
	mu      sync.Mutex
	initial downstream.ToolSnapshot
	current downstream.ToolSnapshot
	changed bool
	done    chan struct{}
	closed  bool
}

func newDynamicToolRuntime(snapshot downstream.ToolSnapshot) *dynamicToolRuntime {
	cloned := snapshot.Clone()
	return &dynamicToolRuntime{initial: cloned, current: cloned, done: make(chan struct{})}
}

func (r *dynamicToolRuntime) InitialTools() downstream.ToolSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initial.Clone()
}
func (r *dynamicToolRuntime) CurrentTools() downstream.ToolSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Clone()
}
func (r *dynamicToolRuntime) ToolContractChanged() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}
func (r *dynamicToolRuntime) setCurrent(snapshot downstream.ToolSnapshot, changed bool) {
	r.mu.Lock()
	r.current = snapshot.Clone()
	r.changed = changed
	r.mu.Unlock()
}
func (r *dynamicToolRuntime) CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{}, nil
}
func (r *dynamicToolRuntime) Close(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.done)
	}
	return nil
}
func (r *dynamicToolRuntime) Done() <-chan struct{} { return r.done }

func TestDynamicToolsRefreshCardAndReconnectBeforeAcquire(t *testing.T) {
	entry := testEntry(serverA, v2config.ModeManaged)
	manager := v2config.DefaultManagerConfig(43150)
	manager.ManagedDefaults.IdleTimeoutSeconds = 30
	var starts atomic.Int32
	var first *dynamicToolRuntime
	dynamic := downstream.ToolSnapshot{Tools: []*mcp.Tool{{Name: "dynamic_tool"}}}
	connect := func(context.Context, v2config.ServerEntry) (RuntimeSession, error) {
		n := starts.Add(1)
		snapshot := downstream.ToolSnapshot{}
		if n > 1 {
			snapshot = dynamic
		}
		runtime := newDynamicToolRuntime(snapshot)
		if n == 1 {
			first = runtime
		}
		return runtime, nil
	}
	service, err := New(context.Background(), manager, testConfig(entry), connect, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = service.Close(ctx)
	})

	if _, err := service.Start(context.Background(), serverA); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 || first == nil {
		t.Fatalf("initial starts = %d, first=%v", starts.Load(), first)
	}
	first.setCurrent(dynamic, true)

	known, err := service.KnownTools(serverA)
	if err != nil {
		t.Fatal(err)
	}
	if len(known.Tools) != 1 || known.Tools[0].Name != "dynamic_tool" {
		t.Fatalf("KnownTools = %#v, want dynamic tool", known.Tools)
	}
	snapshot, err := service.Snapshot(serverA)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ToolCount != 1 {
		t.Fatalf("ToolCount = %d, want 1", snapshot.ToolCount)
	}

	lease, err := service.Acquire(context.Background(), serverA)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if starts.Load() != 2 {
		t.Fatalf("starts after drift acquire = %d, want 2", starts.Load())
	}
	if tools := lease.InitialTools(); len(tools.Tools) != 1 || tools.Tools[0].Name != "dynamic_tool" {
		t.Fatalf("reconnected InitialTools = %#v, want dynamic tool", tools.Tools)
	}
	select {
	case <-first.Done():
	default:
		t.Fatal("stale changed session was not closed before reconnect")
	}
}
