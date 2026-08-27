//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

func configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
}

func terminateGraceful(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	c := ConfigureCommand(exec.Command("taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T"))
	_ = c.Run()
	return nil
}

func terminateForce(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	c := ConfigureCommand(exec.Command("taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F"))
	_ = c.Run()
	return nil
}
