//go:build windows && !nogui

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	desktopUser32              = windows.NewLazySystemDLL("user32.dll")
	desktopFindWindowW         = desktopUser32.NewProc("FindWindowW")
	desktopShowWindow          = desktopUser32.NewProc("ShowWindow")
	desktopSetForegroundWindow = desktopUser32.NewProc("SetForegroundWindow")
)

func desktopWindowHandle() uintptr {
	title, err := windows.UTF16PtrFromString("GPT Tunnel Manager")
	if err != nil {
		return 0
	}
	hwnd, _, _ := desktopFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	return hwnd
}

func hideDesktopWindow() bool {
	hwnd := desktopWindowHandle()
	if hwnd == 0 {
		return false
	}
	const swHide = 0
	desktopShowWindow.Call(hwnd, swHide)
	return true
}

func restoreDesktopWindow() bool {
	hwnd := desktopWindowHandle()
	if hwnd == 0 {
		return false
	}
	const swShow = 5
	desktopShowWindow.Call(hwnd, swShow)
	desktopSetForegroundWindow.Call(hwnd)
	return true
}
