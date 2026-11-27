package syncinput

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Options configures a multi-device mouse and control broadcaster.
type Options struct {
	MasterID       string
	MasterHandle   uintptr
	MasterWidth    int
	MasterHeight   int
	JitterPx       int
	MaxDelayMS     int
	MoveIntervalMS int
}

type slaveEntry struct {
	id        string
	sink      ControlSink
	enabled   bool
	down      bool
	queue     chan func()
	done      chan struct{}
	closing   chan struct{}
	closeOnce sync.Once
	qmu       sync.RWMutex
	closed    bool
}

// Broadcaster coordinates normalized pointer tracking, anti-stuck-touch protection,
// optional random micro-delay/jitter, and ordered per-slave control injection.
type Broadcaster struct {
	opts    Options
	locator WindowLocator

	mu          sync.Mutex
	closed      bool
	paused      bool
	pointerDown bool
	lastRX      float64
	lastRY      float64
	lastMoveAt  time.Time
	slaves      map[string]*slaveEntry
	order       []string
}

// NewBroadcaster constructs a Broadcaster for a master window and initial control sinks.
func NewBroadcaster(opts Options, locator WindowLocator, sinks []ControlSink) *Broadcaster {
	if opts.MoveIntervalMS <= 0 {
		opts.MoveIntervalMS = 16
	}
	if opts.JitterPx < 0 {
		opts.JitterPx = 0
	}
	if opts.MaxDelayMS < 0 {
		opts.MaxDelayMS = 0
	}
	b := &Broadcaster{
		opts:    opts,
		locator: locator,
		slaves:  make(map[string]*slaveEntry, len(sinks)),
		order:   make([]string, 0, len(sinks)),
	}
	for _, s := range sinks {
		if s == nil {
			continue
		}
		entry := &slaveEntry{
			id:      s.InstanceID(),
			sink:    s,
			enabled: true,
			queue:   make(chan func(), 256),
			done:    make(chan struct{}),
			closing: make(chan struct{}),
		}
		go b.runSlaveWorker(entry)
		b.slaves[entry.id] = entry
		b.order = append(b.order, entry.id)
	}
	return b
}

func (b *Broadcaster) runSlaveWorker(entry *slaveEntry) {
	defer close(entry.done)
	for fn := range entry.queue {
		fn()
	}
}

func (e *slaveEntry) sendDropIfFull(fn func()) bool {
	e.qmu.RLock()
	defer e.qmu.RUnlock()
	if e.closed {
		return false
	}
	select {
	case <-e.closing:
		return false
	default:
	}
	select {
	case e.queue <- fn:
		return true
	case <-e.closing:
		return false
	default:
		return false
	}
}

func (e *slaveEntry) sendCritical(fn func()) bool {
	e.qmu.RLock()
	defer e.qmu.RUnlock()
	if e.closed {
		return false
	}
	select {
	case <-e.closing:
		return false
	default:
	}
	select {
	case e.queue <- fn:
		return true
	case <-e.closing:
		return false
	default:
	}
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case e.queue <- fn:
		return true
	case <-e.closing:
		return false
	case <-timer.C:
		return false
	}
}

func (e *slaveEntry) closeAndDrain() {
	e.closeOnce.Do(func() { close(e.closing) })
	e.qmu.Lock()
	if !e.closed {
		e.closed = true
		close(e.queue)
	}
	e.qmu.Unlock()
	<-e.done
}

func (b *Broadcaster) enqueueSlave(entry *slaveEntry, delayMS int, fn func()) {
	task := func() {
		if delayMS > 0 {
			time.Sleep(time.Duration(delayMS) * time.Millisecond)
		}
		fn()
	}
	entry.sendDropIfFull(task)
}

func (b *Broadcaster) enqueueCritical(entry *slaveEntry, delayMS int, fn func()) {
	task := func() {
		if delayMS > 0 {
			time.Sleep(time.Duration(delayMS) * time.Millisecond)
		}
		fn()
	}
	entry.sendCritical(task)
}

