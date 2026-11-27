package mygonative

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
	"virdeck/core/service"
)

func buildTestAXMLForUI(pkg string) []byte {
	strs := []string{"manifest", "package", pkg}
	var pool bytes.Buffer
	offsets := make([]uint32, len(strs))
	for i, s := range strs {
		offsets[i] = uint32(pool.Len())
		u16 := utf16.Encode([]rune(s))
		_ = binary.Write(&pool, binary.LittleEndian, uint16(len(u16)))
		for _, u := range u16 {
			_ = binary.Write(&pool, binary.LittleEndian, u)
		}
		_ = binary.Write(&pool, binary.LittleEndian, uint16(0))
	}
	for pool.Len()%4 != 0 {
		pool.WriteByte(0)
	}
	stringsStart := 28 + len(offsets)*4
	poolSize := stringsStart + pool.Len()
	var pc bytes.Buffer
	_ = binary.Write(&pc, binary.LittleEndian, uint16(0x0001))
	_ = binary.Write(&pc, binary.LittleEndian, uint16(28))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(poolSize))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(len(strs)))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(0))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(0))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(stringsStart))
	_ = binary.Write(&pc, binary.LittleEndian, uint32(0))
	for _, off := range offsets {
		_ = binary.Write(&pc, binary.LittleEndian, off)
	}
	pc.Write(pool.Bytes())

	var ec bytes.Buffer
	_ = binary.Write(&ec, binary.LittleEndian, uint16(0x0102))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(16))
	_ = binary.Write(&ec, binary.LittleEndian, uint32(56))
	_ = binary.Write(&ec, binary.LittleEndian, uint32(1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(-1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(-1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(0))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(20))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(20))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(1))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(0))
	_ = binary.Write(&ec, binary.LittleEndian, int32(-1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(1))
	_ = binary.Write(&ec, binary.LittleEndian, int32(2))
	_ = binary.Write(&ec, binary.LittleEndian, uint16(8))
	ec.WriteByte(0)
	ec.WriteByte(0x03)
	_ = binary.Write(&ec, binary.LittleEndian, uint32(2))

	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0x0003))
	_ = binary.Write(&out, binary.LittleEndian, uint16(8))
	_ = binary.Write(&out, binary.LittleEndian, uint32(8+pc.Len()+ec.Len()))
	out.Write(pc.Bytes())
	out.Write(ec.Bytes())
	return out.Bytes()
}

func writeTempAPKForUITest(t *testing.T, pkg string, abis ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), pkg+".apk")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("AndroidManifest.xml")
	_, _ = w.Write(buildTestAXMLForUI(pkg))
	for _, abi := range abis {
		lw, _ := zw.Create("lib/" + abi + "/libx.so")
		_, _ = lw.Write([]byte("ELF"))
	}
	_ = zw.Close()
	_ = f.Close()
	return p
}

type installPipelineTestSvc struct {
	service.Service
	reqs     chan service.InstallAppRequest
	launched chan string
}

func (s *installPipelineTestSvc) InstallApp(_ context.Context, req service.InstallAppRequest) error {
	s.reqs <- req
	return nil
}

func (s *installPipelineTestSvc) LaunchApp(_ context.Context, id, pkg, comp string) error {
	s.launched <- id + "|" + pkg + "|" + comp
	return nil
}

