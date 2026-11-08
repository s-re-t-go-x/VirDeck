package emu

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"virdeck/core/screenlayout"
)

func TestProjectionSettingsArguments(t *testing.T) {
	settings := ProjectionSettings{MaxFPS: 15, MaxSize: 960, BitRateMbps: 2, Audio: false}
	want := []string{"--video-bit-rate=2M", "--max-fps=15", "--max-size=960", "--no-audio"}
	if got := projectionSettingsArgs(settings); !reflect.DeepEqual(got, want) {
		t.Fatalf("args %v", got)
	}
	settings = ProjectionSettings{BitRateMbps: 8, Audio: true}
	want = []string{"--video-bit-rate=8M", "--audio-codec=aac", "--require-audio"}
	if got := projectionSettingsArgs(settings); !reflect.DeepEqual(got, want) {
		t.Fatalf("audio args %v", got)
	}
}

func (h *testScreenHost) close(w screenlayout.Window) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gone[w.PID] = true
	var keep []screenlayout.Window
	for _, s := range h.seeds {
		if s.Handle != w.Handle {
			keep = append(keep, s)
		}
	}
	h.seeds = keep
	return nil
}
func TestRestartProjectionAppliesParametersAndKeepsGeometry(t *testing.T) {
	c, h := projectionTestController(t)
	if _, err := c.StartProjection(context.Background(), "one", 5554, "one (emulator-5554)"); err != nil {
		t.Fatal(err)
	}
	c.screenMu.Lock()
	old := c.projections["one"]
	c.screenMu.Unlock()
	if err := c.RestartProjection(context.Background(), "one", 5554, "one (emulator-5554)", ProjectionSettings{MaxFPS: 15, MaxSize: 960, BitRateMbps: 2, Audio: true}); err != nil {
		t.Fatal(err)
	}
	c.screenMu.Lock()
	current := c.projections["one"]
	c.screenMu.Unlock()
	if current == old || !strings.Contains(strings.Join(current.cmd.Args, " "), "--require-audio") {
		t.Fatal("new settings not applied")
	}
	if r := h.placements[current.cmd.Process.Pid]; r.Width != 376 || r.Height != 680 {
		t.Fatalf("geometry lost %+v", r)
	}
}
