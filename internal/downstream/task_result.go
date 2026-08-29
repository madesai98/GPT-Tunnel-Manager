package downstream

import (
	"encoding/json"
	"errors"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpcompat"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TaskFromCallResult unwraps the internal task envelope produced at the
// downstream wire boundary. Ordinary CallToolResult values return ok=false.
func TaskFromCallResult(result *mcp.CallToolResult) (*mcpcompat.CreateTaskResult, bool, error) {
	return unwrapTaskResult(result)
}

// CallResultForTask creates the internal envelope used between the fixed-shape
// Go SDK tool handler and the Manager HTTP response adapter. It never crosses a
// downstream or upstream MCP wire unchanged.
func CallResultForTask(task *mcpcompat.CreateTaskResult) (*mcp.CallToolResult, error) {
	if task == nil || task.TaskID == "" || task.Status == "" {
		return nil, errors.New("task result requires taskId and status")
	}
	raw, err := json.Marshal(task)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{},
		StructuredContent: map[string]any{
			taskResultSentinel: string(raw),
		},
	}, nil
}
