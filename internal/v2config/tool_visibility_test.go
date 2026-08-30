package v2config

import "testing"

func testToolVisibilityServer() ServerEntry {
	return ServerEntry{
		ID:        "srv_00000000000000000000000000000001",
		Name:      "tools",
		Mode:      ModeManaged,
		Transport: TransportConfig{Type: TransportStdio, Stdio: &StdioTransport{Executable: "test-mcp"}},
	}
}

func TestServerToolVisibility(t *testing.T) {
	entry := testToolVisibilityServer()
	if !entry.ToolExposed("alpha") {
		t.Fatal("tools must be exposed by default")
	}
	entry.ToolVisibility.Hidden = []string{"alpha"}
	if entry.ToolExposed("alpha") || !entry.ToolExposed("beta") {
		t.Fatal("hidden tool filtering is incorrect")
	}
	if err := ValidateServer(entry); err != nil {
		t.Fatalf("valid hidden tool rejected: %v", err)
	}
	entry.ToolVisibility.Hidden = []string{"alpha", "alpha"}
	if err := ValidateServer(entry); err == nil {
		t.Fatal("duplicate hidden tools must be rejected")
	}
	entry.ToolVisibility.Hidden = []string{" alpha"}
	if err := ValidateServer(entry); err == nil {
		t.Fatal("untrimmed hidden tools must be rejected")
	}
}
