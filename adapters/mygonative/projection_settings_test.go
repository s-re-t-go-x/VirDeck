package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
	"virdeck/core/service"
)

func TestGlobalProjectionSettingsSaveWithShortWindow(t *testing.T) {
	vm := newTestVM(t)
	vm.openSettings()
	vm.settingsProjection = projectionForm{fps: 15, size: 960, rate: 2, audio: true}
	tester := ui.NewTester(vm.view, 610, 480)
	tester.Frame()
	if err := tester.Click("保存全局设置"); err != nil {
		t.Fatal(err)
	}
	want := service.ProjectionSettings{MaxFPS: 15, MaxSize: 960, BitRateMbps: 2, Audio: true}
	if got := vm.svc.GetSettings().Projection; got != want {
		t.Fatalf("settings %+v", got)
	}
}

type projectionTestSvc struct {
	service.Service
	custom bool
	p      service.ProjectionSettings
	reset  bool
}

func (s *projectionTestSvc) GetProjectionSettings(string) (service.ProjectionSettings, bool, error) {
	return s.p, s.custom, nil
}
func (s *projectionTestSvc) SetProjectionSettings(_ context.Context, _ string, p *service.ProjectionSettings) error {
	if p == nil {
		s.reset = true
		s.custom = false
	} else {
		s.p = *p
		s.custom = true
	}
	return nil
}
func TestInstanceProjectionOverrideAndReset(t *testing.T) {
	vm := newTestVM(t)
	svc := &projectionTestSvc{Service: vm.svc, p: service.ProjectionSettings{BitRateMbps: 8}}
	vm.svc = svc
	vm.openProjectionSettings(vm.instances[0])
	vm.projection.inherit = false
	vm.projection.form = projectionForm{fps: 20, size: 720, rate: 3, audio: true}
	tester := ui.NewTester(vm.view, 610, 480)
	tester.Frame()
	if err := tester.Click("保存投屏设置"); err != nil {
		t.Fatal(err)
	}
	if !svc.custom || svc.p.MaxFPS != 20 || !svc.p.Audio {
		t.Fatalf("override %+v", svc)
	}
	vm.projection.inherit = true
	tester.Frame()
	if err := tester.Click("保存投屏设置"); err != nil {
		t.Fatal(err)
	}
	if !svc.reset {
		t.Fatal("inheritance not restored")
	}
}

