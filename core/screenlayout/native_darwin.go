//go:build darwin

package screenlayout

import (
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type cgPoint struct{ X, Y float64 }
type cgSize struct{ Width, Height float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

var mac struct {
	once            sync.Once
	err             error
	release         func(uintptr)
	retain          func(uintptr) uintptr
	stringCreate    func(uintptr, string, uint32) uintptr
	stringGet       func(uintptr, *byte, int64, uint32) bool
	arrayCount      func(uintptr) int64
	arrayAt         func(uintptr, int64) uintptr
	dictionaryGet   func(uintptr, uintptr) uintptr
	numberGet       func(uintptr, int32, unsafe.Pointer) bool
	rectGet         func(uintptr, *cgRect) bool
	windowList      func(uint32, uint32) uintptr
	activeDisplays  func(uint32, *uint32, *uint32) int32
	displayBounds   func(uint32) cgRect
	mainDisplay     func() uint32
	pidPath         func(int32, unsafe.Pointer, uint32) int32
	trusted         func() bool
	booleanGet      func(uintptr) bool
	axApp           func(int32) uintptr
	axCopy          func(uintptr, uintptr, *uintptr) int32
	axSet           func(uintptr, uintptr, uintptr) int32
	axAction        func(uintptr, uintptr) int32
	axTimeout       func(uintptr, float32) int32
	axValueCreate   func(uint32, unsafe.Pointer) uintptr
	axValueGet      func(uintptr, uint32, unsafe.Pointer) bool
	cfTrue, cfFalse uintptr
}

func macInit() error {
	mac.once.Do(func() {
		libraries := map[string]uintptr{}
		for _, path := range []string{"/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", "/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", "/System/Library/Frameworks/AppKit.framework/AppKit", "/usr/lib/libproc.dylib"} {
			h, e := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_LOCAL)
			if e != nil {
				mac.err = e
				return
			}
			libraries[filepath.Base(path)] = h
		}
		bind := func(lib, name string, f any) {
			if mac.err != nil {
				return
			}
			p, e := purego.Dlsym(libraries[lib], name)
			if e != nil {
				mac.err = e
				return
			}
			purego.RegisterFunc(f, p)
		}
		bind("CoreFoundation", "CFRelease", &mac.release)
		bind("CoreFoundation", "CFBooleanGetValue", &mac.booleanGet)
		bind("CoreFoundation", "CFRetain", &mac.retain)
		bind("CoreFoundation", "CFStringCreateWithCString", &mac.stringCreate)
		bind("CoreFoundation", "CFStringGetCString", &mac.stringGet)
		bind("CoreFoundation", "CFArrayGetCount", &mac.arrayCount)
		bind("CoreFoundation", "CFArrayGetValueAtIndex", &mac.arrayAt)
		bind("CoreFoundation", "CFDictionaryGetValue", &mac.dictionaryGet)
		bind("CoreFoundation", "CFNumberGetValue", &mac.numberGet)
		bind("CoreGraphics", "CGRectMakeWithDictionaryRepresentation", &mac.rectGet)
		bind("CoreGraphics", "CGWindowListCopyWindowInfo", &mac.windowList)
		bind("CoreGraphics", "CGGetActiveDisplayList", &mac.activeDisplays)
		bind("CoreGraphics", "CGDisplayBounds", &mac.displayBounds)
		bind("CoreGraphics", "CGMainDisplayID", &mac.mainDisplay)
		bind("libproc.dylib", "proc_pidpath", &mac.pidPath)
		bind("ApplicationServices", "AXIsProcessTrusted", &mac.trusted)
		bind("ApplicationServices", "AXUIElementCreateApplication", &mac.axApp)
		bind("ApplicationServices", "AXUIElementCopyAttributeValue", &mac.axCopy)
		bind("ApplicationServices", "AXUIElementSetAttributeValue", &mac.axSet)
		bind("ApplicationServices", "AXUIElementPerformAction", &mac.axAction)
		bind("ApplicationServices", "AXUIElementSetMessagingTimeout", &mac.axTimeout)
		bind("ApplicationServices", "AXValueCreate", &mac.axValueCreate)
		bind("ApplicationServices", "AXValueGetValue", &mac.axValueGet)
		if mac.err == nil {
			nsNumber := objc.ID(objc.GetClass("NSNumber"))
			sel := objc.RegisterName("numberWithBool:")
			mac.cfTrue = uintptr(nsNumber.Send(sel, true))
			mac.cfFalse = uintptr(nsNumber.Send(sel, false))
		}
	})
	return mac.err
}
func cfString(s string) uintptr { return mac.stringCreate(0, s, 0x08000100) }
func cfText(v uintptr) string {
	if v == 0 {
		return ""
	}
	b := make([]byte, 32768)
	if !mac.stringGet(v, &b[0], int64(len(b)), 0x08000100) {
		return ""
	}
	return strings.TrimRight(string(b), "\x00")
}
func cfDict(d uintptr, key string) uintptr {
	k := cfString(key)
	defer mac.release(k)
	return mac.dictionaryGet(d, k)
}
func cfInt(v uintptr) int64 {
	var n int64
	if v != 0 {
		mac.numberGet(v, 4, unsafe.Pointer(&n))
	}
	return n
}
func processPath(pid int) string {
	b := make([]byte, 4096)
	n := mac.pidPath(int32(pid), unsafe.Pointer(&b[0]), uint32(len(b)))
	if n <= 0 {
		return ""
	}
	path := strings.TrimRight(string(b[:n]), "\x00")
	if p, e := filepath.EvalSymlinks(path); e == nil {
		path = p
	}
	return filepath.Clean(path)
}
func macPermission() error {
	if err := macInit(); err != nil {
		return err
	}
	if !mac.trusted() {
		return fmt.Errorf("请在 macOS 系统设置 → 隐私与安全性 → 辅助功能中允许 VirDeck（终端启动时允许对应终端），然后重新打开应用")
	}
	return nil
}
func Displays() ([]Display, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := macInit(); err != nil {
		return nil, err
	}
	var ids [64]uint32
	var count uint32
	if code := mac.activeDisplays(uint32(len(ids)), &ids[0], &count); code != 0 {
		return nil, fmt.Errorf("CGGetActiveDisplayList: %d", code)
	}
	main := mac.mainDisplay()
	height := mac.displayBounds(main).Size.Height
	// NSScreen.visibleFrame excludes Dock/menu bar and is in Cocoa bottom-left points.
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	screens := objc.ID(objc.GetClass("NSScreen")).Send(objc.RegisterName("screens"))
	key := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "NSScreenNumber")
	visible := map[uint32]Rect{}
	for i, n := uint64(0), objc.Send[uint64](screens, objc.RegisterName("count")); i < n; i++ {
		s := screens.Send(objc.RegisterName("objectAtIndex:"), i)
		d := s.Send(objc.RegisterName("deviceDescription")).Send(objc.RegisterName("objectForKey:"), key)
		id := objc.Send[uint32](d, objc.RegisterName("unsignedIntValue"))
		r := objc.Send[cgRect](s, objc.RegisterName("visibleFrame"))
		visible[id] = darwinVisibleRect(height, r.Origin.X, r.Origin.Y, r.Size.Width, r.Size.Height)
	}
	var out []Display
	for _, id := range ids[:count] {
		r, ok := visible[id]
		if !ok {
			return nil, fmt.Errorf("显示器 %d 暂无可用工作区，请刷新", id)
		}
		out = append(out, Display{ID: fmt.Sprint(id), Name: fmt.Sprintf("Display %d", id), WorkArea: r, Scale: 1, Primary: id == main})
	}
	return out, nil
}
func Windows() ([]Window, error) {
	if err := macPermission(); err != nil {
		return nil, err
	}
	list := mac.windowList(16, 0)
	if list == 0 {
		return nil, fmt.Errorf("CGWindowListCopyWindowInfo failed")
	}
	defer mac.release(list)
	var out []Window
	for i, n := int64(0), mac.arrayCount(list); i < n; i++ {
		d := mac.arrayAt(list, i)
		if cfInt(cfDict(d, "kCGWindowLayer")) != 0 {
			continue
		}
		var r cgRect
		if !mac.rectGet(cfDict(d, "kCGWindowBounds"), &r) {
			continue
		}
		w := Window{Handle: uintptr(cfInt(cfDict(d, "kCGWindowNumber"))), PID: int(cfInt(cfDict(d, "kCGWindowOwnerPID"))), Title: cfText(cfDict(d, "kCGWindowName")), Scale: 1}
		w.Bounds = Rect{int(math.Round(r.Origin.X)), int(math.Round(r.Origin.Y)), int(math.Round(r.Size.Width)), int(math.Round(r.Size.Height))}
		w.Executable = processPath(w.PID)
		if w.Handle == 0 || w.Executable == "" || !validRect(w.Bounds) {
			continue
		}
		// AX supplies titles without requiring Screen Recording access, plus the
		// actual content area; refuse ambiguity instead of targeting by title alone.
		ax, err := macAXWindow(w)
		if err != nil {
			continue
		}
		if title, err := axCopy(ax, "AXTitle"); err == nil {
			w.Title = cfText(title)
			mac.release(title)
		}
		if minimized, e := axCopy(ax, "AXMinimized"); e == nil {
			w.Minimized = mac.booleanGet(minimized)
			mac.release(minimized)
		}
		w.ClientWidth, w.ClientHeight = w.Bounds.Width, w.Bounds.Height
		if content, err := axCopy(ax, "AXContents"); err == nil {
			if mac.arrayCount(content) > 0 {
				if size, err := axCopy(mac.arrayAt(content, 0), "AXSize"); err == nil {
					var s cgSize
					if mac.axValueGet(size, 2, unsafe.Pointer(&s)) && s.Width > 0 && s.Height > 0 && s.Width <= float64(w.Bounds.Width) && s.Height <= float64(w.Bounds.Height) {
						w.ClientWidth = int(s.Width)
						w.ClientHeight = int(s.Height)
					}
					mac.release(size)
				}
			}
			mac.release(content)
		}
		mac.release(ax)
		out = append(out, w)
	}
	return out, nil
}
func axCopy(element uintptr, name string) (uintptr, error) {
	if code := mac.axTimeout(element, 0.5); code != 0 {
		return 0, fmt.Errorf("AX timeout setup: %d", code)
	}
	key := cfString(name)
	defer mac.release(key)
	var value uintptr
	if code := mac.axCopy(element, key, &value); code != 0 {
		return 0, fmt.Errorf("%s: Accessibility error %d", name, code)
	}
	return value, nil
}

