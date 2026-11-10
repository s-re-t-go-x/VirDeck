package emu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"virdeck/core/screenlayout"
)

type screenLaunch struct {
	Args  []string
	PID   int
	Title string
}

func init() {
	if os.Getenv("VIRDECK_TEST_SCRCPY_HELPER") != "1" {
		return
	}
	title := ""
	for i, arg := range os.Args {
		if arg == "--window-title" && i+1 < len(os.Args) {
			title = os.Args[i+1]
		}
	}
	raw, _ := json.Marshal(screenLaunch{PID: os.Getpid(), Title: title, Args: os.Args[1:]})
	file, err := os.OpenFile(os.Getenv("VIRDECK_SCREEN_LAUNCHES"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	_, _ = file.Write(append(raw, '\n'))
	_ = file.Close()
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

type testScreenHost struct {
	mu          sync.Mutex
	file, exe   string
	seeds       []screenlayout.Window
	displays    []screenlayout.Display
	gone        map[int]bool
	activations []int
	placements  map[int]screenlayout.Rect
	titles      map[int]string
}

func (h *testScreenHost) windows() ([]screenlayout.Window, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]screenlayout.Window(nil), h.seeds...)
	raw, _ := os.ReadFile(h.file)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var launch screenLaunch
		if json.Unmarshal([]byte(line), &launch) != nil || h.gone[launch.PID] {
			continue
		}
		title := launch.Title
		if replacement := h.titles[launch.PID]; replacement != "" {
			title = replacement
		}
		out = append(out, screenlayout.Window{Handle: uintptr(launch.PID), PID: launch.PID, Title: title, Executable: h.exe, Bounds: screenlayout.Rect{Width: 376, Height: 680}, ClientWidth: 360, ClientHeight: 640, Scale: 1})
	}
	return out, nil
}
func (h *testScreenHost) monitors() ([]screenlayout.Display, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]screenlayout.Display(nil), h.displays...), nil
}
func (h *testScreenHost) place(w screenlayout.Window, r screenlayout.Rect) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.placements[w.PID] = r
	return nil
}
func (h *testScreenHost) activate(w screenlayout.Window) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.activations = append(h.activations, w.PID)
	return nil
}
func (h *testScreenHost) title(w screenlayout.Window, title string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.titles[w.PID] = title
	return nil
}

func projectionTestController(t *testing.T) (*Controller, *testScreenHost) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "scrcpy.exe")
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, raw, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "launches.jsonl")
	t.Setenv("VIRDECK_TEST_SCRCPY_HELPER", "1")
	t.Setenv("VIRDECK_SCREEN_LAUNCHES", file)
	host := &testScreenHost{file: file, exe: exe, gone: map[int]bool{}, placements: map[int]screenlayout.Rect{}, titles: map[int]string{}, displays: []screenlayout.Display{{ID: "primary", Scale: 1, Primary: true, WorkArea: screenlayout.Rect{Width: 1920, Height: 1040}}, {ID: "left", Scale: 1.25, WorkArea: screenlayout.Rect{X: -1280, Width: 1280, Height: 960}}}}
	c := &Controller{dataDir: dir, sdkRoot: filepath.Join(dir, "sdk"), androidHomeDir: filepath.Join(dir, ".android"), scrcpyPath: exe, adbServerPort: 5137, screenHost: host}
	t.Cleanup(func() {
		c.screenMu.Lock()
		processes := make([]*projectionProcess, 0, len(c.projections))
		for _, p := range c.projections {
			processes = append(processes, p)
		}
		c.screenMu.Unlock()
		for _, p := range processes {
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(3 * time.Second):
				t.Error("projection helper did not exit")
			}
		}
	})
	return c, host
}

