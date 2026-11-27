package mygonative

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"virdeck/core/service"
)

type p3aTestSvc struct {
	service.Service
	started chan string
	screen  chan string
	created chan service.CreateSpec
	edited  chan service.InstanceSettings
}

func (s *p3aTestSvc) Start(_ context.Context, ids ...string) error {
	for _, id := range ids {
		s.started <- id
	}
	return nil
}

func (s *p3aTestSvc) OpenScreen(_ context.Context, id string) error {
	s.screen <- id
	return nil
}

func (s *p3aTestSvc) Create(_ context.Context, spec service.CreateSpec) ([]service.Instance, error) {
	s.created <- spec
	return nil, nil
}

func (s *p3aTestSvc) EditInstance(_ context.Context, _ string, spec service.InstanceSettings) error {
	s.edited <- spec
	return nil
}

func TestRowQuickActionsSortNoteSearchAndCopyADBCommand(t *testing.T) {
	vm := newTestVM(t)
	svc := &p3aTestSvc{
		Service: vm.svc,
		started: make(chan string, 4),
		screen:  make(chan string, 4),
	}
	vm.svc = svc
	posts := make(chan func(), 16)
	vm.post = func(fn func()) { posts <- fn }

	vm.instances = []service.Instance{
		{ID: "id_b", Name: "b_stopped", State: service.Stopped, Note: "测试备注"},
		{ID: "id_a", Name: "a_running", State: service.Running, Port: 5554, GPUMode: "angle_indirect"},
	}

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()

	// Inline quick Start button for stopped instance and quick Screen button for running instance.
	if err := tester.Click("启动 b_stopped"); err != nil {
		t.Fatalf("missing row inline quick start button: %v", err)
	}
	select {
	case id := <-svc.started:
		if id != "id_b" {
			t.Fatalf("expected inline quick start for id_b, got %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inline quick start did not call svc.Start")
	}
	for len(posts) > 0 {
		(<-posts)()
	}

	if err := tester.Click("投屏 a_running"); err != nil {
		t.Fatalf("missing row inline quick screen button: %v", err)
	}
	select {
	case id := <-svc.screen:
		if id != "id_a" {
			t.Fatalf("expected inline quick screen for id_a, got %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inline quick screen did not call svc.OpenScreen")
	}

	// Sort by name (a_running first), and note search ("测试备注" isolates id_b).
	if err := tester.Click("列表排序"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("按名称"); err != nil {
		t.Fatal(err)
	}
	if len(vm.visible) != 2 || vm.visible[0].ID != "id_a" {
		t.Fatalf("expected 按名称 sort to place a_running first, got %+v", vm.visible)
	}

	vm.query = "测试备注"
	vm.refreshVisible()
	if len(vm.visible) != 1 || vm.visible[0].ID != "id_b" {
		t.Fatalf("expected note search to match id_b, got %+v", vm.visible)
	}
	vm.query = ""
	vm.selected.Add("id_a")
	tester.Frame()

	var copied string
	vm.copyText = func(s string) { copied = s }
	if err := tester.Click("更多操作"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("复制 ADB 命令"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(copied, "-s emulator-5554 shell") {
		t.Fatalf("unexpected copied ADB command: %q", copied)
	}
}

func isScrollableClickErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "has no visible area") ||
		strings.Contains(msg, "outside the window") ||
		strings.Contains(msg, "covered by") ||
		strings.Contains(msg, "does not take input")
}

func clickInModalScroll(tester *ui.Tester, target string, x, y float32) error {
	err := tester.Click(target)
	for i := 0; i < 15 && isScrollableClickErr(err); i++ {
		tester.Scroll(x, y, 0, 80)
		tester.Frame()
		err = tester.Click(target)
	}
	return err
}

func TestCreateAndEditPerformancePresetsGPUModeAndNote(t *testing.T) {
	vm := newTestVM(t)
	svc := &p3aTestSvc{
		Service: vm.svc,
		created: make(chan service.CreateSpec, 2),
		edited:  make(chan service.InstanceSettings, 2),
	}
	vm.svc = svc
	posts := make(chan func(), 16)
	vm.post = func(fn func()) { posts <- fn }

	vm.showCreate = true
	vm.createName = "预设测试"
	vm.createNote = "养号主控"
	tester := ui.NewTester(vm.view, 1180, 820)
	tester.Frame()
	if err := clickInModalScroll(tester, "流畅 (4核 · 4G)", 590, 410); err != nil {
		t.Fatalf("click create perf preset failed: %v", err)
	}
	if err := clickInModalScroll(tester, "选择 GPU 渲染模式", 590, 410); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("兼容模式 (ANGLE)"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("立即创建"); err != nil {
		t.Fatal(err)
	}
	select {
	case spec := <-svc.created:
		if spec.Cores != 4 || spec.MemoryMB != 4096 || spec.GPUMode != "angle_indirect" || spec.Note != "养号主控" {
			t.Fatalf("unexpected CreateSpec from presets/GPU/Note: %+v", spec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Create did not submit")
	}

	vm.openEdit(vm.instances[0])
	tester.Frame()
	if err := clickInModalScroll(tester, "省资源 (2核 · 2G)", 590, 410); err != nil {
		t.Fatalf("click edit perf preset failed: %v", err)
	}
	if err := clickInModalScroll(tester, "选择 GPU 渲染模式", 590, 410); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("软件渲染 (SwiftShader)"); err != nil {
		t.Fatal(err)
	}
	vm.editNote = "低配巡检"
	if err := tester.Click("保存更改"); err != nil {
		t.Fatal(err)
	}
	select {
	case spec := <-svc.edited:
		if spec.Cores != 2 || spec.MemoryMB != 2048 || spec.GPUMode != "swiftshader_indirect" || spec.Note != "低配巡检" {
			t.Fatalf("unexpected InstanceSettings from edit presets/GPU/Note: %+v", spec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EditInstance did not submit")
	}

	// Verify scrolling brings lower create-modal fields into view even in a tighter viewport.
	tight := ui.NewTester(vm.view, 1180, 560)
	vm.showCreate = true
	vm.createName = "窄高度测试"
	vm.createGPUMode = gpuModeOptions[0]
	tight.Frame()
	if err := clickInModalScroll(tight, "选择 GPU 渲染模式", 590, 280); err != nil {
		t.Fatalf("tight viewport GPU select click failed: %v", err)
	}
	if err := tight.Click("软件渲染 (SwiftShader)"); err != nil {
		t.Fatal(err)
	}
	if err := tight.Click("立即创建"); err != nil {
		t.Fatal(err)
	}
	select {
	case spec := <-svc.created:
		if spec.GPUMode != "swiftshader_indirect" {
			t.Fatalf("unexpected tight-viewport GPUMode: %+v", spec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Create in tight viewport did not submit")
	}
}
