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
	// The first pass clones the SDK's wire representation into the exact Tool
	// shape that the Manager persists in the authoritative catalog. Fingerprint
	// that persisted shape on a second pass so a later catalog decode/re-encode
	// produces the same aggregate server fingerprint. Without this stabilization,
	// interface-backed nested schemas can serialize differently before and after
	// one JSON round-trip and be mistaken for live contract drift.
	_, canonical, err := toolcontract.FingerprintTools(tools)
	if err != nil {
		return ToolSnapshot{}, err
	}
	fingerprint, stable, err := toolcontract.FingerprintTools(canonical)
	if err != nil {
		return ToolSnapshot{}, err
	}
	return ToolSnapshot{Tools: stable, Fingerprint: fingerprint}, nil
}
