package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"virdeck/core/screenlayout"
	"virdeck/core/service"
)

type screenTestService struct {
	service.Service
	snapshot        service.ScreenLayoutSnapshot
	request         service.ScreenLayoutRequest
	calls           int
	saved, restored int
	autoFill        bool
}

func (s *screenTestService) InspectScreenLayout(context.Context) (service.ScreenLayoutSnapshot, error) {
	return s.snapshot, nil
}
func (s *screenTestService) ArrangeScreenLayout(_ context.Context, r service.ScreenLayoutRequest) (int, error) {
	s.request = r
	s.calls++
	return 1, nil
}
func sampleScreenSnapshot() service.ScreenLayoutSnapshot {
	return service.ScreenLayoutSnapshot{Displays: []screenlayout.Display{{ID: "main", Name: "DISPLAY1", Primary: true, Scale: 1, WorkArea: screenlayout.Rect{Width: 1920, Height: 1040}}, {ID: "left", Name: "DISPLAY2", Scale: 1.25, WorkArea: screenlayout.Rect{X: -1280, Width: 1280, Height: 960}}}, OpenIDs: []string{"a1b2c3d4", "0a0b0c0d"}}
}
func awaitScreenPost(t *testing.T, posts chan func()) {
	t.Helper()
	select {
	case f := <-posts:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("layout operation blocked UI result")
	}
}
func TestScreenLayoutHeaderAndFrozenSelectedScope(t *testing.T) {
	for _, width := range []int{610, 1180} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			vm := newTestVM(t)
			svc := &screenTestService{Service: vm.svc, snapshot: sampleScreenSnapshot()}
			vm.svc = svc
			posts := make(chan func(), 4)
			vm.post = func(f func()) { posts <- f }
			vm.selected.Add("a1b2c3d4")
			tester := ui.NewTester(vm.view, width, 780)
			tester.Frame()
			if err := tester.Click("投屏排版"); err != nil {
				t.Fatal(err)
			}
			if !vm.screens.show || !vm.screens.busy {
				t.Fatal("header did not open loading dialog")
			}
			awaitScreenPost(t, posts)
			tester.Frame()
			vm.selected.Clear()
			vm.selected.Add("0a0b0c0d")
			vm.screens.scope = vm.screenSelectedScopeLabel()
			vm.screens.displayID = "left"
			vm.screens.size = "small"
			tester.Frame()
			if err := tester.Click("一键排列"); err != nil {
				t.Fatal(err)
			}
			awaitScreenPost(t, posts)
			tester.Frame()
			if !svc.request.SelectedOnly || !reflect.DeepEqual(svc.request.IDs, []string{"a1b2c3d4"}) || svc.request.DisplayID != "left" || svc.request.Size != "small" {
				t.Fatalf("request=%+v", svc.request)
			}
			if !tester.HasText("已排列 1 个投屏窗口") {
				t.Fatal("missing success feedback")
			}
		})
	}
}
func TestScreenLayoutMissingSavedMonitorAndEmptySelection(t *testing.T) {
	vm := newTestVM(t)
	st := vm.svc.GetSettings()
	st.ScreenLayout = service.ScreenLayoutPreferences{DisplayID: "removed", Size: "large"}
	if err := vm.svc.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}
	vm.svc = &screenTestService{Service: vm.svc, snapshot: sampleScreenSnapshot()}
	posts := make(chan func(), 4)
	vm.post = func(f func()) { posts <- f }
	vm.openScreenLayout()
	awaitScreenPost(t, posts)
	if vm.screens.displayID != "main" || vm.screens.size != "large" || vm.screens.message == "" {
		t.Fatalf("missing monitor fallback=%+v", vm.screens)
	}
	vm.screens.scope = vm.screenSelectedScopeLabel()
	tester := ui.NewTester(vm.view, 610, 780)
	tester.Frame()
	_ = tester.Click("一键排列")
	if vm.svc.(*screenTestService).calls != 0 {
		t.Fatal("empty selected scope applied")
	}
}
func TestScreenLayoutIgnoresStaleRefresh(t *testing.T) {
	vm := newTestVM(t)
	vm.svc = &screenTestService{Service: vm.svc, snapshot: sampleScreenSnapshot()}
	posts := make(chan func(), 4)
	vm.post = func(f func()) { posts <- f }
	vm.openScreenLayout()
	vm.screens.show = false
	vm.openScreenLayout()
	awaitScreenPost(t, posts)
	awaitScreenPost(t, posts)
	if vm.screens.busy || len(vm.screens.snapshot.OpenIDs) != 2 {
		t.Fatalf("refresh state=%+v", vm.screens)
	}
}
func TestRenderScreenLayout(t *testing.T) {
	dir := os.Getenv("VIRDECK_SHOT_DIR")
	if dir == "" {
		t.Skip("screenshots not requested")
	}
	for _, width := range []int{610, 1180} {
		vm := newTestVM(t)
		vm.screens.show = true
		vm.screens.snapshot = sampleScreenSnapshot()
		vm.screens.displayID = "main"
		vm.screens.size = "auto"
		vm.screens.scope = screenScopeAll
		vm.screens.selectedIDs = []string{"a1b2c3d4"}
		tester := ui.NewTester(vm.view, width, 780)
		tester.Frame()
		file, err := os.Create(filepath.Join(dir, fmt.Sprintf("screen_layout_%d.png", width)))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, tester.Image()); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
	}
}

