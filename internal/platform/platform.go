package platform

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func OpenURL(ctx context.Context, raw string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", raw)
	case "darwin":
		c = exec.CommandContext(ctx, "open", raw)
	default:
		c = exec.CommandContext(ctx, "xdg-open", raw)
	}
	return c.Start()
}

func OpenFolder(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return OpenURL(ctx, (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String())
}

func SetLaunchAtStartup(ctx context.Context, enabled bool, exe string) error {
	switch runtime.GOOS {
	case "windows":
		key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
		if enabled {
			return exec.CommandContext(ctx, "reg", "add", key, "/v", "GPTTunnelManager", "/t", "REG_SZ", "/d", fmt.Sprintf("\"%s\"", exe), "/f").Run()
		}
		cmd := exec.CommandContext(ctx, "reg", "delete", key, "/v", "GPTTunnelManager", "/f")
		if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(strings.ToLower(string(out)), "unable to find") {
			return err
		}
		return nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir := filepath.Join(home, "Library", "LaunchAgents")
		path := filepath.Join(dir, "com.gpt-tunnel-manager.plist")
		if !enabled {
			return removeIfExists(path)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>com.gpt-tunnel-manager</string><key>ProgramArguments</key><array><string>%s</string></array><key>RunAtLoad</key><true/></dict></plist>`, escapeXML(exe))
		return os.WriteFile(path, []byte(xml), 0o600)
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir := filepath.Join(home, ".config", "autostart")
		path := filepath.Join(dir, "gpt-tunnel-manager.desktop")
		if !enabled {
			return removeIfExists(path)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		body := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=GPT Tunnel Manager\nExec=%s\nTerminal=false\nX-GNOME-Autostart-enabled=true\n", desktopQuote(exe))
		return os.WriteFile(path, []byte(body), 0o600)
	}
}

func LaunchAtStartupEnabled(exe string) (bool, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "GPTTunnelManager").CombinedOutput()
		return err == nil && strings.Contains(string(out), exe), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return false, err
		}
		_, err = os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.gpt-tunnel-manager.plist"))
		if os.IsNotExist(err) {
			return false, nil
		}
		return err == nil, err
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return false, err
		}
		_, err = os.Stat(filepath.Join(home, ".config", "autostart", "gpt-tunnel-manager.desktop"))
		if os.IsNotExist(err) {
			return false, nil
		}
		return err == nil, err
	}
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return strings.ReplaceAll(s, "'", "&apos;")
}

func desktopQuote(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
}
