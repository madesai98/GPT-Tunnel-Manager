//go:build !nogui

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func TestV2ToggledServerMode(t *testing.T) {
	for _, mode := range []v2config.ServerMode{v2config.ModeManaged, v2config.ModeAlwaysOn, v2config.ModeManual} {
		if got := v2ToggledServerMode(mode); got != v2config.ModeDisabled {
			t.Fatalf("toggle %q: got %q, want disabled", mode, got)
		}
	}
	if got := v2ToggledServerMode(v2config.ModeDisabled); got != v2config.ModeManaged {
		t.Fatalf("toggle disabled: got %q, want managed", got)
	}
}

func TestFormatV2IdleRemaining(t *testing.T) {
	cases := map[time.Duration]string{
		0:                      "0s",
		500 * time.Millisecond: "1s",
		61 * time.Second:       "1m1s",
		3661 * time.Second:     "1h1m1s",
	}
	for in, want := range cases {
		if got := formatV2IdleRemaining(in); got != want {
			t.Fatalf("format %s: got %q, want %q", in, got, want)
		}
	}
}

func TestV2ServerCardsUseCompactRuntimeControls(t *testing.T) {
	data, err := os.ReadFile("desktop_v2_pages.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, marker := range []string{"compactCard(", "v2IdleCountdown", "v2ServerIconButton", "v2DangerIconButton", "v2ServerToggle"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("server card is missing compact control marker %q", marker)
		}
	}
	for _, old := range []string{
		"secondaryButton(u.th, &actions.start, \"Start\")",
		"secondaryButton(u.th, &actions.stop, \"Stop\")",
		"secondaryButton(u.th, &actions.restart, \"Restart\")",
		"dangerButton(u.th, &actions.remove, \"Remove\")",
	} {
		if strings.Contains(text, old) {
			t.Fatalf("server card still contains legacy full-width control %q", old)
		}
	}
}
