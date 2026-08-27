//go:build !windows && !nogui

package main

func hideDesktopWindow() bool { return false }

func restoreDesktopWindow() bool { return false }

func keepDesktopWindowAliveForTray() bool { return false }
