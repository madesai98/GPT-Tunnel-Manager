package downstream

import (
	"github.com/madesai98/GPT-Tunnel-Manager/internal/mcpcompat"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerTaskMethods(client *mcp.Client) error {
	return mcpcompat.RegisterTaskMethods(client)
}
