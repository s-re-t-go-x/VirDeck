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

func TestRenderProjectionGroupUX(t *testing.T) {
	dir := os.Getenv("VIRDECK_UX_SHOTS")
	if dir == "" {
		t.Skip("screenshots not requested")
	}
	for _, width := range []int{610, 1360} {
		for _, name := range []string{"global", "instance", "batch", "groups", "main", "create"} {
			vm := newTestVM(t)
			vm.svc = &projectionControlSvc{Service: vm.svc, overrides: map[string]service.ProjectionSettings{vm.instances[0].ID: {MaxFPS: 15, MaxSize: 960, BitRateMbps: 2}}, reopened: make(chan string, 4)}
			vm.groups.snapshot = service.GroupSnapshot{Groups: []service.Group{{ID: "work", Name: "工作账号"}, {ID: "daily", Name: "日常与社交应用"}, {ID: "long", Name: "这是一个用于检查较长名称展示的分组"}}, Memberships: map[string]string{vm.instances[0].ID: "work", vm.instances[2].ID: "work", vm.instances[1].ID: "daily"}}
			switch name {
			case "global":
				vm.openSettings()
				vm.settingsTab = 1
			case "instance":
				vm.openProjectionSettings(vm.instances[0])
			case "batch":
				vm.openProjectionBatch(vm.instances[:2])
			case "groups":
				vm.openGroups(nil)
			case "main":
				vm.selected.Add(vm.instances[0].ID)
			case "create":
				vm.showCreate = true
				vm.createGroupID = "work"
			}
			height := 780
			if width == 610 {
				height = 480
			}
			tester := ui.NewTester(vm.view, width, height)
			tester.Frame()
			file, err := os.Create(filepath.Join(dir, fmt.Sprintf("ux_%s_%d.png", name, width)))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, tester.Image())
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
