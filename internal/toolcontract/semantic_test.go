package toolcontract

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSemanticFingerprintIgnoresWebMCPSourceDecoration(t *testing.T) {
	left := &mcp.Tool{
		Name:        "skill_tree_list_skills",
		Description: "[WebMCP tab-a • Skill Tree Maker] List skills in the current project.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	right := &mcp.Tool{
		Name:        left.Name,
		Description: "[WebMCP tab-b • Skill Tree Maker] List skills in the current project.",
		InputSchema: left.InputSchema,
	}

	leftExact, _, err := FingerprintTool(left)
	if err != nil {
		t.Fatal(err)
	}
	rightExact, _, err := FingerprintTool(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftExact == rightExact {
		t.Fatal("exact runtime fingerprints unexpectedly ignored WebMCP source decoration")
	}

	leftSemantic, _, err := SemanticFingerprintTool(left)
	if err != nil {
		t.Fatal(err)
	}
	rightSemantic, normalized, err := SemanticFingerprintTool(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftSemantic != rightSemantic {
		t.Fatalf("semantic fingerprints differ across source decoration: %s != %s", leftSemantic, rightSemantic)
	}
	if string(normalized) == "" || string(normalized) == string(mustCanonicalToolJSON(t, right)) {
		t.Fatal("semantic normalization did not change the decorated contract")
	}
}

func TestSemanticFingerprintStillDetectsRealToolChanges(t *testing.T) {
	base := &mcp.Tool{
		Name:        "skill_tree_list_skills",
		Description: "[WebMCP tab-a • Skill Tree Maker] List skills in the current project.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	changedDescription := &mcp.Tool{
		Name:        base.Name,
		Description: "[WebMCP tab-b • Skill Tree Maker] List skills and prerequisites in the current project.",
		InputSchema: base.InputSchema,
	}
	changedSchema := &mcp.Tool{
		Name:        base.Name,
		Description: "[WebMCP tab-c • Skill Tree Maker] List skills in the current project.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"include_hidden": map[string]any{"type": "boolean"}},
		},
	}

	baseFP, _, err := SemanticFingerprintTool(base)
	if err != nil {
		t.Fatal(err)
	}
	for name, tool := range map[string]*mcp.Tool{
		"description": changedDescription,
		"schema":      changedSchema,
	} {
		fingerprint, _, err := SemanticFingerprintTool(tool)
		if err != nil {
			t.Fatal(err)
		}
		if fingerprint == baseFP {
			t.Fatalf("real %s change was ignored by semantic fingerprint", name)
		}
	}
}

func mustCanonicalToolJSON(t *testing.T, tool *mcp.Tool) []byte {
	t.Helper()
	body, err := CanonicalToolJSON(tool)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
