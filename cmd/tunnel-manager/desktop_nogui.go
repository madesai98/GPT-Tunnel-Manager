//go:build nogui

package main

import coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"

func runDesktop(a *coreapp.App, setFocus func(func())) error {
	_ = setFocus
	return runHeadless(a)
}
