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
	desktopGetForegroundWindow      = desktopUser32.NewProc("GetForegroundWindow")
	desktopGetWindowThreadProcessID = desktopUser32.NewProc("GetWindowThreadProcessId")
	desktopGetWindowRect            = desktopUser32.NewProc("GetWindowRect")
	desktopIsWindow                 = desktopUser32.NewProc("IsWindow")
	desktopIsWindowVisible          = desktopUser32.NewProc("IsWindowVisible")
	desktopShowWindowAsync          = desktopUser32.NewProc("ShowWindowAsync")
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

func desktopWindowBelongsToProcess(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	var windowPID uint32
	desktopGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
	return windowPID == uint32(os.Getpid())
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

func foregroundDesktopWindow() uintptr {
	hwnd, _, _ := desktopGetForegroundWindow.Call()
	if !desktopWindowBelongsToProcess(hwnd) {
		return 0
	}
	visible, _, _ := desktopIsWindowVisible.Call(hwnd)
	if visible == 0 || desktopWindowArea(hwnd) == 0 {
		return 0
	}
	return hwnd
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
	if valid == 0 || !desktopWindowBelongsToProcess(hwnd) {
		return 0
	}
	return hwnd
}

func hideDesktopWindow() bool {
	// The window is normally foreground when the user clicks our custom
	// minimize/close button. Capturing it directly avoids probing the Gio
	// window with message-based APIs while Gio is processing that same frame.
	hwnd := foregroundDesktopWindow()
	if hwnd == 0 {
		hwnd = findDesktopWindow(true)
	}
	if hwnd == 0 {
		return false
	}
	rememberDesktopWindow(hwnd)

	// Queue the visibility change. ShowWindowAsync does not wait for Gio's
	// window procedure to process the show-state event.
	const swHide = 0
	queued, _, _ := desktopShowWindowAsync.Call(hwnd, swHide)
	return queued != 0
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
	queued, _, _ := desktopShowWindowAsync.Call(hwnd, swShow)
	if queued == 0 {
		return false
	}
	desktopSetForegroundWindow.Call(hwnd)
	return true
}

func keepDesktopWindowAliveForTray() bool { return true }
