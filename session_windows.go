package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	userDLL            = windows.NewLazySystemDLL("user32.dll")
	registerClass      = userDLL.NewProc("RegisterClassExW")
	createWindow       = userDLL.NewProc("CreateWindowExW")
	defaultWindow      = userDLL.NewProc("DefWindowProcW")
	getMessage         = userDLL.NewProc("GetMessageW")
	dispatchMessage    = userDLL.NewProc("DispatchMessageW")
	moduleHandle       = kernelDLL.NewProc("GetModuleHandleW")
	shutdownParameters = kernelDLL.NewProc("SetProcessShutdownParameters")
)

type windowClass struct {
	size, style                        uint32
	callback                           uintptr
	classExtra, windowExtra            int32
	instance, icon, cursor, background uintptr
	menu, name                         *uint16
	smallIcon                          uintptr
}

type winMessage struct {
	hwnd           uintptr
	message        uint32
	wparam, lparam uintptr
	time           uint32
	x, y           int32
	private        uint32
}

type watcherState struct {
	Enabled bool   `json:"enabled"`
	Ready   bool   `json:"ready"`
	PID     int    `json:"pid,omitempty"`
	Error   string `json:"error,omitempty"`
}

type sessionWatcher struct {
	mu    sync.RWMutex
	state watcherState
}

func (w *sessionWatcher) snapshot() watcherState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state
}

func (w *sessionWatcher) listen(onEnd func(string)) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	set := func(ready bool, message string) {
		w.mu.Lock()
		w.state = watcherState{Enabled: true, Ready: ready, PID: os.Getpid(), Error: message}
		w.mu.Unlock()
	}
	name, _ := windows.UTF16PtrFromString("MikuScreenHubSession")
	title, _ := windows.UTF16PtrFromString(fmt.Sprintf("MikuScreenHub-%d", os.Getpid()))
	instance, _, _ := moduleHandle.Call(0)
	callback := windows.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x0011:
			return 1 // WM_QUERYENDSESSION
		case 0x0016: // WM_ENDSESSION
			if wp != 0 {
				reason := "shutdown-or-restart"
				if lp&0x80000000 != 0 {
					reason = "logoff"
				}
				onEnd(reason)
			}
			return 0
		}
		result, _, _ := defaultWindow.Call(hwnd, uintptr(msg), wp, lp)
		return result
	})
	class := windowClass{callback: callback, instance: instance, name: name}
	class.size = uint32(unsafe.Sizeof(class))
	if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		set(false, err.Error())
		return
	}
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		set(false, err.Error())
		return
	}
	if ok, _, err := shutdownParameters.Call(0x3ff, 1); ok == 0 {
		set(true, "Shutdown priority: "+err.Error())
	} else {
		set(true, "")
	}
	var msg winMessage
	for {
		r, _, err := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			message := ""
			if int32(r) < 0 {
				message = err.Error()
			}
			set(false, message)
			return
		}
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}
