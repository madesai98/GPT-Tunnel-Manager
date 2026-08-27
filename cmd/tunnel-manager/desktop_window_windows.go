//go:build windows && !nogui

package main

// Gio owns the native Windows window and supports creating a new Window after
// DestroyEvent. Minimize-to-tray therefore closes the current Gio window and
// the tray callback creates a fresh one instead of manipulating Gio's private
// HWND through Win32 APIs. This keeps the manager process, tray icon, runtime
// state, and MCP servers alive while giving restore a clean window lifecycle.
func hideDesktopWindow() bool { return false }

// Returning false tells the shared desktop loop to queue a show request. Once
// the hidden Gio window has finished closing, that request clears the hidden
// state and runWindow creates a new native window.
func restoreDesktopWindow() bool { return false }

// Do not keep a hidden native Gio HWND alive on Windows. Closing and recreating
// the window is the supported and deterministic tray lifecycle.
func keepDesktopWindowAliveForTray() bool { return false }
