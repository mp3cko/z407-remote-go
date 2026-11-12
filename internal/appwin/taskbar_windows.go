//go:build windows

package appwin

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// appID is the identity on the Windows taskbar of the app window shown by
// the program at exe. It includes the program's path: the taskbar remembers
// a pinned window's relaunch command per id and never updates it, so with
// one id for all, a pin made from one copy of z407.exe would go on starting
// that copy, even after another one was pinned in its place.
func appID(exe string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(exe))))
	return "io.github.mp3cko.z407." + hex.EncodeToString(sum[:6])
}

var (
	shell32                         = windows.NewLazySystemDLL("shell32.dll")
	user32                          = windows.NewLazySystemDLL("user32.dll")
	procSHGetPropertyStoreForWindow = shell32.NewProc("SHGetPropertyStoreForWindow")
	procGetWindowTextW              = user32.NewProc("GetWindowTextW")

	iidPropertyStore = windows.GUID{Data1: 0x886d8eeb, Data2: 0x8cf2, Data3: 0x4446, Data4: [8]byte{0x8d, 0x02, 0xcd, 0xba, 0x1d, 0xbd, 0xcf, 0x99}}
	appUserModel     = windows.GUID{Data1: 0x9f4c2855, Data2: 0x9f79, Data3: 0x4b39, Data4: [8]byte{0xa8, 0xd0, 0xe1, 0xd4, 0x2d, 0xe1, 0xd5, 0xf3}}
)

// The System.AppUserModel properties, by property id.
const (
	pidRelaunchCommand             = 2
	pidRelaunchIconResource        = 3
	pidRelaunchDisplayNameResource = 4
	pidID                          = 5
)

type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

// propVariant is a PROPVARIANT holding a string (VT_LPWSTR).
type propVariant struct {
	vt  uint16
	_   [3]uint16
	str *uint16
	_   uintptr
}

const vtLPWSTR = 31

// brandTaskbar waits for the app window and makes it z407's own on the
// taskbar. Left alone, the window belongs to the browser: the taskbar groups
// it with the browser, and pinning it pins the browser, which then opens an
// empty window. With its own app id and a relaunch command naming this
// program, a pinned window starts z407 again, under its name and icon.
//
// The window is found by its title, not by the started process: the browser
// may hand the window to a copy of itself that is already running.
func brandTaskbar() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if brandWindows(exe) > 0 {
			return
		}
	}
}

// brandWindows gives every app window of the remote the taskbar properties
// and returns how many it found.
func brandWindows(exe string) int {
	var found []windows.HWND
	cb := syscall.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		if isAppWindow(hwnd) {
			found = append(found, hwnd)
		}
		return 1 // go on
	})
	_ = windows.EnumWindows(cb, nil)
	if len(found) == 0 {
		return 0
	}

	// The property store is a COM object; COM wants an initialized thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}
	for _, hwnd := range found {
		setProperties(hwnd, map[uint32]string{
			pidRelaunchCommand:             `"` + exe + `"`,
			pidRelaunchIconResource:        exe + ",0",
			pidRelaunchDisplayNameResource: WindowTitle,
			pidID:                          appID(exe),
		})
	}
	return len(found)
}

// isAppWindow reports whether hwnd is a visible Chromium window titled like
// the remote. A browser tab showing the remote is not one: the title of a
// tabbed window names the browser as well.
func isAppWindow(hwnd windows.HWND) bool {
	if !windows.IsWindowVisible(hwnd) {
		return false
	}
	class := make([]uint16, 64)
	if n, err := windows.GetClassName(hwnd, &class[0], int32(len(class))); err != nil || windows.UTF16ToString(class[:n]) != "Chrome_WidgetWin_1" {
		return false
	}
	title := make([]uint16, 128)
	n, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	return windows.UTF16ToString(title[:n]) == WindowTitle
}

// setProperties writes the AppUserModel properties of hwnd. The id goes
// last: the taskbar regroups the window when it changes, and should find
// the relaunch properties already there.
func setProperties(hwnd windows.HWND, values map[uint32]string) {
	var store *struct{ vtbl *[8]uintptr }
	if hr, _, _ := procSHGetPropertyStoreForWindow.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&iidPropertyStore)), uintptr(unsafe.Pointer(&store))); hr != 0 || store == nil {
		return
	}
	self := uintptr(unsafe.Pointer(store))
	const setValue, commit, release = 6, 7, 2
	for _, pid := range []uint32{pidRelaunchCommand, pidRelaunchIconResource, pidRelaunchDisplayNameResource, pidID} {
		s, err := windows.UTF16PtrFromString(values[pid])
		if err != nil {
			continue
		}
		key := propertyKey{appUserModel, pid}
		v := propVariant{vt: vtLPWSTR, str: s}
		_, _, _ = syscall.SyscallN(store.vtbl[setValue], self, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&v)))
		runtime.KeepAlive(s)
	}
	_, _, _ = syscall.SyscallN(store.vtbl[commit], self)
	_, _, _ = syscall.SyscallN(store.vtbl[release], self)
}
