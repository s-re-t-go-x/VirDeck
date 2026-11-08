package mygonative

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"virdeck/core/service"
)

func TestRenderBasicFeaturePanels(t *testing.T) {
	dir := os.Getenv("VIRDECK_SHOT_DIR")
	if dir == "" {
		t.Skip("screenshots not requested")
	}
	cases := []struct {
		name string
		prep func(*viewModel)
	}{
		{"settings", func(vm *viewModel) {
			vm.openSettings()
			vm.settingsTab = 1
			vm.settingsProjection = projectionForm{fps: 15, size: 960, rate: 2, audio: true}
		}},
		{"projection", func(vm *viewModel) {
			vm.svc = &projectionTestSvc{Service: vm.svc, p: service.ProjectionSettings{MaxFPS: 15, MaxSize: 960, BitRateMbps: 2, Audio: true}, custom: true}
			vm.openProjectionSettings(vm.instances[0])
		}},
		{"groups", func(vm *viewModel) {
			vm.groups.show = true
			vm.groups.snapshot = service.GroupSnapshot{Groups: []service.Group{{ID: "work", Name: "工作"}}}
		}},
		{"workbench", func(vm *viewModel) {
			vm.groups.snapshot = service.GroupSnapshot{Groups: []service.Group{{ID: "work", Name: "工作"}}, Memberships: map[string]string{"a1b2c3d4": "work"}}
			vm.groups.active = "work"
			vm.selected.Add("a1b2c3d4")
		}},
	}
	for _, tc := range cases {
		for _, h := range []int{480, 780} {
			vm := newTestVM(t)
			tc.prep(vm)
			tester := ui.NewTester(vm.view, 610, h)
			tester.Frame()
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("basic_%s_610_%d.png", tc.name, h)))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, tester.Image()); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
		}
	}
}
