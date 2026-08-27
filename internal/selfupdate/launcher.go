package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func Launch(plan Plan, currentPID int, restartArgs []string) error {
	if !plan.UpdateAvailable || plan.StageDir == "" || plan.TempDir == "" {
		return errors.New("self-update plan is not staged")
	}
	if currentPID <= 0 {
		return errors.New("invalid current process ID")
	}

	switch runtime.GOOS {
	case "windows":
		script := filepath.Join(plan.TempDir, "gpt-tunnel-manager-updater.ps1")
		if err := os.WriteFile(script, []byte(windowsScript(plan, currentPID, restartArgs)), 0o600); err != nil {
			return fmt.Errorf("write updater script: %w", err)
		}
		cmd := exec.Command("cmd.exe", "/C", "start", "", "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("launch updater terminal: %w", err)
		}
		return nil
	case "darwin":
		script := filepath.Join(plan.TempDir, "GPT Tunnel Manager Updater.command")
		if err := os.WriteFile(script, []byte(unixScript(plan, currentPID, restartArgs, true)), 0o700); err != nil {
			return fmt.Errorf("write updater script: %w", err)
		}
		cmd := exec.Command("open", "-a", "Terminal", script)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("launch updater terminal: %w", err)
		}
		return nil
	case "linux":
		script := filepath.Join(plan.TempDir, "gpt-tunnel-manager-updater.sh")
		if err := os.WriteFile(script, []byte(unixScript(plan, currentPID, restartArgs, false)), 0o700); err != nil {
			return fmt.Errorf("write updater script: %w", err)
		}
		cmd, err := linuxTerminalCommand(script)
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("launch updater terminal: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("self-update is not supported on %s", runtime.GOOS)
	}
}

func windowsScript(plan Plan, currentPID int, restartArgs []string) string {
	args := make([]string, 0, len(restartArgs))
	for _, arg := range restartArgs {
		args = append(args, psQuote(arg))
	}
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$Host.UI.RawUI.WindowTitle = 'GPT Tunnel Manager Updater'
$ProcessId = %d
$Stage = %s
$Target = %s
$Executable = %s
$TempRoot = %s
$RestartArgs = @(%s)
$Protected = @('config', 'data', 'tools')

$deadline = (Get-Date).AddSeconds(75)
while (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue) {
    if ((Get-Date) -ge $deadline) { break }
    Start-Sleep -Milliseconds 500
}
if (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue) {
    Stop-Process -Id $ProcessId -Force
    Wait-Process -Id $ProcessId -ErrorAction SilentlyContinue
}

Get-ChildItem -LiteralPath $Stage -Force | ForEach-Object {
    if ($Protected -notcontains $_.Name) {
        $destination = Join-Path $Target $_.Name
        if ($_.PSIsContainer) {
            New-Item -ItemType Directory -Path $destination -Force | Out-Null
            Get-ChildItem -LiteralPath $_.FullName -Force | ForEach-Object {
                Copy-Item -LiteralPath $_.FullName -Destination $destination -Recurse -Force
            }
        } else {
            Copy-Item -LiteralPath $_.FullName -Destination $destination -Force
        }
    }
}

if ($RestartArgs.Count -gt 0) {
    Start-Process -FilePath $Executable -ArgumentList $RestartArgs -WorkingDirectory $Target
} else {
    Start-Process -FilePath $Executable -WorkingDirectory $Target
}
try { Remove-Item -LiteralPath $TempRoot -Recurse -Force -ErrorAction SilentlyContinue } catch {}
`, currentPID, psQuote(plan.StageDir), psQuote(plan.TargetDir), psQuote(plan.Executable), psQuote(plan.TempDir), strings.Join(args, ", "))
}

func unixScript(plan Plan, currentPID int, restartArgs []string, closeMacTerminal bool) string {
	args := make([]string, 0, len(restartArgs))
	for _, arg := range restartArgs {
		args = append(args, shellQuote(arg))
	}
	macClose := ""
	if closeMacTerminal {
		macClose = `
(osascript <<'APPLESCRIPT'
tell application "Terminal"
    repeat with w in windows
        if name of w contains "GPT Tunnel Manager Updater" then close w
    end repeat
end tell
APPLESCRIPT
) >/dev/null 2>&1 &
`
	}
	return fmt.Sprintf(`#!/bin/sh
set -eu
printf '\033]0;GPT Tunnel Manager Updater\007'
PID=%d
STAGE=%s
TARGET=%s
EXE=%s
TEMP_ROOT=%s

COUNT=0
while kill -0 "$PID" 2>/dev/null && [ "$COUNT" -lt 150 ]; do
    sleep 0.5
    COUNT=$((COUNT + 1))
done
if kill -0 "$PID" 2>/dev/null; then
    kill -TERM "$PID" 2>/dev/null || true
    sleep 5
fi
if kill -0 "$PID" 2>/dev/null; then
    kill -KILL "$PID" 2>/dev/null || true
fi

for ITEM in "$STAGE"/* "$STAGE"/.[!.]* "$STAGE"/..?*; do
    [ -e "$ITEM" ] || continue
    NAME=$(basename "$ITEM")
    case "$NAME" in
        config|data|tools) continue ;;
    esac
    cp -R "$ITEM" "$TARGET/"
done
chmod +x "$EXE" 2>/dev/null || true
cd "$TARGET"
nohup "$EXE" %s >/dev/null 2>&1 &
rm -rf "$TEMP_ROOT"
%s
exit 0
`, currentPID, shellQuote(plan.StageDir), shellQuote(plan.TargetDir), shellQuote(plan.Executable), shellQuote(plan.TempDir), strings.Join(args, " "), macClose)
}

func linuxTerminalCommand(script string) (*exec.Cmd, error) {
	title := "GPT Tunnel Manager Updater"
	candidates := []struct {
		name string
		args []string
	}{
		{"x-terminal-emulator", []string{"-T", title, "-e", "sh", script}},
		{"gnome-terminal", []string{"--title=" + title, "--", "sh", script}},
		{"konsole", []string{"-p", "tabtitle=" + title, "-e", "sh", script}},
		{"xfce4-terminal", []string{"--title=" + title, "--command", "sh " + shellQuote(script)}},
		{"mate-terminal", []string{"--title=" + title, "--", "sh", script}},
		{"xterm", []string{"-T", title, "-e", "sh", script}},
	}
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate.name); err == nil {
			return exec.Command(candidate.name, candidate.args...), nil
		}
	}
	return nil, errors.New("no supported terminal emulator found for self-update")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
