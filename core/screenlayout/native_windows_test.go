//go:build windows

package screenlayout

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var fixtureUser32 = syscall.NewLazyDLL("user32.dll")

func fixtureCall(name string, args ...uintptr) uintptr {
	r, _, _ := fixtureUser32.NewProc(name).Call(args...)
	return r
}

func TestMutationsRejectMissingIdentity(t *testing.T) {
	for _, w := range []Window{{}, {Handle: 1}, {Handle: 1, PID: 1234}} {
		if err := Place(w, Rect{Width: 200, Height: 300}); err == nil {
			t.Fatal("Place accepted invalid identity")
		}
		if err := Activate(w); err == nil {
			t.Fatal("Activate accepted invalid identity")
		}
		if err := SetTitle(w, "test"); err == nil {
			t.Fatal("SetTitle accepted invalid identity")
		}
	}
}

func TestNativeOwnWindowSmoke(t *testing.T) {
	if os.Getenv("VIRDECK_NATIVE_SCREENLAYOUT_TEST") != "1" {
		t.Skip("set VIRDECK_NATIVE_SCREENLAYOUT_TEST=1 for own offscreen windows")
	}
	hwnd, owned, tool, closeFixture := newFixture(t)
	defer closeFixture()
	displays, err := Displays()
	if err != nil || len(displays) == 0 {
		t.Fatalf("Displays()=%+v,%v", displays, err)
	}
	for _, d := range displays {
		if d.ID == "" || d.Scale <= 0 || d.WorkArea.Width <= 0 {
			t.Fatalf("invalid display: %+v", d)
		}
	}
	foreground := fixtureCall("GetForegroundWindow")
	var w Window
	waitFor(t, func() bool {
		windows, e := Windows()
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range windows {
			if item.Handle == owned || item.Handle == tool {
				t.Fatalf("enumerated owned/tool window: %+v", item)
			}
			if item.Handle == hwnd {
				w = item
				return true
			}
		}
		return false
	})

	if w.PID != os.Getpid() || w.ClientWidth <= 0 || w.ClientHeight <= 0 || w.Executable == "" || w.Scale <= 0 {
		t.Fatalf("incomplete snapshot: %+v", w)
	}
	mismatch := w
	mismatch.PID++
	if err := Place(mismatch, Rect{X: -29000, Y: -29000, Width: 260, Height: 340}); err == nil {
		t.Fatal("Place accepted wrong PID")
	}
	if err := Activate(mismatch); err == nil {
		t.Fatal("Activate accepted wrong PID")
	}
	if err := SetTitle(mismatch, "wrong"); err == nil {
		t.Fatal("SetTitle accepted wrong PID")
	}
	mismatch = w
	mismatch.Executable += ".wrong"
	if err := Place(mismatch, Rect{Width: 260, Height: 340}); err == nil {
		t.Fatal("Place accepted wrong executable")
	}
	if err := SetTitle(w, "VirDeck native fixture renamed"); err != nil {
		t.Fatal(err)
	}
	if err := SetTitle(w, "bad\x00title"); err == nil {
		t.Fatal("accepted embedded NUL")
	}
	want := Rect{X: -29000, Y: -29000, Width: 280, Height: 360}
	if err := Place(w, want); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		windows, e := Windows()
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range windows {
			if item.Handle == hwnd {
				return item.Bounds == want && item.Title == "VirDeck native fixture renamed"
			}
		}
		return false
	})
	if fixtureCall("GetForegroundWindow") != foreground {
		t.Fatal("placement stole foreground")
	}
	fixtureCall("ShowWindowAsync", hwnd, 6) // SW_MINIMIZE does not activate the fixture.
	waitFor(t, func() bool { return fixtureCall("IsIconic", hwnd) != 0 })
	windows, err := Windows()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range windows {
		if item.Handle == hwnd {
			found = true
			if item.ClientWidth <= 0 || item.ClientHeight <= 0 || item.Bounds.Width != 280 || item.Bounds.Height != 360 {
				t.Fatalf("bad minimized geometry: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("minimized window omitted")
	}
	if err := Place(w, Rect{X: -29000, Y: -29000, Width: 300, Height: 380}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		if fixtureCall("IsIconic", hwnd) != 0 {
			return false
		}
		windows, e := Windows()
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range windows {
			if item.Handle == hwnd {
				return item.Bounds == (Rect{X: -29000, Y: -29000, Width: 300, Height: 380})
			}
		}
		return false
	})
	if fixtureCall("GetForegroundWindow") != foreground {
		t.Fatal("restoration stole foreground")
	}
	closeFixture()
	if err := Place(w, want); err == nil {
		t.Fatal("accepted destroyed handle")
	}
	if err := SetTitle(w, "stale"); err == nil {
		t.Fatal("renamed destroyed handle")
	}
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native state did not settle")
}

func newFixture(t *testing.T) (uintptr, uintptr, uintptr, func()) {
	t.Helper()
	type handles struct {
		main, owned, tool uintptr
		err               string
	}
	ready := make(chan handles, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		dpiProc := fixtureUser32.NewProc("SetThreadDpiAwarenessContext")
		if dpiProc.Find() == nil {
			old, _, _ := dpiProc.Call(^uintptr(3))
			if old != 0 {
				defer dpiProc.Call(old)
			}
		}
		defer runtime.UnlockOSThread()
		defer close(done)
		class, _ := syscall.UTF16PtrFromString("STATIC")
		title, _ := syscall.UTF16PtrFromString("VirDeck native fixture")
		create := func(ex, style, owner uintptr) uintptr {
			return fixtureCall("CreateWindowExW", ex, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), style, uintptr(uint32(0xffff8ad0)), uintptr(uint32(0xffff8ad0)), 240, 320, owner, 0, 0, 0)
		}
		main := create(0x08000000, 0x10cf0000, 0) // WS_EX_NOACTIVATE; visible overlapped window, offscreen.
		owned := create(0x08000000, 0x10cf0000, main)
		tool := create(0x08000080, 0x10cf0000, 0)
		defer fixtureCall("DestroyWindow", main)
		defer fixtureCall("DestroyWindow", tool)
		defer fixtureCall("DestroyWindow", owned)
		if main == 0 || owned == 0 || tool == 0 {
			ready <- handles{err: "CreateWindowExW failed"}
			return
		}
		ready <- handles{main: main, owned: owned, tool: tool}
		defer fixtureCall("DestroyWindow", owned)
		defer fixtureCall("DestroyWindow", tool)
		defer fixtureCall("DestroyWindow", main)
		// MSG is pointer-aligned and contains a trailing private DWORD on Windows.
		var msg struct {
			hwnd           uintptr
			message        uint32
			wparam, lparam uintptr
			time           uint32
			x, y           int32
			private        uint32
		}
		for {
			select {
			case <-stop:
				return
			default:
			}
			for fixtureCall("PeekMessageW", uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1) != 0 {
				fixtureCall("TranslateMessage", uintptr(unsafe.Pointer(&msg)))
				fixtureCall("DispatchMessageW", uintptr(unsafe.Pointer(&msg)))
			}
			time.Sleep(time.Millisecond)
		}
	}()
	h := <-ready
	if strings.TrimSpace(h.err) != "" {
		t.Fatal(h.err)
	}
	var once sync.Once
	return h.main, h.owned, h.tool, func() { once.Do(func() { close(stop); <-done }) }
}

