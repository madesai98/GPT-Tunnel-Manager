package mcpmanager

// toolError is the stable structured error envelope shared by the canonical
// Manager MCP indexing, discovery, and preference tools.
type toolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
