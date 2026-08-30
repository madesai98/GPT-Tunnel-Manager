package downstream

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCurrentToolsTracksRefreshedSnapshotWithoutChangingInitial(t *testing.T) {
	initial := ToolSnapshot{Tools: []*mcp.Tool{{Name: "initial"}}}.Clone()
	current := ToolSnapshot{Tools: []*mcp.Tool{{Name: "dynamic"}}}.Clone()
	s := &Session{initial: initial, current: initial}
	s.setCurrentTools(current)
	if got := s.InitialTools(); len(got.Tools) != 1 || got.Tools[0].Name != "initial" {
		t.Fatalf("InitialTools = %#v", got.Tools)
	}
	if got := s.CurrentTools(); len(got.Tools) != 1 || got.Tools[0].Name != "dynamic" {
		t.Fatalf("CurrentTools = %#v", got.Tools)
	}
}
