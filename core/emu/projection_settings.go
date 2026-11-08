package emu

import (
	"context"
	"errors"
	"fmt"
	"time"
	"virdeck/config"
	"virdeck/core/screenlayout"
)

type ProjectionSettings = config.ProjectionDefaults

func projectionSettingsArgs(p ProjectionSettings) []string {
	args := []string{fmt.Sprintf("--video-bit-rate=%dM", p.BitRateMbps)}
	if p.MaxFPS > 0 {
		args = append(args, fmt.Sprintf("--max-fps=%d", p.MaxFPS))
	}
	if p.MaxSize > 0 {
		args = append(args, fmt.Sprintf("--max-size=%d", p.MaxSize))
	}
	if p.Audio {
		args = append(args, "--audio-codec="+config.Values.Runtime.ProjectionAudioCodec, "--require-audio")
	} else {
		args = append(args, "--no-audio")
	}
	return args
}

func initialProcessOutput(text string) *projectionOutput {
	out := &projectionOutput{}
	_, _ = out.Write([]byte(text))
	return out
}

func (c *Controller) RestartProjection(ctx context.Context, id string, port int, title string, settings ProjectionSettings) error {
	lock := c.projectionLock(id)
	lock.Lock()
	defer lock.Unlock()
	if err := config.ValidateProjection(settings); err != nil {
		return err
	}
	open, err := c.ScrcpyWindows()
	if err != nil {
		return err
	}
	var previous *screenlayout.Window
	for _, p := range open {
		if p.InstanceID == id {
			w := p.Window
			previous = &w
			break
		}
	}
	c.screenMu.Lock()
	process := c.projections[id]
	if process != nil {
		process.intentional = true
	}
	c.screenMu.Unlock()
	if previous != nil {
		closer, ok := c.projectionHost().(interface {
			close(screenlayout.Window) error
		})
		if !ok {
			return errors.New("无法关闭已有投屏窗口")
		}
		if err := closer.close(*previous); err != nil && process == nil {
			return err
		}
	}
	if process != nil {
		_ = process.cmd.Process.Kill()
		select {
		case <-process.done:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			return errors.New("旧投屏尚未退出，请稍后重试")
		}
	}
	if previous != nil {
		closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		for {
			windows, err := c.projectionHost().windows()
			if err != nil {
				return err
			}
			found := false
			for _, w := range windows {
				if w.Handle == previous.Handle && w.PID == previous.PID {
					found = true
					break
				}
			}
			if !found {
				break
			}
			select {
			case <-closeCtx.Done():
				return errors.New("旧投屏窗口未关闭，请手动关闭后重试")
			case <-time.After(40 * time.Millisecond):
			}
		}
	}
	if _, err := c.startProjectionWithSettings(ctx, id, port, title, settings); err != nil {
		return err
	}
	if previous != nil {
		current, err := c.ScrcpyWindows()
		if err != nil {
			return err
		}
		for _, p := range current {
			if p.InstanceID == id {
				c.layoutMu.Lock()
				err = c.placeProjection(p, previous.Bounds, previous.Scale)
				c.layoutMu.Unlock()
				return err
			}
		}
	}
	return nil
}
