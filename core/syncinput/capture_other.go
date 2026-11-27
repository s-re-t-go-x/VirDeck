//go:build !windows

package syncinput

import "context"

// CaptureSupported reports whether host mouse capture is implemented on this OS.
func CaptureSupported() bool { return false }

// CaptureUnsupportedReason explains why host mouse capture is unavailable.
func CaptureUnsupportedReason() string {
	return ErrCaptureUnsupported.Error()
}

type noopWindowLocator struct{}

func NewNativeWindowLocator() WindowLocator { return noopWindowLocator{} }
func (noopWindowLocator) ClientScreenRect(uintptr) (Rect, bool) {
	return Rect{}, false
}
func (noopWindowLocator) ForegroundWindow() uintptr { return 0 }

type noopCapture struct{}

func NewNativeCapture() CaptureBackend { return noopCapture{} }
func (noopCapture) Start(context.Context, func(MouseEvent), func()) error {
	return ErrCaptureUnsupported
}
func (noopCapture) Stop() error { return nil }