func TestFiveViewsSidebarNavigationAndGroupTree(t *testing.T) {
	vm := newTestVM(t)
	vm.groups.snapshot = service.GroupSnapshot{
		Groups:      []service.Group{{ID: "social", Name: "社媒"}},
		Memberships: map[string]string{"a1b2c3d4": "social"},
	}

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()

	// Embedded sidebar group filter switches to that group cleanly without a middle toolbar.
	if err := tester.Click("社媒"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.groups.active != "social" || len(vm.visible) != 1 || vm.visible[0].ID != "a1b2c3d4" {
		t.Fatalf("sidebar group filter failed: active=%q visible=%d", vm.groups.active, len(vm.visible))
	}

	if err := tester.Click("文件传输"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.page != pageTransfer || !tester.HasText("发送配置") {
		t.Fatalf("failed switching to transfer page: page=%v", vm.page)
	}

	if err := tester.Click("镜像与环境"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.page != pageImages || !tester.HasText("支持 ARM 应用") {
		t.Fatalf("failed switching to images page: page=%v", vm.page)
	}

	if err := tester.Click("运行日志"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.page != pageLogs || !tester.HasText("全部日志") {
		t.Fatalf("failed switching to logs page: page=%v", vm.page)
	}

	if err := tester.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.page != pageSettings || !tester.HasText("常规与下载") {
		t.Fatalf("failed switching to settings page: page=%v", vm.page)
	}
}

func TestDropRoutingAndUnifiedInstallModal(t *testing.T) {
	vm := newTestVM(t)
	svc := &installPipelineTestSvc{Service: vm.svc, reqs: make(chan service.InstallAppRequest, 2)}
	vm.svc = svc
	posts := make(chan func(), 16)
	vm.post = func(fn func()) { posts <- fn }

	// 1. Mixed drop (.apk + normal) -> rejected toast.
	apkPath := writeTempAPKForUITest(t, "com.example.chat", "arm64-v8a")
	vm.handleDroppedFiles([]string{apkPath, "C:/tmp/photo.png"}, nil)
	if !strings.Contains(vm.toastMsg, "分开拖入") || vm.showInstall {
		t.Fatalf("expected mixed-drop rejection toast, got toast=%q showInstall=%v", vm.toastMsg, vm.showInstall)
	}

	// 2. Unsupported split-package (.apkm) -> explicit unsupported toast; .xapk -> opens split install modal.
	vm.toastMsg = ""
	vm.handleDroppedFiles([]string{"C:/tmp/bundle.apkm"}, nil)
	if !strings.Contains(vm.toastMsg, "暂不支持 .apkm 格式") {
		t.Fatalf("expected .apkm unsupported toast, got %q", vm.toastMsg)
	}
	xapkPath := filepath.Join(t.TempDir(), "bundle.xapk")
	xf, err := os.Create(xapkPath)
	if err != nil {
		t.Fatal(err)
	}
	xzw := zip.NewWriter(xf)
	for _, split := range []struct {
		name string
		abis []string
	}{
		{"base.apk", nil},
		{"config.x86_64.apk", []string{"x86_64"}},
	} {
		w, _ := xzw.Create(split.name)
		var b bytes.Buffer
		szw := zip.NewWriter(&b)
		mw, _ := szw.Create("AndroidManifest.xml")
		_, _ = mw.Write(buildTestAXMLForUI("com.example.xapk"))
		for _, abi := range split.abis {
			lw, _ := szw.Create("lib/" + abi + "/libx.so")
			_, _ = lw.Write([]byte("ELF"))
		}
		_ = szw.Close()
		_, _ = w.Write(b.Bytes())
	}
	_ = xzw.Close()
	_ = xf.Close()
	vm.handleDroppedFiles([]string{xapkPath}, []string{"a1b2c3d4"})
	for deadline := time.Now().Add(2 * time.Second); vm.install.inspecting && time.Now().Before(deadline); {
		select {
		case fn := <-posts:
			fn()
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !vm.showInstall || !vm.install.meta.IsSplit || vm.install.meta.SplitCount != 2 || vm.install.meta.Package != "com.example.xapk" {
		t.Fatalf(".xapk drop did not load split metadata: show=%v meta=%+v", vm.showInstall, vm.install.meta)
	}
	vm.showInstall = false

	// 3. Single .apk dropped onto stopped instance row e5f60718 -> opens Install confirm modal.
	vm.handleDroppedFiles([]string{apkPath}, []string{"e5f60718"})
	for deadline := time.Now().Add(2 * time.Second); vm.install.inspecting && time.Now().Before(deadline); {
		select {
		case fn := <-posts:
			fn()
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !vm.showInstall || !vm.install.targetIDs["e5f60718"] || vm.install.meta.Package != "com.example.chat" {
		t.Fatalf("APK drop did not prefill install modal: show=%v meta=%+v targets=%v", vm.showInstall, vm.install.meta, vm.install.targetIDs)
	}
	if vm.install.grantPermissions {
		t.Fatal("-g runtime permissions must default to false")
	}
	if !vm.install.launchAndScreen {
		t.Fatal("single target must default launchAndScreen to true")
	}
	if !vm.anyModalOpen() {
		t.Fatal("anyModalOpen must be true when install modal is open")
	}

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if !tester.HasText("com.example.chat") || !tester.HasText("已停止 · 将自动启动") {
		t.Fatalf("install modal missing APK metadata or stopped auto-start hint: %v", tester.Texts())
	}
	if err := tester.Click("开始安装 (1 台)"); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-svc.reqs:
		if req.APKPath != apkPath || !req.LaunchApp || !req.OpenScreen || req.GrantPermissions {
			t.Fatalf("unexpected InstallAppRequest: %+v", req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("InstallApp was not invoked")
	}
	select {
	case f := <-posts:
		f()
	default:
	}

	// 4. Normal file drop routes directly to File Transfer send queue.
	vm.handleDroppedFiles([]string{"C:/tmp/readme.txt"}, nil)
	if vm.page != pageTransfer || len(vm.transfer.localPaths) != 1 || vm.transfer.localPaths[0] != "C:/tmp/readme.txt" {
		t.Fatalf("normal drop did not route to File Transfer: page=%v paths=%v", vm.page, vm.transfer.localPaths)
	}
}

func TestInstallModalShowsARMMismatchWarningAndHeadlessNote(t *testing.T) {
	vm := newTestVM(t)
	posts := make(chan func(), 16)
	vm.post = func(fn func()) { posts <- fn }
	// Point instances[0] to an x86-only image without ARM translation and native QEMU window mode.
	vm.instances[0].SystemImage = "system-images;android-28;google_apis_playstore;x86_64"
	vm.instances[0].Headless = false
	apkPath := writeTempAPKForUITest(t, "com.example.armonly", "arm64-v8a")
	vm.openInstallModalWithAPK(apkPath, []string{vm.instances[0].ID})
	for deadline := time.Now().Add(2 * time.Second); vm.install.inspecting && time.Now().Before(deadline); {
		select {
		case fn := <-posts:
			fn()
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if !tester.HasText("不支持 ARM 转译") {
		t.Fatalf("expected ARM mismatch warning in install modal: %v", tester.Texts())
	}
	if !tester.HasText("自带窗口模式") {
		t.Fatalf("expected non-headless window notice in install modal: %v", tester.Texts())
	}
}

func TestDetailRecentAppsCardLaunchesApp(t *testing.T) {
	vm := newTestVM(t)
	svc := &installPipelineTestSvc{Service: vm.svc, launched: make(chan string, 1)}
	vm.svc = svc
	vm.instances[0].RecentApps = []service.RecentApp{{
		Package:   "com.example.demo",
		Component: "com.example.demo/.MainActivity",
	}}
	vm.selected.Add(vm.instances[0].ID)

	tester := ui.NewTester(vm.view, 1180, 920)
	tester.Frame()
	if !tester.HasText("最近运行应用") || !tester.HasText("com.example.demo") {
		t.Fatalf("missing recent apps card in detail pane: %v", tester.Texts())
	}
	if err := tester.Click("启动"); err != nil {
		// Multiple "启动" may not exist since instances[0] is Running (its primary button is 投屏)!
		t.Fatal(err)
	}
	select {
	case got := <-svc.launched:
		if got != vm.instances[0].ID+"|com.example.demo|com.example.demo/.MainActivity" {
			t.Fatalf("unexpected LaunchApp payload: %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("LaunchApp was not called")
	}
}

func TestImagesARMFilterAndLogsFilter(t *testing.T) {
	vm := newTestVM(t)
	vm.page = pageImages
	vm.envTab = envTabImages

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if err := tester.Click("支持 ARM 应用"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Android 14 (API 34) - Google Play 商店") || tester.HasText("Android 7.1 (API 25) - Google Play 商店 · 32 位") {
		t.Fatalf("ARM filter should keep Android 14 (ARM translation) and exclude Android 7.1: %v", tester.Texts())
	}

	vm.logs = []string{
		"[10:00:00] 实例启动完成",
		"[10:00:05] [流水线] 正在安装 demo.apk...",
		"[10:00:10] 安装失败：签名冲突",
	}
	vm.page = pageLogs
	tester.Frame()
	if err := tester.Click("仅看错误"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("签名冲突") || tester.HasText("实例启动完成") {
		t.Fatalf("error log filter mismatch: %v", tester.Texts())
	}
}

func TestEnvMissingDecisionCardAndFloatingTaskPipeline(t *testing.T) {
	vm := newTestVM(t)
	vm.env.EmulatorPath = ""
	vm.env.ADBPath = ""
	vm.showEnvMissing = true
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if !tester.HasText("检测到运行环境不完整") {
		t.Fatal("missing env decision card title")
	}
	if err := tester.Click("去环境页手动处理"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.showEnvMissing || vm.page != pageImages || vm.envTab != envTabRuntime {
		t.Fatalf("env decision card navigation failed: show=%v page=%v tab=%d", vm.showEnvMissing, vm.page, vm.envTab)
	}

	// Verify Scheme 1 live pipeline stage updates in floating capsule & card.
	vm.page = pageWorkbench
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm.tasks = append(vm.tasks, &task{
		id:          200,
		title:       "安装应用",
		act:         actInstallAPK,
		cancel:      cancel,
		items:       []*taskItem{{target: target{id: "a1b2c3d4", name: "account_01"}, status: itemRunning}},
		steps:       []taskStep{{label: "启动与引导就绪"}, {label: "安装 APK"}, {label: "拉起应用"}, {label: "打开投屏"}},
		currentStep: 0,
		stageText:   "准备安装",
	})
	vm.applyPipelineStage(service.PipelineStage{
		InstanceID: "a1b2c3d4",
		StageText:  "正在安装 demo.apk...",
		Step:       2,
		TotalSteps: 4,
		Percent:    55,
	})
	tester.Frame()
	if !tester.HasText("安装应用 · 2/4 (55%)") {
		t.Fatalf("floating capsule missing live stage percentage: %v", tester.Texts())
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("inline cancel in floating capsule did not cancel pipeline context")
	}
}

type dataDirRedirectTestSvc struct {
	service.Service
	suggested string
	saved     chan string
}

func (s *dataDirRedirectTestSvc) SuggestPureASCIIDataDir() string {
	return s.suggested
}

func (s *dataDirRedirectTestSvc) SetDataDirOverride(_ context.Context, newDir string) (string, error) {
	s.saved <- newDir
	return newDir + `\virdeck_datadir.txt`, nil
}

func TestWindowsPathMappingGuidanceAndOneClickASCIIRedirect(t *testing.T) {
	// 1. Transparently mapped path passes validation with 0 path warnings and shows mapped detail.
	checks := buildEnvChecksForHost(service.EnvStatus{
		DataDir:          `C:\测试目录\VirDeckData`,
		EffectiveDataDir: `C:\Users\Public\VirDeckLinks\d_1234567890ab`,
		PathCompatMode:   "junction",
		PathASCIIOK:      true,
		PathWritableOK:   true,
	}, "windows")
	foundPath := false
	for _, ck := range checks {
		if ck.name == "数据目录与权限" {
			foundPath = true
			if !ck.ok || !strings.Contains(ck.detail, "NTFS 目录联接") {
				t.Fatalf("expected mapped path check to succeed with junction detail, got %+v", ck)
			}
		}
	}
	if !foundPath {
		t.Fatal("missing path check item")
	}

	// 2. When mapping fails (PathASCIIOK == false), missing-env modal & banner expose one-click redirect.
	vm := newTestVM(t)
	redirSvc := &dataDirRedirectTestSvc{
		Service:   vm.svc,
		suggested: `C:\Users\Public\VirDeckData`,
		saved:     make(chan string, 1),
	}
	vm.svc = redirSvc
	vm.env.DataDir = `C:\测试目录\VirDeckData`
	vm.env.PathASCIIOK = false
	vm.env.PathWarning = "未能自动建立纯英文路径映射"
	vm.showEnvMissing = true

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if err := tester.Click("一键改用纯英文数据目录"); err != nil {
		t.Fatalf("missing one-click ASCII redirect button in modal: %v", err)
	}
	select {
	case got := <-redirSvc.saved:
		if got != `C:\Users\Public\VirDeckData` {
			t.Fatalf("SetDataDirOverride received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SetDataDirOverride was not called")
	}
	if vm.showEnvMissing || !strings.Contains(vm.toastMsg, "重启 VirDeck 后生效") {
		t.Fatalf("expected modal closed and restart toast shown, show=%v toast=%q", vm.showEnvMissing, vm.toastMsg)
	}
}

func TestErrorLogFalsePositiveAndActiveDownloadCapsuleToggle(t *testing.T) {
	// 1. "失败 0" task summaries must NOT be flagged as error logs.
	if isErrorLogLine("[13:59:02] 任务「补齐基础工具」结束：完成 1/1 · 成功 1 · 失败 0") {
		t.Fatal("summary with '失败 0' falsely flagged as an error log")
	}
	if !isErrorLogLine("[13:59:05] 任务「补齐基础工具」结束：完成 1/1 · 成功 0 · 失败 1") {
		t.Fatal("summary with '失败 1' should be flagged as an error log")
	}
	if !isErrorLogLine("[13:59:06] 安装失败：签名冲突") {
		t.Fatal("failure log should be flagged as an error log")
	}

	// 2. Clicking the active download/extract capsule opens the floating task card and exposes cancel.
	vm := newTestVM(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm.tasks = append(vm.tasks, &task{
		id:     301,
		title:  "补齐基础工具",
		act:    actCreate,
		cancel: cancel,
		items:  []*taskItem{{target: target{name: "adb / emulator / scrcpy"}, status: itemRunning}},
	})
	vm.dlActive = true
	vm.dlProgress = service.DownloadProgress{
		Name:    "Android Emulator",
		Stage:   "extracting",
		Percent: 30,
	}

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if err := tester.Click("[解压安装中] Android Emulator · 30%"); err != nil {
		t.Fatalf("active download capsule label not clickable: %v", err)
	}
	tester.Frame()
	if !vm.tasksOpen || !tester.HasText("任务中心") {
		t.Fatalf("clicking download capsule did not open floating task card: tasksOpen=%v", vm.tasksOpen)
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatalf("inline cancel button missing on active download capsule: %v", err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("clicking cancel on download capsule did not cancel task context")
	}
}

type groupActionTestSvc struct {
	service.Service
	maxConc int
	started chan string
	stopped chan string
}

func (s *groupActionTestSvc) GetSettings() service.Settings {
	st := s.Service.GetSettings()
	if s.maxConc > 0 {
		st.MaxConcurrent = s.maxConc
	}
	return st
}

func (s *groupActionTestSvc) Start(_ context.Context, ids ...string) error {
	for _, id := range ids {
		s.started <- id
	}
	return nil
}

func (s *groupActionTestSvc) Stop(_ context.Context, ids ...string) error {
	for _, id := range ids {
		s.stopped <- id
	}
	return nil
}

func TestGroupStaggeredStartConcurrencyHeadroomAndStop(t *testing.T) {
	origStagger := groupStartStagger
	groupStartStagger = 5 * time.Millisecond
	t.Cleanup(func() { groupStartStagger = origStagger })

	vm := newTestVM(t)
	svc := &groupActionTestSvc{
		Service: vm.svc,
		maxConc: 2,
		started: make(chan string, 8),
		stopped: make(chan string, 8),
	}
	vm.svc = svc
	posts := make(chan func(), 32)
	vm.post = func(fn func()) { posts <- fn }

	// 2 running (a1b2c3d4, 0a0b0c0d) + 2 stopped in group "ops" -> 0 headroom blocks start.
	vm.instances = []service.Instance{
		{ID: "run1", Name: "run_01", State: service.Running},
		{ID: "run2", Name: "run_02", State: service.Running},
		{ID: "stop1", Name: "stop_01", State: service.Stopped},
		{ID: "stop2", Name: "stop_02", State: service.Stopped},
	}
	vm.groups.snapshot = service.GroupSnapshot{
		Groups: []service.Group{{ID: "ops", Name: "运营组"}},
		Memberships: map[string]string{
			"run1":  "ops",
			"stop1": "ops",
			"stop2": "ops",
		},
	}
	vm.groups.active = "ops"

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if !tester.HasText("分组协作 · 运营组") {
		t.Fatalf("missing group collaboration strip: %v", tester.Texts())
	}

	// Case 1: At limit (2/2) -> blocked with toast, zero starts.
	if err := tester.Click("启动整组"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(vm.toastMsg, "并发上限") || len(svc.started) != 0 {
		t.Fatalf("expected concurrency cap toast without starting, toast=%q", vm.toastMsg)
	}

	// Case 2: 1 running / max 2 -> 1 slot left, 2 stopped in group -> confirmation modal opens.
	vm.instances[1].State = service.Stopped
	tester.Frame()
	if err := tester.Click("启动整组"); err != nil {
		t.Fatal(err)
	}
	if !vm.showGroupStartConfirm || vm.groupStartAvailable != 1 {
		t.Fatalf("expected headroom confirmation modal for 1 available slot, show=%v avail=%d", vm.showGroupStartConfirm, vm.groupStartAvailable)
	}
	tester.Frame()
	if err := tester.Click("仅启动前 1 台（错峰）"); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-svc.started:
		if id != "stop1" {
			t.Fatalf("expected capped start for stop1, got %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("capped group start did not execute")
	}

	// Drain background task UI posts.
	for deadline := time.Now().Add(200 * time.Millisecond); time.Now().Before(deadline); {
		select {
		case fn := <-posts:
			fn()
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Case 3: Group stop targets running member ("run1").
	if err := tester.Click("停止整组"); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-svc.stopped:
		if id != "run1" {
			t.Fatalf("expected stopGroup to stop run1, got %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stopGroup did not call svc.Stop")
	}
}

func TestStaggeredTaskCancelSkipsQueuedInstancesAndClearsBusy(t *testing.T) {
	vm := newTestVM(t)
	posts := make(chan func(), 32)
	vm.post = func(fn func()) { posts <- fn }

	called := make(chan string, 4)
	doneCh := make(chan struct{}, 1)
	targets := []target{
		{id: "id1", name: "n1"},
		{id: "id2", name: "n2"},
		{id: "id3", name: "n3"},
	}
	tk := vm.startStaggeredTask("批量启动", actStart, targets, false, 3*time.Second, func(ctx context.Context, tg target) error {
		called <- tg.id
		return nil
	}, func(*task) {
		close(doneCh)
	})

	select {
	case first := <-called:
		if first != "id1" {
			t.Fatalf("expected id1 first, got %q", first)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first staggered target did not run")
	}

	vm.cancelInstanceTask("id2")

	for finished := false; !finished; {
		select {
		case fn := <-posts:
			fn()
		case <-doneCh:
			for len(posts) > 0 {
				(<-posts)()
			}
			finished = true
		case <-time.After(2 * time.Second):
			t.Fatal("canceled staggered task hung waiting on remaining stagger")
		}
	}

	select {
	case extra := <-called:
		t.Fatalf("queued target %q ran after task cancellation", extra)
	default:
	}
	for _, id := range []string{"id1", "id2", "id3"} {
		if vm.busy[id] != "" {
			t.Fatalf("busy not cleared for %s: %q", id, vm.busy[id])
		}
	}
	if !tk.finished || tk.items[1].status != itemFailed || tk.items[2].status != itemFailed {
		t.Fatalf("unexpected canceled task items: finished=%v items=%+v %+v", tk.finished, tk.items[1], tk.items[2])
	}
}

type slowInspectSvc struct {
	service.Service
	gate chan struct{}
}

func (s *slowInspectSvc) InspectAPK(p string) (service.APKMetadata, error) {
	<-s.gate
	return service.APKMetadata{Path: p, Package: "com.example.slow", ABIs: []string{"x86_64"}}, nil
}

func TestOpenInstallModalInspectsAPKAsynchronouslyWithoutBlockingUI(t *testing.T) {
	vm := newTestVM(t)
	gate := make(chan struct{})
	vm.svc = &slowInspectSvc{Service: vm.svc, gate: gate}
	posts := make(chan func(), 8)
	vm.post = func(fn func()) { posts <- fn }

	doneReturn := make(chan struct{})
	go func() {
		vm.openInstallModalWithAPK("huge.apks", []string{"e5f60718"})
		close(doneReturn)
	}()
	select {
	case <-doneReturn:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("openInstallModalWithAPK blocked on InspectAPK")
	}
	if !vm.showInstall || !vm.install.inspecting {
		t.Fatalf("expected modal open in inspecting state, show=%v inspecting=%v", vm.showInstall, vm.install.inspecting)
	}

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if !tester.HasText("正在离线解析安装包信息…") {
		t.Fatalf("expected inspecting spinner message, got texts=%v", tester.Texts())
	}

	close(gate)
	select {
	case fn := <-posts:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("async InspectAPK callback not posted")
	}
	tester.Frame()
	if vm.install.inspecting || !tester.HasText("com.example.slow") {
		t.Fatalf("expected resolved package com.example.slow after inspect, inspecting=%v texts=%v", vm.install.inspecting, tester.Texts())
	}
}
