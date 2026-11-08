//go:build windows

package screenlayout

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	shcore              = syscall.NewLazyDLL("shcore.dll")
	enumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	getMonitorInfo      = user32.NewProc("GetMonitorInfoW")
	getDpiForMonitor    = shcore.NewProc("GetDpiForMonitor")
	getDpiForWindow     = user32.NewProc("GetDpiForWindow")
	setThreadDPI        = user32.NewProc("SetThreadDpiAwarenessContext")
	enumWindows         = user32.NewProc("EnumWindows")
	isWindowVisible     = user32.NewProc("IsWindowVisible")
	getWindow           = user32.NewProc("GetWindow")
	getWindowLong       = user32.NewProc("GetWindowLongW")
	getWindowPID        = user32.NewProc("GetWindowThreadProcessId")
	getWindowText       = user32.NewProc("GetWindowTextW")
	getWindowRect       = user32.NewProc("GetWindowRect")
	getClientRect       = user32.NewProc("GetClientRect")
	getWindowPlacement  = user32.NewProc("GetWindowPlacement")
	monitorFromWindow   = user32.NewProc("MonitorFromWindow")
	adjustWindowRect    = user32.NewProc("AdjustWindowRectEx")
	adjustWindowRectDPI = user32.NewProc("AdjustWindowRectExForDpi")
	getMenu             = user32.NewProc("GetMenu")
	isIconic            = user32.NewProc("IsIconic")
	isZoomed            = user32.NewProc("IsZoomed")
	showWindowAsync     = user32.NewProc("ShowWindowAsync")
	setWindowPos        = user32.NewProc("SetWindowPos")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	sendMessageTimeout  = user32.NewProc("SendMessageTimeoutW")
	openProcess         = kernel32.NewProc("OpenProcess")
	queryProcessImage   = kernel32.NewProc("QueryFullProcessImageNameW")
	closeHandle         = kernel32.NewProc("CloseHandle")
	displayCallback     = syscall.NewCallback(enumDisplay)
	windowCallback      = syscall.NewCallback(enumWindow)
)

var displayContexts, windowContexts sync.Map
var nextContext atomic.Uintptr

type nativeRect struct{ Left, Top, Right, Bottom int32 }
type nativePoint struct{ X, Y int32 }
type monitorInfo struct {
	Size          uint32
	Monitor, Work nativeRect
	Flags         uint32
	Device        [32]uint16
}
type windowPlacement struct {
	Size, Flags, ShowCommand uint32
	Min, Max                 nativePoint
	Normal                   nativeRect
}
type displayList struct {
	items []Display
	err   error
}
type windowList struct{ items []Window }

func (r nativeRect) rect() Rect {
	return Rect{X: int(r.Left), Y: int(r.Top), Width: int(r.Right - r.Left), Height: int(r.Bottom - r.Top)}
}

// physicalCoordinates scopes DPI awareness to this OS thread; it never changes
// the application's global policy. Older Windows versions use their native default.
func physicalCoordinates() func() {
	runtime.LockOSThread()
	var previous uintptr
	if setThreadDPI.Find() == nil {
		previous, _, _ = setThreadDPI.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
		if previous == 0 {
			previous, _, _ = setThreadDPI.Call(^uintptr(2))
		} // per-monitor v1 (-3)
	}
	return func() {
		if previous != 0 {
			setThreadDPI.Call(previous)
		}
		runtime.UnlockOSThread()
	}
}

func nativeError(operation string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func Displays() ([]Display, error) {
	defer physicalCoordinates()()
	list := displayList{}
	token := nextContext.Add(1)
	displayContexts.Store(token, &list)
	defer displayContexts.Delete(token)
	ok, _, err := enumDisplayMonitors.Call(0, 0, displayCallback, token)
	runtime.KeepAlive(&list)
	if list.err != nil {
		return nil, list.err
	}
	if ok == 0 {
		return nil, nativeError("EnumDisplayMonitors", err)
	}
	sort.Slice(list.items, func(i, j int) bool {
		if list.items[i].Primary != list.items[j].Primary {
			return list.items[i].Primary
		}
		return list.items[i].ID < list.items[j].ID
	})
	return list.items, nil
}

func enumDisplay(monitor, dc, rect, param uintptr) uintptr {
	value, exists := displayContexts.Load(param)
	if !exists {
		return 0
	}
	list := value.(*displayList)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	ok, _, err := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		list.err = nativeError("GetMonitorInfoW", err)
		return 0
	}
	scale := 1.0
	if getDpiForMonitor.Find() == nil {
		var x, y uint32
		result, _, _ := getDpiForMonitor.Call(monitor, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y)))
		if result == 0 && x > 0 {
			scale = float64(x) / 96
		}
	}
	name := syscall.UTF16ToString(info.Device[:])
	list.items = append(list.items, Display{ID: name, Name: name, WorkArea: info.Work.rect(), Scale: scale, Primary: info.Flags&1 != 0})
	return 1
}

