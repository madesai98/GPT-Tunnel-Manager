package tunnelclient

import (
	"slices"
	"testing"
)

func TestAppendMCPArgsEnablesStdioInitializedNotification(t *testing.T) {
	args := appendMCPArgs([]string{"run"}, RunSpec{
		MCPCommand:                       []string{"rust-mcp-filesystem", "--enable-roots"},
		StdioSendInitializedNotification: true,
	})
	if !slices.Contains(args, "--mcp.stdio-send-initialized-notification") {
		t.Fatalf("args=%q; initialized notification flag missing", args)
	}
}

func TestAppendMCPArgsDoesNotAddStdioShimForHTTP(t *testing.T) {
	args := appendMCPArgs([]string{"run"}, RunSpec{
		MCPURL:                           "http://127.0.0.1:1234/mcp",
		StdioSendInitializedNotification: true,
	})
	if slices.Contains(args, "--mcp.stdio-send-initialized-notification") {
		t.Fatalf("args=%q; stdio-only flag must not be used for HTTP MCP", args)
	}
}

func TestStdioInitializedNotificationCompatible(t *testing.T) {
	if !StdioInitializedNotificationCompatible("v0.0.13+4b5267f823be0b046bb883aacb51603cfde3a0ea") {
		t.Fatal("v0.0.13 builds should support the initialized notification shim")
	}
	if StdioInitializedNotificationCompatible("v0.0.12") {
		t.Fatal("v0.0.12 should not receive an unsupported flag")
	}
}
