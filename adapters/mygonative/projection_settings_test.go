package mygonative

import (
	"context"
	"github.com/egoist/mygo/ui"
	"testing"
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
