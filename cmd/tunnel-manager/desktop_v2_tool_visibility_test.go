//go:build !nogui

package main

import (
	"os"
	"strings"
	"testing"
)

func TestV2ServerCardsShowToolsAndVisibilityEditor(t *testing.T) {
	pages, err := os.ReadFile("desktop_v2_pages.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(pages)
	for _, marker := range []string{"KnownServerToolCounts", "%d tools", "actions.tools", "openV2ToolVisibilityEditor", "&actions.tools, \"≡\""} {
		if !strings.Contains(text, marker) {
			t.Fatalf("missing server tool UI marker %q", marker)
		}
	}
	if strings.Contains(text, "active leases") {
		t.Fatal("server card still displays active leases")
	}

	editor, err := os.ReadFile("desktop_v2_tool_visibility.go")
	if err != nil {
		t.Fatal(err)
	}
	editorText := string(editor)
	for _, marker := range []string{"Tool Exposure", "tools found", "EXPOSED", "HIDDEN", "ToolVisibility.Hidden", "SaveServer"} {
		if !strings.Contains(editorText, marker) {
			t.Fatalf("missing tool visibility editor marker %q", marker)
		}
	}
}