func TestProjectionConcurrentOpenStartsOneProcess(t *testing.T) {
	c, host := projectionTestController(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	created := make(chan bool, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh, err := c.StartProjection(context.Background(), "one", 5554, "工作 (emulator-5554)")
			errs <- err
			created <- fresh
		}()
	}
	wg.Wait()
	close(errs)
	close(created)
	freshCount := 0
	for fresh := range created {
		if fresh {
			freshCount++
		}
	}
	if freshCount != 1 {
		t.Fatalf("new process classification count %d", freshCount)
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(host.file)
	if count := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); count != 1 {
		t.Fatalf("opened %d processes for one instance", count)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.activations) != 3 {
		t.Fatalf("duplicate opens did not activate existing window: %v", host.activations)
	}
}
func TestProjectionRediscoveryRequiresOwnedExecutable(t *testing.T) {
	c, host := projectionTestController(t)
	host.seeds = []screenlayout.Window{{Handle: 1, PID: 99, Executable: host.exe, Title: projectionTitle("one", "旧名 (emulator-5554)"), Bounds: screenlayout.Rect{Width: 376, Height: 680}, ClientWidth: 360, ClientHeight: 640, Scale: 1}}
	if err := c.StartScrcpy(context.Background(), "one", 5554, "新名 (emulator-5554)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(host.file); !os.IsNotExist(err) {
		t.Fatal("rediscovered projection launched another process")
	}
	host.mu.Lock()
	activated := append([]int(nil), host.activations...)
	host.mu.Unlock()
	if len(activated) != 1 || activated[0] != 99 {
		t.Fatalf("existing process not activated: %v", activated)
	}
	host.mu.Lock()
	host.seeds[0].Executable = filepath.Join(t.TempDir(), "other.exe")
	host.mu.Unlock()
	if err := c.StartScrcpy(context.Background(), "one", 5554, "新名 (emulator-5554)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(host.file); err != nil {
		t.Fatal("unowned window prevented opening own projection")
	}
}
func TestProjectionWindowCanReopenAfterProcessExit(t *testing.T) {
	c, host := projectionTestController(t)
	if err := c.StartScrcpy(context.Background(), "one", 5554, "one (emulator-5554)"); err != nil {
		t.Fatal(err)
	}
	c.screenMu.Lock()
	p := c.projections["one"]
	c.screenMu.Unlock()
	host.mu.Lock()
	host.gone[p.cmd.Process.Pid] = true
	host.mu.Unlock()
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("helper exit timeout")
	}
	if err := c.StartScrcpy(context.Background(), "one", 5554, "one (emulator-5554)"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(host.file)
	if count := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); count != 2 {
		t.Fatalf("closed projection not reopened: %s", raw)
	}
}
func TestProjectionLayoutSelectedScopeAndMissingMonitor(t *testing.T) {
	c, host := projectionTestController(t)
	for i, id := range []string{"one", "two"} {
		host.seeds = append(host.seeds, screenlayout.Window{Handle: uintptr(i + 1), PID: i + 10, Executable: host.exe, Title: projectionTitle(id, id), Bounds: screenlayout.Rect{Width: 376, Height: 680}, ClientWidth: 360, ClientHeight: 640, Scale: 1})
	}
	count, err := c.ArrangeScrcpy(context.Background(), []string{"two"}, "left", 360, 8)
	if err != nil || count != 1 {
		t.Fatalf("selected layout failed: %d %v", count, err)
	}
	host.mu.Lock()
	r, ok := host.placements[11]
	_, other := host.placements[10]
	host.mu.Unlock()
	if !ok || other || r.X < -1280 || r.X+r.Width > 0 || r.Y < 0 || r.Y+r.Height > 960 {
		t.Fatalf("wrong target/scope/area: %+v other=%v", r, other)
	}
	if _, err := c.ArrangeScrcpy(context.Background(), []string{"one"}, "missing", 0, 8); err == nil {
		t.Fatal("missing display accepted")
	}
}
func TestProjectionMarkerRoundTrip(t *testing.T) {
	for _, id := range []string{"a1b2", "中文 ID ]"} {
		title := projectionTitle(id, "Display [text]")
		if got, ok := projectionID(title); !ok || got != id {
			t.Fatalf("bad marker %q => %q %v", title, got, ok)
		}
	}
	if _, ok := projectionID("Other app"); ok {
		t.Fatal("unmarked window accepted")
	}
}

func TestProjectionDoesNotActivateStalePortAfterRestart(t *testing.T) {
	c, h := projectionTestController(t)
	h.seeds = []screenlayout.Window{{Handle: 1, PID: 99, Executable: h.exe, Title: projectionTitle("one", "旧名 (emulator-5554)")}}
	if err := c.StartScrcpy(context.Background(), "one", 5556, "新名 (emulator-5556)"); err == nil {
		t.Fatal("activated projection for the previous emulator port")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.activations) != 0 {
		t.Fatalf("wrong serial activated: %v", h.activations)
	}
}

