package screenlayout

import "math"

// Darwin uses logical display points throughout: CG window bounds and AX
// position/size share this coordinate space. Scale is therefore 1, including
// Retina displays; multiplying coordinates by backingScaleFactor is incorrect.
func darwinVisibleRect(mainHeight, x, y, width, height float64) Rect {
	return Rect{int(math.Round(x)), int(math.Round(mainHeight - y - height)), int(math.Round(width)), int(math.Round(height))}
}
func darwinSameWindow(a, b Window) bool {
	return a.Handle != 0 && a.PID > 0 && a.Executable != "" && a.Handle == b.Handle && a.PID == b.PID && a.Executable == b.Executable
}
