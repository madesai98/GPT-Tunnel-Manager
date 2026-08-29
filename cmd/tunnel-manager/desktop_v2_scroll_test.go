//go:build !nogui

package main

import (
	"os"
	"strings"
	"testing"
)

func TestV2ScrollablePagesUsePersistentListState(t *testing.T) {
	required := map[string][]string{
		"desktop_v2_server_editor.go": {"v2ServerEditorState.scroll.Layout"},
		"desktop_v2_index_reviews.go": {"v2IndexReviewState.scroll.Layout"},
		"desktop_v2_routing_editor.go": {"v2RoutingEditorState.scroll.Layout"},
		"desktop_v2_gio.go":            {"u.settingsScroll.Layout"},
	}
	for file, needles := range required {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(data)
		for _, needle := range needles {
			if !strings.Contains(text, needle) {
				t.Fatalf("%s is missing persistent scroll state marker %q", file, needle)
			}
		}
	}

	for _, file := range []string{
		"desktop_v2_server_editor.go",
		"desktop_v2_index_reviews.go",
		"desktop_v2_routing_editor.go",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(data), "var list layout.List") {
			t.Fatalf("%s recreates scroll state every frame", file)
		}
	}
}
