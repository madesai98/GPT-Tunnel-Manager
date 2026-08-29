package downstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ToolFingerprintAlgorithm = "sha256"

type ToolSnapshot struct {
	Tools       []*mcp.Tool
	Fingerprint string
}

func (s ToolSnapshot) Clone() ToolSnapshot {
	body, err := json.Marshal(s.Tools)
	if err != nil {
		return ToolSnapshot{Fingerprint: s.Fingerprint}
	}
	var tools []*mcp.Tool
	if err := json.Unmarshal(body, &tools); err != nil {
		return ToolSnapshot{Fingerprint: s.Fingerprint}
	}
	return ToolSnapshot{Tools: tools, Fingerprint: s.Fingerprint}
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
		if tool == nil || tool.Name == "" {
			return ToolSnapshot{}, errors.New("downstream tools/list returned a tool without a name")
		}
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	for i := 1; i < len(tools); i++ {
		if tools[i-1].Name == tools[i].Name {
			return ToolSnapshot{}, fmt.Errorf("downstream tools/list returned duplicate tool name %q", tools[i].Name)
		}
	}

	body, err := json.Marshal(tools)
	if err != nil {
		return ToolSnapshot{}, fmt.Errorf("marshal tools/list contract: %w", err)
	}
	digest := sha256.Sum256(body)

	// Round-trip once so the stored authoritative snapshot cannot alias SDK
	// objects later mutated by caller code or a notification handler.
	var snapshotTools []*mcp.Tool
	if err := json.Unmarshal(body, &snapshotTools); err != nil {
		return ToolSnapshot{}, fmt.Errorf("clone tools/list contract: %w", err)
	}
	return ToolSnapshot{
		Tools:       snapshotTools,
		Fingerprint: ToolFingerprintAlgorithm + ":" + hex.EncodeToString(digest[:]),
	}, nil
}
