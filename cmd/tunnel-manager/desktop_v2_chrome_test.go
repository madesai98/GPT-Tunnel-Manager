//go:build !nogui

package main

import (
	"os"
	"strings"
	"testing"
)

func TestV2DesktopRestoresFramelessChrome(t *testing.T) {
	gioSource, err := os.ReadFile("desktop_v2_gio.go")
	if err != nil {
		t.Fatal(err)
	}
	gioText := string(gioSource)
	for _, marker := range []string{
		"gioapp.Decorated(false)",
		"case gioapp.ConfigEvent:",
		"u.deco.Maximized = event.Config.Mode == gioapp.Maximized",
		"requestedHidden := u.windowHidden",
		"requestedHidden := u.windowHidden",
		"layout.Rigid(u.v2TitleBar)",
	} {
		if !strings.Contains(gioText, marker) {
			t.Fatalf("v2 desktop is missing frameless chrome marker %q", marker)
		}
	}

	chromeSource, err := os.ReadFile("desktop_v2_chrome.go")
	if err != nil {
		t.Fatal(err)
	}
	chromeText := string(chromeSource)
	for _, marker := range []string{
		"system.ActionInputOp(system.ActionMove)",
		"u.requestClose()",
		"u.hideToTray()",
		"system.ActionMaximize",
		"system.ActionUnmaximize",
		"R: 255, G: 95, B: 87",
		"R: 254, G: 188, B: 46",
		"R: 40, G: 200, B: 64",
		"op.Defer(gtx.Ops, call)",
	} {
		if !strings.Contains(chromeText, marker) {
			t.Fatalf("custom chrome is missing marker %q", marker)
		}
	}
}
