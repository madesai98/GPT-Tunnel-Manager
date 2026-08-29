package downstream

import (
	"context"
	"errors"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpcompat"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Session) CallToolOrTask(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, *mcpcompat.CreateTaskResult, error) {
	result, err := s.CallTool(ctx, params)
	if err != nil {
		return nil, nil, err
	}
	task, wrapped, err := unwrapTaskResult(result)
	if err != nil {
		return nil, nil, fmt.Errorf("decode downstream task result: %w", err)
	}
	if wrapped {
		return nil, task, nil
	}
	return result, nil, nil
}

func (s *Session) GetTask(ctx context.Context, taskID string) (*mcpcompat.GetTaskResult, error) {
	if s == nil || s.sdk == nil {
		return nil, fmt.Errorf("%w: downstream session unavailable", ErrDownstreamUnavailable)
	}
	if taskID == "" {
		return nil, errors.New("downstream task id is required")
	}
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	return mcpcompat.GetTask(ctx, s.sdk, taskID)
}

func (s *Session) UpdateTask(ctx context.Context, taskID string, responses mcp.InputResponseMap) (*mcpcompat.UpdateTaskResult, error) {
	if s == nil || s.sdk == nil {
		return nil, fmt.Errorf("%w: downstream session unavailable", ErrDownstreamUnavailable)
	}
	if taskID == "" {
		return nil, errors.New("downstream task id is required")
	}
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	return mcpcompat.UpdateTask(ctx, s.sdk, taskID, responses)
}

func (s *Session) CancelTask(ctx context.Context, taskID string) (*mcpcompat.CancelTaskResult, error) {
	if s == nil || s.sdk == nil {
		return nil, fmt.Errorf("%w: downstream session unavailable", ErrDownstreamUnavailable)
	}
	if taskID == "" {
		return nil, errors.New("downstream task id is required")
	}
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	return mcpcompat.CancelTask(ctx, s.sdk, taskID)
}

func (s *Session) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	if s == nil || s.sdk == nil {
		return nil, fmt.Errorf("%w: downstream session unavailable", ErrDownstreamUnavailable)
	}
	if uri == "" {
		return nil, errors.New("downstream resource URI is required")
	}
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	return s.sdk.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
}