// HandleMouse processes a host mouse event against the master window content rectangle.
func (b *Broadcaster) HandleMouse(ev MouseEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed || b.paused {
		return
	}

	fgOK := b.locator == nil || b.opts.MasterHandle == 0 || b.locator.ForegroundWindow() == b.opts.MasterHandle
	if !fgOK {
		if b.pointerDown {
			b.releaseAllLocked()
		}
		return
	}

	var content Rect
	if b.locator != nil && b.opts.MasterHandle != 0 {
		client, ok := b.locator.ClientScreenRect(b.opts.MasterHandle)
		if !ok {
			if b.pointerDown {
				b.releaseAllLocked()
			}
			return
		}
		content = ContentRect(client, b.opts.MasterWidth, b.opts.MasterHeight)
	} else {
		content = Rect{Width: max(1, b.opts.MasterWidth), Height: max(1, b.opts.MasterHeight)}
	}

	rx, ry, inside := NormalizePoint(ev.X, ev.Y, content)
	switch ev.Kind {
	case MouseDown:
		if !inside {
			return
		}
		b.pointerDown = true
		b.lastRX, b.lastRY = rx, ry
		b.lastMoveAt = time.Now()
		b.dispatchTouchLocked(ActionDown, rx, ry, b.opts.JitterPx)

	case MouseMove:
		if !b.pointerDown {
			return
		}
		if !inside {
			b.releaseAllLocked()
			return
		}
		now := time.Now()
		if now.Sub(b.lastMoveAt) < time.Duration(b.opts.MoveIntervalMS)*time.Millisecond {
			return
		}
		b.lastMoveAt = now
		b.lastRX, b.lastRY = rx, ry
		b.dispatchTouchLocked(ActionMove, rx, ry, 0)

	case MouseUp:
		if !b.pointerDown {
			return
		}
		b.pointerDown = false
		if inside {
			b.lastRX, b.lastRY = rx, ry
		}
		b.dispatchTouchLocked(ActionUp, b.lastRX, b.lastRY, 0)

	case MouseWheel:
		if !inside {
			return
		}
		hScroll, vScroll := ev.HScroll, ev.VScroll
		for _, id := range b.order {
			entry := b.slaves[id]
			if entry == nil || !entry.enabled {
				continue
			}
			s := entry.sink
			delay := b.pickDelayLocked()
			b.enqueueSlave(entry, delay, func() {
				_ = s.SendScroll(rx, ry, hScroll, vScroll)
			})
		}
	}
}

func (b *Broadcaster) pickDelayLocked() int {
	if b.opts.MaxDelayMS <= 0 {
		return 0
	}
	return cryptoRandIntn(b.opts.MaxDelayMS + 1)
}

func (b *Broadcaster) dispatchTouchLocked(action uint8, rx, ry float64, jitterPx int) {
	for _, id := range b.order {
		entry := b.slaves[id]
		if entry == nil || !entry.enabled {
			continue
		}
		if action == ActionDown {
			entry.down = true
		} else {
			if !entry.down {
				continue
			}
			if action == ActionUp {
				entry.down = false
			}
		}
		s := entry.sink
		delay := b.pickDelayLocked()
		if action == ActionMove {
			b.enqueueSlave(entry, delay, func() {
				_ = s.SendTouch(action, rx, ry, jitterPx)
			})
		} else {
			b.enqueueCritical(entry, delay, func() {
				_ = s.SendTouch(action, rx, ry, jitterPx)
			})
		}
	}
}

func (b *Broadcaster) releaseAllLocked() {
	b.pointerDown = false
	for _, id := range b.order {
		entry := b.slaves[id]
		if entry == nil || !entry.down {
			continue
		}
		entry.down = false
		s := entry.sink
		rx, ry := b.lastRX, b.lastRY
		b.enqueueCritical(entry, 0, func() {
			_ = s.SendTouch(ActionUp, rx, ry, 0)
		})
	}
}

// SetPaused toggles sync forwarding; pausing while pressed releases all active pointers.
func (b *Broadcaster) SetPaused(paused bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.paused = paused
	if paused && b.pointerDown {
		b.releaseAllLocked()
	}
}

// IsPaused reports current pause state.
func (b *Broadcaster) IsPaused() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.paused
}

// SetSlaveEnabled temporarily kicks or re-joins a slave without tearing down its control socket.
func (b *Broadcaster) SetSlaveEnabled(id string, enabled bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.slaves[id]
	if !ok {
		return false
	}
	if !enabled && entry.down {
		entry.down = false
		s := entry.sink
		rx, ry := b.lastRX, b.lastRY
		b.enqueueCritical(entry, 0, func() {
			_ = s.SendTouch(ActionUp, rx, ry, 0)
		})
	}
	entry.enabled = enabled
	return true
}