func seedLayoutWindows(h *testScreenHost) {
	h.displays = []screenlayout.Display{{ID: "primary", Primary: true, Scale: 1, WorkArea: screenlayout.Rect{Width: 500, Height: 360}}}
	for i, id := range []string{"one", "two", "three"} {
		h.seeds = append(h.seeds, screenlayout.Window{Handle: uintptr(i + 1), PID: i + 10, Executable: h.exe, Title: projectionTitle(id, id), Bounds: screenlayout.Rect{X: 8 + i*700, Y: 8, Width: 200, Height: 340}, ClientWidth: 180, ClientHeight: 300, Scale: 1})
	}
}
func TestProjectionReferenceKeepsAnchorAndLeavesOverflowUnmoved(t *testing.T) {
	c, h := projectionTestController(t)
	seedLayoutWindows(h)
	n, err := c.ArrangeProjection(context.Background(), []string{"one", "two", "three"}, ScreenLayoutOptions{DisplayID: "primary", ReferenceID: "one", Gap: 8})
	if n != 1 || err == nil || !strings.Contains(err.Error(), "桌面空间不足") {
		t.Fatalf("result %d %v", n, err)
	}
	if _, ok := h.placements[10]; ok {
		t.Fatal("reference window moved")
	}
	if _, ok := h.placements[12]; ok {
		t.Fatal("overflow window moved")
	}
	if r := h.placements[11]; r.Height != 340 || r.Width != 200 || r.X < 216 {
		t.Fatalf("wrong reference size/position %+v", r)
	}
}
func TestProjectionFillOnlyMovesNewWindow(t *testing.T) {
	c, h := projectionTestController(t)
	seedLayoutWindows(h)
	n, err := c.ArrangeProjection(context.Background(), []string{"two"}, ScreenLayoutOptions{DisplayID: "primary", Height: 300, Gap: 8, FillOnly: true})
	if n != 1 || err != nil {
		t.Fatalf("fill %d %v", n, err)
	}
	if len(h.placements) != 1 || h.placements[11].Height != 340 {
		t.Fatalf("fill moved existing: %v", h.placements)
	}
}
func TestProjectionRestoreRescalesAndSkipsMissingMonitor(t *testing.T) {
	c, h := projectionTestController(t)
	seedLayoutWindows(h)
	h.displays[0].Scale = 1.25
	h.displays[0].WorkArea = screenlayout.Rect{X: -1000, Width: 1200, Height: 900}
	saved := []SavedProjection{{InstanceID: "one", DisplayID: "primary", Scale: 1, WorkArea: screenlayout.Rect{Width: 1920, Height: 1080}, Bounds: screenlayout.Rect{X: 100, Y: 40, Width: 200, Height: 340}}, {InstanceID: "two", DisplayID: "gone", Scale: 1, Bounds: screenlayout.Rect{Width: 200, Height: 340}}}
	n, err := c.RestoreProjection(context.Background(), []string{"one", "two"}, saved)
	if n != 1 || err == nil {
		t.Fatalf("restore %d %v", n, err)
	}
	if r := h.placements[10]; r != (screenlayout.Rect{X: -875, Y: 50, Width: 250, Height: 425}) {
		t.Fatalf("restored %+v", r)
	}
	if _, ok := h.placements[11]; ok {
		t.Fatal("missing monitor window moved")
	}
}

func TestProjectionBackToBackFillReservesPendingPlacements(t *testing.T) {
	c, h := projectionTestController(t)
	seedLayoutWindows(h)
	h.displays[0].WorkArea = screenlayout.Rect{Width: 720, Height: 360}
	if n, err := c.ArrangeProjection(context.Background(), []string{"two"}, ScreenLayoutOptions{DisplayID: "primary", Height: 300, Gap: 8, FillOnly: true}); n != 1 || err != nil {
		t.Fatalf("first %d %v", n, err)
	}
	if n, err := c.ArrangeProjection(context.Background(), []string{"three"}, ScreenLayoutOptions{DisplayID: "primary", Height: 300, Gap: 8, FillOnly: true}); n != 1 || err != nil {
		t.Fatalf("second %d %v", n, err)
	}
	first, second := h.placements[11], h.placements[12]
	if first.X < second.X+second.Width && second.X < first.X+first.Width && first.Y < second.Y+second.Height && second.Y < first.Y+first.Height {
		t.Fatalf("pending fills overlap %v %v", first, second)
	}
	windows, err := c.ScrcpyWindows()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range windows {
		if p.InstanceID == "two" && p.Window.Bounds != first {
			t.Fatalf("snapshot lost pending layout: %+v want %+v", p.Window.Bounds, first)
		}
	}
}

func TestProjectionPendingSnapshotRespectsManualResize(t *testing.T) {
	c, h := projectionTestController(t)
	seedLayoutWindows(h)
	if _, err := c.ArrangeProjection(context.Background(), []string{"two"}, ScreenLayoutOptions{DisplayID: "primary", Height: 300, Gap: 8, FillOnly: true}); err != nil {
		t.Fatal(err)
	}
	manual := screenlayout.Rect{X: 80, Y: 40, Width: 150, Height: 220}
	h.seeds[1].Bounds = manual
	h.seeds[1].ClientWidth = 130
	h.seeds[1].ClientHeight = 180
	windows, err := c.ScrcpyWindows()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range windows {
		if p.InstanceID == "two" && p.Window.Bounds != manual {
			t.Fatalf("manual resize hidden by pending layout %+v", p.Window.Bounds)
		}
	}
}

func TestProjectionStartReportsLegacyAdoption(t *testing.T) {
	c, h := projectionTestController(t)
	title := "旧名 (emulator-5554)"
	h.seeds = []screenlayout.Window{{Handle: 1, PID: 99, Executable: h.exe, Title: title}}
	created, err := c.StartProjection(context.Background(), "one", 5554, title)
	if created || err != nil {
		t.Fatalf("adoption classified as new: %v %v", created, err)
	}
}
func TestProjectionOverflowMaximizedWindowKeepsWholeMonitorOccupied(t *testing.T) {
	c, h := projectionTestController(t)
	seedLayoutWindows(h)
	h.seeds = h.seeds[:2]
	h.seeds[0].Maximized = true
	h.seeds[0].Bounds.Height = 500
	n, err := c.ArrangeProjection(context.Background(), []string{"one", "two"}, ScreenLayoutOptions{DisplayID: "primary", Height: 300, Gap: 8})
	if n != 0 || err == nil || len(h.placements) != 0 {
		t.Fatalf("placed underneath unchanged maximized window %d %v %v", n, err, h.placements)
	}
}
