package mcpmanager

import (
	"context"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionrouter"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type continuationSessionProvider struct {
	lifecycle    *routedlifecycle.Service
	continuation *continuationService
}

func newContinuationSessionProvider(lifecycle *routedlifecycle.Service, continuation *continuationService) executionrouter.SessionProvider {
	return &continuationSessionProvider{lifecycle: lifecycle, continuation: continuation}
}

func (p *continuationSessionProvider) Session(ctx context.Context, serverID string) (executionrouter.Session, error) {
	lease, err := p.lifecycle.Acquire(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return &continuationExecutionSession{UseLease: lease, serverID: serverID, continuation: p.continuation}, nil
}

type continuationExecutionSession struct {
	*routedlifecycle.UseLease
	serverID     string
	continuation *continuationService
}

func (s *continuationExecutionSession) InitialTools() downstream.ToolSnapshot {
	return s.UseLease.InitialTools()
}

func (s *continuationExecutionSession) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	result, err := s.UseLease.CallTool(ctx, params)
	if err != nil || s.continuation == nil {
		return result, err
	}
	// This runs before executionrouter's deferred Release. If the downstream
	// returned a Task extension result, ProcessCallResult acquires/persists the
	// task-held Phase 9 lease before this short routed-call lease can disappear.
	return s.continuation.ProcessCallResult(ctx, s.serverID, result)
}
