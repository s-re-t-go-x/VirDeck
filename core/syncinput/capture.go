package syncinput

import (
	"context"
	"errors"
)

type MouseEventKind int

const (
	MouseDown MouseEventKind = iota + 1
	MouseMove
	MouseUp
	MouseWheel
)

// MouseEvent represents a physical desktop mouse event captured from the host OS.
type MouseEvent struct {
	Kind    MouseEventKind
	X       int
	Y       int
	HScroll float32
	VScroll float32
}

// WindowLocator resolves host window client screen bounds and foreground ownership.
type WindowLocator interface {
	ClientScreenRect(hwnd uintptr) (Rect, bool)
	ForegroundWindow() uintptr
}

// CaptureBackend streams host mouse events and pause hotkey triggers until stopped.
type CaptureBackend interface {
	Start(ctx context.Context, onEvent func(MouseEvent), onHotkey func()) error
	Stop() error
}

// ErrCaptureUnsupported is returned on platforms without a native mouse capture hook yet.
var ErrCaptureUnsupported = errors.New("当前系统暂不支持主控鼠标全局捕获（仍可使用同步导航键与发送文本）")
