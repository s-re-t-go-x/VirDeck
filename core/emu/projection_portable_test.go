package emu

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"virdeck/config"
	"virdeck/core/screenlayout"
)

type unsupportedProjectionHost struct{ *testScreenHost }

func (h unsupportedProjectionHost) windows() ([]screenlayout.Window, error) {
	return nil, screenlayout.ErrUnsupported
}
func TestProjectionStartsWithoutNativeEnumeration(t *testing.T) {
	c, h := projectionTestController(t)
	c.screenHost = unsupportedProjectionHost{h}
	fresh, err := c.StartProjection(context.Background(), "portable", 5554, "portable")
	if err != nil || !fresh {
		t.Fatalf("start: fresh=%v err=%v", fresh, err)
	}
	fresh, err = c.StartProjection(context.Background(), "portable", 5554, "portable")
	if err != nil || fresh {
		t.Fatalf("duplicate: fresh=%v err=%v", fresh, err)
	}
}
func TestProjectionGeometryArgs(t *testing.T) {
	got, err := projectionBoundsArgs(screenlayout.Rect{X: -1200, Y: 40, Width: 360, Height: 640})
	want := []string{"--window-x=-1200", "--window-y=40", "--window-width=360", "--window-height=640"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := projectionBoundsArgs(screenlayout.Rect{Width: -1, Height: 640}); err == nil {
		t.Fatal("accepted invalid size")
	}
}

func TestPortableRestartPreservesLaunchGeometry(t *testing.T) {
	c, h := projectionTestController(t)
	c.screenHost = unsupportedProjectionHost{h}
	bounds := screenlayout.Rect{X: -900, Y: 20, Width: 360, Height: 640}
	if _, err := c.StartProjectionWithBounds(context.Background(), "portable", 5554, "portable", config.Values.Projection, bounds); err != nil {
		t.Fatal(err)
	}
	before, err := c.ScrcpyWindows()
	if err != nil || len(before) != 1 || before[0].Window.Handle != 0 || before[0].Window.Bounds != bounds {
		t.Fatalf("tracked: %v %v", before, err)
	}
	if err := c.RestartProjection(context.Background(), "portable", 5554, "portable", config.Values.Projection); err != nil {
		t.Fatal(err)
	}
	after, err := c.ScrcpyWindows()
	if err != nil || len(after) != 1 || after[0].Window.PID == before[0].Window.PID || after[0].Window.Bounds != bounds {
		t.Fatalf("restart: %v %v", after, err)
	}
	raw, err := os.ReadFile(h.file)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("launches %s", raw)
	}
	for _, line := range lines {
		var launch screenLaunch
		if err := json.Unmarshal([]byte(line), &launch); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"--window-x=-900", "--window-y=20", "--window-width=360", "--window-height=640"} {
			found := false
			for _, arg := range launch.Args {
				if arg == want {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %v", want, launch.Args)
			}
		}
	}
}
