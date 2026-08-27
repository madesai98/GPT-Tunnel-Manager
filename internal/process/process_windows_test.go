//go:build windows

package process

import (
	"os/exec"
	"testing"
)

func TestConfigureCommandPreventsConsoleWindows(t *testing.T) {
	cmd := ConfigureCommand(exec.Command("cmd.exe", "/c", "exit", "0"))
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr was not configured")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow is false")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CREATE_NO_WINDOW missing from creation flags: %#x", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&createNewProcessGroup == 0 {
		t.Fatalf("CREATE_NEW_PROCESS_GROUP missing from creation flags: %#x", cmd.SysProcAttr.CreationFlags)
	}
}
