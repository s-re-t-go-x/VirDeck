package screenlayout

import (
	"reflect"
	"testing"
)

func TestTileFitsMixedAspectRatiosAtLargestCommonHeight(t *testing.T) {
	windows := []Window{{Bounds: Rect{Width: 100, Height: 200}, ClientWidth: 100, ClientHeight: 200}, {Bounds: Rect{Width: 400, Height: 200}, ClientWidth: 400, ClientHeight: 200}}
	got, err := Tile(Rect{Width: 800, Height: 400}, windows, 0, 0)
	want := []Rect{{Width: 160, Height: 320}, {X: 160, Width: 640, Height: 320}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Tile()=%+v,%v; want %+v", got, err, want)
	}
}

func TestTileNegativeOriginAndFrameExtents(t *testing.T) {
	got, err := Tile(Rect{X: -100, Y: 20, Width: 500, Height: 400}, []Window{{Bounds: Rect{Width: 116, Height: 239}, ClientWidth: 100, ClientHeight: 200}}, 0, 10)
	want := []Rect{{X: -90, Y: 30, Width: 187, Height: 380}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Tile()=%+v,%v; want %+v", got, err, want)
	}
}

func TestTileCapsClientHeightWithDifferentFrames(t *testing.T) {
	windows := []Window{{Bounds: Rect{Width: 116, Height: 240}, ClientWidth: 100, ClientHeight: 200}, {Bounds: Rect{Width: 420, Height: 230}, ClientWidth: 400, ClientHeight: 200}}
	got, err := Tile(Rect{Width: 1000, Height: 500}, windows, 200, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Tile returned %d windows; want 2", len(got))
	}
	for i, r := range got {
		if r.Height != []int{240, 230}[i] || r.Width != []int{116, 420}[i] {
			t.Fatalf("frame or cap lost: %+v", got)
		}
	}
	assertLayout(t, Rect{Width: 1000, Height: 500}, got, 10)
}

func TestTileChoosesGridForManyWindows(t *testing.T) {
	windows := make([]Window, 4)
	for i := range windows {
		windows[i] = Window{Bounds: Rect{Width: 100, Height: 100}, ClientWidth: 100, ClientHeight: 100}
	}
	got, err := Tile(Rect{Width: 450, Height: 450}, windows, 0, 10)
	want := []Rect{{X: 10, Y: 10, Width: 210, Height: 210}, {X: 230, Y: 10, Width: 210, Height: 210}, {X: 10, Y: 230, Width: 210, Height: 210}, {X: 230, Y: 230, Width: 210, Height: 210}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Tile()=%+v,%v; want %+v", got, err, want)
	}
	assertLayout(t, Rect{Width: 450, Height: 450}, got, 10)
}

func TestTileDropsOuterMarginOnlyWhenNecessary(t *testing.T) {
	got, err := Tile(Rect{Width: 5, Height: 5}, []Window{{Bounds: Rect{Width: 1, Height: 1}, ClientWidth: 1, ClientHeight: 1}}, 0, 10)
	if err != nil || !reflect.DeepEqual(got, []Rect{{Width: 5, Height: 5}}) {
		t.Fatalf("Tile()=%+v,%v", got, err)
	}
}

func TestTileRejectsInvalidAndInsufficientGeometry(t *testing.T) {
	valid := Window{Bounds: Rect{Width: 116, Height: 240}, ClientWidth: 100, ClientHeight: 200}
	for _, tc := range []struct {
		name     string
		work     Rect
		windows  []Window
		cap, gap int
	}{
		{"empty work", Rect{}, []Window{valid}, 0, 0},
		{"zero client", Rect{Width: 100, Height: 100}, []Window{{Bounds: Rect{Width: 10, Height: 10}}}, 0, 0},
		{"negative frame", Rect{Width: 100, Height: 100}, []Window{{Bounds: Rect{Width: 10, Height: 10}, ClientWidth: 20, ClientHeight: 20}}, 0, 0},
		{"frame too large", Rect{Width: 10, Height: 10}, []Window{valid}, 0, 0},
		{"negative gap", Rect{Width: 100, Height: 100}, []Window{valid}, 0, -1},
		{"negative cap", Rect{Width: 100, Height: 100}, []Window{valid}, -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Tile(tc.work, tc.windows, tc.cap, tc.gap); err == nil {
				t.Fatal("expected geometry error")
			}
		})
	}
	if got, err := Tile(Rect{}, nil, 0, 0); err != nil || len(got) != 0 {
		t.Fatalf("empty Tile()=%v,%v", got, err)
	}
}

func assertLayout(t *testing.T, work Rect, rs []Rect, gap int) {
	t.Helper()
	for i, r := range rs {
		if r.X < work.X || r.Y < work.Y || r.X+r.Width > work.X+work.Width || r.Y+r.Height > work.Y+work.Height {
			t.Fatalf("out of bounds: %+v", r)
		}
		for _, s := range rs[:i] {
			if r.X < s.X+s.Width+gap && s.X < r.X+r.Width+gap && r.Y < s.Y+s.Height+gap && s.Y < r.Y+r.Height+gap {
				t.Fatalf("overlap/gap: %+v and %+v", r, s)
			}
		}
	}
}
