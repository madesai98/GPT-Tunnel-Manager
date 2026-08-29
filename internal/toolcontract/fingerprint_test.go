package toolcontract

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFingerprintToolsIsOrderIndependentAndCloned(t *testing.T) {
	a := &mcp.Tool{Name: "a", Description: "first"}
	b := &mcp.Tool{Name: "b", Description: "second"}
	first, cloned, err := FingerprintTools([]*mcp.Tool{b, a})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := FingerprintTools([]*mcp.Tool{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("fingerprints differ across input order: %s != %s", first, second)
	}
	if len(cloned) != 2 || cloned[0].Name != "a" || cloned[1].Name != "b" {
		t.Fatalf("unexpected canonical clone: %#v", cloned)
	}
	a.Description = "mutated"
	if cloned[0].Description != "first" {
		t.Fatal("canonical clone aliases caller-owned tool")
	}
}

func TestFingerprintToolMatchesCanonicalJSON(t *testing.T) {
	tool := &mcp.Tool{Name: "example", Description: "stable"}
	fingerprint, body, err := FingerprintTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	if want := FingerprintJSON(body); fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", fingerprint, want)
	}
}
