package emu

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"virdeck/config"
	"virdeck/core/screenlayout"
)

type projectionHost interface {
	windows() ([]screenlayout.Window, error)
	monitors() ([]screenlayout.Display, error)
	place(screenlayout.Window, screenlayout.Rect) error
	activate(screenlayout.Window) error
	title(screenlayout.Window, string) error
}
type nativeProjectionHost struct{}

func (nativeProjectionHost) windows() ([]screenlayout.Window, error) { return screenlayout.Windows() }
func (nativeProjectionHost) monitors() ([]screenlayout.Display, error) {
	return screenlayout.Displays()
}
func (nativeProjectionHost) place(w screenlayout.Window, r screenlayout.Rect) error {
	return screenlayout.Place(w, r)
}
func (nativeProjectionHost) activate(w screenlayout.Window) error { return screenlayout.Activate(w) }
func (nativeProjectionHost) close(w screenlayout.Window) error    { return screenlayout.Close(w) }
func (nativeProjectionHost) title(w screenlayout.Window, s string) error {
	return screenlayout.SetTitle(w, s)
}
func (c *Controller) projectionHost() projectionHost {
	if c.screenHost != nil {
		return c.screenHost
	}
	return nativeProjectionHost{}
}

type projectionOutput struct {
	mu   sync.Mutex
	tail []byte
}

func (b *projectionOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.tail = append(b.tail, p...)
	if len(b.tail) > 8192 {
		b.tail = append([]byte(nil), b.tail[len(b.tail)-8192:]...)
	}
	return n, nil
}
func (b *projectionOutput) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.tail))
}

type projectionProcess struct {
	intentional bool
	cmd         *exec.Cmd
	done        chan struct{}
	port        int
	err         error
	output      projectionOutput
	bounds      *screenlayout.Rect
	title       string
}
type ProjectionWindow struct {
	InstanceID string
	Window     screenlayout.Window
}

const projectionMarker = " [VirDeck:"

