package mygonative

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"virdeck/core/service"
)

type p3bUITestSvc struct {
	service.Service
	locCh     chan struct {
		ids      []string
		lat, lon float64
	}
	shotCh    chan []string
	appListCh chan bool
	appOpCh   chan struct{ id, pkg, op string }
	cleanCh   chan string
	backupCh  chan struct {
		id, dst      string
		includeProxy bool
	}
	restoreCh chan struct {
		srcZip, name string
		includeProxy bool
	}
	apps     []service.InstalledApp
	restored service.Instance
}

func (s *p3bUITestSvc) SetLocation(_ context.Context, ids []string, lat, lon float64) error {
	s.locCh <- struct {
		ids      []string
		lat, lon float64
	}{ids: ids, lat: lat, lon: lon}
	return nil
}

func (s *p3bUITestSvc) CaptureScreenshots(_ context.Context, ids []string, _ string) (map[string]string, error) {
	s.shotCh <- ids
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		out[id] = filepath.Join(osTempShotDir, id+".png")
	}
	return out, nil
}

var osTempShotDir = "shots"

func (s *p3bUITestSvc) ListInstalledApps(_ context.Context, _ string, thirdPartyOnly bool) ([]service.InstalledApp, error) {
	if s.appListCh != nil {
		s.appListCh <- thirdPartyOnly
	}
	return append([]service.InstalledApp(nil), s.apps...), nil
}

func (s *p3bUITestSvc) ManageInstalledApp(_ context.Context, id, pkg, op string) error {
	s.appOpCh <- struct{ id, pkg, op string }{id: id, pkg: pkg, op: op}
	return nil
}

func (s *p3bUITestSvc) CleanInstanceDisk(_ context.Context, id string) (service.DiskCleanResult, error) {
	s.cleanCh <- id
	return service.DiskCleanResult{Mode: "offline_compact", Message: "已清理 12.0 MB"}, nil
}

func (s *p3bUITestSvc) BackupInstance(ctx context.Context, id, dst string) (service.BackupResult, error) {
	return s.BackupInstanceWithOptions(ctx, id, dst, service.BackupOptions{})
}

func (s *p3bUITestSvc) BackupInstanceWithOptions(_ context.Context, id, dst string, opts service.BackupOptions) (service.BackupResult, error) {
	s.backupCh <- struct {
		id, dst      string
		includeProxy bool
	}{id: id, dst: dst, includeProxy: opts.IncludeProxy}
	return service.BackupResult{Path: dst, SizeBytes: 2048}, nil
}

func (s *p3bUITestSvc) RestoreInstance(ctx context.Context, srcZip, name string) (service.Instance, error) {
	return s.RestoreInstanceWithOptions(ctx, srcZip, name, service.RestoreOptions{})
}

func (s *p3bUITestSvc) RestoreInstanceWithOptions(_ context.Context, srcZip, name string, opts service.RestoreOptions) (service.Instance, error) {
	s.restoreCh <- struct {
		srcZip, name string
		includeProxy bool
	}{srcZip: srcZip, name: name, includeProxy: opts.IncludeProxy}
	return s.restored, nil
}

func newP3BUISvc(vm *viewModel) *p3bUITestSvc {
	return &p3bUITestSvc{
		Service: vm.svc,
		locCh: make(chan struct {
			ids      []string
			lat, lon float64
		}, 2),
		shotCh:    make(chan []string, 2),
		appListCh: make(chan bool, 4),
		appOpCh:   make(chan struct{ id, pkg, op string }, 8),
		cleanCh:   make(chan string, 2),
		backupCh: make(chan struct {
			id, dst      string
			includeProxy bool
		}, 4),
		restoreCh: make(chan struct {
			srcZip, name string
			includeProxy bool
		}, 4),
		apps: []service.InstalledApp{
			{PackageName: "com.alpha.app", ThirdParty: true},
			{PackageName: "com.beta.app", ThirdParty: true},
		},
		restored: service.Instance{
			ID: "rest99", Name: "还原的测试机", State: service.Stopped, MemoryMB: 2048, Cores: 2,
		},
	}
}