// RemoveSlave releases any held touch, drains pending work, and closes the slave sink.
func (b *Broadcaster) RemoveSlave(id string) bool {
	b.mu.Lock()
	entry, ok := b.slaves[id]
	if !ok {
		b.mu.Unlock()
		return false
	}
	if entry.down {
		entry.down = false
		s := entry.sink
		rx, ry := b.lastRX, b.lastRY
		b.enqueueCritical(entry, 0, func() {
			_ = s.SendTouch(ActionUp, rx, ry, 0)
		})
	}
	delete(b.slaves, id)
	for i, oid := range b.order {
		if oid == id {
			b.order = append(b.order[:i], b.order[i+1:]...)
			break
		}
	}
	b.mu.Unlock()

	entry.closeAndDrain()
	_ = entry.sink.Close()
	return true
}

// EnabledSlaves returns all currently enabled slave IDs.
func (b *Broadcaster) EnabledSlaves() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, id := range b.order {
		if e := b.slaves[id]; e != nil && e.enabled {
			out = append(out, id)
		}
	}
	return out
}

// BroadcastKey sends a short key press/release to all enabled slaves.
func (b *Broadcaster) BroadcastKey(keycode uint32) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("同步会话已停止")
	}
	var targets []*slaveEntry
	for _, id := range b.order {
		if e := b.slaves[id]; e != nil && e.enabled {
			targets = append(targets, e)
		}
	}
	b.mu.Unlock()

	if len(targets) == 0 {
		return errors.New("当前没有启用的被控实例")
	}
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for _, entry := range targets {
		wg.Add(1)
		go func(e *slaveEntry) {
			defer wg.Done()
			done := make(chan error, 1)
			if !e.sendCritical(func() {
				done <- e.sink.SendKey(keycode)
			}) {
				mu.Lock()
				errs = append(errs, errors.New("同步会话或目标实例已停止"))
				mu.Unlock()
				return
			}
			select {
			case err := <-done:
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			case <-e.done:
				select {
				case err := <-done:
					if err != nil {
						mu.Lock()
						errs = append(errs, err)
						mu.Unlock()
					}
				default:
					mu.Lock()
					errs = append(errs, errors.New("同步会话或目标实例已停止"))
					mu.Unlock()
				}
			}
		}(entry)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// BroadcastText injects UTF-8 text into all enabled slaves.
func (b *Broadcaster) BroadcastText(text string) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("同步会话已停止")
	}
	var targets []*slaveEntry
	for _, id := range b.order {
		if e := b.slaves[id]; e != nil && e.enabled {
			targets = append(targets, e)
		}
	}
	b.mu.Unlock()

	if len(targets) == 0 {
		return errors.New("当前没有启用的被控实例")
	}
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	for _, entry := range targets {
		wg.Add(1)
		go func(e *slaveEntry) {
			defer wg.Done()
			done := make(chan error, 1)
			if !e.sendCritical(func() {
				done <- e.sink.SendText(text)
			}) {
				mu.Lock()
				errs = append(errs, errors.New("同步会话或目标实例已停止"))
				mu.Unlock()
				return
			}
			select {
			case err := <-done:
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			case <-e.done:
				select {
				case err := <-done:
					if err != nil {
						mu.Lock()
						errs = append(errs, err)
						mu.Unlock()
					}
				default:
					mu.Lock()
					errs = append(errs, errors.New("同步会话或目标实例已停止"))
					mu.Unlock()
				}
			}
		}(entry)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// Close releases active touches, drains all slave queues, and closes every control sink.
func (b *Broadcaster) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	wasDown := b.pointerDown
	b.pointerDown = false
	rx, ry := b.lastRX, b.lastRY
	entries := make([]*slaveEntry, 0, len(b.order))
	for _, id := range b.order {
		if e := b.slaves[id]; e != nil {
			if wasDown && e.down {
				e.down = false
				s := e.sink
				e.sendDropIfFull(func() {
					_ = s.SendTouch(ActionUp, rx, ry, 0)
				})
			}
			entries = append(entries, e)
		}
	}
	b.slaves = map[string]*slaveEntry{}
	b.order = nil
	b.mu.Unlock()

	var errs []error
	for _, e := range entries {
		e.closeAndDrain()
		if err := e.sink.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Unused helper check to ensure context import remains meaningful if extended.
var _ context.Context
