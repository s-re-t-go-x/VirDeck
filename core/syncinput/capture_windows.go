//go:build windows

package syncinput

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	setWindowsHookExW    = user32.NewProc("SetWindowsHookExW")
	unhookWindowsHookEx  = user32.NewProc("UnhookWindowsHookEx")
	callNextHookEx       = user32.NewProc("CallNextHookEx")
	registerHotKey       = user32.NewProc("RegisterHotKey")
	unregisterHotKey     = user32.NewProc("UnregisterHotKey")
	getMessageW          = user32.NewProc("GetMessageW")
	peekMessageW         = user32.NewProc("PeekMessageW")
	postThreadMessageW   = user32.NewProc("PostThreadMessageW")
	getForegroundWindow  = user32.NewProc("GetForegroundWindow")
	getClientRect        = user32.NewProc("GetClientRect")
	clientToScreen       = user32.NewProc("ClientToScreen")
	setThreadDPICtx      = user32.NewProc("SetThreadDpiAwarenessContext")
	getCurrentThreadID   = kernel32.NewProc("GetCurrentThreadId")
	mouseHookCallbackPtr = syscall.NewCallback(lowLevelMouseProc)
)

const (
	whMouseLL     = 14
	wmQuit        = 0x0012
	wmHotkey      = 0x0312
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmMouseWheel  = 0x020A
	wmMouseHWheel = 0x020E
	llmhfInjected = 0x00000001
	modAlt        = 0x0001
	modControl    = 0x0002
	vkP           = 0x50
	hotkeyIDPause = 1
)

type nativePoint struct {
	X, Y int32
}

type nativeRect struct {
	Left, Top, Right, Bottom int32
}

type msllHookStruct struct {
	Pt        nativePoint
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      nativePoint
	Private uint32
}

var activeCapture atomic.Pointer[winMouseCapture]

// CaptureSupported reports whether host mouse capture is implemented on this OS.
func CaptureSupported() bool { return true }

// CaptureUnsupportedReason explains why host mouse capture is unavailable.
func CaptureUnsupportedReason() string { return "" }

type nativeWindowLocator struct{}

// NewNativeWindowLocator returns a Windows Per-Monitor-V2 DPI aware window locator.
func NewNativeWindowLocator() WindowLocator { return nativeWindowLocator{} }

func (nativeWindowLocator) ForegroundWindow() uintptr {
	hwnd, _, _ := getForegroundWindow.Call()
	return hwnd
}

func (nativeWindowLocator) ClientScreenRect(hwnd uintptr) (Rect, bool) {
	if hwnd == 0 {
		return Rect{}, false
	}
	runtime.LockOSThread()
	var prev uintptr
	if setThreadDPICtx.Find() == nil {
		prev, _, _ = setThreadDPICtx.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
	}
	defer func() {
		if prev != 0 {
			setThreadDPICtx.Call(prev)
		}
		runtime.UnlockOSThread()
	}()

	var rc nativeRect
	if ok, _, _ := getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc))); ok == 0 {
		return Rect{}, false
	}
	w := int(rc.Right - rc.Left)
	h := int(rc.Bottom - rc.Top)
	if w <= 0 || h <= 0 {
		return Rect{}, false
	}
	origin := nativePoint{X: rc.Left, Y: rc.Top}
	if ok, _, _ := clientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin))); ok == 0 {
		return Rect{}, false
	}
	return Rect{X: int(origin.X), Y: int(origin.Y), Width: w, Height: h}, true
}

// NewNativeCapture creates a Windows non-admin WH_MOUSE_LL capture backend.
func NewNativeCapture() CaptureBackend {
	return &winMouseCapture{}
}

type winMouseCapture struct {
	mu            sync.Mutex
	running       bool
	threadID      uint32
	hook          uintptr
	evCh          chan MouseEvent
	hotkeyCh      chan struct{}
	readyDone     chan struct{}
	done          chan struct{}
	evCloseOnce   sync.Once
	stopped       atomic.Bool
	masterHandle  atomic.Uintptr
	pausedPtr     atomic.Pointer[atomic.Bool]
	wasForeground atomic.Bool
}

// SetMasterScope narrows mouse hook forwarding to events while masterHandle is foreground and not paused.
func (c *winMouseCapture) SetMasterScope(masterHandle uintptr, paused *atomic.Bool) {
	c.masterHandle.Store(masterHandle)
	c.pausedPtr.Store(paused)
	if masterHandle != 0 {
		fg, _, _ := getForegroundWindow.Call()
		c.wasForeground.Store(fg == masterHandle)
	}
}

func (c *winMouseCapture) Start(ctx context.Context, onEvent func(MouseEvent), onHotkey func()) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	c.running = true
	c.stopped.Store(false)
	c.evCloseOnce = sync.Once{}
	c.evCh = make(chan MouseEvent, 256)
	c.hotkeyCh = make(chan struct{}, 8)
	c.readyDone = make(chan struct{})
	c.done = make(chan struct{})
	ready := make(chan error, 1)

	activeCapture.Store(c)

	go c.dispatchLoop(ctx, onEvent, onHotkey)
	go c.hookLoop(ready)
	c.mu.Unlock()

	err := <-ready
	close(c.readyDone)
	if err != nil {
		_ = c.Stop()
		return err
	}
	return nil
}