func Windows() ([]Window, error) {
	defer physicalCoordinates()()
	list := windowList{}
	token := nextContext.Add(1)
	windowContexts.Store(token, &list)
	defer windowContexts.Delete(token)
	ok, _, err := enumWindows.Call(windowCallback, token)
	runtime.KeepAlive(&list)
	if ok == 0 {
		return nil, nativeError("EnumWindows", err)
	}
	sort.Slice(list.items, func(i, j int) bool { return list.items[i].Handle < list.items[j].Handle })
	return list.items, nil
}

func enumWindow(hwnd, param uintptr) uintptr {
	if visible, _, _ := isWindowVisible.Call(hwnd); visible == 0 {
		return 1
	}
	if owner, _, _ := getWindow.Call(hwnd, 4); owner != 0 {
		return 1
	} // GW_OWNER
	exstyle, _, _ := getWindowLong.Call(hwnd, uintptr(uint32(0xffffffec))) // GWL_EXSTYLE (-20)
	style, _, _ := getWindowLong.Call(hwnd, uintptr(uint32(0xfffffff0)))   // GWL_STYLE (-16)
	if exstyle&0x80 != 0 || style&0x40000000 != 0 {
		return 1
	} // tool/child window
	var pid uint32
	thread, _, _ := getWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if thread == 0 || pid == 0 {
		return 1
	} // Window may disappear during enumeration.
	var outer, client nativeRect
	if ok, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&outer))); ok == 0 {
		return 1
	}
	if ok, _, _ := getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client))); ok == 0 {
		return 1
	}
	dpi := uint32(96)
	if getDpiForWindow.Find() == nil {
		if value, _, _ := getDpiForWindow.Call(hwnd); value > 0 {
			dpi = uint32(value)
		}
	}
	minimized, _, _ := isIconic.Call(hwnd)
	maximized, _, _ := isZoomed.Call(hwnd)
	if minimized != 0 || maximized != 0 {
		if normal, normalClient, ok := restoredGeometry(hwnd, style, exstyle, dpi); ok {
			outer, client = normal, normalClient
		}
	}
	title := make([]uint16, 32768)
	getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	executable, _ := processExecutable(pid) // Access denial for another process is not an enumeration failure.
	// Guard against a handle changing identity while collecting its metadata.
	var finalPID uint32
	getWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&finalPID)))
	if finalPID != pid {
		return 1
	}
	value, exists := windowContexts.Load(param)
	if !exists {
		return 0
	}
	list := value.(*windowList)
	list.items = append(list.items, Window{Minimized: minimized != 0, Maximized: maximized != 0, Handle: hwnd, PID: int(pid), Title: syscall.UTF16ToString(title), Executable: executable, Bounds: outer.rect(), ClientWidth: int(client.Right - client.Left), ClientHeight: int(client.Bottom - client.Top), Scale: float64(dpi) / 96})
	return 1
}

func restoredGeometry(hwnd, style, exstyle uintptr, dpi uint32) (nativeRect, nativeRect, bool) {
	placement := windowPlacement{Size: uint32(unsafe.Sizeof(windowPlacement{}))}
	if ok, _, _ := getWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&placement))); ok == 0 {
		return nativeRect{}, nativeRect{}, false
	}
	outer := placement.Normal
	// WINDOWPLACEMENT normal rectangles use workspace coordinates for main windows.
	monitor, _, _ := monitorFromWindow.Call(hwnd, 2)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
		dx, dy := info.Work.Left-info.Monitor.Left, info.Work.Top-info.Monitor.Top
		outer.Left += dx
		outer.Right += dx
		outer.Top += dy
		outer.Bottom += dy
	}
	// Frame extents must describe the restored window, not its current show state.
	style &^= 0x20000000 | 0x01000000 // WS_MINIMIZE | WS_MAXIMIZE
	frame := nativeRect{}
	menu, _, _ := getMenu.Call(hwnd)
	hasMenu := uintptr(0)
	if menu != 0 {
		hasMenu = 1
	}
	var ok uintptr
	if adjustWindowRectDPI.Find() == nil {
		ok, _, _ = adjustWindowRectDPI.Call(uintptr(unsafe.Pointer(&frame)), style, hasMenu, exstyle, uintptr(dpi))
	} else {
		ok, _, _ = adjustWindowRect.Call(uintptr(unsafe.Pointer(&frame)), style, hasMenu, exstyle)
	}
	if ok == 0 {
		return nativeRect{}, nativeRect{}, false
	}
	client := nativeRect{Right: outer.Right - outer.Left - (frame.Right - frame.Left), Bottom: outer.Bottom - outer.Top - (frame.Bottom - frame.Top)}
	return outer, client, client.Right > 0 && client.Bottom > 0
}

