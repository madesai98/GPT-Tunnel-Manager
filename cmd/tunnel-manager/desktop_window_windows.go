//go:build windows && !nogui

package main

import (
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	desktopUser32                   = windows.NewLazySystemDLL("user32.dll")
	desktopEnumWindows              = desktopUser32.NewProc("EnumWindows")
	desktopGetWindowThreadProcessID = desktopUser32.NewProc("GetWindowThreadProcessId")
	desktopGetWindowTextLengthW     = desktopUser32.NewProc("GetWindowTextLengthW")
	desktopGetWindowTextW           = desktopUser32.NewProc("GetWindowTextW")
	desktopGetWindowRect            = desktopUser32.NewProc("GetWindowRect")
	desktopIsWindow                 = desktopUser32.NewProc("IsWindow")
	desktopIsWindowVisible          = desktopUser32.NewProc("IsWindowVisible")
	desktopShowWindow               = desktopUser32.NewProc("ShowWindow")
	desktopSetForegroundWindow      = desktopUser32.NewProc("SetForegroundWindow")

	desktopWindowStateMu sync.Mutex
	desktopWindowHWND    uintptr
)

type desktopRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func desktopWindowTitle(hwnd uintptr) string {
	length, _, _ := desktopGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, int(length)+1)
	n, _, _ := desktopGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func desktopWindowArea(hwnd uintptr) int64 {
	var rect desktopRect
	ok, _, _ := desktopGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if ok == 0 {
		return 0
	}
	width := int64(rect.Right - rect.Left)
	height := int64(rect.Bottom - rect.Top)
	if width <= 0 || height <= 0 {
		return 0
	}
	return width * height
}

func findDesktopWindow(visibleOnly bool) uintptr {
	pid := uint32(os.Getpid())
	var found uintptr
	var foundArea int64
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var windowPID uint32
		desktopGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
		if windowPID != pid {
			return 1
		}
		if visibleOnly {
			visible, _, _ := desktopIsWindowVisible.Call(hwnd)
			if visible == 0 {
				return 1
			}
		}

		area := desktopWindowArea(hwnd)
		if area == 0 {
			return 1
		}

		// Prefer the expected Gio title when Windows exposes it, but do not
		// require an exact title. Gio can briefly expose the native window with
		// an empty/different caption even though it is the correct top-level UI.
		if desktopWindowTitle(hwnd) == "GPT Tunnel Manager" {
			found = hwnd
			foundArea = area
			return 0
		}
		if area > foundArea {
			found = hwnd
			foundArea = area
		}
		return 1
	})
	desktopEnumWindows.Call(callback, 0)
	return found
}

func rememberDesktopWindow(hwnd uintptr) {
	desktopWindowStateMu.Lock()
	desktopWindowHWND = hwnd
	desktopWindowStateMu.Unlock()
}

func rememberedDesktopWindow() uintptr {
	desktopWindowStateMu.Lock()
	hwnd := desktopWindowHWND
	desktopWindowStateMu.Unlock()
	if hwnd == 0 {
		return 0
	}
	valid, _, _ := desktopIsWindow.Call(hwnd)
	if valid == 0 {
		return 0
	}
	return hwnd
}

func hideDesktopWindow() bool {
	hwnd := findDesktopWindow(true)
	if hwnd == 0 {
		return false
	}
	rememberDesktopWindow(hwnd)
	const swHide = 0
	desktopShowWindow.Call(hwnd, swHide)
	visible, _, _ := desktopIsWindowVisible.Call(hwnd)
	return visible == 0
}

func restoreDesktopWindow() bool {
	hwnd := rememberedDesktopWindow()
	if hwnd == 0 {
		hwnd = findDesktopWindow(false)
	}
	if hwnd == 0 {
		return false
	}
	rememberDesktopWindow(hwnd)
	const swShow = 5
	desktopShowWindow.Call(hwnd, swShow)
	visible, _, _ := desktopIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return false
	}
	desktopSetForegroundWindow.Call(hwnd)
	return true
}

func keepDesktopWindowAliveForTray() bool { return true }
