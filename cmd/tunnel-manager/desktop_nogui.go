//go:build nogui

package main

import (
	"context"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/platform"
)

func runDesktop(a *coreapp.App, setFocus func(func())) error {
	if setFocus != nil { setFocus(func(){ _ = platform.OpenURL(context.Background(), a.AdminURL()) }) }
	_ = platform.OpenURL(context.Background(), a.AdminURL())
	return runHeadless(a)
}
