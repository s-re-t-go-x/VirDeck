package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"virdeck/core/emu"
	"virdeck/core/screenlayout"
	"virdeck/core/syncinput"
)

type svcFakeSink struct {
	id     string
	w, h   int
	mu     sync.Mutex
	keys   []uint32
	texts  []string
	down   int
	up     int
	closed bool
}

func (s *svcFakeSink) InstanceID() string     { return s.id }
func (s *svcFakeSink) ScreenSize() (int, int) { return s.w, s.h }
func (s *svcFakeSink) SendTouch(action uint8, rx, ry float64, jitterPx int) error {
	s.mu.Lock()
	switch action {
	case syncinput.ActionDown:
		s.down++
	case syncinput.ActionUp:
		s.up++
	}
	s.mu.Unlock()
	return nil
}
func (s *svcFakeSink) SendScroll(float64, float64, float32, float32) error { return nil }
func (s *svcFakeSink) SendKey(k uint32) error {
	s.mu.Lock()
	s.keys = append(s.keys, k)
	s.mu.Unlock()
	return nil
}
func (s *svcFakeSink) SendText(t string) error {
	s.mu.Lock()
	s.texts = append(s.texts, t)
	s.mu.Unlock()
	return nil
}
func (s *svcFakeSink) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

type svcFakeLocator struct {
	fg uintptr
}

func (l *svcFakeLocator) ForegroundWindow() uintptr { return l.fg }
func (l *svcFakeLocator) ClientScreenRect(hwnd uintptr) (syncinput.Rect, bool) {
	return syncinput.Rect{X: 0, Y: 0, Width: 360, Height: 640}, true
}

type svcFakeCapture struct {
	mu       sync.Mutex
	started  bool
	stopped  bool
	onEvent  func(syncinput.MouseEvent)
	onHotkey func()
}

func (c *svcFakeCapture) Start(_ context.Context, onEvent func(syncinput.MouseEvent), onHotkey func()) error {
	c.mu.Lock()
	c.started = true
	c.onEvent = onEvent
	c.onHotkey = onHotkey
	c.mu.Unlock()
	return nil
}

func (c *svcFakeCapture) Stop() error {
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	return nil
}

func TestManagerInputSyncLifecycleControlsAndStopCleanup(t *testing.T) {
	m, fe, fa := newTestManager(t, 4, 100*time.Millisecond)
	close(fa.bootReady)

	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "sync",
		Count:       3,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		Headless:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	master, s1, s2 := created[0], created[1], created[2]
	if err := m.Start(ctx, master.ID, s1.ID, s2.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{master.ID, s1.ID, s2.ID} {
		if _, err := m.WaitInstanceReady(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = m.Stop(context.Background(), master.ID, s1.ID, s2.ID) })

	fe.screens = []emu.ProjectionWindow{
		{InstanceID: master.ID, Window: screenlayout.Window{Handle: 777, PID: 1}},
	}

	var sinksMu sync.Mutex
	sinks := map[string]*svcFakeSink{}
	capMock := &svcFakeCapture{}
	m.syncHooks = &inputSyncHooks{
		captureSupported: func() bool { return true },
		captureReason:    func() string { return "" },
		locator:          func() syncinput.WindowLocator { return &svcFakeLocator{fg: 777} },
		newCapture:       func() syncinput.CaptureBackend { return capMock },
		detectVersion: func(context.Context) (string, string, string, []string, error) {
			return "adb.exe", "scrcpy-server", "3.1", nil, nil
		},
		querySize: func(context.Context, string, []string, int) (int, int, error) {
			return 720, 1280, nil
		},
		dialSink: func(_ context.Context, cfg syncinput.ScrcpyControlConfig) (syncinput.ControlSink, error) {
			sk := &svcFakeSink{id: cfg.InstanceID, w: cfg.DeviceWidth, h: cfg.DeviceHeight}
			sinksMu.Lock()
			sinks[cfg.InstanceID] = sk
			sinksMu.Unlock()
			return sk, nil
		},
	}

	st, err := m.StartInputSync(ctx, InputSyncConfig{
		MasterID:   master.ID,
		SlaveIDs:   []string{s1.ID, s2.ID},
		JitterPx:   3,
		MaxDelayMS: 0,
	})
	if err != nil {
		t.Fatalf("StartInputSync failed: %v", err)
	}
	if !st.Active || st.Paused || len(st.Slaves) != 2 {
		t.Fatalf("unexpected status: %+v", st)
	}

	// Simulate mouse press and release while active, then hotkey pause.
	capMock.onEvent(syncinput.MouseEvent{Kind: syncinput.MouseDown, X: 180, Y: 320})
	capMock.onHotkey() // pauses and releases active pointer!

	if cur := m.GetInputSyncStatus(); !cur.Paused {
		t.Fatal("expected hotkey to toggle paused=true")
	}
	if _, err := m.SetInputSyncPaused(false); err != nil {
		t.Fatal(err)
	}

	// Kick s2 temporarily, broadcast Back + Text -> only s1 receives.
	st, err = m.SetInputSyncSlaveEnabled(s2.ID, false)
	if err != nil || st.Slaves[1].Enabled {
		t.Fatalf("expected s2 disabled, got %+v err=%v", st, err)
	}
	if err := m.BroadcastSyncKey(ctx, "back"); err != nil {
		t.Fatalf("BroadcastSyncKey failed: %v", err)
	}
	if err := m.BroadcastSyncText(ctx, "hi"); err != nil {
		t.Fatalf("BroadcastSyncText failed: %v", err)
	}

	// Stop slave s1 -> detached cleanly while session remains active; stopping master tears down session.
	if err := m.Stop(ctx, s1.ID); err != nil {
		t.Fatal(err)
	}
	sinks[s1.ID].mu.Lock()
	s1Keys := append([]uint32(nil), sinks[s1.ID].keys...)
	s1Texts := append([]string(nil), sinks[s1.ID].texts...)
	s1Closed := sinks[s1.ID].closed
	s1Down := sinks[s1.ID].down
	s1Up := sinks[s1.ID].up
	sinks[s1.ID].mu.Unlock()
	if !s1Closed || s1Down != 1 || s1Up != 1 || len(s1Keys) != 1 || s1Keys[0] != syncinput.KeyBack || len(s1Texts) != 1 || s1Texts[0] != "hi" {
		t.Fatalf("unexpected s1 records: closed=%v down=%d up=%d keys=%v texts=%v", s1Closed, s1Down, s1Up, s1Keys, s1Texts)
	}

	if err := m.Stop(ctx, master.ID); err != nil {
		t.Fatal(err)
	}
	if cur := m.GetInputSyncStatus(); cur.Active {
		t.Fatal("expected sync session to stop when master instance stops")
	}
	if !capMock.stopped {
		t.Fatal("expected capture backend stopped when master stops")
	}
}