// A synthetic WS_MAXIMIZE state exercises real placement/client queries without
// ever asking Windows to maximize a fixture onto a user's display.
func TestNativeMaximizedSnapshotUsesRestoredPortraitGeometry(t *testing.T) {
	if os.Getenv("VIRDECK_NATIVE_SCREENLAYOUT_TEST") != "1" {
		t.Skip("set VIRDECK_NATIVE_SCREENLAYOUT_TEST=1 for own offscreen windows")
	}
	hwnd, _, _, cleanup := newFixture(t)
	defer cleanup()
	defer physicalCoordinates()()
	foreground := fixtureCall("GetForegroundWindow")
	snapshot := func() Window {
		windows, err := Windows()
		if err != nil {
			t.Fatal(err)
		}
		for _, window := range windows {
			if window.Handle == hwnd {
				return window
			}
		}
		t.Fatal("fixture missing from Windows")
		return Window{}
	}
	initial := snapshot()
	normalBounds := Rect{X: -29000, Y: -29000, Width: 240, Height: 320}
	if err := Place(initial, normalBounds); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return snapshot().Bounds == normalBounds })
	before := snapshot()
	var originalPlacement windowPlacement
	originalPlacement.Size = uint32(unsafe.Sizeof(originalPlacement))
	fixtureCall("GetWindowPlacement", hwnd, uintptr(unsafe.Pointer(&originalPlacement)))

	if before.ClientWidth >= before.ClientHeight {
		t.Fatalf("fixture must start portrait: %+v", before)
	}
	style := fixtureCall("GetWindowLongW", hwnd, uintptr(uint32(0xfffffff0)))
	fixtureCall("SetWindowLongW", hwnd, uintptr(uint32(0xfffffff0)), style|0x01000000) // WS_MAXIMIZE, no show-state request.
	x, y := -28000, -28000
	if ok := fixtureCall("SetWindowPos", hwnd, 0, uintptr(x), uintptr(y), 640, 240, 0x4000|0x0010|0x0004|0x0020); ok == 0 {
		t.Fatal("could not resize synthetic maximized fixture")
	}
	waitFor(t, func() bool {
		var client nativeRect
		fixtureCall("GetClientRect", hwnd, uintptr(unsafe.Pointer(&client)))
		return client.Right > client.Bottom
	})
	if fixtureCall("IsZoomed", hwnd) == 0 {
		t.Fatal("fixture lost maximized style")
	}
	var placement windowPlacement
	placement.Size = uint32(unsafe.Sizeof(placement))
	if fixtureCall("GetWindowPlacement", hwnd, uintptr(unsafe.Pointer(&placement))) == 0 {
		t.Fatal("missing normal placement")
	}
	if placement.Normal != originalPlacement.Normal {
		t.Fatalf("synthetic state did not retain original normal dimensions: %+v versus %+v", placement.Normal, before.Bounds)
	}

	// Tile consumes dimensions; synthetic show-state changes do not exercise actual restore positioning.
	got := snapshot()
	if got.Bounds.Width != before.Bounds.Width || got.Bounds.Height != before.Bounds.Height || got.ClientWidth != before.ClientWidth || got.ClientHeight != before.ClientHeight {
		t.Fatalf("maximized snapshot lost restored portrait geometry: got %+v, want %+v", got, before)
	}
	if fixtureCall("GetForegroundWindow") != foreground {
		t.Fatal("synthetic maximized snapshot changed foreground")
	}
}