// Match AX windows against CG owner, position and size, and title when CG makes
// it available. No private _AXUIElementGetWindow symbol or title-only matching.
func macAXWindow(w Window) (uintptr, error) {
	if w.PID <= 0 || w.Handle == 0 || w.Executable == "" || processPath(w.PID) != w.Executable {
		return 0, fmt.Errorf("投屏进程身份已变化")
	}
	app := mac.axApp(int32(w.PID))
	if app == 0 {
		return 0, fmt.Errorf("AX application unavailable")
	}
	defer mac.release(app)
	windows, err := axCopy(app, "AXWindows")
	if err != nil {
		return 0, err
	}
	defer mac.release(windows)
	var match uintptr
	matches := 0
	for i, n := int64(0), mac.arrayCount(windows); i < n; i++ {
		a := mac.arrayAt(windows, i)
		p, e := axCopy(a, "AXPosition")
		if e != nil {
			continue
		}
		var point cgPoint
		ok := mac.axValueGet(p, 1, unsafe.Pointer(&point))
		mac.release(p)
		if !ok {
			continue
		}
		s, e := axCopy(a, "AXSize")
		if e != nil {
			continue
		}
		var size cgSize
		ok = mac.axValueGet(s, 2, unsafe.Pointer(&size))
		mac.release(s)
		if !ok {
			continue
		}
		if int(math.Round(point.X)) != w.Bounds.X || int(math.Round(point.Y)) != w.Bounds.Y || int(math.Round(size.Width)) != w.Bounds.Width || int(math.Round(size.Height)) != w.Bounds.Height {
			continue
		}
		if w.Title != "" {
			v, e := axCopy(a, "AXTitle")
			if e != nil {
				continue
			}
			title := cfText(v)
			mac.release(v)
			if title != w.Title {
				continue
			}
		}
		match = a
		matches++
	}
	if matches != 1 {
		return 0, fmt.Errorf("无法唯一识别投屏辅助功能窗口（%d 个匹配），请刷新后重试", matches)
	}
	// Retain by retrieving the same element via a retained array is insufficient
	// after releasing that array; retain the matched element explicitly.
	result := mac.retain(match)
	return result, nil
}
func validatedAX(w Window) (uintptr, error) {
	if err := macPermission(); err != nil {
		return 0, err
	}
	// Re-enumerate CG independently before every mutation, rejecting reused IDs.
	list := mac.windowList(16, 0)
	if list == 0 {
		return 0, fmt.Errorf("无法验证投屏窗口")
	}
	defer mac.release(list)
	for i, n := int64(0), mac.arrayCount(list); i < n; i++ {
		d := mac.arrayAt(list, i)
		current := Window{Handle: uintptr(cfInt(cfDict(d, "kCGWindowNumber"))), PID: int(cfInt(cfDict(d, "kCGWindowOwnerPID")))}
		if current.Handle != w.Handle {
			continue
		}
		current.Executable = processPath(current.PID)
		if !darwinSameWindow(w, current) {
			break
		}
		var r cgRect
		if !mac.rectGet(cfDict(d, "kCGWindowBounds"), &r) {
			break
		}
		current.Bounds = Rect{int(math.Round(r.Origin.X)), int(math.Round(r.Origin.Y)), int(math.Round(r.Size.Width)), int(math.Round(r.Size.Height))}
		current.Title = cfText(cfDict(d, "kCGWindowName"))
		return macAXWindow(current)
	}
	return 0, fmt.Errorf("投屏窗口已关闭或身份已变化")
}
func axSet(element uintptr, name string, value uintptr) error {
	if code := mac.axTimeout(element, 0.5); code != 0 {
		return fmt.Errorf("AX timeout setup: %d", code)
	}
	key := cfString(name)
	defer mac.release(key)
	if code := mac.axSet(element, key, value); code != 0 {
		return fmt.Errorf("%s: Accessibility error %d（目标窗口可能不允许此操作）", name, code)
	}
	return nil
}
func Place(w Window, r Rect) error {
	if !validRect(r) {
		return fmt.Errorf("invalid window bounds")
	}
	a, err := validatedAX(w)
	if err != nil {
		return err
	}
	defer mac.release(a)
	if err = axSet(a, "AXMinimized", mac.cfFalse); err != nil {
		return err
	}
	point, size := cgPoint{float64(r.X), float64(r.Y)}, cgSize{float64(r.Width), float64(r.Height)}
	p := mac.axValueCreate(1, unsafe.Pointer(&point))
	s := mac.axValueCreate(2, unsafe.Pointer(&size))
	defer mac.release(p)
	defer mac.release(s)
	if err = axSet(a, "AXSize", s); err != nil {
		return err
	}
	return axSet(a, "AXPosition", p)
}
func Activate(w Window) error {
	a, err := validatedAX(w)
	if err != nil {
		return err
	}
	defer mac.release(a)
	if err = axSet(a, "AXMinimized", mac.cfFalse); err != nil {
		return err
	}
	app := mac.axApp(int32(w.PID))
	defer mac.release(app)
	if err = axSet(app, "AXFrontmost", mac.cfTrue); err != nil {
		return err
	}
	return axPerform(a, "AXRaise")
}
func SetTitle(w Window, title string) error {
	if strings.ContainsRune(title, 0) {
		return fmt.Errorf("invalid title")
	}
	a, err := validatedAX(w)
	if err != nil {
		return err
	}
	defer mac.release(a)
	value := cfString(title)
	defer mac.release(value)
	return axSet(a, "AXTitle", value)
}
func axPerform(a uintptr, action string) error {
	if code := mac.axTimeout(a, 0.5); code != 0 {
		return fmt.Errorf("AX timeout setup: %d", code)
	}
	key := cfString(action)
	defer mac.release(key)
	if code := mac.axAction(a, key); code != 0 {
		return fmt.Errorf("%s: Accessibility error %d", action, code)
	}
	return nil
}