func TestGlobalAudioCheckboxPersists(t *testing.T) {
	vm := newTestVM(t)
	vm.openSettings()
	tester := ui.NewTester(vm.view, 610, 780)
	tester.Frame()
	if err := tester.Click("投屏与声音"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("播放安卓声音"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("保存全局设置"); err != nil {
		t.Fatal(err)
	}
	if !vm.svc.GetSettings().Projection.Audio {
		t.Fatal("audio checkbox did not persist")
	}
}

func TestInstanceAudioCheckboxReachableAtMinimumHeight(t *testing.T) {
	vm := newTestVM(t)
	vm.svc = &projectionTestSvc{Service: vm.svc, p: service.ProjectionSettings{BitRateMbps: 8}, custom: true}
	vm.openProjectionSettings(vm.instances[0])
	tester := ui.NewTester(vm.view, 610, 480)
	tester.Frame()
	if err := tester.Click("播放安卓声音"); err != nil {
		t.Fatal(err)
	}
	if !vm.projection.form.audio {
		t.Fatal("minimum-height audio toggle failed")
	}
}

func TestProjectionFieldsDecreaseToMinimum(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field int
		want  float64
	}{
		{"frame rate", 0, 5},
		{"longest edge", 1, 320},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := projectionFormFrom(service.ProjectionSettings{BitRateMbps: 8})
			if p.fps != 60 || p.size != 1280 {
				t.Fatalf("expected concrete defaults, got %+v", p)
			}
			tester := ui.NewTester(func(c *ui.Context) { buildProjectionFields(c, &p, false) }, 610, 480)
			tester.Frame()
			// Locate the decrement button alongside the corresponding field label.
			label := "投屏帧率"
			if tc.field == 1 {
				label = "投屏最长边"
			}
			r, ok := tester.Find(label)
			if !ok {
				t.Fatal("missing field label")
			}
			// The form has one decrement button per number input. Use its row's Y.
			minus, ok := tester.Find("Decrease")
			if !ok {
				t.Fatal("missing decrement control")
			}
			for i := 0; i < 100; i++ {
				tester.ClickAt(minus.X+minus.W/2, r.Y+r.H/2)
			}
			got := p.fps
			if tc.field == 1 {
				got = p.size
			}
			if got != tc.want {
				t.Fatalf("decrement stopped at %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProjectionFieldsStopAtUpperLimits(t *testing.T) {
	for _, tc := range []struct {
		label string
		value func(*projectionForm) float64
		want  float64
	}{
		{"投屏帧率", func(p *projectionForm) float64 { return p.fps }, 240},
		{"投屏最长边", func(p *projectionForm) float64 { return p.size }, 8192},
		{"投屏码率", func(p *projectionForm) float64 { return p.rate }, 100},
	} {
		t.Run(tc.label, func(t *testing.T) {
			p := projectionForm{fps: 239, size: 8190, rate: 99}
			tester := ui.NewTester(func(c *ui.Context) { buildProjectionFields(c, &p, false) }, 610, 480)
			tester.Frame()
			r, ok := tester.Find(tc.label)
			if !ok {
				t.Fatal("missing field label")
			}
			plus, ok := tester.Find("Increase")
			if !ok {
				t.Fatal("missing increment control")
			}
			for i := 0; i < 3; i++ {
				tester.ClickAt(plus.X+plus.W/2, r.Y+r.H/2)
			}
			if got := tc.value(&p); got != tc.want {
				t.Fatalf("increment reached %v, want upper limit %v", got, tc.want)
			}
		})
	}
}

func TestProjectionPresetFillsVideoAndPreservesAudio(t *testing.T) {
	vm := newTestVM(t)
	vm.openSettings()
	vm.settingsTab = 1
	vm.settingsProjection.audio = true
	tester := ui.NewTester(vm.view, 610, 780)
	tester.Frame()
	if err := tester.Click("省资源多开"); err != nil {
		t.Fatal(err)
	}
	if got := vm.settingsProjection.values(); got != (service.ProjectionSettings{MaxFPS: 15, MaxSize: 960, BitRateMbps: 2, Audio: true}) {
		t.Fatalf("preset %+v", got)
	}
}

func TestBatchProjectionSettingsReachableForMixedStates(t *testing.T) {
	vm := newTestVM(t)
	vm.svc = &projectionTestSvc{Service: vm.svc, p: service.ProjectionSettings{MaxFPS: 60, MaxSize: 1280, BitRateMbps: 8}}
	vm.selected.Add(vm.instances[0].ID)
	vm.selected.Add(vm.instances[1].ID)
	tester := ui.NewTester(vm.view, 1180, 780)
	tester.Frame()
	if err := tester.Click("投屏设置"); err != nil {
		t.Fatal(err)
	}
	if !vm.projection.show {
		t.Fatal("batch form not opened")
	}
}

// Android process/window boundaries are replaced; settings persistence stays real.
type projectionControlSvc struct {
	service.Service
	overrides map[string]service.ProjectionSettings
	openIDs   []string
	reopened  chan string
}

func (s *projectionControlSvc) GetSettings() service.Settings {
	st := s.Service.GetSettings()
	st.ProjectionOverrides = map[string]service.ProjectionSettings{}
	for id, p := range s.overrides {
		st.ProjectionOverrides[id] = p
	}
	return st
}
func (s *projectionControlSvc) GetProjectionSettings(id string) (service.ProjectionSettings, bool, error) {
	if p, ok := s.overrides[id]; ok {
		return p, true, nil
	}
	return s.Service.GetSettings().Projection, false, nil
}
func (s *projectionControlSvc) SetProjectionSettings(_ context.Context, id string, p *service.ProjectionSettings) error {
	if p == nil {
		delete(s.overrides, id)
	} else {
		s.overrides[id] = *p
	}
	return nil
}
func (s *projectionControlSvc) InspectScreenLayout(context.Context) (service.ScreenLayoutSnapshot, error) {
	return service.ScreenLayoutSnapshot{OpenIDs: append([]string(nil), s.openIDs...)}, nil
}
func (s *projectionControlSvc) ReopenScreen(_ context.Context, id string) error {
	s.reopened <- id
	return nil
}
func drainProjectionTask(t *testing.T, vm *viewModel, posts chan func()) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		busy := vm.settingsRefreshing
		for _, task := range vm.tasks {
			if !task.finished {
				busy = true
			}
		}
		if !busy {
			return
		}
		select {
		case f := <-posts:
			f()
		case <-deadline:
			t.Fatal("projection task stalled")
		}
	}
}
func TestGlobalReopenOnlyTouchesOpenFollowingInstances(t *testing.T) {
	vm := newTestVM(t)
	svc := &projectionControlSvc{Service: vm.svc, overrides: map[string]service.ProjectionSettings{vm.instances[2].ID: {MaxFPS: 15, MaxSize: 960, BitRateMbps: 2}}, openIDs: []string{vm.instances[0].ID, vm.instances[2].ID}, reopened: make(chan string, 4)}
	vm.svc = svc
	posts := make(chan func(), 32)
	vm.post = func(f func()) { posts <- f }
	vm.openSettings()
	vm.settingsProjection.fps = 30
	vm.saveGlobalAndReopen()
	drainProjectionTask(t, vm, posts)
	if len(svc.reopened) != 1 {
		t.Fatalf("reopened %d windows", len(svc.reopened))
	}
	if id := <-svc.reopened; id != "a1b2c3d4" {
		t.Fatalf("reopened custom/closed window %s", id)
	}
	if vm.svc.GetSettings().Projection.MaxFPS != 30 {
		t.Fatal("global values not saved before reopen")
	}
}
func TestAudioShortcutPreservesVideoAndDoesNotOpenClosedProjection(t *testing.T) {
	for _, open := range []bool{false, true} {
		t.Run(fmt.Sprint(open), func(t *testing.T) {
			vm := newTestVM(t)
			p := service.ProjectionSettings{MaxFPS: 15, MaxSize: 960, BitRateMbps: 2}
			svc := &projectionControlSvc{Service: vm.svc, overrides: map[string]service.ProjectionSettings{vm.instances[0].ID: p}, reopened: make(chan string, 4)}
			if open {
				svc.openIDs = []string{vm.instances[0].ID}
			}
			vm.svc = svc
			posts := make(chan func(), 32)
			vm.post = func(f func()) { posts <- f }
			vm.toggleProjectionAudio(vm.instances[0])
			drainProjectionTask(t, vm, posts)
			got, custom, _ := svc.GetProjectionSettings(vm.instances[0].ID)
			p.Audio = true
			if got != p || !custom {
				t.Fatalf("sound changed video parameters %+v", got)
			}
			want := 0
			if open {
				want = 1
			}
			if len(svc.reopened) != want {
				t.Fatalf("reopen count=%d want=%d", len(svc.reopened), want)
			}
		})
	}
}
func TestBatchProjectionSavesAndRestoresInheritanceForFrozenTargets(t *testing.T) {
	vm := newTestVM(t)
	svc := &projectionControlSvc{Service: vm.svc, overrides: map[string]service.ProjectionSettings{}, reopened: make(chan string, 4)}
	vm.svc = svc
	vm.openProjectionBatch(vm.instances[:2])
	vm.selected.Clear()
	vm.selected.Add(vm.instances[2].ID)
	vm.projection.inherit = false
	vm.projection.form = projectionForm{fps: 15, size: 960, rate: 2, audio: true}
	vm.saveInstanceProjection(false)
	if len(svc.overrides) != 2 {
		t.Fatalf("batch saved %d overrides", len(svc.overrides))
	}
	if _, ok := svc.overrides[vm.instances[2].ID]; ok {
		t.Fatal("selection drift changed target")
	}
	vm.projection.inherit = true
	vm.saveInstanceProjection(false)
	if len(svc.overrides) != 0 {
		t.Fatal("batch did not restore inheritance")
	}
}

func TestAudioShortcutRestoresInheritanceWhenMatchingGlobal(t *testing.T) {
	vm := newTestVM(t)
	svc := &projectionControlSvc{Service: vm.svc, overrides: map[string]service.ProjectionSettings{}, reopened: make(chan string, 4)}
	vm.svc = svc
	posts := make(chan func(), 32)
	vm.post = func(f func()) { posts <- f }

	vm.toggleProjectionAudio(vm.instances[0])
	drainProjectionTask(t, vm, posts)
	got, custom, _ := svc.GetProjectionSettings(vm.instances[0].ID)
	if !custom || !got.Audio {
		t.Fatalf("first toggle should create audio override, got %+v custom=%v", got, custom)
	}

	vm.toggleProjectionAudio(vm.instances[0])
	drainProjectionTask(t, vm, posts)
	got, custom, _ = svc.GetProjectionSettings(vm.instances[0].ID)
	if custom || got.Audio {
		t.Fatalf("second toggle should restore global inheritance, got %+v custom=%v", got, custom)
	}
}

func TestDetailAudioVisibilityFollowsStartedActions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   service.State
		port    int
		visible bool
	}{
		{"stopped", service.Stopped, 0, false},
		{"failed without process", service.Failed, 0, false},
		{"starting", service.Starting, 5554, true},
		{"running", service.Running, 5554, true},
		{"failed with process", service.Failed, 5554, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := newTestVM(t)
			vm.svc = &projectionTestSvc{Service: vm.svc, p: service.ProjectionSettings{MaxFPS: 60, MaxSize: 1280, BitRateMbps: 8}}
			inst := vm.instances[0]
			inst.State = tc.state
			inst.Port = tc.port
			tester := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(workbenchTheme)
				ui.Column(c).Width(detailWidth).Children(func() { vm.buildDetailBody(c, inst) })
			}, 320, 780)
			tester.Frame()
			_, hasAudio := tester.Find("开启声音")
			_, hasStop := tester.Find("停止")
			if hasAudio != tc.visible || hasStop != tc.visible {
				t.Fatalf("audio=%v stop=%v want=%v", hasAudio, hasStop, tc.visible)
			}
		})
	}
}
