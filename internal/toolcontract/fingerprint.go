package toolcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const FingerprintAlgorithm = "sha256"

func FingerprintTool(tool *mcp.Tool) (string, []byte, error) {
	body, err := CanonicalToolJSON(tool)
	if err != nil {
		return "", nil, err
	}
	return FingerprintJSON(body), body, nil
}

func CanonicalToolJSON(tool *mcp.Tool) ([]byte, error) {
	if tool == nil {
		return nil, errors.New("tool is required")
	}
	if tool.Name == "" {
		return nil, errors.New("tool name is required")
	}
	body, err := json.Marshal(tool)
	if err != nil {
		return nil, fmt.Errorf("marshal tool contract %q: %w", tool.Name, err)
	}
	return body, nil
}

func FingerprintJSON(body []byte) string {
	digest := sha256.Sum256(body)
	return FingerprintAlgorithm + ":" + hex.EncodeToString(digest[:])
}

func FingerprintTools(tools []*mcp.Tool) (string, []*mcp.Tool, error) {
	sorted := append([]*mcp.Tool(nil), tools...)
	for _, tool := range sorted {
		if tool == nil || tool.Name == "" {
			return "", nil, errors.New("downstream tools/list returned a tool without a name")
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for i, tool := range sorted {
		if i > 0 && sorted[i-1].Name == tool.Name {
			return "", nil, fmt.Errorf("downstream tools/list returned duplicate tool name %q", tool.Name)
		}
	}

	body, err := json.Marshal(sorted)
	if err != nil {
		return "", nil, fmt.Errorf("marshal tools/list contract: %w", err)
	}
	var cloned []*mcp.Tool
	if err := json.Unmarshal(body, &cloned); err != nil {
		return "", nil, fmt.Errorf("clone tools/list contract: %w", err)
	}
	return FingerprintJSON(body), cloned, nil
}