func TestP3BLocationAndScreenshotFromMenus(t *testing.T) {
	vm := newTestVM(t)
	svc := newP3BUISvc(vm)
	vm.svc = svc
	posts := make(chan func(), 16)
	vm.post = func(fn func()) { posts <- fn }

	// vm.instances[0] is Running ("a1b2c3d4").
	vm.selected.Add("a1b2c3d4")
	tester := ui.NewTester(vm.view, 1180, 760)
	tester.Frame()

	if err := tester.Click("更多操作"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("虚拟定位"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !vm.showLocation {
		t.Fatal("location modal did not open")
	}
	if err := tester.Click("东京"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("应用定位"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-svc.locCh:
		if !reflect.DeepEqual(got.ids, []string{"a1b2c3d4"}) || got.lat != 35.6762 || got.lon != 139.6503 {
			t.Fatalf("unexpected SetLocation payload: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SetLocation not called")
	}
	(<-posts)()
	tester.Frame()

	// Select a second instance so the tiered action bar shows "工具" -> "一键截图" (running filter keeps "a1b2c3d4").
	vm.selected.Add("e5f60718")
	tester.Frame()
	if err := tester.Click("工具"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("一键截图"); err != nil {
		t.Fatal(err)
	}
	select {
	case ids := <-svc.shotCh:
		if !reflect.DeepEqual(ids, []string{"a1b2c3d4"}) {
			t.Fatalf("unexpected screenshot targets: %v", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CaptureScreenshots not called")
	}
	(<-posts)()
}

func TestP3BAppManagerSearchLaunchAndTwoStepConfirm(t *testing.T) {
	vm := newTestVM(t)
	svc := newP3BUISvc(vm)
	vm.svc = svc
	posts := make(chan func(), 16)
	vm.post = func(fn func()) { posts <- fn }

	vm.openAppsModal(vm.instances[0])
	// Drain initial async ListInstalledApps call.
	<-svc.appListCh
	(<-posts)()

	tester := ui.NewTester(vm.view, 1180, 760)
	tester.Frame()
	vm.appsQuery = "beta"
	tester.Frame()

	if tester.HasText("com.alpha.app") {
		t.Fatal("search query did not filter out com.alpha.app")
	}
	if !tester.HasText("com.beta.app") {
		t.Fatal("com.beta.app should remain visible")
	}

	if err := tester.Click("启动"); err != nil {
		t.Fatal(err)
	}
	select {
	case op := <-svc.appOpCh:
		if op.pkg != "com.beta.app" || op.op != "launch" {
			t.Fatalf("unexpected app launch call: %+v", op)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("launch not called")
	}
	(<-posts)()
	tester.Frame()

	// Clear requires two-step inline confirmation.
	if err := tester.Click("清数据"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	select {
	case op := <-svc.appOpCh:
		t.Fatalf("clear should wait for confirmation, got immediate call: %+v", op)
	default:
	}
	if err := tester.Click("确认执行"); err != nil {
		t.Fatal(err)
	}
	select {
	case op := <-svc.appOpCh:
		if op.pkg != "com.beta.app" || op.op != "clear" {
			t.Fatalf("unexpected confirmed clear call: %+v", op)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("confirmed clear not called")
	}
	(<-posts)()
}

func TestP3BDiskCleanBackupAndRestoreFlows(t *testing.T) {
	vm := newTestVM(t)
	svc := newP3BUISvc(vm)
	vm.svc = svc
	posts := make(chan func(), 16)
	vm.post = func(fn func()) { posts <- fn }

	wantZip := filepath.Join(t.TempDir(), "mybackup.zip")
	vm.pickBackupSavePath = func(string) (string, bool) { return wantZip, true }
	vm.pickBackupOpenPath = func() (string, bool) { return wantZip, true }

	// Pick a stopped instance ("e5f60718").
	var stopped service.Instance
	for _, inst := range vm.instances {
		if inst.State == service.Stopped {
			stopped = inst
			break
		}
	}
	if stopped.ID == "" {
		t.Fatal("no stopped sample instance found")
	}
	vm.selected.Add(stopped.ID)
	tester := ui.NewTester(vm.view, 1180, 760)
	tester.Frame()

	if err := tester.Click("更多操作"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("磁盘清理"); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-svc.cleanCh:
		if id != stopped.ID {
			t.Fatalf("CleanInstanceDisk id=%q, want %q", id, stopped.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CleanInstanceDisk not invoked")
	}
	(<-posts)()
	tester.Frame()

	if err := tester.Click("更多操作"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("备份实例"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !vm.showBackup || vm.backupIncludeProxy {
		t.Fatalf("expected backup modal open with includeProxy=false, got show=%v inc=%v", vm.showBackup, vm.backupIncludeProxy)
	}
	if err := tester.Click("选择保存位置…"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-svc.backupCh:
		if got.id != stopped.ID || got.dst != wantZip || got.includeProxy {
			t.Fatalf("unexpected backup args: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BackupInstance not invoked")
	}
	(<-posts)()
	tester.Frame()

	// Restore via header "选择" -> "从备份还原…"
	if err := tester.Click("选择"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("从备份还原…"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !vm.showRestore || vm.restoreIncludeProxy {
		t.Fatalf("expected restore modal open with includeProxy=false, got show=%v inc=%v", vm.showRestore, vm.restoreIncludeProxy)
	}
	if err := tester.Click("选择备份文件…"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-svc.restoreCh:
		if got.srcZip != wantZip || got.includeProxy {
			t.Fatalf("unexpected restore args: %+v (want zip=%q, includeProxy=false)", got, wantZip)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RestoreInstance not invoked")
	}
	(<-posts)()
	tester.Frame()
	if !vm.selected.Has("rest99") {
		t.Fatal("expected restored instance rest99 to be selected")
	}

	// Opt-in checkboxes in backup and restore modals forward IncludeProxy=true.
	vm.selected.Clear()
	vm.selected.Add(stopped.ID)
	tester.Frame()
	if err := tester.Click("更多操作"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("备份实例"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("包含网络代理配置（含敏感凭据，分享备份时请勿勾选）"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("选择保存位置…"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-svc.backupCh:
		if !got.includeProxy {
			t.Fatalf("expected backup includeProxy=true after checkbox click, got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BackupInstance with includeProxy not invoked")
	}
	(<-posts)()
	tester.Frame()

	svc.restored.ID = "rest100"
	svc.restored.Name = "还原的测试机2"
	if err := tester.Click("选择"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("从备份还原…"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("继承备份中的网络代理配置（仅限本人可信备份）"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("选择备份文件…"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-svc.restoreCh:
		if !got.includeProxy {
			t.Fatalf("expected restore includeProxy=true after checkbox click, got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RestoreInstance with includeProxy not invoked")
	}
	(<-posts)()
	tester.Frame()
	if !vm.selected.Has("rest100") {
		t.Fatal("expected second restored instance rest100 to be selected")
	}
}

func TestAnyModalOpenIncludesLocationAndAppsModals(t *testing.T) {
	vm := newTestVM(t)
	if vm.anyModalOpen() {
		t.Fatal("expected anyModalOpen() to be false initially")
	}

	vm.showLocation = true
	if !vm.anyModalOpen() {
		t.Fatal("expected anyModalOpen() to be true when showLocation is true")
	}
	vm.showLocation = false

	vm.showApps = true
	if !vm.anyModalOpen() {
		t.Fatal("expected anyModalOpen() to be true when showApps is true")
	}
	vm.showApps = false

	vm.showBackup = true
	if !vm.anyModalOpen() {
		t.Fatal("expected anyModalOpen() to be true when showBackup is true")
	}
	vm.showBackup = false

	vm.showRestore = true
	if !vm.anyModalOpen() {
		t.Fatal("expected anyModalOpen() to be true when showRestore is true")
	}
	vm.showRestore = false

	if vm.anyModalOpen() {
		t.Fatal("expected anyModalOpen() to return to false after closing modals")
	}
}

func TestProxyInputsMaskByDefaultAndToggleReveal(t *testing.T) {
	vm := newTestVM(t)
	tester := ui.NewTester(vm.view, 1180, 760)

	// Settings page download proxy defaults to masked and toggles reveal on click.
	vm.settingsProxyReveal = true
	vm.openSettings()
	if vm.settingsProxyReveal {
		t.Fatal("openSettings must reset settingsProxyReveal to false")
	}
	tester.Frame()
	if err := tester.Click("显示"); err != nil {
		t.Fatalf("click settings show toggle: %v", err)
	}
	if !vm.settingsProxyReveal {
		t.Fatal("expected settingsProxyReveal=true after clicking 显示")
	}
	tester.Frame()
	if err := tester.Click("隐藏"); err != nil {
		t.Fatalf("click settings hide toggle: %v", err)
	}
	if vm.settingsProxyReveal {
		t.Fatal("expected settingsProxyReveal=false after clicking 隐藏")
	}

	// Edit modal proxy resets reveal on open and toggles cleanly.
	vm.editProxyReveal = true
	vm.openEdit(vm.instances[0])
	if vm.editProxyReveal {
		t.Fatal("openEdit must reset editProxyReveal to false")
	}
	tester.Frame()
	if err := clickInModalScroll(tester, "显示", 590, 380); err != nil {
		t.Fatalf("click edit show toggle: %v", err)
	}
	if !vm.editProxyReveal {
		t.Fatal("expected editProxyReveal=true after clicking 显示")
	}
	vm.showEdit = false

	// Batch proxy modal resets reveal on open and toggles cleanly.
	vm.proxyReveal = true
	vm.openProxy(vm.instances[:1])
	if vm.proxyReveal {
		t.Fatal("openProxy must reset proxyReveal to false")
	}
	tester.Frame()
	if err := tester.Click("显示"); err != nil {
		t.Fatalf("click batch proxy show toggle: %v", err)
	}
	if !vm.proxyReveal {
		t.Fatal("expected proxyReveal=true after clicking 显示")
	}
	vm.showProxy = false
}
