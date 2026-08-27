from pathlib import Path

p = Path('cmd/tunnel-manager/desktop_gio.go')
s = p.read_text()

replacements = [
    (
        "\tmessage      string\n\twindowHidden bool\n\texiting      bool",
        "\tmessage      string\n\twindowHidden bool\n\thidePending  bool\n\texiting      bool",
    ),
    (
        "\t\tcase gioapp.FrameEvent:\n\t\t\tgtx := gioapp.NewContext(&ops, event)\n\t\t\tu.layout(gtx)\n\t\t\tevent.Frame(gtx.Ops)",
        "\t\tcase gioapp.FrameEvent:\n\t\t\tgtx := gioapp.NewContext(&ops, event)\n\t\t\tu.layout(gtx)\n\t\t\tevent.Frame(gtx.Ops)\n\t\t\tu.applyPendingHide(win)",
    ),
    (
        "func (u *desktopUI) showWindow() {\n\tu.mu.RLock()\n\texiting := u.exiting\n\thidden := u.windowHidden\n\twin := u.win\n\tu.mu.RUnlock()",
        "func (u *desktopUI) showWindow() {\n\tu.mu.Lock()\n\texiting := u.exiting\n\thidden := u.windowHidden\n\twin := u.win\n\tif hidden && u.hidePending && win != nil && !exiting {\n\t\tu.hidePending = false\n\t\tu.windowHidden = false\n\t\thidden = false\n\t}\n\tu.mu.Unlock()",
    ),
    (
        "func (u *desktopUI) hideToTray() {\n\tu.mu.Lock()\n\tif u.windowHidden || u.exiting || u.win == nil {\n\t\tu.mu.Unlock()\n\t\treturn\n\t}\n\twin := u.win\n\tu.windowHidden = true\n\tu.mu.Unlock()\n\tif hideDesktopWindow() {\n\t\treturn\n\t}\n\twin.Perform(system.ActionClose)\n}\n\nfunc (u *desktopUI) requestClose() {",
        "func (u *desktopUI) hideToTray() {\n\tu.mu.Lock()\n\tif u.windowHidden || u.exiting || u.win == nil {\n\t\tu.mu.Unlock()\n\t\treturn\n\t}\n\twin := u.win\n\tu.windowHidden = true\n\tu.hidePending = true\n\tu.mu.Unlock()\n\twin.Invalidate()\n}\n\nfunc (u *desktopUI) applyPendingHide(win *gioapp.Window) {\n\tu.mu.Lock()\n\tif !u.hidePending {\n\t\tu.mu.Unlock()\n\t\treturn\n\t}\n\tu.hidePending = false\n\thidden := u.windowHidden\n\texiting := u.exiting\n\tu.mu.Unlock()\n\n\tif !hidden || exiting {\n\t\treturn\n\t}\n\tif hideDesktopWindow() {\n\t\treturn\n\t}\n\tif keepDesktopWindowAliveForTray() {\n\t\tu.mu.Lock()\n\t\tu.windowHidden = false\n\t\tu.message = \"Unable to hide GPT Tunnel Manager to the system tray.\"\n\t\tu.mu.Unlock()\n\t\twin.Invalidate()\n\t\treturn\n\t}\n\twin.Perform(system.ActionClose)\n}\n\nfunc (u *desktopUI) requestClose() {",
    ),
]

for old, new in replacements:
    count = s.count(old)
    if count != 1:
        raise SystemExit(f'expected exactly one match for patch block, got {count}')
    s = s.replace(old, new)
p.write_text(s)

Path('cmd/tunnel-manager/desktop_window_other.go').write_text('''//go:build !windows && !nogui

package main

func hideDesktopWindow() bool { return false }

func restoreDesktopWindow() bool { return false }

func keepDesktopWindowAliveForTray() bool { return false }
''')

Path('cmd/tunnel-manager/desktop_window_windows.go').write_text('''//go:build windows && !nogui

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
    desktopIsWindow                 = desktopUser32.NewProc("IsWindow")
    desktopIsWindowVisible          = desktopUser32.NewProc("IsWindowVisible")
    desktopShowWindow               = desktopUser32.NewProc("ShowWindow")
    desktopSetForegroundWindow      = desktopUser32.NewProc("SetForegroundWindow")

    desktopWindowStateMu sync.Mutex
    desktopWindowHWND    uintptr
)

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

func findDesktopWindow(visibleOnly bool) uintptr {
    pid := uint32(os.Getpid())
    var found uintptr
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
        if desktopWindowTitle(hwnd) != "GPT Tunnel Manager" {
            return 1
        }
        found = hwnd
        return 0
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
''')