func TestScreenLayoutHeaderFitsNarrowWindow(t *testing.T) {
	for _, width := range []int{610, 640, 700, 820, 900, 1180} {
		vm := newTestVM(t)
		vm.detailHidden = true
		tester := ui.NewTester(vm.view, width, 780)
		tester.Frame()
		for _, label := range []string{"投屏排版", "新建实例"} {
			r, ok := tester.Find(label)
			if !ok || r.X < 24 || r.X+r.W > float32(width-24) {
				t.Fatalf("width %d %s bounds %+v", width, label, r)
			}
		}
	}
}

func TestScreenLayoutDropdownsUpdateRequestValues(t *testing.T) {
	vm := newTestVM(t)
	vm.screens = screenLayoutState{show: true, snapshot: sampleScreenSnapshot(), displayID: "main", size: "auto", scope: screenScopeAll, selectedIDs: []string{"a1b2c3d4"}}
	tester := ui.NewTester(vm.view, 610, 780)
	tester.Frame()
	for _, click := range []string{"排列显示器", "显示器 2 · 1280 × 960", "投屏窗口大小", "小", "投屏排列范围", vm.screenSelectedScopeLabel()} {
		if err := tester.Click(click); err != nil {
			t.Fatalf("click %s: %v; state=%+v", click, err, vm.screens)
		}
	}
	if vm.screens.displayID != "left" || vm.screens.size != "small" || vm.screens.scope != vm.screenSelectedScopeLabel() {
		t.Fatalf("dropdown choice lost: %+v", vm.screens)
	}
}

