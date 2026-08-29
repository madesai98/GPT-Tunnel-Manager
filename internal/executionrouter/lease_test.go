package executionrouter

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type releasingSession struct {
	*fakeSession
	releases int
}

func (s *releasingSession) Release() { s.releases++ }

type classifiedProviderError struct {
	code      string
	retryable bool
}

func (e classifiedProviderError) Error() string            { return e.code }
func (e classifiedProviderError) ExecutionCode() string    { return e.code }
func (e classifiedProviderError) ExecutionRetryable() bool { return e.retryable }

func TestExecuteReleasesLifecycleLeaseOnEveryPostAcquisitionPath(t *testing.T) {
	tool := toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema())
	tests := []struct {
		name      string
		configure func(*testing.T, *fixture, *releasingSession)
		maxResult int
		wantCode  string
		wantCalls int
	}{
		{
			name: "success",
			configure: func(_ *testing.T, f *fixture, session *releasingSession) {
				session.snapshot = f.session.snapshot
				session.result = &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}
			},
			wantCalls: 1,
		},
		{
			name: "live contract mismatch before call",
			configure: func(t *testing.T, _ *fixture, session *releasingSession) {
				drifted := *tool
				drifted.Description = "drifted"
				session.snapshot = snapshotFor(t, &drifted)
			},
			wantCode:  CodeIndexRequired,
			wantCalls: 0,
		},
		{
			name: "call reports contract drift",
			configure: func(_ *testing.T, f *fixture, session *releasingSession) {
				session.snapshot = f.session.snapshot
				session.err = downstream.ErrToolContractChanged
			},
			wantCode:  CodeIndexRequired,
			wantCalls: 1,
		},
		{
			name: "ambiguous call error",
			configure: func(_ *testing.T, f *fixture, session *releasingSession) {
				session.snapshot = f.session.snapshot
				session.err = errors.New("connection lost after write")
			},
			wantCode:  CodeDownstreamCallFailed,
			wantCalls: 1,
		},
		{
			name: "completed result too large",
			configure: func(_ *testing.T, f *fixture, session *releasingSession) {
				session.snapshot = f.session.snapshot
				session.result = &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "this result is deliberately too large"}}}
			},
			maxResult: 8,
			wantCode:  CodeResultTooLarge,
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tool, nil, tt.maxResult)
			leased := &releasingSession{fakeSession: &fakeSession{}}
			tt.configure(t, f, leased)
			service, err := NewService(f.catalog, f.tracker, f.handles, SessionProviderFunc(func(context.Context, string) (Session, error) {
				return leased, nil
			}), Options{MaxResultBytes: tt.maxResult})
			if err != nil {
				t.Fatal(err)
			}
			_, failure := service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
			if tt.wantCode == "" {
				if failure != nil {
					t.Fatalf("Execute failure = %#v", failure)
				}
			} else if failure == nil || failure.Code != tt.wantCode {
				t.Fatalf("Execute failure = %#v, want code %q", failure, tt.wantCode)
			}
			if leased.releases != 1 {
				t.Fatalf("lease releases = %d, want exactly 1", leased.releases)
			}
			if leased.calls != tt.wantCalls {
				t.Fatalf("downstream calls = %d, want %d", leased.calls, tt.wantCalls)
			}
		})
	}
}

func TestExecutePreservesLifecycleProviderBlockerClassification(t *testing.T) {
	f := newFixture(t, toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema()), nil, 0)
	for _, tt := range []struct {
		code      string
		retryable bool
	}{
		{CodeManualServerStopped, false},
		{CodeServerDisabled, false},
		{CodeServerBusy, true},
		{CodeManagerShuttingDown, true},
	} {
		service, err := NewService(f.catalog, f.tracker, f.handles, SessionProviderFunc(func(context.Context, string) (Session, error) {
			return nil, classifiedProviderError{code: tt.code, retryable: tt.retryable}
		}), Options{})
		if err != nil {
			t.Fatal(err)
		}
		_, failure := service.Execute(context.Background(), f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
		if failure == nil || failure.Code != tt.code || failure.Outcome != OutcomeNotStarted || failure.Retryable != tt.retryable {
			t.Fatalf("provider blocker %q mapped to %#v", tt.code, failure)
		}
	}
}

type cancellationLeasedSession struct {
	snapshot downstream.ToolSnapshot
	releases int
	calls    int
	started  chan struct{}
	once     sync.Once
}

func (s *cancellationLeasedSession) InitialTools() downstream.ToolSnapshot { return s.snapshot.Clone() }
func (s *cancellationLeasedSession) Release()                             { s.releases++ }
func (s *cancellationLeasedSession) CallTool(ctx context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	s.calls++
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestExecuteCancellationStillReleasesLifecycleLease(t *testing.T) {
	f := newFixture(t, toolForClass(t, toolcontract.ExecutorReadOnlyClosed, basicSchema()), nil, 0)
	leased := &cancellationLeasedSession{snapshot: f.session.snapshot, started: make(chan struct{})}
	service, err := NewService(f.catalog, f.tracker, f.handles, SessionProviderFunc(func(context.Context, string) (Session, error) {
		return leased, nil
	}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failureCh := make(chan *ExecutionError, 1)
	go func() {
		_, failure := service.Execute(ctx, f.class, Input{ExecutionHandle: f.handle, Arguments: map[string]any{"text": "x"}})
		failureCh <- failure
	}()
	select {
	case <-leased.started:
	case <-time.After(time.Second):
		t.Fatal("downstream CallTool was not reached before cancellation")
	}
	cancel()
	var failure *ExecutionError
	select {
	case failure = <-failureCh:
	case <-time.After(time.Second):
		t.Fatal("cancelled Execute did not return")
	}
	if failure == nil || failure.Code != CodeDownstreamCallFailed || failure.Outcome != OutcomeUnknown {
		t.Fatalf("cancelled post-dispatch failure = %#v", failure)
	}
	if leased.calls != 1 || leased.releases != 1 {
		t.Fatalf("cancelled call calls=%d releases=%d, want 1/1", leased.calls, leased.releases)
	}
}
