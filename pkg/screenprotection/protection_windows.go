//go:build windows

package screenprotection

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	enumWindows      = user32.NewProc("EnumWindows")
	getWindowProcess = user32.NewProc("GetWindowThreadProcessId")
	getClassName     = user32.NewProc("GetClassNameW")
	setAffinity      = user32.NewProc("SetWindowDisplayAffinity")
	getAffinity      = user32.NewProc("GetWindowDisplayAffinity")
	// NewCallback allocates a permanent trampoline. Allocate it once, rather
	// than leaking one for every conversation switch. Set serializes access.
	mainWindowHandle   uintptr
	mainWindowCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var pid uint32
		getWindowProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid != uint32(os.Getpid()) {
			return 1
		}
		var name [256]uint16
		length, _, _ := getClassName.Call(hwnd, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
		if length != 0 && windows.UTF16ToString(name[:]) == "LoomMainWindow" {
			mainWindowHandle = hwnd
			return 0
		}
		return 1
	})
)

func Supported() bool {
	v := windows.RtlGetVersion()
	return v.MajorVersion > 10 || (v.MajorVersion == 10 && v.BuildNumber >= 19041)
}
func Limited() bool { return false }

func set(enabled bool) error {
	if !Supported() {
		if !enabled {
			return nil
		}
		return fmt.Errorf("capture exclusion requires Windows 10 version 2004 or later")
	}
	mainWindowHandle = 0
	enumWindows.Call(mainWindowCallback, 0)
	handle := mainWindowHandle
	if handle == 0 {
		return fmt.Errorf("Loom main window not found")
	}
	var affinity uintptr
	if enabled {
		affinity = 0x11 // WDA_EXCLUDEFROMCAPTURE
	}
	if ok, _, err := setAffinity.Call(handle, affinity); ok == 0 {
		return fmt.Errorf("set window capture protection: %w", err)
	}
	var actual uint32
	if ok, _, err := getAffinity.Call(handle, uintptr(unsafe.Pointer(&actual))); ok == 0 {
		return fmt.Errorf("verify window capture protection: %w", err)
	}
	if actual != uint32(affinity) {
		return fmt.Errorf("window capture protection was not applied")
	}
	return nil
}