func (s *screenTestService) SaveScreenLayout(_ context.Context, r service.ScreenLayoutRequest) (int, error) {
	s.saved++
	return 2, nil
}
func (s *screenTestService) RestoreScreenLayout(_ context.Context, r service.ScreenLayoutRequest) (int, error) {
	s.restored++
	return 2, nil
}
func (s *screenTestService) SetScreenAutoFill(_ context.Context, enabled bool) error {
	s.autoFill = enabled
	return nil
}
func TestScreenLayoutReferenceSaveRestoreAndAutoFillControls(t *testing.T) {
	vm := newTestVM(t)
	snap := sampleScreenSnapshot()
	snap.Windows = []service.ScreenWindowInfo{{InstanceID: "a1b2c3d4", Name: "参考主屏", DisplayID: "main"}, {InstanceID: "0a0b0c0d", Name: "参考副屏", DisplayID: "left"}}
	svc := &screenTestService{Service: vm.svc, snapshot: snap}
	vm.svc = svc
	posts := make(chan func(), 8)
	vm.post = func(f func()) { posts <- f }
	vm.openScreenLayout()
	awaitScreenPost(t, posts)
	tester := ui.NewTester(vm.view, 610, 780)
	tester.Frame()
	for _, label := range []string{"投屏窗口大小", "以窗口为基准", "基准投屏选择", "参考副屏"} {
		if err := tester.Click(label); err != nil {
			t.Fatalf("click %s: %v", label, err)
		}
	}
	if vm.screens.referenceID != "0a0b0c0d" || vm.screens.displayID != "left" {
		t.Fatalf("reference=%+v", vm.screens)
	}
	if err := tester.Click("一键排列"); err != nil {
		t.Fatal(err)
	}
	awaitScreenPost(t, posts)
	tester.Frame()
	if svc.request.ReferenceID != "0a0b0c0d" || svc.request.Size != "reference" {
		t.Fatalf("request=%+v", svc.request)
	}
	for _, label := range []string{"保存当前布局", "恢复已存布局", "新投屏自动补位"} {
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
		awaitScreenPost(t, posts)
		tester.Frame()
	}
	if svc.saved != 1 || svc.restored != 1 || !svc.autoFill {
		t.Fatalf("controls %+v", svc)
	}
}
func TestScreenLayoutFooterFitsShortWindow(t *testing.T) {
	vm := newTestVM(t)
	vm.screens = screenLayoutState{show: true, size: "reference", scope: screenScopeAll, displayID: "main", referenceID: "a1b2c3d4", snapshot: sampleScreenSnapshot()}
	tester := ui.NewTester(vm.view, 610, 480)
	tester.Frame()
	svc := &screenTestService{Service: vm.svc}
	vm.svc = svc
	posts := make(chan func(), 4)
	vm.post = func(f func()) { posts <- f }
	vm.screens.snapshot.SavedCount = 1
	tester.Frame()
	for _, action := range []string{"保存当前布局", "恢复已存布局"} {
		if err := tester.Click(action); err != nil {
			t.Fatalf("short window action %s: %v", action, err)
		}
		awaitScreenPost(t, posts)
		tester.Frame()
	}
	for _, label := range []string{"保存当前布局", "恢复已存布局", "关闭", "一键排列"} {
		r, ok := tester.Find(label)
		if !ok || r.Y < 0 || r.Y+r.H > 464 || r.X < 24 || r.X+r.W > 586 {
			t.Fatalf("footer %s %+v", label, r)
		}
	}
}

func TestScreenLayoutRefreshShowsSavedWarningAndClosedReference(t *testing.T) {
	vm := newTestVM(t)
	st := vm.svc.GetSettings()
	st.ScreenLayout = service.ScreenLayoutPreferences{Size: "reference", ReferenceID: "closed"}
	if err := vm.svc.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}
	snap := sampleScreenSnapshot()
	snap.Warning = "读取已保存布局失败"
	vm.svc = &screenTestService{Service: vm.svc, snapshot: snap}
	posts := make(chan func(), 2)
	vm.post = func(f func()) { posts <- f }
	vm.openScreenLayout()
	awaitScreenPost(t, posts)
	if vm.screens.referenceID != "" || !vm.screens.failed || vm.screens.message != snap.Warning {
		t.Fatalf("refresh %+v", vm.screens)
	}
}

func TestRenderReferenceScreenLayout(t *testing.T) {
	dir := os.Getenv("VIRDECK_SHOT_DIR")
	if dir == "" {
		t.Skip("screenshots not requested")
	}
	for _, height := range []int{780, 480} {
		vm := newTestVM(t)
		snap := sampleScreenSnapshot()
		snap.Windows = []service.ScreenWindowInfo{{InstanceID: "a1b2c3d4", Name: "account_01", DisplayID: "main"}}
		snap.SavedCount = 2
		vm.screens = screenLayoutState{show: true, size: "reference", referenceID: "a1b2c3d4", scope: screenScopeAll, displayID: "main", snapshot: snap, autoFill: true}
		tester := ui.NewTester(vm.view, 610, height)
		tester.Frame()
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("screen_reference_610_%d.png", height)))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tester.Image()); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
}
