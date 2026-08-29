package downstream

import (
	"context"
	"errors"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ToolFingerprintAlgorithm = toolcontract.FingerprintAlgorithm

type ToolSnapshot struct {
	Tools       []*mcp.Tool
	Fingerprint string
}

func (s ToolSnapshot) Clone() ToolSnapshot {
	fingerprint, tools, err := toolcontract.FingerprintTools(s.Tools)
	if err != nil {
		return ToolSnapshot{Fingerprint: s.Fingerprint}
	}
	if s.Fingerprint != "" {
		fingerprint = s.Fingerprint
	}
	return ToolSnapshot{Tools: tools, Fingerprint: fingerprint}
}

func SnapshotTools(ctx context.Context, session *mcp.ClientSession) (ToolSnapshot, error) {
	if session == nil {
		return ToolSnapshot{}, errors.New("downstream MCP session is required")
	}
	tools := make([]*mcp.Tool, 0)
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return ToolSnapshot{}, err
		}
		tools = append(tools, tool)
	}
	fingerprint, canonical, err := toolcontract.FingerprintTools(tools)
	if err != nil {
		return ToolSnapshot{}, err
	}
	return ToolSnapshot{Tools: canonical, Fingerprint: fingerprint}, nil
}