func processExecutable(pid uint32) (string, error) {
	process, _, err := openProcess.Call(0x1000, 0, uintptr(pid)) // PROCESS_QUERY_LIMITED_INFORMATION
	if process == 0 {
		return "", nativeError("OpenProcess", err)
	}
	defer closeHandle.Call(process)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	ok, _, err := queryProcessImage.Call(process, 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return "", nativeError("QueryFullProcessImageNameW", err)
	}
	return syscall.UTF16ToString(buffer[:size]), nil
}

func validate(window Window) error {
	if window.Handle == 0 || window.PID <= 0 || uint64(window.PID) > uint64(^uint32(0)) {
		return fmt.Errorf("invalid window handle or process identity")
	}
	var pid uint32
	thread, _, err := getWindowPID.Call(window.Handle, uintptr(unsafe.Pointer(&pid)))
	if thread == 0 {
		return nativeError("GetWindowThreadProcessId", err)
	}
	if int(pid) != window.PID {
		return fmt.Errorf("window process changed: expected %d, got %d", window.PID, pid)
	}
	if window.Executable != "" {
		executable, err := processExecutable(pid)
		if err != nil {
			return fmt.Errorf("verify window executable: %w", err)
		}
		if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(window.Executable)) {
			return fmt.Errorf("window executable changed")
		}
	}
	return nil
}

// Place queues physical-pixel positioning without activation or Z-order changes.
// A successful return means Windows accepted the asynchronous request.
func Place(window Window, bounds Rect) error {
	defer physicalCoordinates()()
	if !validRect(bounds) {
		return fmt.Errorf("invalid placement bounds")
	}
	if err := validate(window); err != nil {
		return err
	}
	minimized, _, _ := isIconic.Call(window.Handle)
	maximized, _, _ := isZoomed.Call(window.Handle)
	if minimized != 0 || maximized != 0 {
		ok, _, err := showWindowAsync.Call(window.Handle, 4) // SW_SHOWNOACTIVATE restores without focus.
		if ok == 0 {
			return nativeError("ShowWindowAsync", err)
		}
	}
	const flags = 0x4000 | 0x0010 | 0x0004 | 0x0200 // ASYNCWINDOWPOS | NOACTIVATE | NOZORDER | NOOWNERZORDER
	ok, _, err := setWindowPos.Call(window.Handle, 0, uintptr(bounds.X), uintptr(bounds.Y), uintptr(bounds.Width), uintptr(bounds.Height), flags)
	if ok == 0 {
		return nativeError("SetWindowPos", err)
	}
	return nil
}

func Activate(window Window) error {
	defer physicalCoordinates()()
	if err := validate(window); err != nil {
		return err
	}
	if minimized, _, _ := isIconic.Call(window.Handle); minimized != 0 {
		if ok, _, err := showWindowAsync.Call(window.Handle, 9); ok == 0 {
			return nativeError("ShowWindowAsync", err)
		}
	}
	if ok, _, err := setForegroundWindow.Call(window.Handle); ok == 0 {
		return nativeError("foreground activation denied by Windows", err)
	}
	return nil
}

func SetTitle(window Window, title string) error {
	if err := validate(window); err != nil {
		return err
	}
	text, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return fmt.Errorf("invalid window title: %w", err)
	}
	var result uintptr
	ok, _, err := sendMessageTimeout.Call(window.Handle, 0x000c, 0, uintptr(unsafe.Pointer(text)), 0x0022, 1000, uintptr(unsafe.Pointer(&result))) // WM_SETTEXT; ABORTIFHUNG | ERRORONEXIT
	runtime.KeepAlive(text)
	if ok == 0 {
		return nativeError("SetTitle (window may be unresponsive)", err)
	}
	if result == 0 {
		return fmt.Errorf("window rejected title")
	}
	return nil
}
