package downstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpcompat"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const taskResultSentinel = "io.gpt-tunnel-manager/task-result"

var ErrTaskResult = errors.New("downstream returned an MCP task result")

type taskAwareTransport struct {
	delegate mcp.Transport
}

func wrapTaskAwareTransport(delegate mcp.Transport) mcp.Transport {
	if delegate == nil {
		return nil
	}
	return &taskAwareTransport{delegate: delegate}
}

func (t *taskAwareTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.delegate.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &taskAwareConnection{Connection: connection}, nil
}

// Preserve protocol-version filtering exposed by transports such as the SDK's
// streamable HTTP transport.
func (t *taskAwareTransport) SupportsProtocolVersion(version string) bool {
	if supporter, ok := t.delegate.(mcp.ProtocolVersionSupporter); ok {
		return supporter.SupportsProtocolVersion(version)
	}
	return true
}

type taskAwareConnection struct {
	mcp.Connection
}

func (c *taskAwareConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	message, err := c.Connection.Read(ctx)
	if err != nil {
		return nil, err
	}
	response, ok := message.(*jsonrpc.Response)
	if !ok || response.Error != nil || len(response.Result) == 0 {
		return message, nil
	}
	var probe struct {
		ResultType string `json:"resultType"`
	}
	if err := json.Unmarshal(response.Result, &probe); err != nil || probe.ResultType != "task" {
		return message, nil
	}
	if _, task, err := mcpcompat.DecodeToolOrTaskResult(response.Result); err != nil || task == nil {
		return message, nil
	}
	// The pinned Go SDK models tools/call as a fixed CallToolResult. Convert the
	// extension result into an internal-only valid CallToolResult envelope so the
	// SDK can finish its normal request/cancellation/MRTR machinery. Session then
	// unwraps the exact raw Task result before it crosses the Manager boundary.
	envelope := &mcp.CallToolResult{
		Content: []mcp.Content{},
		StructuredContent: map[string]any{
			taskResultSentinel: string(response.Result),
		},
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode internal task result envelope: %w", err)
	}
	copy := *response
	copy.Result = encoded
	return &copy, nil
}

func unwrapTaskResult(result *mcp.CallToolResult) (*mcpcompat.CreateTaskResult, bool, error) {
	if result == nil || result.StructuredContent == nil {
		return nil, false, nil
	}
	object, ok := result.StructuredContent.(map[string]any)
	if !ok {
		return nil, false, nil
	}
	value, ok := object[taskResultSentinel]
	if !ok {
		return nil, false, nil
	}
	raw, ok := value.(string)
	if !ok || raw == "" {
		return nil, true, errors.New("invalid internal task result envelope")
	}
	tool, task, err := mcpcompat.DecodeToolOrTaskResult(json.RawMessage(raw))
	if err != nil {
		return nil, true, err
	}
	if tool != nil || task == nil {
		return nil, true, errors.New("internal task result envelope did not contain a task")
	}
	return task, true, nil
}
