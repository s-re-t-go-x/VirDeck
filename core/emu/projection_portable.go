package emu

import (
	"context"
	"fmt"
	"virdeck/core/screenlayout"
)

// projectionBoundsArgs uses scrcpy's SDL window coordinates and client size.
func projectionBoundsArgs(r screenlayout.Rect) ([]string, error) {
	if r.Width <= 0 || r.Height <= 0 || r.Width > 65535 || r.Height > 65535 || r.X < -32767 || r.X > 32767 || r.Y < -32767 || r.Y > 32767 {
		return nil, fmt.Errorf("投屏启动坐标或尺寸超出 scrcpy 支持范围")
	}
	return []string{fmt.Sprintf("--window-x=%d", r.X), fmt.Sprintf("--window-y=%d", r.Y), fmt.Sprintf("--window-width=%d", r.Width), fmt.Sprintf("--window-height=%d", r.Height)}, nil
}

// StartProjectionWithBounds sets the initial client geometry on all hosts.
// On Wayland the compositor may ignore the requested position.
func (c *Controller) StartProjectionWithBounds(ctx context.Context, id string, port int, title string, settings ProjectionSettings, bounds screenlayout.Rect) (bool, error) {
	if _, err := projectionBoundsArgs(bounds); err != nil {
		return false, err
	}
	lock := c.projectionLock(id)
	lock.Lock()
	defer lock.Unlock()
	return c.startProjectionAt(ctx, id, port, title, settings, &bounds)
}

// trackedProjections does not invent native handles or observed geometry.
func (c *Controller) trackedProjections() []ProjectionWindow {
	c.screenMu.Lock()
	defer c.screenMu.Unlock()
	var out []ProjectionWindow
	for id, p := range c.projections {
		select {
		case <-p.done:
			continue
		default:
		}
		w := screenlayout.Window{PID: p.cmd.Process.Pid, Executable: p.cmd.Path, Title: p.title, Scale: 1}
		if p.bounds != nil {
			w.Bounds = *p.bounds
			w.ClientWidth = w.Bounds.Width
			w.ClientHeight = w.Bounds.Height
		}
		out = append(out, ProjectionWindow{InstanceID: id, Window: w})
	}
	return out
}
