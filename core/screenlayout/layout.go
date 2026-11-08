// Package screenlayout discovers and arranges native projection windows.
package screenlayout

import "fmt"

type Rect struct{ X, Y, Width, Height int }
type Display struct {
	ID, Name string
	WorkArea Rect
	Scale    float64
	Primary  bool
}
type Window struct {
	Minimized, Maximized      bool
	Handle                    uintptr
	PID                       int
	Title, Executable         string
	Bounds                    Rect
	ClientWidth, ClientHeight int
	Scale                     float64
}

// Tile chooses a row-major grid with the largest common integer client height.
// Widths round up to preserve the client aspect ratio within one pixel. Frame
// extents remain fixed. Outer margins are omitted only if no grid fits with them.
func Tile(work Rect, windows []Window, clientHeight, gap int) ([]Rect, error) {
	if len(windows) == 0 {
		return []Rect{}, nil
	}
	if !validRect(work) || clientHeight < 0 || gap < 0 || gap > maxCoordinate {
		return nil, fmt.Errorf("invalid work area, client height or gap")
	}
	for i, w := range windows {
		if w.ClientWidth <= 0 || w.ClientHeight <= 0 || w.ClientWidth > maxCoordinate || w.ClientHeight > maxCoordinate || w.Bounds.Width < w.ClientWidth || w.Bounds.Height < w.ClientHeight || w.Bounds.Width > maxCoordinate || w.Bounds.Height > maxCoordinate {
			return nil, fmt.Errorf("window %d has invalid client or frame dimensions", i+1)
		}
	}
	bestHeight, bestColumns, margin := 0, 0, gap
	for {
		for columns := 1; columns <= len(windows); columns++ {
			hi := work.Height
			if clientHeight > 0 && clientHeight < hi {
				hi = clientHeight
			}
			lo := 0
			for lo < hi {
				mid := lo + (hi-lo+1)/2
				if gridFits(work, windows, columns, mid, gap, margin) {
					lo = mid
				} else {
					hi = mid - 1
				}
			}
			if lo > bestHeight {
				bestHeight, bestColumns = lo, columns
			}
		}
		if bestHeight > 0 || margin == 0 {
			break
		}
		margin = 0
	}
	if bestHeight == 0 {
		return nil, fmt.Errorf("work area cannot fit window frames and gap")
	}
	result := make([]Rect, 0, len(windows))
	x, y, rowHeight := work.X+margin, work.Y+margin, 0
	for i, w := range windows {
		width, height := outerSize(w, bestHeight)
		if i > 0 && i%bestColumns == 0 {
			x = work.X + margin
			y += rowHeight + gap
			rowHeight = 0
		}
		result = append(result, Rect{X: x, Y: y, Width: width, Height: height})
		x += width + gap
		if height > rowHeight {
			rowHeight = height
		}
	}
	return result, nil
}

const maxCoordinate = 1<<31 - 1

func validRect(r Rect) bool {
	return r.Width > 0 && r.Height > 0 && r.Width <= maxCoordinate && r.Height <= maxCoordinate && r.X >= -1<<31 && r.Y >= -1<<31 && r.X <= maxCoordinate-r.Width && r.Y <= maxCoordinate-r.Height
}

func outerSize(w Window, h int) (int, int) {
	width := (int64(w.ClientWidth)*int64(h) + int64(w.ClientHeight) - 1) / int64(w.ClientHeight)
	return int(width) + w.Bounds.Width - w.ClientWidth, h + w.Bounds.Height - w.ClientHeight
}

func gridFits(work Rect, windows []Window, columns, h, gap, margin int) bool {
	availableWidth, availableHeight := int64(work.Width)-2*int64(margin), int64(work.Height)-2*int64(margin)
	var rowWidth, rowHeight, totalHeight int64
	for i, w := range windows {
		if i > 0 && i%columns == 0 {
			totalHeight += rowHeight + int64(gap)
			rowWidth, rowHeight = 0, 0
		}
		width, height := outerSize(w, h)
		if i%columns != 0 {
			rowWidth += int64(gap)
		}
		rowWidth += int64(width)
		if int64(height) > rowHeight {
			rowHeight = int64(height)
		}
		if rowWidth > availableWidth || totalHeight+rowHeight > availableHeight {
			return false
		}
	}
	return true
}
