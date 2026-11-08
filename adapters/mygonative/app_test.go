package mygonative

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"virdeck/core/service"
)

func sampleInstances() []service.Instance {
	img := "system-images;android-34;google_apis_playstore;x86_64"
	return []service.Instance{
		{ID: "a1b2c3d4", Name: "account_01", SystemImage: img, MemoryMB: 3072, Cores: 2, Port: 5554,
			Proxy: "http://127.0.0.1:7890", AppliedProxy: "http://127.0.0.1:7890", Headless: true, State: service.Running},
		{ID: "e5f60718", Name: "account_02", SystemImage: img, MemoryMB: 3072, Cores: 2, Headless: true, State: service.Stopped},
		{ID: "0a0b0c0d", Name: "account_03", SystemImage: img, MemoryMB: 4096, Cores: 4, Port: 5556,
			Proxy: "http://127.0.0.1:1080", AppliedProxy: "http://127.0.0.1:7890", Headless: true, State: service.Running},
		{ID: "11223344", Name: "base_01", SystemImage: img, MemoryMB: 3072, Cores: 2, State: service.Failed,
			LastError: "模拟器进程在完成引导前意外退出"},
	}
}

func newTestVM(t *testing.T) *viewModel {
	t.Helper()
	mgr, err := service.NewManager(service.Config{
		DataDir:      t.TempDir(),
		RegistryPath: filepath.Join(t.TempDir(), "instances.json"),
		SkipTCPCheck: true,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	vm := newViewModel(mgr)
	vm.instances = sampleInstances()
	vm.envLoaded = true
	vm.env = service.EnvStatus{
		DataDir:        `C:\VirDeck\VirDeckData`,
		SDKRoot:        `C:\VirDeck\VirDeckData\sdk`,
		AVDHome:        `C:\VirDeck\VirDeckData\avd`,
		PathASCIIOK:    true,
		PathWritableOK: true,
		AccelOK:        false,
		AccelMessage:   "WHPX 未开启",
		SystemImages:   []string{defaultImageSuggestions[0]},
	}
	return vm
}

func TestNativeViewRendersAllModals(t *testing.T) {
	vm := newTestVM(t)
	vm.selected.Add("a1b2c3d4")
	vm.logs = []string{"[16:00:00] 实例 account_01 启动完成"}

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()

	vm.showCreate = true
	tester.Frame()
	vm.showCreate = false

	vm.showClone, vm.cloneSrcID = true, "e5f60718"
	tester.Frame()
	vm.showClone = false

	vm.openProxy(vm.selectedInsts())
	tester.Frame()
	vm.showProxy = false

	vm.shellTargets, vm.showShell = vm.instances[:1], true
	tester.Frame()
	vm.showShell = false

	vm.deleteTargets, vm.showDelete = vm.instances[:2], true
	tester.Frame()
	vm.showDelete = false

	vm.mixedPlan = planAction(actStart, vm.instances, vm.busy)
	vm.showMixed = true
	tester.Frame()
	vm.showMixed = false

	vm.showEnv = true
	vm.envTab = 0
	tester.Frame()
	vm.envTab = 1
	vm.showCustomImg = true
	tester.Frame()
	vm.showCustomImg = false
	vm.showEnv = false
	vm.openSettings()
	tester.Frame()
	vm.showSettings = false
	vm.showHelp = true
	tester.Frame()
	vm.showHelp = false

	vm.showLicense = true
	tester.Frame()
	vm.showLicense = false

	vm.tasksOpen = true
	tester.Frame()
	vm.panelTab = 1
	tester.Frame()
	tester.Frame()
}

func TestLicenseConsentAndSettingsPersistence(t *testing.T) {
	vm := newTestVM(t)
	called := false
	vm.requireLicense(func() { called = true })
	if called || !vm.showLicense {
		t.Fatalf("expected license modal before acceptance, called=%v showLicense=%v", called, vm.showLicense)
	}

	// Simulate accepting license.
	st := vm.svc.GetSettings()
	st.LicenseAccepted = true
	st.DownloadProxy = "http://127.0.0.1:7890"
	st.MaxConcurrent = 6
	if err := vm.svc.UpdateSettings(st); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	vm.showLicense = false

	vm.requireLicense(func() { called = true })
	if !called || vm.showLicense {
		t.Fatalf("expected direct execution after license acceptance, called=%v showLicense=%v", called, vm.showLicense)
	}
}

func TestDownloadStageActiveAndFormatting(t *testing.T) {
	activeStages := []string{"downloading", "verifying", "extracting"}
	for _, st := range activeStages {
		if !isDownloadStageActive(st) {
			t.Errorf("stage %q should be active", st)
		}
	}
	inactiveStages := []string{"", "completed", "COMPLETED", "done", "failed"}
	for _, st := range inactiveStages {
		if isDownloadStageActive(st) {
			t.Errorf("stage %q must not stay active after completion/failure", st)
		}
	}
	if got := formatDownloadStage("downloading"); got != "下载中" {
		t.Errorf("formatDownloadStage(downloading) = %q", got)
	}
	if got := formatDownloadStage("completed"); got != "已完成" {
		t.Errorf("formatDownloadStage(completed) = %q", got)
	}
}

func TestLayoutAdaptsToWindowSize(t *testing.T) {
	vm := newTestVM(t)
	vm.selected.Add("a1b2c3d4")
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if !tester.HasText("实例详情") || !tester.HasText("镜像与环境") {
		t.Fatalf("wide window should show navigation and detail columns: %v", tester.Texts())
	}

	tester.SetSize(900, 600)
	tester.Frame()
	if tester.HasText("镜像与环境") || !tester.HasText("实例详情") {
		t.Fatalf("medium window should hide navigation but keep detail: %v", tester.Texts())
	}

	tester.SetSize(700, 520)
	tester.Frame()
	if tester.HasText("实例详情") || !tester.HasText("实例工作台") {
		t.Fatalf("small window should keep only the list: %v", tester.Texts())
	}
	// The list stays usable and the single selection falls back to the action bar.
	if !tester.HasText("account_01") || !tester.HasText("批量命令") {
		t.Fatalf("small window lost list or action bar: %v", tester.Texts())
	}
}

func TestRenderScreenshotForReview(t *testing.T) {
	dir := os.Getenv("VIRDECK_SHOT_DIR")
	if dir == "" {
		t.Skip("set VIRDECK_SHOT_DIR to dump screenshots")
	}
	vm := newTestVM(t)
	vm.selected.Add("a1b2c3d4")
	vm.appendLog("[16:00:00] 实例 account_01 启动完成")
	tk := vm.startTaskForTest("启动", actStart)
	_ = tk

	shot := func(name string, w, h int, prep func()) {
		tester := ui.NewTester(vm.view, w, h)
		if prep != nil {
			prep()
		}
		tester.Frame()
		tester.Frame()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tester.Image()); err != nil {
			t.Fatal(err)
		}
	}
	shot("main_single", 1180, 740, nil)
	shot("main_reference", 1476, 980, nil)
	shot("main_min", 640, 480, nil)
	shot("main_multi", 1180, 740, func() { vm.selected.Add("0a0b0c0d"); vm.selected.Add("e5f60718") })
	shot("main_tasks", 1180, 740, func() { vm.tasksOpen = true })
	shot("main_small", 700, 520, func() { vm.tasksOpen = false })
	shot("modal_env", 1180, 740, func() { vm.showEnv = true; vm.envTab = 0 })
	shot("modal_env_images", 1180, 740, func() { vm.showEnv = true; vm.envTab = 1 })
	shot("modal_env_directories", 1180, 740, func() {
		vm.envTab = 0
		vm.env.EmulatorPath, vm.env.ADBPath, vm.env.ScrcpyPath = "emulator.exe", "adb.exe", "scrcpy.exe"
		vm.env.AccelOK = true
		vm.env.DataDir = `C:\Users\SX\Downloads\01_Projects\Dev\VirDeck\VirDeckData`
		vm.env.AVDHome = vm.env.DataDir + `\avd`
	})
	shot("modal_env_favorite", 1180, 740, func() {
		vm.envTab = 1
		id := vm.catalog[1].ID
		vm.saveImageFavorite(id, true, "工作账号 · 兼容性测试")
		vm.favoriteID, vm.favoriteNote = id, "工作账号 · 兼容性测试"
	})
	shot("modal_cleanup", 1180, 740, func() {
		vm.envTab = 2
		vm.cleanupScanned = true
		vm.cleanupReport = service.CleanupReport{Downloads: service.CleanupSize{Files: 3, Bytes: 1400000000}, Partials: service.CleanupSize{Files: 2, Bytes: 300000000}, AVDCache: service.CleanupSize{Files: 4, Bytes: 138000000}}
	})
	shot("modal_settings", 1180, 740, func() { vm.showEnv = false; vm.openSettings() })
	shot("modal_settings_small", 640, 480, nil)
	shot("modal_help", 1180, 740, func() { vm.showSettings = false; vm.showHelp = true })
	shot("modal_license", 1180, 740, func() { vm.showHelp = false; vm.showEnv = false; vm.showLicense = true })
	shot("modal_create", 1180, 740, func() { vm.showLicense = false; vm.showCreate = true })
	shot("modal_edit", 1180, 740, func() {
		vm.showCreate = false
		vm.openEdit(vm.instances[0])
		vm.editName = "工作账号一号"
	})
	shot("modal_edit_small", 640, 480, nil)
	shot("modal_proxy_mixed", 1180, 740, func() {
		vm.showCreate = false
		vm.showEdit = false
		vm.openProxy(vm.selectedInsts())
	})
	shot("modal_delete", 1180, 740, func() {
		vm.showProxy = false
		vm.deleteTargets, vm.showDelete = vm.selectedInsts(), true
	})
	shot("modal_mixed", 1180, 740, func() {
		vm.showDelete = false
		vm.mixedPlan, vm.showMixed = planAction(actStart, vm.selectedInsts(), vm.busy), true
	})
}

// startTaskForTest seeds a finished task with one failure so the dock and panel have content.
func (vm *viewModel) startTaskForTest(title string, a action) *task {
	tk := &task{id: 99, title: title, act: a, retryable: true, finished: true}
	tk.items = []*taskItem{
		{target: target{id: "e5f60718", name: "account_02"}, status: itemOK},
		{target: target{id: "11223344", name: "base_01"}, status: itemFailed, err: "为 base_01 分配端口失败"},
	}
	vm.tasks = append(vm.tasks, tk)
	return tk
}

func TestCheckActionStateRules(t *testing.T) {
	running := service.Instance{ID: "r", Name: "r", State: service.Running}
	starting := service.Instance{ID: "s", Name: "s", State: service.Starting}
	stopped := service.Instance{ID: "p", Name: "p", State: service.Stopped}
	failed := service.Instance{ID: "f", Name: "f", State: service.Failed}

	cases := []struct {
		act  action
		inst service.Instance
		busy string
		want bool
	}{
		{actStart, stopped, "", true},
		{actStart, failed, "", true},
		{actStart, running, "", false},
		{actStart, starting, "", false},
		{actStop, running, "", true},
		{actStop, starting, "", true},
		{actStop, stopped, "", false},
		{actClone, stopped, "", true},
		{actClone, failed, "", true},
		{actClone, running, "", false},
		{actScreen, running, "", true},
		{actScreen, starting, "", false},
		{actScreen, stopped, "", false},
		{actInstallAPK, running, "", true},
		{actInstallAPK, stopped, "", false},
		{actShell, running, "", true},
		{actShell, failed, "", false},
		{actProxy, stopped, "", true},
		{actProxy, running, "", true},
		{actDelete, running, "", true},
		{actDelete, failed, "", true},
		// A running task on the instance blocks every conflicting operation.
		{actStop, running, "启动", false},
		{actDelete, stopped, "安装 APK", false},
		{actProxy, stopped, "删除", false},
	}
	for _, tc := range cases {
		got, reason := checkAction(tc.act, tc.inst, tc.busy)
		if got != tc.want {
			t.Errorf("%s on %s (busy=%q) = %v, want %v", tc.act.label(), tc.inst.State, tc.busy, got, tc.want)
		}
		if !got && reason == "" {
			t.Errorf("%s on %s: blocked without a reason", tc.act.label(), tc.inst.State)
		}
	}
}

func TestMixedSelectionOnlyBlocksUnavailableInstances(t *testing.T) {
	insts := sampleInstances() // 2 running, 1 stopped, 1 failed
	p := planAction(actStart, insts, nil)
	if len(p.eligible) != 2 || len(p.skipped) != 2 || !p.mixed() {
		t.Fatalf("start plan: eligible=%d skipped=%d", len(p.eligible), len(p.skipped))
	}
	if got := p.buttonLabel("启动"); got != "启动 (2/4)" {
		t.Fatalf("button label = %q", got)
	}
	if p.disabledHint() != "" {
		t.Fatalf("a partly eligible selection must stay clickable")
	}

	none := planAction(actClone, insts[:1], nil) // running only
	if none.disabledHint() == "" || none.mixed() {
		t.Fatalf("fully ineligible selection must be disabled with a reason: %+v", none)
	}

	if got := planAction(actStop, insts[:1], nil).buttonLabel("停止"); got != "停止" {
		t.Fatalf("fully eligible selection should keep the plain label, got %q", got)
	}
}

func TestPendingRestartAndProxyFormState(t *testing.T) {
	insts := sampleInstances()
	if pendingRestart(insts[0]) {
		t.Fatalf("account_01 runs with its saved proxy")
	}
	if !pendingRestart(insts[2]) {
		t.Fatalf("account_03 saved a new proxy while running")
	}
	if pendingRestart(insts[1]) {
		t.Fatalf("stopped instances are never pending a restart")
	}

	vm := newTestVM(t)

	// Uniform selection prefills the shared value; saving needs a change.
	vm.openProxy(insts[:1])
	if vm.proxyMixed || vm.proxyValue != "http://127.0.0.1:7890" || vm.proxySaveReady() {
		t.Fatalf("uniform form wrong: %+v", vm)
	}
	vm.proxyValue = "http://127.0.0.1:9999"
	if !vm.proxySaveReady() {
		t.Fatalf("changed value should be savable")
	}

	// Mixed selection: nothing is prefilled and the user must act explicitly.
	vm.openProxy(insts[:3])
	if !vm.proxyMixed || vm.proxyValue != "" || vm.proxySaveReady() {
		t.Fatalf("mixed form must start empty and unsaveable: mixed=%v value=%q", vm.proxyMixed, vm.proxyValue)
	}
	vm.proxyCleared = true
	if !vm.proxySaveReady() {
		t.Fatalf("explicit clear should be savable")
	}

	// Reopening never inherits the previous dialog's value.
	vm.openProxy(insts[1:2])
	if vm.proxyValue != "" || vm.proxyCleared || vm.proxyMixed {
		t.Fatalf("form kept stale state: %+v", vm)
	}
}

func TestEnvChecksExplainMissingPieces(t *testing.T) {
	checks := buildEnvChecks(service.EnvStatus{})
	blocking, optional := envIssues(checks)
	if blocking != 6 || optional != 1 {
		t.Fatalf("empty env: blocking=%d optional=%d, want 6/1", blocking, optional)
	}
	for _, ck := range checks {
		if !ck.ok && (ck.impact == "" || ck.fix == "") {
			t.Errorf("%s: missing impact or fix text", ck.name)
		}
	}

	ready := service.EnvStatus{
		DataDir: `C:\VirDeckData`, SDKRoot: `C:\sdk`, EmulatorPath: "e", ADBPath: "a", ScrcpyPath: "s",
		PathASCIIOK: true, PathWritableOK: true,
		AccelOK: true, AccelMessage: "ok", SystemImages: []string{defaultImageSuggestions[1], "system-images;android-35;default;x86_64"},
	}
	if b, o := envIssues(buildEnvChecks(ready)); b != 0 || o != 0 {
		t.Fatalf("ready env reports issues: %d/%d", b, o)
	}

	rows := imageRows(ready)
	if len(rows) != len(defaultImageSuggestions)+1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if !rows[1].recommended || !rows[1].installed || rows[0].installed {
		t.Fatalf("recommended/installed flags wrong: %+v", rows[:2])
	}
	last := rows[len(rows)-1]
	if last.recommended || !last.installed {
		t.Fatalf("extra installed image should be listed as installed only: %+v", last)
	}

	choices := imageChoices(ready)
	if choices[0] != defaultImageSuggestions[1] || !imageInstalled(ready, choices[0]) {
		t.Fatalf("installed images must come first: %v", choices)
	}
}

func TestTaskReportsPerInstanceResultsAndRetries(t *testing.T) {
	vm := newTestVM(t)
	var mu sync.Mutex
	vm.post = func(fn func()) { mu.Lock(); defer mu.Unlock(); fn() }

	insts := vm.instances
	var attempts sync.Map
	op := func(ctx context.Context, tg target) error {
		n, _ := attempts.LoadOrStore(tg.id, new(int))
		cnt := n.(*int)
		mu.Lock()
		*cnt++
		c := *cnt
		mu.Unlock()
		if tg.id == "e5f60718" && c == 1 {
			return errors.New("端口池已满")
		}
		return nil
	}

	var tk *task
	mu.Lock()
	tk = vm.startTask("启动", actStart, targetsOf(insts[1:2]), true, op, nil)
	if vm.busy["e5f60718"] != "启动" {
		mu.Unlock()
		t.Fatalf("instance must be held busy while the task runs")
	}
	// A conflicting action is refused meanwhile.
	if p := planAction(actDelete, insts[1:2], vm.busy); len(p.eligible) != 0 {
		mu.Unlock()
		t.Fatalf("delete should be blocked while start runs")
	}
	mu.Unlock()

	waitFinished(t, &mu, tk)

	mu.Lock()
	done, ok, failed := tk.counts()
	errText := tk.items[0].err
	_, stillBusy := vm.busy["e5f60718"]
	mu.Unlock()
	if done != 1 || ok != 0 || failed != 1 || errText != "端口池已满" || stillBusy {
		t.Fatalf("counts done=%d ok=%d failed=%d err=%q busy=%v", done, ok, failed, errText, stillBusy)
	}

	mu.Lock()
	vm.retryFailed(tk)
	retry := vm.latestTask()
	mu.Unlock()
	if retry == tk {
		t.Fatalf("retry should start a new task")
	}
	waitFinished(t, &mu, retry)
	mu.Lock()
	_, ok, failed = retry.counts()
	mu.Unlock()
	if ok != 1 || failed != 0 {
		t.Fatalf("retry result ok=%d failed=%d", ok, failed)
	}
}

func TestDeleteKeepsFailedInstancesSelected(t *testing.T) {
	vm := newTestVM(t)
	var mu sync.Mutex
	vm.post = func(fn func()) { mu.Lock(); defer mu.Unlock(); fn() }

	for _, inst := range vm.instances {
		vm.selected.Add(inst.ID)
	}
	op := func(ctx context.Context, tg target) error {
		if tg.id == "0a0b0c0d" {
			return errors.New("删除失败")
		}
		return nil
	}
	mu.Lock()
	tk := vm.startTask("删除", actDelete, targetsOf(vm.instances), true, op, func(tk *task) {
		for _, it := range tk.items {
			if it.status == itemOK {
				vm.selected.Remove(it.id)
			}
		}
	})
	mu.Unlock()
	waitFinished(t, &mu, tk)

	mu.Lock()
	defer mu.Unlock()
	if vm.selected.Len() != 1 || !vm.selected.Has("0a0b0c0d") {
		t.Fatalf("only the failed instance should stay selected, len=%d", vm.selected.Len())
	}
}

func waitFinished(t *testing.T, mu *sync.Mutex, tk *task) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		fin := tk.finished
		mu.Unlock()
		if fin {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %q did not finish", tk.title)
}

func TestSettingsOpensSeparateFromEnvironment(t *testing.T) {
	vm := newTestVM(t)
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if err := tester.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("保存全局设置") || tester.HasText("一键下载补齐缺失工具") {
		t.Fatalf("settings must open its own form: %v", tester.Texts())
	}
	vm.settingsProxy = "http://127.0.0.1:7891"
	vm.settingsConcurrent = 5
	if err := tester.Click("保存全局设置"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if st := vm.svc.GetSettings(); st.DownloadProxy != "http://127.0.0.1:7891" || st.MaxConcurrent != 5 {
		t.Fatalf("settings were not persisted: %+v", st)
	}
	if err := tester.Click("关闭设置"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.HasText("保存全局设置") {
		t.Fatal("settings did not close")
	}
	vm.showEnv = true
	tester.Frame()
	if tester.HasText("保存全局设置") || !tester.HasText("系统镜像") {
		t.Fatal("environment must contain components and images, without duplicating settings")
	}
}

func TestInstanceCheckboxesKeepIndependentSelections(t *testing.T) {
	vm := newTestVM(t)
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	for _, name := range []string{"account_01", "account_02"} {
		if err := tester.Click("选择 " + name); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
	}
	if !vm.selected.Has("a1b2c3d4") || !vm.selected.Has("e5f60718") {
		t.Fatal("checkboxes must add to the selection without replacing previous rows")
	}
	if err := tester.Click("选择 account_01"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.selected.Has("a1b2c3d4") || !vm.selected.Has("e5f60718") {
		t.Fatal("unchecking one row must preserve other selected rows")
	}
}

func TestDetailMenuRetainsCloneAction(t *testing.T) {
	vm := newTestVM(t)
	vm.selected.Add("e5f60718")
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if err := tester.Click("更多操作"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.ChooseMenuItem("克隆"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !vm.showClone || vm.cloneSrcID != "e5f60718" {
		t.Fatal("detail menu did not open clone for the selected device")
	}
}

func TestListHeaderSelectsVisibleInstances(t *testing.T) {
	vm := newTestVM(t)
	vm.filterTab = 1
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if err := tester.Click("全选当前列表"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.selected.Len() != 2 || !vm.selected.Has("a1b2c3d4") || !vm.selected.Has("0a0b0c0d") {
		t.Fatal("header checkbox must select only filtered rows")
	}
	if err := tester.Click("全选当前列表"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.selected.Len() != 0 {
		t.Fatal("header checkbox must clear selected visible rows")
	}
}

func TestFailedBootWithLiveProcessCanOnlyStopBeforeRestart(t *testing.T) {
	inst := sampleInstances()[3]
	inst.Port = 5554
	if ok, _ := checkAction(actStop, inst, ""); !ok {
		t.Fatal("failed live process must still be stoppable")
	}
	if ok, _ := checkAction(actStart, inst, ""); ok {
		t.Fatal("must stop the failed process before retrying startup")
	}
	if ok, _ := checkAction(actClone, inst, ""); ok {
		t.Fatal("must not clone a failed instance whose process is still alive")
	}
}

func TestSidebarButtonsShowAndClearHoverFeedback(t *testing.T) {
	for _, label := range []string{"镜像与环境", "设置", "使用帮助"} {
		t.Run(label, func(t *testing.T) {
			vm := newTestVM(t)
			tester := ui.NewTester(vm.view, 1180, 740)
			tester.Frame()
			r, ok := tester.Find(label)
			if !ok {
				t.Fatalf("missing sidebar button %s", label)
			}
			x, y := int(r.X+4), int(r.Y+r.H/2)
			save := func(state string) {
				if dir := os.Getenv("VIRDECK_SHOT_DIR"); dir != "" {
					name := map[string]string{"镜像与环境": "environment", "设置": "settings", "使用帮助": "help"}[label]
					f, err := os.Create(filepath.Join(dir, "sidebar_"+name+"_"+state+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tester.Image()); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := tester.Image().RGBAAt(x, y)
			save("idle")
			tester.Move(r.X+r.W/2, r.Y+r.H/2)
			tester.Frame()
			hovered := tester.Image().RGBAAt(x, y)
			if hovered == before {
				t.Fatal("button must visibly respond when the pointer enters it")
			}
			save("hover")
			tester.Press(r.X+r.W/2, r.Y+r.H/2)
			tester.Frame()
			pressed := tester.Image().RGBAAt(x, y)
			if pressed == hovered || pressed == before {
				t.Fatal("button must have distinct pressed feedback")
			}
			save("pressed")
			// Release outside the button so this style test does not open a modal.
			tester.Move(400, 80)
			tester.Release(400, 80)
			tester.Frame()
			if got := tester.Image().RGBAAt(x, y); got != before {
				t.Fatalf("hover fill must clear on pointer leave: got %v, want %v", got, before)
			}
		})
	}
}

func TestImageFavoriteInteractionPersistsNote(t *testing.T) {
	vm := newTestVM(t)
	for _, it := range vm.catalog {
		if it.Favorite {
			if err := vm.svc.SetImageFavorite(context.Background(), it.ID, false, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	vm.catalog, _ = vm.svc.ListCatalog(context.Background())
	vm.catalog = vm.catalog[:1]
	tester := ui.NewTester(vm.buildImageList, 900, 600)
	tester.Frame()
	if err := tester.Click("☆"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	got, err := vm.svc.ListCatalog(context.Background())
	if err != nil || !got[0].Favorite {
		t.Fatalf("star did not save favorite: %+v, %v", got, err)
	}
	if !tester.HasText("收藏备注") {
		t.Fatal("missing optional note editor")
	}
	// Editing the draft and saving must survive a new catalog read.
	vm.favoriteNote = "工作账号"
	tester.Frame()
	if err := tester.Click("保存备注"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	got, _ = vm.svc.ListCatalog(context.Background())
	if got[0].Note != "工作账号" {
		t.Fatalf("note not saved: %+v", got[0])
	}
	if err := tester.Click("★"); err != nil {
		t.Fatal(err)
	}
	got, _ = vm.svc.ListCatalog(context.Background())
	for _, item := range got {
		if item.Favorite {
			t.Fatal("star did not remove favorite")
		}
	}
}

func TestInactiveSelectionStaysLightAndPreservesSelection(t *testing.T) {
	vm := newTestVM(t)
	vm.selected.Add("a1b2c3d4")
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	r, ok := tester.Find("选择 account_01")
	if !ok {
		t.Fatal("missing selected row")
	}
	x, y := int(r.X+80), int(r.Y+r.H/2+28)
	tester.SetFocused(false)
	tester.SetWindowActive(false)
	tester.Frame()
	col := tester.Image().RGBAAt(x, y)
	if col.R < 220 || col.G < 220 || col.B < 220 {
		t.Fatalf("inactive selection became dark: %v", col)
	}
	if !vm.selected.Has("a1b2c3d4") {
		t.Fatal("deactivation lost selection")
	}
	if dir := os.Getenv("VIRDECK_SHOT_DIR"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "main_inactive.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tester.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecondaryMenusShowHoverAndUseSystemMenus(t *testing.T) {
	for _, label := range []string{"选择", "更多操作"} {
		t.Run(label, func(t *testing.T) {
			vm := newTestVM(t)
			vm.selected.Add("a1b2c3d4")
			tester := ui.NewTester(vm.view, 1180, 740)
			tester.Frame()
			r, ok := tester.Find(label)
			if !ok {
				t.Fatalf("missing menu %s", label)
			}
			x, y := int(r.X+4), int(r.Y+r.H/2)
			before := tester.Image().RGBAAt(x, y)
			tester.Move(r.X+r.W/2, r.Y+r.H/2)
			tester.Frame()
			hover := tester.Image().RGBAAt(x, y)
			if hover == before {
				t.Fatal("menu has no hover feedback")
			}
			tester.Press(r.X+r.W/2, r.Y+r.H/2)
			tester.Frame()
			if len(tester.Menu()) == 0 {
				t.Fatal("pressing menu did not open it")
			}
			item := "取消选择"
			if label == "更多操作" {
				item = "克隆"
			}
			if tester.HasText(item) {
				t.Fatal("menu was drawn inside the view instead of using the system menu")
			}
			tester.Release(r.X+r.W/2, r.Y+r.H/2)
			if label == "选择" {
				if err := tester.ChooseMenuItem("取消选择"); err != nil {
					t.Fatal(err)
				}
				if vm.selected.Len() != 0 {
					t.Fatal("system selection menu action failed")
				}
			} else {
				if err := tester.ChooseMenuItem("编辑"); err != nil {
					t.Fatal(err)
				}
				if !vm.showEdit || vm.editID != "a1b2c3d4" {
					t.Fatal("system detail menu action failed")
				}
			}
		})
	}
}

func TestEditMenuSavesExistingInstanceAndReplacesRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances.json")
	raw := []byte(`[{"id":"existing","name":"原名称","avdName":"stable_avd","memoryMB":2048,"cores":2,"state":"stopped","systemImage":"system-images;android-30;google_apis_playstore;x86_64"}]`)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	mgr, err := service.NewManager(service.Config{DataDir: dir, RegistryPath: path, SkipTCPCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	vm := newViewModel(mgr)
	vm.instances, _ = mgr.List(context.Background())
	vm.selected.Add("existing")
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if tester.HasText("改名") {
		t.Fatal("standalone rename control still shown")
	}
	if err := tester.Click("更多操作"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("编辑"); err != nil {
		t.Fatal(err)
	}
	if !vm.showEdit || vm.editMemory != 2048 || vm.editCores != 2 {
		t.Fatal("editor did not load existing configuration")
	}
	vm.editName, vm.editMemory, vm.editCores, vm.editHeadless = "编辑后的中文名称", 4096, 4, true
	tester.Frame()
	if err := tester.Click("保存更改"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	list, err := mgr.List(context.Background())
	if err != nil || vm.showEdit || list[0].Name != "编辑后的中文名称" || list[0].MemoryMB != 4096 || list[0].Cores != 4 || !list[0].Headless || list[0].InternalName() != "stable_avd" {
		t.Fatalf("editor did not save configuration: %+v, %v", list, err)
	}
}

func TestCreateImagePickerMarksInstalledAndKeepsPackageID(t *testing.T) {
	vm := newTestVM(t)
	installed := "system-images;android-30;google_apis_playstore;x86_64"
	missing := "system-images;android-34;google_apis_playstore;x86_64"
	vm.env.SystemImages = []string{installed}
	vm.createImage = missing
	vm.showCreate = true
	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	r, ok := tester.Find("系统镜像")
	if !ok {
		t.Fatal("missing image field")
	}
	tester.ClickAt(r.X+r.W+24, r.Y+r.H/2)
	tester.Frame()
	label := "✓ 已安装  " + installed
	if !tester.HasText(label) {
		t.Fatalf("installed image lacks visible marker: %v", tester.Texts())
	}
	if !tester.HasText(missing) {
		t.Fatal("uninstalled image disappeared")
	}
	if dir := os.Getenv("VIRDECK_SHOT_DIR"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "create_image_choices.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tester.Image()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tester.Click(label); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if vm.createImage != installed {
		t.Fatalf("display marker contaminated package ID: %q", vm.createImage)
	}
	if err := tester.Click("系统镜像选择"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type(missing)
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if vm.createImage != missing {
		t.Fatalf("manual package ID input broken: %q", vm.createImage)
	}
}

func TestEnvironmentModalKeepsSymmetricPaddingAtNarrowWidths(t *testing.T) {
	for _, width := range []int{610, 640, 1180} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			vm := newTestVM(t)
			vm.env.DataDir = `C:\Users\SX\Downloads\01_Projects\Dev\VirDeck\dist\VirDeckData`
			vm.env.SDKRoot = filepath.Join(vm.env.DataDir, "sdk")
			vm.env.AVDHome = filepath.Join(vm.env.DataDir, "avd")
			vm.env.EmulatorPath = filepath.Join(vm.env.SDKRoot, "emulator", "emulator.exe")
			vm.env.ADBPath = filepath.Join(vm.env.SDKRoot, "platform-tools", "adb.exe")
			vm.env.ScrcpyPath = filepath.Join(vm.env.DataDir, "scrcpy", "scrcpy.exe")
			vm.env.AccelOK = true
			vm.showEnv = true
			tester := ui.NewTester(vm.view, width, 780)
			tester.Frame()
			title, ok := tester.Find("镜像与环境管理")
			if !ok {
				t.Fatal("missing modal title")
			}
			status, ok := tester.Find("运行环境已就绪")
			if !ok {
				t.Fatal("missing header status")
			}
			y := int(title.Y) - 8
			panelLeft, panelRight := -1, -1
			for x := 0; x < width; x++ {
				col := tester.Image().RGBAAt(x, y)
				if col.R >= 250 && col.G >= 250 && col.B >= 250 {
					if panelLeft < 0 {
						panelLeft = x
					}
					panelRight = x + 1
				}
			}
			if panelLeft < 0 {
				t.Fatal("modal panel not found")
			}
			left := title.X - float32(panelLeft)
			right := float32(panelRight) - (status.X + status.W)
			if dir := os.Getenv("VIRDECK_SHOT_DIR"); dir != "" {
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("modal_env_padding_%d.png", width)))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, tester.Image()); err != nil {
					t.Fatal(err)
				}
			}
			if left < 16 || right < 16 || math.Abs(float64(left-right)) > 2 {
				t.Fatalf("asymmetric modal content padding: left=%.1f right=%.1f", left, right)
			}
		})
	}
}

func TestAllDialogsKeepNarrowWindowContentInsidePanel(t *testing.T) {
	cases := []struct {
		name, title string
		prep        func(*viewModel)
		render      func(*viewModel, *ui.Context)
	}{
		{"screen_layout", "投屏排版", func(vm *viewModel) {
			vm.screens.show = true
			vm.screens.snapshot = sampleScreenSnapshot()
			vm.screens.displayID = "main"
			vm.screens.size = "auto"
			vm.screens.scope = screenScopeAll
		}, (*viewModel).buildScreenLayoutModal},
		{"create", "新建干净安卓实例", func(vm *viewModel) { vm.showCreate = true }, (*viewModel).buildCreateModal},
		{"clone", "克隆实例（可选母版复制）", func(vm *viewModel) { vm.showClone = true; vm.cloneSrcID = "e5f60718" }, (*viewModel).buildCloneModal},
		{"proxy", "设置 HTTP 代理（共 2 个实例）", func(vm *viewModel) { vm.openProxy(vm.instances[:2]) }, (*viewModel).buildProxyModal},
		{"shell", "批量执行 ADB Shell 命令（1 个运行中的实例）", func(vm *viewModel) { vm.showShell = true; vm.shellTargets = vm.instances[:1] }, (*viewModel).buildShellModal},
		{"delete", "确认删除 2 个实例？", func(vm *viewModel) { vm.showDelete = true; vm.deleteTargets = vm.instances[:2] }, (*viewModel).buildDeleteModal},
		{"mixed", "「启动」只能对 2 台实例执行", func(vm *viewModel) { vm.showMixed = true; vm.mixedPlan = planAction(actStart, vm.instances, vm.busy) }, (*viewModel).buildMixedModal},
		{"license", "Google Android SDK 许可协议确认", func(vm *viewModel) { vm.showLicense = true }, (*viewModel).buildLicenseModal},
		{"environment", "镜像与环境管理", func(vm *viewModel) { vm.showEnv = true }, (*viewModel).buildEnvModal},
		{"settings", "设置", func(vm *viewModel) { vm.openSettings() }, (*viewModel).buildSettingsModal},
		{"help", "使用帮助", func(vm *viewModel) { vm.showHelp = true }, (*viewModel).buildHelpModal},
		{"edit", "编辑实例", func(vm *viewModel) { vm.openEdit(vm.instances[0]) }, (*viewModel).buildEditModal},
	}
	for _, tc := range cases {
		for _, width := range []int{610, 640, 1180} {
			t.Run(fmt.Sprintf("%s_%d", tc.name, width), func(t *testing.T) {
				vm := newTestVM(t)
				tc.prep(vm)
				tester := ui.NewTester(func(c *ui.Context) { c.SetTheme(workbenchTheme); tc.render(vm, c) }, width, 780)
				tester.Frame()
				title, ok := tester.Find(tc.title)
				if !ok {
					t.Fatalf("missing dialog title %q", tc.title)
				}
				y := int(title.Y) - 8
				left, right := -1, -1
				for x := 0; x < width; x++ {
					col := tester.Image().RGBAAt(x, y)
					if col.R >= 250 && col.G >= 250 && col.B >= 250 {
						if left < 0 {
							left = x
						}
						right = x + 1
					}
				}
				if left < 0 {
					t.Fatal("dialog panel not found")
				}
				for _, label := range tester.Texts() {
					r, ok := tester.Find(label)
					if !ok || r.W <= 0 {
						continue
					}
					if r.X < float32(left+16) || r.X+r.W > float32(right-16) {
						t.Errorf("%q crosses dialog padding: x=%.1f right=%.1f panel=%d..%d", label, r.X, r.X+r.W, left, right)
					}
				}
				if dir := os.Getenv("VIRDECK_SHOT_DIR"); dir != "" && width == 610 {
					f, err := os.Create(filepath.Join(dir, "narrow_"+tc.name+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tester.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestCompactActionBarOpensEditorDirectly(t *testing.T) {
	vm := newTestVM(t)
	vm.selected.Add("e5f60718")
	tester := ui.NewTester(vm.view, 610, 780)
	tester.Frame()
	if tester.HasText("更多操作") {
		t.Fatal("compact action bar still uses a single-item menu")
	}
	if err := tester.Click("编辑"); err != nil {
		t.Fatal(err)
	}
	if !vm.showEdit || vm.editID != "e5f60718" {
		t.Fatal("direct edit button did not open selected instance")
	}
}