func projectionTitle(id, base string) string {
	return base + projectionMarker + hex.EncodeToString([]byte(id)) + "]"
}
func projectionID(title string) (string, bool) {
	i := strings.LastIndex(title, projectionMarker)
	if i < 0 || !strings.HasSuffix(title, "]") {
		return "", false
	}
	raw, err := hex.DecodeString(title[i+len(projectionMarker) : len(title)-1])
	return string(raw), err == nil && len(raw) > 0
}
func projectionPath(path string) string {
	if path == "" {
		return ""
	}
	path = strings.TrimPrefix(path, `\\?\`)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}
func ownedProjection(w screenlayout.Window, exe string) bool {
	return w.PID > 0 && w.Handle != 0 && w.Executable != "" && sameProjectionExecutable(projectionPath(w.Executable), exe)
}
func sameProjectionExecutable(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func (c *Controller) scrcpyExecutable() (string, error) {
	_, _, _, _, bin := c.ResolvePaths()
	if bin == "" {
		return "", errors.New("未找到 scrcpy，请先补齐投屏组件")
	}
	return projectionPath(bin), nil
}

// StartScrcpy owns one process per stable instance ID, including concurrent opens.
func (c *Controller) StartScrcpy(ctx context.Context, id string, port int, title string) error {
	_, err := c.StartProjection(ctx, id, port, title)
	return err
}

// StartProjection reports true only for the caller that created the process.
func (c *Controller) StartProjection(ctx context.Context, id string, port int, title string) (bool, error) {
	return c.StartProjectionWithSettings(ctx, id, port, title, config.Values.Projection)
}
func (c *Controller) projectionLock(id string) *sync.Mutex {
	value, _ := c.projectionLocks.LoadOrStore(id, &sync.Mutex{})
	return value.(*sync.Mutex)
}
func (c *Controller) SetProjectionLogger(log func(string, string)) {
	c.screenMu.Lock()
	c.screenLog = log
	c.screenMu.Unlock()
}
func (c *Controller) StartProjectionWithSettings(ctx context.Context, id string, port int, title string, settings ProjectionSettings) (bool, error) {
	lock := c.projectionLock(id)
	lock.Lock()
	defer lock.Unlock()
	return c.startProjectionAt(ctx, id, port, title, settings, nil)
}
func (c *Controller) startProjectionWithSettings(ctx context.Context, id string, port int, title string, settings ProjectionSettings) (bool, error) {
	return c.startProjectionAt(ctx, id, port, title, settings, nil)
}
func (c *Controller) startProjectionAt(ctx context.Context, id string, port int, title string, settings ProjectionSettings, bounds *screenlayout.Rect) (bool, error) {
	if err := config.ValidateProjection(settings); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if id == "" || port <= 0 {
		return false, errors.New("无效的投屏实例或端口")
	}
	exe, err := c.scrcpyExecutable()
	if err != nil {
		return false, err
	}
	host := c.projectionHost()
	windows, err := host.windows()
	portable := errors.Is(err, screenlayout.ErrUnsupported)
	if err != nil && !portable {
		return false, err
	}
	desired := projectionTitle(id, title)
	stale := false
	// Adopt already-open windows after an app restart; never trust titles alone.
	for _, w := range windows {
		existingID, tagged := projectionID(w.Title)
		if ownedProjection(w, exe) && ((tagged && existingID == id) || w.Title == title) {
			if tagged && !strings.HasSuffix(w.Title, fmt.Sprintf("(emulator-%d)", port)+projectionMarker+hex.EncodeToString([]byte(id))+"]") {
				stale = true
				continue
			}
			if w.Title != desired {
				if err := host.title(w, desired); err != nil {
					return false, err
				}
			}
			return false, host.activate(w)
		}
	}
	c.screenMu.Lock()
	if c.projections == nil {
		c.projections = make(map[string]*projectionProcess)
	}
	p := c.projections[id]
	if p != nil {
		select {
		case <-p.done:
			delete(c.projections, id)
			p = nil
		default:
		}
	}
	fresh := false
	if p != nil && p.port != port {
		c.screenMu.Unlock()
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
			return c.startProjectionAt(ctx, id, port, title, settings, bounds)
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Second):
			return false, errors.New("旧投屏正在退出，请稍后重试")
		}
	}
	if p == nil && stale {
		c.screenMu.Unlock()
		return false, errors.New("旧投屏连接已变化，请关闭旧投屏窗口后重试")
	}
	if p == nil {
		args := []string{"-s", fmt.Sprintf("emulator-%d", port), "--window-title", desired}
		args = append(args, projectionSettingsArgs(settings)...)
		if bounds != nil {
			geometry, err := projectionBoundsArgs(*bounds)
			if err != nil {
				c.screenMu.Unlock()
				return false, err
			}
			args = append(args, geometry...)
		}
		cmd := exec.Command(exe, args...)
		cmd.Env = c.IsolatedEnv()
		_, adbBin, _, _, _ := c.ResolvePaths()
		if adbBin != "" {
			cmd.Env = append(cmd.Env, "ADB="+adbBin)
		}
		cmd.WaitDelay = time.Second
		hideConsoleKeepGUI(cmd)
		p = &projectionProcess{cmd: cmd, done: make(chan struct{}), port: port, bounds: bounds, title: desired}
		cmd.Stdout = &p.output
		cmd.Stderr = &p.output
		if err := cmd.Start(); err != nil {
			c.screenMu.Unlock()
			return false, fmt.Errorf("启动投屏失败: %w", err)
		}
		c.projections[id] = p
		fresh = true
		go func(p *projectionProcess) {
			p.err = p.cmd.Wait()
			close(p.done)
			c.screenMu.Lock()
			if c.projections[id] == p {
				delete(c.projections, id)
			}
			logger := c.screenLog
			intentional := p.intentional
			c.screenMu.Unlock()
			if logger != nil && p.err != nil && !intentional {
				logger(id, fmt.Sprintf("投屏进程已退出：%v\n%s", p.err, p.output.text()))
			}
		}(p)
	}
	c.screenMu.Unlock()
	if portable {
		// Only process startup can be observed without a native window backend.
		select {
		case <-p.done:
			return false, fmt.Errorf("投屏进程已退出: %v %s", p.err, p.output.text())
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return fresh, nil
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(config.Values.ScreenLayout.WindowTimeoutSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		windows, err := host.windows()
		if err != nil {
			return false, err
		}
		for _, w := range windows {
			if w.PID == p.cmd.Process.Pid && ownedProjection(w, exe) {
				if !fresh {
					return false, host.activate(w)
				}
				return fresh, nil
			}
		}
		select {
		case <-p.done:
			return false, fmt.Errorf("投屏进程已退出: %v %s", p.err, p.output.text())
		case <-waitCtx.Done():
			return false, fmt.Errorf("投屏窗口尚未就绪，请稍后再点投屏: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (c *Controller) ScrcpyDisplays() ([]screenlayout.Display, error) {
	return c.projectionHost().monitors()
}
func (c *Controller) scrcpyWindows() ([]ProjectionWindow, error) {
	exe, err := c.scrcpyExecutable()
	if err != nil {
		return nil, err
	}
	windows, err := c.projectionHost().windows()
	if errors.Is(err, screenlayout.ErrUnsupported) {
		return c.trackedProjections(), nil
	}
	if err != nil {
		return nil, err
	}
	var out []ProjectionWindow
	for _, w := range windows {
		if id, ok := projectionID(w.Title); ok && ownedProjection(w, exe) {
			out = append(out, ProjectionWindow{InstanceID: id, Window: w})
		}
	}
	return out, nil
}

func (c *Controller) ArrangeScrcpy(ctx context.Context, ids []string, displayID string, height, gap int) (int, error) {
	return c.ArrangeProjection(ctx, ids, ScreenLayoutOptions{DisplayID: displayID, Height: height, Gap: gap})
}
