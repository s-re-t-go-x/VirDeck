package screenlayout

import "testing"

func TestDarwinCoordinateConversion(t *testing.T) {
	// Cocoa origin is bottom-left, CG/AX origin is top-left of main display.
	got := darwinVisibleRect(1440, -1280, 200, 1280, 1000)
	want := Rect{X: -1280, Y: 240, Width: 1280, Height: 1000}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
func TestDarwinIdentityRejectsStaleOrAmbiguousWindows(t *testing.T) {
	w := Window{Handle: 17, PID: 42, Executable: "/bin/scrcpy", Title: "device", Bounds: Rect{Width: 360, Height: 640}}
	if !darwinSameWindow(w, w) {
		t.Fatal("same window rejected")
	}
	for _, change := range []func(*Window){func(v *Window) { v.PID++ }, func(v *Window) { v.Handle++ }, func(v *Window) { v.Executable = "/bin/other" }} {
		v := w
		change(&v)
		if darwinSameWindow(w, v) {
			t.Fatal("stale identity accepted")
		}
	}
}
