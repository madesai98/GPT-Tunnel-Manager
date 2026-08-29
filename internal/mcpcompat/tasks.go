package mcpcompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const TasksExtensionID = "io.modelcontextprotocol/tasks"

const (
	taskMethodGet    = "tasks/get"
	taskMethodUpdate = "tasks/update"
	taskMethodCancel = "tasks/cancel"
)

type TaskStatus string

const (
	TaskWorking       TaskStatus = "working"
	TaskInputRequired TaskStatus = "input_required"
	TaskCompleted     TaskStatus = "completed"
	TaskCancelled     TaskStatus = "cancelled"
	TaskFailed        TaskStatus = "failed"
)

type Task struct {
	TaskID         string     `json:"taskId"`
	Status         TaskStatus `json:"status"`
	StatusMessage  string     `json:"statusMessage,omitempty"`
	CreatedAt      string     `json:"createdAt"`
	LastUpdatedAt  string     `json:"lastUpdatedAt"`
	TTLMS          *int64     `json:"ttlMs,omitempty"`
	PollIntervalMS *int64     `json:"pollIntervalMs,omitempty"`
}

type CreateTaskResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
	Task
}

type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type GetTaskParams struct {
	mcp.ParamsBase
	TaskID string `json:"taskId"`
}

type GetTaskResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
	Task
	InputRequests mcp.InputRequestMap `json:"inputRequests,omitempty"`
	Result        json.RawMessage     `json:"result,omitempty"`
	Error         *JSONRPCError       `json:"error,omitempty"`
}

type UpdateTaskParams struct {
	mcp.ParamsBase
	TaskID         string               `json:"taskId"`
	InputResponses mcp.InputResponseMap `json:"inputResponses"`
}

type UpdateTaskResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
}

type CancelTaskParams struct {
	mcp.ParamsBase
	TaskID string `json:"taskId"`
}

type CancelTaskResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
}

// RegisterTaskMethods registers the task-management methods from the
// io.modelcontextprotocol/tasks extension on an MCP client. Task-augmented
// tools/call responses still require a task-aware result decoder because the
// upstream Go SDK v1.7.0 models tools/call with a fixed CallToolResult shape.
func RegisterTaskMethods(client *mcp.Client) error {
	if client == nil {
		return errors.New("nil MCP client")
	}
	if err := mcp.AddSendingCustomMethod[*GetTaskParams, *GetTaskResult](client, taskMethodGet); err != nil {
		return fmt.Errorf("register %s: %w", taskMethodGet, err)
	}
	if err := mcp.AddSendingCustomMethod[*UpdateTaskParams, *UpdateTaskResult](client, taskMethodUpdate); err != nil {
		return fmt.Errorf("register %s: %w", taskMethodUpdate, err)
	}
	if err := mcp.AddSendingCustomMethod[*CancelTaskParams, *CancelTaskResult](client, taskMethodCancel); err != nil {
		return fmt.Errorf("register %s: %w", taskMethodCancel, err)
	}
	return nil
}

func GetTask(ctx context.Context, session *mcp.ClientSession, taskID string) (*GetTaskResult, error) {
	return mcp.CallCustomMethod[*GetTaskParams, *GetTaskResult](ctx, session, taskMethodGet, &GetTaskParams{TaskID: taskID})
}

func UpdateTask(ctx context.Context, session *mcp.ClientSession, taskID string, responses mcp.InputResponseMap) (*UpdateTaskResult, error) {
	return mcp.CallCustomMethod[*UpdateTaskParams, *UpdateTaskResult](ctx, session, taskMethodUpdate, &UpdateTaskParams{
		TaskID:         taskID,
		InputResponses: responses,
	})
}

func CancelTask(ctx context.Context, session *mcp.ClientSession, taskID string) (*CancelTaskResult, error) {
	return mcp.CallCustomMethod[*CancelTaskParams, *CancelTaskResult](ctx, session, taskMethodCancel, &CancelTaskParams{TaskID: taskID})
}

// DecodeToolOrTaskResult performs the polymorphic result discrimination needed
// by the Tasks extension before the fixed-shape SDK tools/call decoder runs.
// Production routed transports use this at the task-aware wire boundary.
func DecodeToolOrTaskResult(raw json.RawMessage) (*mcp.CallToolResult, *CreateTaskResult, error) {
	var probe struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, fmt.Errorf("decode result discriminator: %w", err)
	}
	if probe.ResultType == "task" {
		var task CreateTaskResult
		if err := json.Unmarshal(raw, &task); err != nil {
			return nil, nil, fmt.Errorf("decode task result: %w", err)
		}
		if task.TaskID == "" {
			return nil, nil, errors.New("task result missing taskId")
		}
		if task.Status == "" {
			return nil, nil, errors.New("task result missing status")
		}
		return nil, &task, nil
	}

	var result mcp.CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, nil, fmt.Errorf("decode tool result: %w", err)
	}
	return &result, nil, nil
}