func (c *winMouseCapture) dispatchLoop(ctx context.Context, onEvent func(MouseEvent), onHotkey func()) {
	for {
		select {
		case <-ctx.Done():
			_ = c.Stop()
			return
		case ev, ok := <-c.evCh:
			if !ok {
				return
			}
			if onEvent != nil {
				onEvent(ev)
			}
		case <-c.hotkeyCh:
			if onHotkey != nil {
				onHotkey()
			}
		}
	}
}

func (c *winMouseCapture) hookLoop(ready chan<- error) {
	defer close(c.done)
	runtime.LockOSThread()
	var prevDPI uintptr
	if setThreadDPICtx.Find() == nil {
		prevDPI, _, _ = setThreadDPICtx.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
	}
	defer func() {
		if prevDPI != 0 {
			setThreadDPICtx.Call(prevDPI)
		}
		runtime.UnlockOSThread()
	}()

	tid, _, _ := getCurrentThreadID.Call()
	c.mu.Lock()
	c.threadID = uint32(tid)
	c.mu.Unlock()

	// Ensure this thread's Win32 message queue exists before Stop() can call PostThreadMessageW.
	var msg winMsg
	peekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 0)

	hook, _, err := setWindowsHookExW.Call(whMouseLL, mouseHookCallbackPtr, 0, 0)
	if hook == 0 {
		activeCapture.CompareAndSwap(c, nil)
		ready <- fmt.Errorf("注册 Windows 低级鼠标钩子失败: %v", err)
		return
	}
	c.mu.Lock()
	c.hook = hook
	c.mu.Unlock()

	hotkeyRegistered, _, _ := registerHotKey.Call(0, hotkeyIDPause, modControl|modAlt, vkP)
	ready <- nil

	for {
		r, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if msg.Message == wmHotkey && msg.WParam == hotkeyIDPause {
			select {
			case c.hotkeyCh <- struct{}{}:
			default:
			}
		}
	}

	if hotkeyRegistered != 0 {
		unregisterHotKey.Call(0, hotkeyIDPause)
	}
	unhookWindowsHookEx.Call(hook)
	activeCapture.CompareAndSwap(c, nil)
}

func (c *winMouseCapture) enqueue(ev MouseEvent) {
	if c.stopped.Load() {
		return
	}
	if p := c.pausedPtr.Load(); p != nil && p.Load() {
		return
	}
	if mh := c.masterHandle.Load(); mh != 0 {
		fg, _, _ := getForegroundWindow.Call()
		if fg != mh {
			// Pass a single transition event so Broadcaster releases any held touch, then suppress background clicks.
			if !c.wasForeground.Swap(false) {
				return
			}
		} else {
			c.wasForeground.Store(true)
		}
	}
	select {
	case c.evCh <- ev:
	default:
		// Never block the WH_MOUSE_LL thread; drop stale events if saturated.
	}
}

func (c *winMouseCapture) Stop() error {
	c.mu.Lock()
	readyDone := c.readyDone
	done := c.done
	evCh := c.evCh
	wasRunning := c.running
	c.running = false
	c.stopped.Store(true)
	c.mu.Unlock()

	if readyDone != nil {
		<-readyDone
	}

	c.mu.Lock()
	tid := c.threadID
	c.mu.Unlock()

	if wasRunning && tid != 0 && done != nil {
		for attempt := 0; attempt < 20; attempt++ {
			ok, _, _ := postThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
			if ok != 0 {
				break
			}
			select {
			case <-done:
				attempt = 20
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	if done != nil {
		<-done
	}
	if evCh != nil {
		c.evCloseOnce.Do(func() { close(evCh) })
	}
	return nil
}

func lowLevelMouseProc(nCode int32, wParam, lParam uintptr) uintptr {
	if nCode >= 0 && lParam != 0 {
		ptr := *(*unsafe.Pointer)(unsafe.Pointer(&lParam))
		info := (*msllHookStruct)(ptr)
		if info.Flags&llmhfInjected == 0 {
			var ev MouseEvent
			emit := true
			switch uint32(wParam) {
			case wmLButtonDown:
				ev = MouseEvent{Kind: MouseDown, X: int(info.Pt.X), Y: int(info.Pt.Y)}
			case wmLButtonUp:
				ev = MouseEvent{Kind: MouseUp, X: int(info.Pt.X), Y: int(info.Pt.Y)}
			case wmMouseMove:
				ev = MouseEvent{Kind: MouseMove, X: int(info.Pt.X), Y: int(info.Pt.Y)}
			case wmMouseWheel:
				delta := float32(int16(uint16(info.MouseData>>16))) / 120.0
				ev = MouseEvent{Kind: MouseWheel, X: int(info.Pt.X), Y: int(info.Pt.Y), VScroll: delta}
			case wmMouseHWheel:
				delta := float32(int16(uint16(info.MouseData>>16))) / 120.0
				ev = MouseEvent{Kind: MouseWheel, X: int(info.Pt.X), Y: int(info.Pt.Y), HScroll: delta}
			default:
				emit = false
			}
			if emit {
				if cap := activeCapture.Load(); cap != nil {
					cap.enqueue(ev)
				}
			}
		}
	}
	ret, _, _ := callNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}
