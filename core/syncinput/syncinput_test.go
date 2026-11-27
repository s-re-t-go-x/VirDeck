package syncinput

import (
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedTouch struct {
	action   uint8
	rx, ry   float64
	jitterPx int
}

type recordedScroll struct {
	rx, ry float64
	h, v   float32
}

type fakeSink struct {
	id      string
	w, h    int
	mu      sync.Mutex
	touches []recordedTouch
	scrolls []recordedScroll
	keys    []uint32
	texts   []string
	closed  bool
}

func (f *fakeSink) InstanceID() string      { return f.id }
func (f *fakeSink) ScreenSize() (int, int)  { return f.w, f.h }
func (f *fakeSink) SendTouch(action uint8, rx, ry float64, jitterPx int) error {
	f.mu.Lock()
	f.touches = append(f.touches, recordedTouch{action: action, rx: rx, ry: ry, jitterPx: jitterPx})
	f.mu.Unlock()
	return nil
}
func (f *fakeSink) SendScroll(rx, ry float64, hScroll, vScroll float32) error {
	f.mu.Lock()
	f.scrolls = append(f.scrolls, recordedScroll{rx: rx, ry: ry, h: hScroll, v: vScroll})
	f.mu.Unlock()
	return nil
}
func (f *fakeSink) SendKey(keycode uint32) error {
	f.mu.Lock()
	f.keys = append(f.keys, keycode)
	f.mu.Unlock()
	return nil
}
func (f *fakeSink) SendText(text string) error {
	f.mu.Lock()
	f.texts = append(f.texts, text)
	f.mu.Unlock()
	return nil
}
func (f *fakeSink) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

type fakeLocator struct {
	mu     sync.Mutex
	fg     uintptr
	client Rect
	ok     bool
}

func (l *fakeLocator) ForegroundWindow() uintptr {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fg
}

func (l *fakeLocator) ClientScreenRect(uintptr) (Rect, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.client, l.ok
}

func TestProtocolEncodingAndParsers(t *testing.T) {
	touch := EncodeTouch(ActionDown, PointerIDGenericFinger, 360, 640, 720, 1280, 0xffff, int32(ButtonPrimary), int32(ButtonPrimary))
	if len(touch) != 32 || touch[0] != MsgInjectTouchEvent || touch[1] != ActionDown {
		t.Fatalf("unexpected touch packet: len=%d header=%v", len(touch), touch[:2])
	}
	if int64(binary.BigEndian.Uint64(touch[2:10])) != PointerIDGenericFinger {
		t.Fatal("pointer ID mismatch")
	}
	if int32(binary.BigEndian.Uint32(touch[10:14])) != 360 || int32(binary.BigEndian.Uint32(touch[14:18])) != 640 {
		t.Fatal("touch xy mismatch")
	}
	if binary.BigEndian.Uint16(touch[18:20]) != 720 || binary.BigEndian.Uint16(touch[20:22]) != 1280 {
		t.Fatal("touch dimensions mismatch")
	}

	scroll := EncodeScroll(100, 200, 720, 1280, 0.5, -1.0, 0)
	if len(scroll) != 21 || scroll[0] != MsgInjectScrollEvent {
		t.Fatalf("unexpected scroll packet: len=%d", len(scroll))
	}
	if got := int16(binary.BigEndian.Uint16(scroll[15:17])); got != math.MinInt16 {
		t.Fatalf("expected clamped -1.0 scroll fixed point, got %d", got)
	}

	key := EncodeKeycode(ActionDown, KeyBack, 0, 0)
	if len(key) != 14 || key[0] != MsgInjectKeycode || binary.BigEndian.Uint32(key[2:6]) != KeyBack {
		t.Fatalf("unexpected key packet: %v", key)
	}

	longUTF8 := strings.Repeat("中", 120) // 360 bytes > 300 byte cap
	txt := EncodeText(longUTF8)
	if txt[0] != MsgInjectText {
		t.Fatal("wrong text msg type")
	}
	n := binary.BigEndian.Uint32(txt[1:5])
	if int(n) > maxInjectTextBytes || len(txt) != 5+int(n) || n%3 != 0 {
		t.Fatalf("UTF-8 truncation misaligned: n=%d total=%d", n, len(txt))
	}

	clip := EncodeSetClipboard(42, true, "你好 VirDeck https://example.com")
	if clip[0] != MsgSetClipboard || binary.BigEndian.Uint64(clip[1:9]) != 42 || clip[9] != 1 {
		t.Fatalf("unexpected SET_CLIPBOARD header: %v", clip[:14])
	}
	clipLen := binary.BigEndian.Uint32(clip[10:14])
	if int(clipLen) != len(clip)-14 || string(clip[14:]) != "你好 VirDeck https://example.com" {
		t.Fatalf("unexpected SET_CLIPBOARD payload: len=%d body=%q", clipLen, string(clip[14:]))
	}

	if v := ParseScrcpyVersion("scrcpy 3.1 <https://github.com/Genymobile/scrcpy>"); v != "3.1" {
		t.Fatalf("ParseScrcpyVersion got %q", v)
	}
	w, h, ok := ParseWMSize("Physical size: 720x1280\nOverride size: 1080x1920\n")
	if !ok || w != 1080 || h != 1920 {
		t.Fatalf("ParseWMSize should prefer Override size, got %dx%d ok=%v", w, h, ok)
	}
}

func TestGeometryBlackBarDeductionAndJitter(t *testing.T) {
	// 720x1280 portrait Android rendered inside wide 800x640 client area -> pillarbox width 360 centered at X=220.
	c1 := ContentRect(Rect{X: 100, Y: 50, Width: 800, Height: 640}, 720, 1280)
	if c1 != (Rect{X: 320, Y: 50, Width: 360, Height: 640}) {
		t.Fatalf("unexpected pillarbox rect: %+v", c1)
	}
	// Click inside black bar at X=150 -> outside!
	if _, _, inside := NormalizePoint(150, 100, c1); inside {
		t.Fatal("point in pillarbox black bar must be outside")
	}
	rx, ry, inside := NormalizePoint(320+180, 50+320, c1)
	if !inside || math.Abs(rx-0.5) > 0.01 || math.Abs(ry-0.5) > 0.01 {
		t.Fatalf("unexpected normalized center: (%v,%v) inside=%v", rx, ry, inside)
	}

	// Letterbox (tall client 360x800, 720x1280 -> height 640 centered at Y=80).
	c2 := ContentRect(Rect{X: 0, Y: 0, Width: 360, Height: 800}, 720, 1280)
	if c2 != (Rect{X: 0, Y: 80, Width: 360, Height: 640}) {
		t.Fatalf("unexpected letterbox rect: %+v", c2)
	}

	// Jitter clamped cleanly to [0, target-1].
	x, y := DenormalizePoint(0.0, 1.0, 1080, 1920, 10, func(n int) int { return 0 }) // -10 offset
	if x != 0 || y != 1909 {
		t.Fatalf("expected jitter clamped at (0, 1909), got (%d, %d)", x, y)
	}
}

func TestBroadcasterThrottleAntiStuckDelayAndControls(t *testing.T) {
	loc := &fakeLocator{
		fg:     100,
		client: Rect{X: 0, Y: 0, Width: 360, Height: 640},
		ok:     true,
	}
	s1 := &fakeSink{id: "s1", w: 720, h: 1280}
	s2 := &fakeSink{id: "s2", w: 1080, h: 1920}

	b := NewBroadcaster(Options{
		MasterID:       "m",
		MasterHandle:   100,
		MasterWidth:    720,
		MasterHeight:   1280,
		JitterPx:       3,
		MaxDelayMS:     15,
		MoveIntervalMS: 20,
	}, loc, []ControlSink{s1, s2})

	// 1. MouseDown inside content, rapid moves throttled, then cursor leaves content -> auto ActionUp!
	b.HandleMouse(MouseEvent{Kind: MouseDown, X: 180, Y: 320})
	b.HandleMouse(MouseEvent{Kind: MouseMove, X: 185, Y: 325})
	b.HandleMouse(MouseEvent{Kind: MouseMove, X: 190, Y: 330}) // throttled
	time.Sleep(25 * time.Millisecond)
	b.HandleMouse(MouseEvent{Kind: MouseMove, X: 200, Y: 340})
	b.HandleMouse(MouseEvent{Kind: MouseMove, X: 500, Y: 340}) // outside -> triggers ActionUp!
	b.HandleMouse(MouseEvent{Kind: MouseUp, X: 500, Y: 340})   // ignored since already released

	// 2. Wheel scroll inside content.
	b.HandleMouse(MouseEvent{Kind: MouseWheel, X: 180, Y: 320, VScroll: -1.0})

	// 3. Kick s2, broadcast key + text -> only s1 receives them.
	if !b.SetSlaveEnabled("s2", false) {
		t.Fatal("SetSlaveEnabled failed")
	}
	if err := b.BroadcastKey(KeyHome); err != nil {
		t.Fatalf("BroadcastKey failed: %v", err)
	}
	if err := b.BroadcastText("hello"); err != nil {
		t.Fatalf("BroadcastText failed: %v", err)
	}

	// 4. Pause while pointer is pressed releases pointer cleanly.
	b.HandleMouse(MouseEvent{Kind: MouseDown, X: 100, Y: 200})
	b.SetPaused(true)

	// Drain queues on Close().
	if err := b.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	s1.mu.Lock()
	t1 := append([]recordedTouch(nil), s1.touches...)
	sc1 := append([]recordedScroll(nil), s1.scrolls...)
	k1 := append([]uint32(nil), s1.keys...)
	tx1 := append([]string(nil), s1.texts...)
	c1 := s1.closed
	s1.mu.Unlock()

	s2.mu.Lock()
	k2 := append([]uint32(nil), s2.keys...)
	c2 := s2.closed
	s2.mu.Unlock()

	if !c1 || !c2 {
		t.Fatal("sinks not closed on Broadcaster.Close")
	}
	// Sequence on s1: Down -> Move (1) -> Up (auto leave) -> Down -> Up (auto pause) = 5 touch events.
	if len(t1) != 5 || t1[0].action != ActionDown || t1[1].action != ActionMove || t1[2].action != ActionUp || t1[3].action != ActionDown || t1[4].action != ActionUp {
		t.Fatalf("unexpected ordered touch sequence on s1: %+v", t1)
	}
	if t1[0].jitterPx != 3 {
		t.Fatalf("expected JitterPx=3 on ActionDown, got %d", t1[0].jitterPx)
	}
	if len(sc1) != 1 || sc1[0].v != -1.0 {
		t.Fatalf("expected 1 scroll on s1, got %+v", sc1)
	}
	if len(k1) != 1 || k1[0] != KeyHome || len(tx1) != 1 || tx1[0] != "hello" {
		t.Fatalf("unexpected key/text on s1: keys=%v texts=%v", k1, tx1)
	}
	if len(k2) != 0 {
		t.Fatalf("disabled slave s2 should not receive key: %v", k2)
	}
}

func TestBroadcasterConcurrentBroadcastAndStopDoesNotPanic(t *testing.T) {
	loc := &fakeLocator{fg: 1, client: Rect{X: 0, Y: 0, Width: 360, Height: 640}, ok: true}
	for iter := 0; iter < 50; iter++ {
		s1 := &fakeSink{id: "s1", w: 720, h: 1280}
		s2 := &fakeSink{id: "s2", w: 720, h: 1280}
		b := NewBroadcaster(Options{MasterID: "m", MasterHandle: 1, MasterWidth: 720, MasterHeight: 1280}, loc, []ControlSink{s1, s2})

		start := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-start
			for i := 0; i < 10; i++ {
				_ = b.BroadcastKey(KeyHome)
				_ = b.BroadcastText("中文同步")
			}
		}()
		close(start)
		if iter%2 == 0 {
			b.RemoveSlave("s1")
		}
		_ = b.Close()
		<-done
	}
}
