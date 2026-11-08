package screenlayout

import (
	"fmt"
	"sort"
)

// Placement maps a packed outer window rectangle to its original input index.
type Placement struct {
	Index  int
	Bounds Rect
}

// Pack places windows at exactly clientHeight, preserving aspect ratios and
// fixed frame extents. Windows that do not fit are omitted. An optional anchor
// is also an obstacle, with space beside and below it preferred first.
func Pack(work Rect, windows []Window, clientHeight, gap int, occupied []Rect, anchor *Rect) ([]Placement, error) {
	if len(windows) == 0 {
		return []Placement{}, nil
	}
	if !validRect(work) || clientHeight <= 0 || clientHeight > maxCoordinate || gap < 0 || gap > maxCoordinate {
		return nil, fmt.Errorf("invalid work area, client height or gap")
	}
	for i, w := range windows {
		if w.ClientWidth <= 0 || w.ClientHeight <= 0 || w.ClientWidth > maxCoordinate || w.ClientHeight > maxCoordinate || !validRect(w.Bounds) || w.Bounds.Width < w.ClientWidth || w.Bounds.Height < w.ClientHeight {
			return nil, fmt.Errorf("window %d has invalid client or frame dimensions", i+1)
		}
	}
	obstacles := make([]Rect, 0, len(occupied)+len(windows)+1)
	addObstacle := func(r Rect) error {
		if r.Width < 0 || r.Height < 0 {
			return fmt.Errorf("invalid obstacle dimensions")
		}
		if r.Width == 0 || r.Height == 0 {
			return nil
		}
		if !validRect(r) {
			return fmt.Errorf("invalid obstacle coordinates or dimensions")
		}
		x, y := max(r.X, work.X), max(r.Y, work.Y)
		right, bottom := min(r.X+r.Width, work.X+work.Width), min(r.Y+r.Height, work.Y+work.Height)
		if right > x && bottom > y {
			obstacles = append(obstacles, Rect{X: x, Y: y, Width: right - x, Height: bottom - y})
		}
		return nil
	}
	for _, r := range occupied {
		if err := addObstacle(r); err != nil {
			return nil, err
		}
	}
	if anchor != nil {
		if err := addObstacle(*anchor); err != nil {
			return nil, err
		}
		if anchor.Width == 0 || anchor.Height == 0 {
			anchor = nil
		}
	}
	result := make([]Placement, 0, len(windows))
	for i, w := range windows {
		// Use 64-bit arithmetic before checking fit, including on 32-bit hosts.
		width := (int64(w.ClientWidth)*int64(clientHeight)+int64(w.ClientHeight)-1)/int64(w.ClientHeight) + int64(w.Bounds.Width-w.ClientWidth)
		height := int64(clientHeight) + int64(w.Bounds.Height-w.ClientHeight)
		if width > int64(work.Width) || height > int64(work.Height) {
			continue
		}
		if r, ok := packPosition(work, int(width), int(height), gap, obstacles, anchor); ok {
			result = append(result, Placement{Index: i, Bounds: r})
			obstacles = append(obstacles, r)
		}
	}
	return result, nil
}

func packPosition(work Rect, width, height, gap int, obstacles []Rect, anchor *Rect) (Rect, bool) {
	left, top := int64(work.X), int64(work.Y)
	right, bottom := left+int64(work.Width), top+int64(work.Height)
	w, h, g := int64(width), int64(height), int64(gap)
	clear := func(x, y int64) bool {
		if x < left || y < top || x+w > right || y+h > bottom {
			return false
		}
		for _, o := range obstacles {
			if x < int64(o.X)+int64(o.Width)+g && int64(o.X) < x+w+g && y < int64(o.Y)+int64(o.Height)+g && int64(o.Y) < y+h+g {
				return false
			}
		}
		return true
	}
	result := func(x, y int64) Rect { return Rect{X: int(x), Y: int(y), Width: width, Height: height} }
	var anchorRight, anchorBottom int64
	if anchor != nil {
		anchorRight = int64(anchor.X) + int64(anchor.Width) + g
		anchorBottom = int64(anchor.Y) + int64(anchor.Height) + g
		// Try positions aligned with the anchor before searching obstacle edges.
		for _, point := range [][2]int64{{anchorRight, max(top, int64(anchor.Y))}, {max(left, int64(anchor.X)), anchorBottom}} {
			if clear(point[0], point[1]) {
				return result(point[0], point[1]), true
			}
		}
	}
	search := func(margin int64, region int) (Rect, bool) {
		minX, minY, maxX, maxY := left+margin, top+margin, right-w-margin, bottom-h-margin
		if minX > maxX || minY > maxY {
			return Rect{}, false
		}
		xs, ys := []int64{minX, maxX}, []int64{minY, maxY}
		for _, o := range obstacles {
			xs = append(xs, int64(o.X)+int64(o.Width)+g, int64(o.X)-w-g)
			ys = append(ys, int64(o.Y)+int64(o.Height)+g, int64(o.Y)-h-g)
		}
		if anchor != nil {
			xs = append(xs, int64(anchor.X), anchorRight)
			ys = append(ys, int64(anchor.Y), anchorBottom)
		}
		xs, ys = packCoordinates(xs, minX, maxX), packCoordinates(ys, minY, maxY)
		for _, y := range ys {
			for _, x := range xs {
				if region == 1 && x < anchorRight || region == 2 && y < anchorBottom {
					continue
				}
				if clear(x, y) {
					return result(x, y), true
				}
			}
		}
		return Rect{}, false
	}
	if anchor != nil {
		for region := 1; region <= 2; region++ {
			if r, ok := search(0, region); ok {
				return r, true
			}
		}
	}
	if r, ok := search(g, 0); ok {
		return r, true
	}
	if gap > 0 {
		return search(0, 0)
	}
	return Rect{}, false
}

func packCoordinates(values []int64, minimum, maximum int64) []int64 {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	result := values[:0]
	for _, v := range values {
		if v >= minimum && v <= maximum && (len(result) == 0 || v != result[len(result)-1]) {
			result = append(result, v)
		}
	}
	return result
}
