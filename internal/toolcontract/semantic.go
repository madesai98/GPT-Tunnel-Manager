package toolcontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SemanticFingerprintTool returns a fingerprint for the stable semantic tool
// contract used by indexing and enrichment reuse. It deliberately normalizes
// presentation-only decoration injected by transport bridges while preserving
// the actual MCP tool contract for runtime drift detection.
func SemanticFingerprintTool(tool *mcp.Tool) (string, []byte, error) {
	body, err := CanonicalToolJSON(tool)
	if err != nil {
		return "", nil, err
	}
	return SemanticFingerprintJSON(body)
}

// SemanticFingerprintJSON normalizes a serialized MCP tool contract and
// returns its stable semantic fingerprint plus normalized JSON.
func SemanticFingerprintJSON(body []byte) (string, []byte, error) {
	if len(body) == 0 {
		return "", nil, errors.New("tool contract JSON is required")
	}
	var contract map[string]any
	if err := json.Unmarshal(body, &contract); err != nil {
		return "", nil, fmt.Errorf("decode tool contract for semantic fingerprint: %w", err)
	}
	name, _ := contract["name"].(string)
	if strings.TrimSpace(name) == "" {
		return "", nil, errors.New("tool name is required")
	}
	if description, ok := contract["description"].(string); ok {
		contract["description"] = stripWebMCPSourceDecoration(description)
	}
	normalized, err := json.Marshal(contract)
	if err != nil {
		return "", nil, fmt.Errorf("marshal semantic tool contract %q: %w", name, err)
	}
	return FingerprintJSON(normalized), normalized, nil
}

// @mcp-b/webmcp-local-relay prefixes relayed descriptions with a browser
// source label such as "[WebMCP tab-123 • Skill Tree Maker] ". The tab/source
// identity changes across reconnects but does not change what the tool does.
func stripWebMCPSourceDecoration(description string) string {
	if !strings.HasPrefix(description, "[WebMCP ") {
		return description
	}
	closing := strings.IndexByte(description, ']')
	if closing < len("[WebMCP ") || closing+1 >= len(description) || description[closing+1] != ' ' {
		return description
	}
	return strings.TrimLeft(description[closing+1:], " ")
}
