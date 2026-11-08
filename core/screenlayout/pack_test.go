package screenlayout

import (
	"reflect"
	"testing"
)

func TestPackPreservesRequestedClientSize(t *testing.T) {
	windows := []Window{
		{Bounds: Rect{Width: 110, Height: 220}, ClientWidth: 100, ClientHeight: 200},
		{Bounds: Rect{Width: 316, Height: 240}, ClientWidth: 300, ClientHeight: 200},
	}
	got, err := Pack(Rect{Width: 1000, Height: 400}, windows, 101, 10, nil, nil)
	want := []Placement{{Index: 0, Bounds: Rect{X: 10, Y: 10, Width: 61, Height: 121}}, {Index: 1, Bounds: Rect{X: 81, Y: 10, Width: 168, Height: 141}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Pack = %v, %v; want %v", got, err, want)
	}
}

func TestPackSkipsOversizedAndReturnsPartialCapacity(t *testing.T) {
	windows := []Window{
		{Bounds: Rect{Width: 400, Height: 100}, ClientWidth: 400, ClientHeight: 100},
		{Bounds: Rect{Width: 100, Height: 100}, ClientWidth: 100, ClientHeight: 100},
		{Bounds: Rect{Width: 100, Height: 100}, ClientWidth: 100, ClientHeight: 100},
		{Bounds: Rect{Width: 100, Height: 100}, ClientWidth: 100, ClientHeight: 100},
	}
	got, err := Pack(Rect{Width: 210, Height: 100}, windows, 100, 10, nil, nil)
	want := []Placement{{Index: 1, Bounds: Rect{Width: 100, Height: 100}}, {Index: 2, Bounds: Rect{X: 110, Width: 100, Height: 100}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Pack = %v, %v; want %v", got, err, want)
	}
}

func TestPackAvoidsObstaclesAndDoesNotMutateInputs(t *testing.T) {
	work := Rect{X: -500, Y: -300, Width: 500, Height: 300}
	windows := make([]Window, 6)
	for i := range windows {
		windows[i] = Window{Bounds: Rect{Width: 90, Height: 90}, ClientWidth: 90, ClientHeight: 90}
	}
	occupied := []Rect{{X: -510, Y: -310, Width: 110, Height: 110}, {X: 100, Y: 100, Width: 200, Height: 200}, {Width: 0, Height: 100}}
	anchor := Rect{X: -300, Y: -200, Width: 90, Height: 90}
	beforeWindows, beforeOccupied, beforeAnchor := append([]Window(nil), windows...), append([]Rect(nil), occupied...), anchor
	got, err := Pack(work, windows, 90, 10, occupied, &anchor)
	if err != nil || len(got) != 6 {
		t.Fatalf("Pack = %v, %v; want six windows", got, err)
	}
	if got[0].Bounds != (Rect{X: -200, Y: -200, Width: 90, Height: 90}) {
		t.Fatalf("first placement = %v; want aligned right of anchor", got[0])
	}
	assertPackGeometry(t, work, got, append(append([]Rect(nil), occupied...), anchor), 10)
	if !reflect.DeepEqual(windows, beforeWindows) || !reflect.DeepEqual(occupied, beforeOccupied) || anchor != beforeAnchor {
		t.Fatal("Pack changed its inputs")
	}
	again, err := Pack(work, windows, 90, 10, occupied, &anchor)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("Pack is not deterministic: %v, %v", again, err)
	}
}

func TestPackSearchesBelowAnchorThenRemainingDesktop(t *testing.T) {
	windows := []Window{{Bounds: Rect{Width: 100, Height: 100}, ClientWidth: 100, ClientHeight: 100}}
	for _, tc := range []struct {
		name   string
		anchor Rect
		want   Rect
	}{
		{"below", Rect{X: 200, Width: 100, Height: 100}, Rect{X: 200, Y: 110, Width: 100, Height: 100}},
		{"remaining", Rect{X: 200, Y: 110, Width: 100, Height: 100}, Rect{X: 10, Y: 10, Width: 100, Height: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Pack(Rect{Width: 300, Height: 210}, windows, 100, 10, nil, &tc.anchor)
			if err != nil || len(got) != 1 || got[0].Bounds != tc.want {
				t.Fatalf("Pack = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestPackUsesOnlyIntersectingObstacleParts(t *testing.T) {
	window := Window{Bounds: Rect{Width: 100, Height: 100}, ClientWidth: 100, ClientHeight: 100}
	got, err := Pack(Rect{Width: 100, Height: 100}, []Window{window}, 100, 10, []Rect{{X: -20, Width: 20, Height: 100}}, nil)
	if err != nil || len(got) != 1 || got[0].Bounds != (Rect{Width: 100, Height: 100}) {
		t.Fatalf("outside obstacle consumed work area: %v, %v", got, err)
	}
}

func TestPackRejectsInvalidDimensionsAndCoordinates(t *testing.T) {
	window := Window{Bounds: Rect{Width: 100, Height: 100}, ClientWidth: 100, ClientHeight: 100}
	for _, tc := range []struct {
		name        string
		work        Rect
		window      Window
		height, gap int
		occupied    []Rect
		anchor      *Rect
	}{
		{name: "zero height", work: Rect{Width: 100, Height: 100}, window: window},
		{name: "negative gap", work: Rect{Width: 100, Height: 100}, window: window, height: 100, gap: -1},
		{name: "overflow work", work: Rect{X: maxCoordinate, Width: 100, Height: 100}, window: window, height: 100},
		{name: "negative obstacle", work: Rect{Width: 100, Height: 100}, window: window, height: 100, occupied: []Rect{{Width: -1, Height: 100}}},
		{name: "overflow obstacle", work: Rect{Width: 100, Height: 100}, window: window, height: 100, occupied: []Rect{{X: maxCoordinate, Width: 1, Height: 100}}},
		{name: "invalid anchor", work: Rect{Width: 100, Height: 100}, window: window, height: 100, anchor: &Rect{Width: 100, Height: -1}},
		{name: "zero client", work: Rect{Width: 100, Height: 100}, window: Window{Bounds: Rect{Width: 100, Height: 100}, ClientHeight: 100}, height: 100},
		{name: "negative frame", work: Rect{Width: 100, Height: 100}, window: Window{Bounds: Rect{Width: 99, Height: 100}, ClientWidth: 100, ClientHeight: 100}, height: 100},
		{name: "oversized request", work: Rect{Width: 100, Height: 100}, window: window, height: maxCoordinate + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Pack(tc.work, []Window{tc.window}, tc.height, tc.gap, tc.occupied, tc.anchor); err == nil {
				t.Fatal("expected invalid input error")
			}
		})
	}
}

func TestPackEmptyInputAndHugeValidAspect(t *testing.T) {
	got, err := Pack(Rect{}, nil, 0, -1, nil, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty Pack = %v, %v; want non-nil empty result", got, err)
	}
	window := Window{Bounds: Rect{Width: maxCoordinate, Height: 1}, ClientWidth: maxCoordinate, ClientHeight: 1}
	got, err = Pack(Rect{Width: 100, Height: 100}, []Window{window}, maxCoordinate, 0, nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("oversized aspect should be skipped without overflow: %v, %v", got, err)
	}
}

func assertPackGeometry(t *testing.T, work Rect, placements []Placement, obstacles []Rect, gap int) {
	t.Helper()
	for i, p := range placements {
		r := p.Bounds
		if r.X < work.X || r.Y < work.Y || r.X+r.Width > work.X+work.Width || r.Y+r.Height > work.Y+work.Height {
			t.Fatalf("placement %v outside %v", r, work)
		}
		for _, o := range append(append([]Rect(nil), obstacles...), placementBounds(placements[:i])...) {
			if o.Width == 0 || o.Height == 0 || o.X >= work.X+work.Width || o.Y >= work.Y+work.Height || o.X+o.Width <= work.X || o.Y+o.Height <= work.Y {
				continue
			}
			if r.X < o.X+o.Width+gap && o.X < r.X+r.Width+gap && r.Y < o.Y+o.Height+gap && o.Y < r.Y+r.Height+gap {
				t.Fatalf("placement %v violates gap %d from %v", r, gap, o)
			}
		}
	}
}

func placementBounds(placements []Placement) []Rect {
	result := make([]Rect, len(placements))
	for i, p := range placements {
		result[i] = p.Bounds
	}
	return result
}
