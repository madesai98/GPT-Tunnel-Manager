package routedlifecycle

import (
	"context"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpcompat"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type taskAwareRuntime interface {
	CallToolOrTask(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, *mcpcompat.CreateTaskResult, error)
	GetTask(context.Context, string) (*mcpcompat.GetTaskResult, error)
	UpdateTask(context.Context, string, mcp.InputResponseMap) (*mcpcompat.UpdateTaskResult, error)
	CancelTask(context.Context, string) (*mcpcompat.CancelTaskResult, error)
}

type resourceRuntime interface {
	ReadResource(context.Context, string) (*mcp.ReadResourceResult, error)
}

func (l *UseLease) CallToolOrTask(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, *mcpcompat.CreateTaskResult, error) {
	if l == nil || l.service == nil || l.session == nil {
		return nil, nil, fmt.Errorf("%w: missing routed lifecycle session", downstream.ErrDownstreamUnavailable)
	}
	if l.released.Load() {
		return nil, nil, fmt.Errorf("%w: routed lifecycle lease already released", downstream.ErrDownstreamUnavailable)
	}
	l.touch()
	callCtx, cancel := l.service.operationContext(ctx)
	defer cancel()
	if runtime, ok := l.session.(taskAwareRuntime); ok {
		result, task, err := runtime.CallToolOrTask(callCtx, params)
		l.touch()
		return result, task, err
	}
	result, err := l.session.CallTool(callCtx, params)
	l.touch()
	return result, nil, err
}

func (l *UseLease) GetTask(ctx context.Context, taskID string) (*mcpcompat.GetTaskResult, error) {
	runtime, callCtx, cancel, err := l.taskRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := runtime.GetTask(callCtx, taskID)
	l.touch()
	return result, err
}

func (l *UseLease) UpdateTask(ctx context.Context, taskID string, responses mcp.InputResponseMap) (*mcpcompat.UpdateTaskResult, error) {
	runtime, callCtx, cancel, err := l.taskRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := runtime.UpdateTask(callCtx, taskID, responses)
	l.touch()
	return result, err
}

func (l *UseLease) CancelTask(ctx context.Context, taskID string) (*mcpcompat.CancelTaskResult, error) {
	runtime, callCtx, cancel, err := l.taskRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := runtime.CancelTask(callCtx, taskID)
	l.touch()
	return result, err
}

func (l *UseLease) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	if l == nil || l.service == nil || l.session == nil {
		return nil, fmt.Errorf("%w: missing routed lifecycle session", downstream.ErrDownstreamUnavailable)
	}
	if l.released.Load() {
		return nil, fmt.Errorf("%w: routed lifecycle lease already released", downstream.ErrDownstreamUnavailable)
	}
	runtime, ok := l.session.(resourceRuntime)
	if !ok {
		return nil, errorsUnsupportedContinuation("resources/read")
	}
	l.touch()
	callCtx, cancel := l.service.operationContext(ctx)
	defer cancel()
	result, err := runtime.ReadResource(callCtx, uri)
	l.touch()
	return result, err
}

func (l *UseLease) taskRuntime(ctx context.Context) (taskAwareRuntime, context.Context, context.CancelFunc, error) {
	if l == nil || l.service == nil || l.session == nil {
		return nil, nil, nil, fmt.Errorf("%w: missing routed lifecycle session", downstream.ErrDownstreamUnavailable)
	}
	if l.released.Load() {
		return nil, nil, nil, fmt.Errorf("%w: routed lifecycle lease already released", downstream.ErrDownstreamUnavailable)
	}
	runtime, ok := l.session.(taskAwareRuntime)
	if !ok {
		return nil, nil, nil, errorsUnsupportedContinuation("tasks")
	}
	l.touch()
	callCtx, cancel := l.service.operationContext(ctx)
	return runtime, callCtx, cancel, nil
}

func errorsUnsupportedContinuation(feature string) error {
	return fmt.Errorf("%w: downstream runtime does not support %s continuation", downstream.ErrDownstreamUnavailable, feature)
}
