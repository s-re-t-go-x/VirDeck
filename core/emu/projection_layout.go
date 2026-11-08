package emu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
	"virdeck/core/screenlayout"
)

type ScreenLayoutOptions struct {
	DisplayID, ReferenceID string
	Height, Gap            int // logical pixels, zero height uses current window height
	FillOnly               bool
}
type SavedProjection struct {
	InstanceID string            `json:"instanceId"`
	DisplayID  string            `json:"displayId"`
	Bounds     screenlayout.Rect `json:"bounds"`
	WorkArea   screenlayout.Rect `json:"workArea"`
	Scale      float64           `json:"scale"`
}
type projectionReservation struct {
	bounds                    screenlayout.Rect
	clientWidth, clientHeight int
	scale                     float64
	handle                    uintptr
	pid                       int
	until                     time.Time
	initial                   screenlayout.Rect
}

func (c *Controller) ScrcpyWindows() ([]ProjectionWindow, error) {
	c.layoutMu.Lock()
	defer c.layoutMu.Unlock()
	return c.layoutWindows()
}

func ProjectionDisplay(w screenlayout.Window, displays []screenlayout.Display) *screenlayout.Display {
	var best *screenlayout.Display
	var area int64
	for i, d := range displays {
		width := min(int64(w.Bounds.X)+int64(w.Bounds.Width), int64(d.WorkArea.X)+int64(d.WorkArea.Width)) - max(int64(w.Bounds.X), int64(d.WorkArea.X))
		height := min(int64(w.Bounds.Y)+int64(w.Bounds.Height), int64(d.WorkArea.Y)+int64(d.WorkArea.Height)) - max(int64(w.Bounds.Y), int64(d.WorkArea.Y))
		if width > 0 && height > 0 && width*height > area {
			best = &displays[i]
			area = width * height
		}
	}
	return best
}
func windowScale(s float64) float64 {
	if s <= 0 || math.IsInf(s, 0) || math.IsNaN(s) {
		return 1
	}
	return s
}
func rectWithin(r, work screenlayout.Rect) bool {
	return r.Width > 0 && r.Height > 0 && r.X >= work.X && r.Y >= work.Y && int64(r.X)+int64(r.Width) <= int64(work.X)+int64(work.Width) && int64(r.Y)+int64(r.Height) <= int64(work.Y)+int64(work.Height)
}
func (c *Controller) layoutWindows() ([]ProjectionWindow, error) {
	windows, err := c.scrcpyWindows()
	if err != nil {
		return nil, err
	}
	for id, r := range c.reservations {
		if time.Now().After(r.until) {
			delete(c.reservations, id)
		}
	}
	for i, p := range windows {
		r, ok := c.reservations[p.InstanceID]
		if !ok {
			continue
		}
		if r.pid != p.Window.PID || r.handle != p.Window.Handle || p.Window.Bounds == r.bounds || p.Window.Bounds != r.initial {
			delete(c.reservations, p.InstanceID)
			continue
		}
		w := &windows[i].Window
		w.Bounds = r.bounds
		w.ClientWidth = r.clientWidth
		w.ClientHeight = r.clientHeight
		w.Scale = r.scale
		w.Minimized = false
		w.Maximized = false
	}
	return windows, nil
}
func (c *Controller) placeProjection(p ProjectionWindow, r screenlayout.Rect, scale float64) error {
	if err := c.projectionHost().place(p.Window, r); err != nil {
		return err
	}
	if c.reservations == nil {
		c.reservations = map[string]projectionReservation{}
	}
	ratio := windowScale(scale) / windowScale(p.Window.Scale)
	frameW := int(math.Ceil(float64(max(0, p.Window.Bounds.Width-p.Window.ClientWidth)) * ratio))
	frameH := int(math.Ceil(float64(max(0, p.Window.Bounds.Height-p.Window.ClientHeight)) * ratio))
	initial := p.Window.Bounds
	if previous, ok := c.reservations[p.InstanceID]; ok && previous.pid == p.Window.PID && previous.handle == p.Window.Handle {
		initial = previous.initial
	}
	c.reservations[p.InstanceID] = projectionReservation{initial: initial, bounds: r, clientWidth: max(1, r.Width-frameW), clientHeight: max(1, r.Height-frameH), scale: windowScale(scale), pid: p.Window.PID, handle: p.Window.Handle, until: time.Now().Add(2 * time.Second)}
	return nil
}
func occupiedProjection(p ProjectionWindow, displays []screenlayout.Display) screenlayout.Rect {
	if p.Window.Maximized {
		if d := ProjectionDisplay(p.Window, displays); d != nil {
			return d.WorkArea
		}
	}
	return p.Window.Bounds
}
func (c *Controller) ArrangeProjection(ctx context.Context, ids []string, opt ScreenLayoutOptions) (int, error) {
	c.layoutMu.Lock()
	defer c.layoutMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	displays, err := c.ScrcpyDisplays()
	if err != nil {
		return 0, err
	}
	open, err := c.layoutWindows()
	if err != nil {
		return 0, err
	}
	byID := map[string]ProjectionWindow{}
	for _, p := range open {
		if _, ok := byID[p.InstanceID]; !ok {
			byID[p.InstanceID] = p
		}
	}
	var target *screenlayout.Display
	for i, d := range displays {
		if d.ID == opt.DisplayID || opt.DisplayID == "" && d.Primary {
			target = &displays[i]
			break
		}
	}
	if target == nil {
		return 0, errors.New("目标显示器已不可用，请刷新显示器列表")
	}
	var anchor *screenlayout.Rect
	ref, hasRef := byID[opt.ReferenceID]
	if opt.ReferenceID != "" && !hasRef && !opt.FillOnly {
		return 0, errors.New("基准投屏已关闭，请重新选择")
	}
	if hasRef {
		d := ProjectionDisplay(ref.Window, displays)
		if d == nil || d.ID != target.ID {
			return 0, errors.New("请将目标显示器选为基准窗口所在的显示器")
		}
		r := ref.Window.Bounds
		if ref.Window.Maximized {
			r = target.WorkArea
		}
		anchor = &r
	}
	if ids == nil {
		for id := range byID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	var windows []ProjectionWindow
	chosen := map[string]bool{}
	for _, id := range ids {
		p, ok := byID[id]
		if ok && !chosen[id] && id != opt.ReferenceID {
			windows = append(windows, p)
			chosen[id] = true
		}
	}
	if len(windows) == 0 {
		if hasRef {
			return 0, nil
		}
		return 0, errors.New("没有符合范围的已打开投屏，请先打开投屏窗口")
	}
	scale := windowScale(target.Scale)
	height := int(math.Round(float64(opt.Height) * scale))
	if hasRef {
		height = ref.Window.ClientHeight
	} else if height <= 0 {
		height = int(math.Round(float64(windows[0].Window.ClientHeight) * scale / windowScale(windows[0].Window.Scale)))
	}
	gap := int(math.Round(float64(opt.Gap) * scale))
	var occupied []screenlayout.Rect
	for _, p := range open {
		if !chosen[p.InstanceID] && p.InstanceID != opt.ReferenceID {
			r := p.Window.Bounds
			if p.Window.Maximized {
				if d := ProjectionDisplay(p.Window, displays); d != nil {
					r = d.WorkArea
				}
			}
			occupied = append(occupied, r)
		}
	}
	geometry := make([]screenlayout.Window, len(windows))
	for i, p := range windows {
		w := p.Window
		ratio := scale / windowScale(w.Scale)
		w.Bounds.Width = w.ClientWidth + int(math.Ceil(float64(max(0, w.Bounds.Width-w.ClientWidth))*ratio))
		w.Bounds.Height = w.ClientHeight + int(math.Ceil(float64(max(0, w.Bounds.Height-w.ClientHeight))*ratio))
		geometry[i] = w
	}
	// Reserve original rectangles of windows that cannot fit, then replan the rest.
	active := make([]int, len(windows))
	for i := range active {
		active[i] = i
	}
	placements := map[int]screenlayout.Rect{}
	skipped := 0
	for len(active) > 0 {
		input := make([]screenlayout.Window, len(active))
		for i, index := range active {
			input[i] = geometry[index]
		}
		packed, err := screenlayout.Pack(target.WorkArea, input, height, gap, occupied, anchor)
		if err != nil {
			return 0, err
		}
		found := map[int]screenlayout.Rect{}
		for _, p := range packed {
			found[p.Index] = p.Bounds
		}
		if len(found) == len(active) {
			for i, r := range found {
				placements[active[i]] = r
			}
			break
		}
		var next []int
		for i, index := range active {
			if _, ok := found[i]; ok {
				next = append(next, index)
			} else {
				occupied = append(occupied, occupiedProjection(windows[index], displays))
				skipped++
			}
		}
		active = next
	}
	count := 0
	var errs []error
	for i, p := range windows {
		r, ok := placements[i]
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := c.placeProjection(p, r, scale); err != nil {
			errs = append(errs, err)
		} else {
			count++
		}
	}
	if skipped > 0 {
		errs = append(errs, fmt.Errorf("桌面空间不足，%d 个投屏保持原位", skipped))
	}
	return count, errors.Join(errs...)
}
func (c *Controller) RestoreProjection(ctx context.Context, ids []string, saved []SavedProjection) (int, error) {
	c.layoutMu.Lock()
	defer c.layoutMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	displays, err := c.ScrcpyDisplays()
	if err != nil {
		return 0, err
	}
	open, err := c.layoutWindows()
	if err != nil {
		return 0, err
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	records := map[string]SavedProjection{}
	for _, r := range saved {
		records[r.InstanceID] = r
	}
	count, skipped := 0, 0
	var errs []error
	for _, p := range open {
		if ids != nil && !allowed[p.InstanceID] {
			continue
		}
		r, ok := records[p.InstanceID]
		if !ok {
			continue
		}
		var target *screenlayout.Display
		for i, d := range displays {
			if d.ID == r.DisplayID {
				target = &displays[i]
				break
			}
		}
		if target == nil {
			skipped++
			continue
		}
		ratio := windowScale(target.Scale) / windowScale(r.Scale)
		converted := screenlayout.Rect{X: target.WorkArea.X + int(math.Round(float64(r.Bounds.X-r.WorkArea.X)*ratio)), Y: target.WorkArea.Y + int(math.Round(float64(r.Bounds.Y-r.WorkArea.Y)*ratio)), Width: int(math.Round(float64(r.Bounds.Width) * ratio)), Height: int(math.Round(float64(r.Bounds.Height) * ratio))}
		if !rectWithin(converted, target.WorkArea) {
			skipped++
			continue
		}
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if err := c.placeProjection(p, converted, target.Scale); err != nil {
			errs = append(errs, err)
		} else {
			count++
		}
	}
	if skipped > 0 {
		errs = append(errs, fmt.Errorf("桌面空间不足或显示器不可用，%d 个投屏保持原位", skipped))
	}
	if count == 0 && skipped == 0 {
		return 0, errors.New("当前范围没有可恢复的已打开投屏")
	}
	return count, errors.Join(errs...)
}
