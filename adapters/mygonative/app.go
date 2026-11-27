package mygonative

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"virdeck/assets"
	"virdeck/config"
	"virdeck/core/service"
)

type pageID int

const (
	pageWorkbench pageID = iota
	pageTransfer
	pageImages
	pageLogs
	pageSettings
)

var defaultImageSuggestions = func() []string {
	var out []string
	for _, image := range config.Values.Images {
		out = append(out, image.ID)
	}
	return out
}()

var resolutionOptions = []string{
	"720p (720×1280 · 省内存推荐)",
	"1080p (1080×1920 · 高清)",
	"平板横屏 (1280×800 · 横屏)",
}

func parseResolutionChoice(label string) string {
	s := strings.TrimSpace(label)
	switch {
	case strings.HasPrefix(s, "1080p"):
		return "1080p"
	case strings.HasPrefix(s, "平板"):
		return "tablet"
	case strings.HasPrefix(s, "720p"), s == "":
		return "720p"
	default:
		return s
	}
}

func resolutionChoiceLabel(spec string) string {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "1080p", "1080x1920", "fhd":
		return resolutionOptions[1]
	case "tablet", "tablet_landscape", "1280x800":
		return resolutionOptions[2]
	case "", "720p", "720x1280":
		return resolutionOptions[0]
	default:
		return spec
	}
}

const (
	navWidth       = 212
	detailWidth    = 304
	showNavAt      = 820  // window width from which the sidebar is shown
	showDetailAt   = 1000 // window width from which the detail column is shown
	maxNamePreview = 8
)

// Run launches the MyGo native UI desktop application backed by svc.
func Run(svc service.Service) error {
	vm := newViewModel(svc)
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     assets.Product.Description,
			Width:     1360,
			Height:    860,
			MinWidth:  640,
			MinHeight: 480,
			Content:   ui.View(vm.view),
			Page:      mygo.PageOptions{DevTools: mygo.DevToolsDisabled},
		})
		if err := win.SetIcon(assets.IconPNG); err != nil {
			vm.appendLog(fmt.Sprintf("设置窗口图标失败: %v", err))
		}
		vm.bindWindow(win)
	})
	return mygo.App.Run()
}

type viewModel struct {
	page               pageID
	projection         projectionUIState
	settingsProjection projectionForm
	settingsRefreshing bool
	settingsTab        int
	groups             groupUIState
	transfer           transferUIState
	screens            screenLayoutState
	install            installModalState
	syncUI             syncUIState
	syncStatus         service.InputSyncStatus
	svc                service.Service
	win                *mygo.Window
	// post runs fn on the UI thread (directly when there is no window, as in tests).
	post func(fn func())

	// Snapshot state (only read/mutated on the UI thread via view or post).
	instances     []service.Instance
	visible       []service.Instance // instances after the status filter and search
	env           service.EnvStatus
	envLoaded     bool
	envGuideShown bool
	catalog       []service.ImageItem
	logs          []string

	// Real-time download progress state.
	dlActive   bool
	dlProgress service.DownloadProgress

	// Tasks and the instances they currently hold.
	tasks                []*task
	nextTaskID           int
	busy                 map[string]string             // instance ID -> title of the task running on it
	busyCancel           map[string]context.CancelFunc // instance ID -> cancel func of active task
	startingSince        map[string]time.Time
	tasksOpen            bool
	copiedText           string
	copyText             func(string)
	toastMsg             string
	toastUntil           time.Time
	dropHandledFrame     bool
	lastInstallTargetIDs []string

	// Workbench list & filter state.
	tableState ui.ListState
	selected   ui.Selection[string]
	lastRow    int
	logState   ui.ListState
	filterTab  int
	sortMode   int
	query      string
	prevTab    int
	prevQuery  string

	// Images & logs page filters.
	imageFilter int // 0=全部 1=已安装 2=已收藏 3=支持 ARM 应用
	imageQuery  string
	logFilter   int // 0=全部日志 1=仅看错误 2=流水线 / 任务
	logQuery    string

	// Create modal.
	showCreate      bool
	createName      string
	createCount     float64
	createImage     string
	createMemMB     float64
	createCores     float64
	createStorageGB float64
	createRes       string
	createProxy     string
	createGPUMode   string
	createNote      string
	createHeadless  bool
	createQuickBoot bool
	createGroupID   string

	// Edit modal.
	showEdit      bool
	editID        string
	editName      string
	editMemory    float64
	editCores     float64
	editRes       string
	editProxy     string
	editGPUMode   string
	editNote      string
	editHeadless  bool
	editQuickBoot bool
	editSnapHint  string
	editImage     string
	editError     string

	// Install APK confirm modal.
	showInstall bool

	// Lightweight missing-env decision card.
	showEnvMissing bool

	// Proxy modal. Targets are fixed when the modal opens so the form never
	// inherits values from an earlier dialog or a changed selection.
	showProxy    bool
	proxyTargets []service.Instance
	proxyValue   string
	proxyInitial string
	proxyMixed   bool
	proxyCleared bool

	// Shell modal.
	showShell    bool
	shellTargets []service.Instance
	shellCmd     string

	// Delete confirmation.
	showDelete    bool
	deleteTargets []service.Instance

	// Mixed-selection confirmation.
	showMixed bool
	mixedPlan actionPlan

	// Group staggered-start quota confirmation.
	showGroupStartConfirm bool
	groupStartName        string
	groupStartTargets     []service.Instance
	groupStartAvailable   int
	groupStartMax         int
	groupStartActive      int

	// Images page tabs, help modal & detail collapse.
	envTab       int
	showHelp     bool
	detailHidden bool

	favoriteID    string
	favoriteNote  string
	favoriteError string

	cleanupReport    service.CleanupReport
	cleanupScanned   bool
	cleanupBusy      bool
	cleanupDownloads bool
	cleanupPartials  bool
	cleanupAVD       bool
	cleanupMessage   string

	// Custom system image form inside the catalog tab.
	showCustomImg bool
	customImgID   string
	customImgName string
	customImgDesc string
	customImgURL  string
	customImgSHA1 string

	// Global settings form inside the settings page.
	settingsProxy               string
	settingsConcurrent          float64
	settingsAutoDeleteDownloads bool
	settingsSavedHint           string

	// Google Android SDK License consent modal.
	showLicense bool
	onLicenseOK func()

	// P3-B practical tool modals & file picker hooks (injectable for headless tests).
	pickBackupSavePath func(defaultName string) (string, bool)
	pickBackupOpenPath func() (string, bool)

	showLocation bool
	locTargets   []service.Instance
	locLat       string
	locLon       string
	locError     string
	locBusy      bool

	showApps           bool
	appsSeq            uint64
	appsTarget         service.Instance
	appsQuery          string
	appsThirdPartyOnly bool
	appsList           []service.InstalledApp
	appsLoading        bool
	appsBusy           bool
	appsError          string
	appsNotice         string
	appsConfirmPkg     string
	appsConfirmOp      string

	showBackup          bool
	backupTarget        service.Instance
	backupIncludeProxy  bool
	showRestore         bool
	restoreName         string
	restoreIncludeProxy bool

	createProxyReveal   bool
	editProxyReveal     bool
	proxyReveal         bool
	settingsProxyReveal bool
}

func newViewModel(svc service.Service) *viewModel {
	st := svc.GetSettings()
	maxConc := float64(st.MaxConcurrent)
	if maxConc <= 0 {
		maxConc = float64(config.Values.Runtime.MaxConcurrent)
	}
	cat, _ := svc.ListCatalog(context.Background())

	vm := &viewModel{
		svc:                         svc,
		syncStatus:                  svc.GetInputSyncStatus(),
		page:                        pageWorkbench,
		busy:                        map[string]string{},
		busyCancel:                  map[string]context.CancelFunc{},
		startingSince:               map[string]time.Time{},
		lastRow:                     -1,
		prevTab:                     0,
		catalog:                     cat,
		cleanupDownloads:            true,
		createName:                  config.Values.Create.Name,
		createCount:                 float64(config.Values.Create.Count),
		createImage:                 config.Values.Create.Image,
		createMemMB:                 float64(config.Values.Create.MemoryMB),
		createCores:                 float64(config.Values.Create.Cores),
		createStorageGB:             6,
		createRes:                   defaultResolutionLabel(),
		createGPUMode:               gpuModeOptions[0],
		createHeadless:              config.Values.Create.Headless,
		shellCmd:                    "getprop ro.build.version.release",
		settingsProxy:               st.DownloadProxy,
		settingsConcurrent:          maxConc,
		settingsAutoDeleteDownloads: st.AutoDeleteDownloads,
	}
	vm.post = func(fn func()) {
		if vm.win != nil {
			vm.win.Update(fn)
			return
		}
		fn()
	}
	vm.tableState.Key = func(row int) any {
		if row >= 0 && row < len(vm.visible) {
			return vm.visible[row].ID
		}
		return ""
	}
	vm.tableState.Selection = &vm.selected
	vm.tableState.Selected = &vm.lastRow
	if st.LoadWarning != "" {
		vm.appendLog(st.LoadWarning)
		vm.tasksOpen = true
	}
	vm.refreshGroups()
	vm.logState.FollowEnd = true
	return vm
}

func (vm *viewModel) anyModalOpen() bool {
	return vm.showCreate ||
		vm.showEdit ||
		vm.showProxy ||
		vm.showShell ||
		vm.showDelete ||
		vm.showMixed ||
		vm.showGroupStartConfirm ||
		vm.showLicense ||
		vm.showHelp ||
		vm.showInstall ||
		vm.showEnvMissing ||
		vm.showLocation ||
		vm.showApps ||
		vm.showBackup ||
		vm.showRestore ||
		vm.groups.show ||
		vm.screens.show ||
		vm.syncUI.show ||
		vm.projection.show
}

func (vm *viewModel) showToast(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	vm.toastMsg = msg
	vm.toastUntil = time.Now().Add(4 * time.Second)
	vm.appendLog(fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg))
}

// isDownloadStageActive returns true only while a download, checksum verification, or zip extraction is actively in progress.
func isDownloadStageActive(stage string) bool {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "", "completed", "done", "failed":
		return false
	default:
		return true
	}
}

func formatDownloadStage(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "downloading":
		return "下载中"
	case "verifying":
		return "校验摘要中"
	case "extracting":
		return "解压安装中"
	case "completed", "done":
		return "已完成"
	case "failed":
		return "失败"
	default:
		return stage
	}
}

func (vm *viewModel) bindWindow(win *mygo.Window) {
	vm.win = win

	// Keep elapsed boot timers & toast expiry fresh on the UI thread.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			win.Update(func() {
				_ = vm.syncStartingTimers()
				if vm.toastMsg != "" && !vm.toastUntil.IsZero() && time.Now().After(vm.toastUntil) {
					vm.toastMsg = ""
				}
			})
		}
	}()

	// Subscribe to core service events and push updates onto the UI thread.
	evCh, _ := vm.svc.Subscribe()
	go func() {
		for ev := range evCh {
			if ev.Type == service.EventDownloadProgress {
				if p, ok := ev.Data.(service.DownloadProgress); ok {
					win.Update(func() {
						vm.dlProgress = p
						vm.dlActive = isDownloadStageActive(p.Stage)
					})
				}
				continue
			}
			if ev.Type == service.EventPipelineStage {
				if st, ok := ev.Data.(service.PipelineStage); ok {
					win.Update(func() {
						vm.applyPipelineStage(st)
						if ev.Message != "" {
							vm.appendLog(fmt.Sprintf("[%s] [流水线] %s", ev.Timestamp, ev.Message))
						}
					})
				}
				continue
			}
			if ev.Type == service.EventInputSyncChanged {
				if st, ok := ev.Data.(service.InputSyncStatus); ok {
					win.Update(func() {
						vm.syncStatus = st
					})
				}
				continue
			}
			list, _ := vm.svc.List(context.Background())
			cat, _ := vm.svc.ListCatalog(context.Background())
			win.Update(func() {
				vm.instances = list
				if len(cat) > 0 {
					vm.catalog = cat
				}
				if ev.Message != "" {
					vm.appendLog(fmt.Sprintf("[%s] %s", ev.Timestamp, ev.Message))
				}
			})
		}
	}()

	// Initial load of instances and environment check.
	go func() {
		ctx := context.Background()
		list, _ := vm.svc.List(ctx)
		env, _ := vm.svc.CheckEnv(ctx)
		cat, _ := vm.svc.ListCatalog(ctx)
		vm.post(func() {
			vm.instances = list
			if len(cat) > 0 {
				vm.catalog = cat
			}
			vm.applyEnv(env)
			vm.appendLog(fmt.Sprintf("[%s] 已加载 %d 个实例 | 硬件加速: %s",
				time.Now().Format("15:04:05"), len(list), env.AccelMessage))
			// First-run guide: open lightweight decision card (stay on workbench, non-blocking).
			if blocking, _ := envIssues(buildEnvChecks(env)); blocking > 0 && !vm.envGuideShown {
				vm.envGuideShown = true
				vm.showEnvMissing = true
			}
		})
	}()
}

func (vm *viewModel) applyPipelineStage(st service.PipelineStage) {
	for i := len(vm.tasks) - 1; i >= 0; i-- {
		tk := vm.tasks[i]
		if tk.finished || len(tk.steps) == 0 {
			continue
		}
		matched := false
		for _, it := range tk.items {
			if it.id == st.InstanceID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		tk.stageText = st.StageText
		tk.progressPct = st.Percent
		if st.Step > 0 {
			tk.currentStep = st.Step - 1
			for idx := range tk.steps {
				if idx < st.Step-1 || st.Percent >= 100 {
					tk.steps[idx].done = true
				}
			}
		}
		break
	}
}

// applyEnv stores a fresh environment snapshot and keeps the create form on a usable image.
func (vm *viewModel) applyEnv(env service.EnvStatus) {
	vm.env = env
	vm.envLoaded = true
	if len(env.SystemImages) > 0 && !imageInstalled(env, vm.createImage) {
		vm.createImage = env.SystemImages[0]
	}
}

func (vm *viewModel) refreshEnv() {
	go func() {
		ctx := context.Background()
		env, _ := vm.svc.CheckEnv(ctx)
		cat, _ := vm.svc.ListCatalog(ctx)
		vm.post(func() {
			vm.applyEnv(env)
			if len(cat) > 0 {
				vm.catalog = cat
			}
		})
	}()
}

// requireLicense ensures the user has accepted the Android SDK License before online downloads.
func (vm *viewModel) requireLicense(fn func()) {
	st := vm.svc.GetSettings()
	if st.LicenseAccepted {
		fn()
		return
	}
	vm.onLicenseOK = fn
	vm.showLicense = true
}

func (vm *viewModel) appendLog(line string) {
	line = config.RedactText(strings.ToValidUTF8(line, ""))
	var b strings.Builder
	b.Grow(min(len(line), 8192))
	for _, r := range line {
		if b.Len() >= 8192 {
			b.WriteString("…(已截断)")
			break
		}
		if r == '\r' || r == '\n' || r == '\t' {
			b.WriteByte(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	vm.logs = append(vm.logs, b.String())
	if len(vm.logs) > 500 {
		vm.logs = vm.logs[len(vm.logs)-500:]
	}
}

func openLocalDir(dir string) error {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean == "" || clean == "." || !filepath.IsAbs(clean) || strings.HasPrefix(clean, `\\`) {
		return errors.New("仅支持打开已存在的本地绝对目录")
	}
	info, err := os.Stat(clean)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("目标路径不是目录")
	}
	return mygo.Shell.OpenPath(clean)
}

func isErrorLogLine(line string) bool {
	clean := strings.ReplaceAll(line, "失败 0", "")
	return strings.Contains(clean, "失败") ||
		strings.Contains(clean, "错误") ||
		strings.Contains(clean, "异常") ||
		strings.Contains(strings.ToLower(clean), "error")
}

func (vm *viewModel) errorLogCount() int {
	n := 0
	for _, line := range vm.logs {
		if isErrorLogLine(line) {
			n++
		}
	}
	return n
}

func (vm *viewModel) writeClipboard(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	vm.copiedText = text
	if vm.copyText != nil {
		vm.copyText(text)
		return
	}
	if vm.win != nil {
		mygo.Clipboard.WriteText(text)
	}
}

func (vm *viewModel) syncStartingTimers() bool {
	if vm.startingSince == nil {
		vm.startingSince = map[string]time.Time{}
	}
	hasStarting := false
	active := map[string]bool{}
	now := time.Now()
	for _, inst := range vm.instances {
		if inst.State == service.Starting {
			hasStarting = true
			active[inst.ID] = true
			if _, ok := vm.startingSince[inst.ID]; !ok {
				vm.startingSince[inst.ID] = now
			}
		}
	}
	for id := range vm.startingSince {
		if !active[id] {
			delete(vm.startingSince, id)
		}
	}
	return hasStarting
}

func (vm *viewModel) instanceStateLabel(inst service.Instance) string {
	if inst.State == service.Starting {
		if since, ok := vm.startingSince[inst.ID]; ok {
			if sec := int(time.Since(since).Seconds()); sec > 0 {
				return fmt.Sprintf("◐ 启动中 · %ds", sec)
			}
		}
	}
	return formatStateLabel(inst.State)
}

// refreshVisible applies the status tab and search box to the instance list.
func (vm *viewModel) refreshVisible() {
	if vm.filterTab != vm.prevTab || vm.query != vm.prevQuery {
		vm.prevTab, vm.prevQuery = vm.filterTab, vm.query
		vm.lastRow = -1
	}
	q := strings.ToLower(strings.TrimSpace(vm.query))
	vm.visible = vm.visible[:0]
	for _, inst := range vm.instances {
		if !vm.inGroup(inst.ID) {
			continue
		}
		if !matchesTab(vm.filterTab, inst) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(inst.Name), q) &&
			!strings.Contains(strings.ToLower(inst.ID), q) &&
			!strings.Contains(strings.ToLower(inst.Note), q) {
			continue
		}
		vm.visible = append(vm.visible, inst)
	}
	switch vm.sortMode {
	case 1: // 按名称 A-Z
		sort.SliceStable(vm.visible, func(i, j int) bool {
			return strings.ToLower(vm.visible[i].Name) < strings.ToLower(vm.visible[j].Name)
		})
	case 2: // 运行中优先
		sort.SliceStable(vm.visible, func(i, j int) bool {
			return stateSortRank(vm.visible[i].State) < stateSortRank(vm.visible[j].State)
		})
	case 3: // 最新创建
		sort.SliceStable(vm.visible, func(i, j int) bool {
			return vm.visible[i].CreatedAt > vm.visible[j].CreatedAt
		})
	}
}

func stateSortRank(s service.State) int {
	switch s {
	case service.Running:
		return 0
	case service.Starting:
		return 1
	case service.Stopped:
		return 2
	case service.Failed:
		return 3
	default:
		return 4
	}
}

func adbCommandForInstance(inst service.Instance, adbPort int) string {
	if adbPort <= 0 {
		adbPort = config.Values.Runtime.ADBServerPort
	}
	if inst.Port > 0 {
		return fmt.Sprintf("adb -P %d -s emulator-%d shell", adbPort, inst.Port)
	}
	return fmt.Sprintf("adb -P %d devices", adbPort)
}

func matchesTab(tab int, inst service.Instance) bool {
	switch tab {
	case 1:
		return inst.State == service.Running || inst.State == service.Starting
	case 2:
		return inst.State == service.Stopped
	case 3:
		return inst.State == service.Failed
	}
	return true
}

// selectedInsts returns the selected instances that are in the current list.
func (vm *viewModel) selectedInsts() []service.Instance {
	var out []service.Instance
	for _, inst := range vm.visible {
		if vm.selected.Has(inst.ID) {
			out = append(out, inst)
		}
	}
	return out
}

func (vm *viewModel) freshInstances(ids []string) []service.Instance {
	var out []service.Instance
	for _, id := range ids {
		if inst, ok := vm.findInstance(id); ok {
			out = append(out, inst)
		}
	}
	return out
}

func idsOf(insts []service.Instance) []string {
	ids := make([]string, 0, len(insts))
	for _, inst := range insts {
		ids = append(ids, inst.ID)
	}
	return ids
}

// ---------------------------------------------------------------- actions

// invoke runs an action for a selection plan; a mixed selection asks first.
func (vm *viewModel) invoke(a action, p actionPlan) {
	if p.mixed() {
		vm.mixedPlan = p
		vm.showMixed = true
		return
	}
	vm.perform(a, p.eligible)
}

func (vm *viewModel) perform(a action, insts []service.Instance) {
	if len(insts) == 0 {
		return
	}
	ids := idsOf(insts)
	switch a {
	case actTransfer:
		vm.openTransfer(insts)
	case actStart:
		vm.startTask("启动", a, targetsOf(insts), true, func(ctx context.Context, t target) error {
			return vm.svc.Start(ctx, t.id)
		}, nil)
	case actStop:
		for _, inst := range insts {
			vm.cancelInstanceTask(inst.ID)
		}
		vm.startTask("停止", a, targetsOf(insts), true, func(ctx context.Context, t target) error {
			return vm.svc.Stop(ctx, t.id)
		}, nil)
	case actScreen:
		vm.startTask("投屏", a, targetsOf(insts), true, func(ctx context.Context, t target) error {
			return vm.svc.OpenScreen(ctx, t.id)
		}, nil)
	case actInstallAPK:
		vm.pickAndOpenInstallAPK(ids)
	case actShell:
		vm.shellTargets = insts
		vm.showShell = true
	case actProxy:
		vm.openProxy(insts)
	case actDelete:
		vm.deleteTargets = insts
		vm.showDelete = true
	}
}

// actionButton draws one action button for the selection sel and wires its click.
func (vm *viewModel) actionButton(c *ui.Context, scope string, a action, sel []service.Instance, primary bool) ui.Element {
	p := planAction(a, sel, vm.busy)
	label := p.buttonLabel(a.label())
	btn := control(c, scope+"_"+strconv.Itoa(int(a)), label, actionIcon(a), primary)

	hint := p.disabledHint()
	btn = btn.Disabled(hint != "")
	switch {
	case hint != "":
		btn = btn.Tooltip(hint)
	case p.mixed():
		btn = btn.Tooltip(fmt.Sprintf("所选 %d 台中 %d 台可操作，点击后可确认", p.total, len(p.eligible)))
	}
	if btn.Clicked() {
		vm.invoke(a, p)
	}
	return btn
}

func (vm *viewModel) openProxy(insts []service.Instance) {
	vm.proxyTargets = insts
	groups := proxyBreakdown(insts)
	vm.proxyMixed = len(groups) > 1
	vm.proxyCleared = false
	vm.proxyReveal = false
	if vm.proxyMixed {
		// Never carry a guess over: the user must type a value or clear explicitly.
		vm.proxyInitial = ""
		vm.proxyValue = ""
	} else {
		vm.proxyInitial = groups[0].proxy
		vm.proxyValue = groups[0].proxy
	}
	vm.showProxy = true
}

func (vm *viewModel) proxySaveReady() bool {
	val := strings.TrimSpace(vm.proxyValue)
	if vm.proxyMixed {
		return val != "" || vm.proxyCleared
	}
	return val != strings.TrimSpace(vm.proxyInitial)
}

func (vm *viewModel) startInstallAllBaseTools() {
	vm.requireLicense(func() {
		vm.startTask("补齐基础工具", actCreate, []target{{name: "adb / emulator / scrcpy"}}, false, func(ctx context.Context, _ target) error {
			err := vm.svc.EnsureBaseTool(ctx, "all")
			vm.refreshEnv()
			return err
		}, func(_ *task) {
			vm.dlActive = false
		})
	})
}

// ---------------------------------------------------------------- lightweight modals

func (vm *viewModel) buildCreateModal(c *ui.Context) {
	ui.Modal(c, &vm.showCreate, func() {
		t := c.Theme()
		modalContent(c, 560).Gap(12).Children(func() {
			ui.Text(c, "新建干净安卓实例").FontSize(16).Bold()
			ui.Text(c, "每台实例拥有独立应用、账号和数据。先选择归属与系统镜像，再调整设备配置。").
				FontSize(12).TextColor(t.TextMuted)

			options := imageChoices(vm.env, vm.catalog...)
			for i, pkg := range options {
				options[i] = imageChoiceLabel(vm.env, pkg)
			}
			imgInstalled := imageInstalled(vm.env, vm.createImage)

			_, modalHeight := c.Size()
			ui.Scroll(c.Key("create_fields")).Height(min(float32(420), max(float32(130), modalHeight-270))).Children(func() {
				ui.Form(c, func() {
					ui.Field(c, "所属分组", func() { vm.groupPicker(c, "create_group", &vm.createGroupID) })
					ui.Field(c, "实例名称 / 批量前缀", func() {
						ui.TextInput(c, &vm.createName).Placeholder("例如 工作账号")
					})
					ui.Field(c, "创建数量", func() {
						ui.NumberInput(c, &vm.createCount, 1, 32, 1)
					})
					ui.Field(c, "系统镜像", func() {
						selectedLabel := imageChoiceLabel(vm.env, vm.createImage)
						ui.Combobox(c, &selectedLabel, options).Label("系统镜像选择").Placeholder("选择或输入 system-images;...")
						// Decoration is presentation only; service calls always receive the package ID.
						vm.createImage = strings.TrimPrefix(selectedLabel, installedImagePrefix)
					}).Description("已安装的镜像排在前面；Gmail / WhatsApp 推荐选用 google_apis_playstore 镜像")
					ui.Field(c, "分辨率", func() {
						ui.Combobox(c, &vm.createRes, resolutionOptions)
					})
					ui.Field(c, "性能预设", func() {
						renderPerfPresets(c, "create", &vm.createCores, &vm.createMemMB)
					})
					ui.Field(c, "内存 (MB)", func() {
						ui.NumberInput(c, &vm.createMemMB, 1024, 16384, 512)
					})
					ui.Field(c, "CPU 核数", func() {
						ui.NumberInput(c, &vm.createCores, 1, 8, 1)
					})
					ui.Field(c, "存储容量 (GB)", func() {
						ui.NumberInput(c, &vm.createStorageGB, 4, 128, 2)
					})
					ui.Field(c, "GPU 渲染模式", func() {
						if vm.createGPUMode == "" {
							vm.createGPUMode = gpuModeOptions[0]
						}
						ui.Select(c.Key("create_gpu"), &vm.createGPUMode, gpuModeOptions).Label("选择 GPU 渲染模式")
					})
					ui.Field(c, "网络代理", func() {
						proxyTextInput(c, "create_proxy", &vm.createProxy, &vm.createProxyReveal, "可选，http://127.0.0.1:7890 或 socks5://127.0.0.1:1080")
					})
					ui.Field(c, "实例备注", func() {
						ui.TextInput(c.Key("create_note"), &vm.createNote).Placeholder("可选，用途或账号标签（最多 80 字）")
					})
					ui.Field(c, "运行模式", func() {
						ui.Checkbox(c, &vm.createHeadless, "默认无头运行 (-no-window)，需要看画面时用 scrcpy 投屏")
					})
					ui.Field(c, "启动方式", func() {
						ui.Checkbox(c.Key("create_quickboot"), &vm.createQuickBoot, "快速启动（保存快照，多占 ~1-2 GB 磁盘）")
					})
				})
			})
			if vm.envLoaded {
				if imgInstalled {
					ui.Text(c, "✓ 该镜像已安装就绪，可直接创建实例").FontSize(12).TextColor(t.Success)
				} else {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "! 该镜像尚未安装，请先在「镜像与环境」中一键下载或导入 .zip。").
							FontSize(12).TextColor(t.Warning)
						if ui.Button(c.Key("goto_dl_img"), "去下载该镜像").Clicked() {
							vm.showCreate = false
							vm.page = pageImages
							vm.envTab = envTabImages
							vm.refreshEnv()
						}
					})
				}
			}

			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("create_restore"), "从备份还原…").Clicked() {
					vm.showCreate = false
					vm.openRestoreModal()
				}
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_create"), "取消").Clicked() {
					vm.showCreate = false
				}
				if ui.PrimaryButton(c.Key("confirm_create"), "立即创建").Clicked() {
					res := parseResolutionChoice(vm.createRes)
					spec := service.CreateSpec{
						NamePrefix:  vm.createName,
						Count:       int(vm.createCount),
						SystemImage: vm.createImage,
						MemoryMB:    int(vm.createMemMB),
						Cores:       int(vm.createCores),
						StorageGB:   int(vm.createStorageGB),
						Resolution:  res,
						QuickBoot:   vm.createQuickBoot,
						Proxy:       vm.createProxy,
						GPUMode:     parseGPUModeChoice(vm.createGPUMode),
						Note:        vm.createNote,
						Headless:    vm.createHeadless,
					}
					vm.createNote = ""
					vm.showCreate = false
					name := fmt.Sprintf("%s ×%d", strings.TrimSpace(spec.NamePrefix), spec.Count)
					groupID := vm.createGroupID
					vm.startTask("创建实例", actCreate, []target{{name: name}}, false, func(ctx context.Context, _ target) error {
						created, err := vm.svc.Create(ctx, spec)
						if len(created) > 0 && groupID != "" {
							if e := vm.svc.AssignGroup(ctx, idsOf(created), groupID); e != nil {
								if err != nil {
									return fmt.Errorf("创建出现错误：%v；已创建实例归组失败：%w", err, e)
								}
								return fmt.Errorf("实例已创建，但归组失败（保留在未分组）：%w", e)
							}
						}
						return err
					}, func(*task) { vm.refreshGroups(); vm.refreshVisible() })
				}
			})
		})
	})
}

func (vm *viewModel) buildProxyModal(c *ui.Context) {
	ui.Modal(c, &vm.showProxy, func() {
		t := c.Theme()
		targets := vm.proxyTargets
		runningN := 0
		for _, inst := range targets {
			if inst.State == service.Running || inst.State == service.Starting {
				runningN++
			}
		}

		modalContent(c, 480).Gap(12).Children(func() {
			ui.Textf(c, "设置 HTTP / SOCKS5 代理（共 %d 个实例）", len(targets)).FontSize(16).Bold()
			ui.Text(c, "支持 http:// 或 socks5://（本地桥接仅覆盖 TCP/HTTP 栈，默认跟随宿主 DNS）。").FontSize(12).TextColor(t.TextMuted)

			switch {
			case vm.proxyMixed && vm.proxyCleared && strings.TrimSpace(vm.proxyValue) == "":
				ui.Text(c, "将清除所选全部实例的代理（改为直连）。").FontSize(12).TextColor(t.Warning)
			case vm.proxyMixed:
				ui.Textf(c, "所选实例的代理配置不一致：%s。\n请输入一个新代理统一覆盖，或点击「清除代理」全部改为直连。",
					describeProxyGroups(proxyBreakdown(targets))).FontSize(12).TextColor(t.Warning)
			default:
				ui.Textf(c, "所选实例当前代理一致：%s。", proxyText(vm.proxyInitial)).FontSize(12).TextColor(t.TextMuted)
			}

			ui.Form(c, func() {
				ui.Field(c, "代理地址", func() {
					proxyTextInput(c, "modal_proxy_input", &vm.proxyValue, &vm.proxyReveal, "http://127.0.0.1:7890 或 socks5://127.0.0.1:1080")
				})
			})

			if runningN > 0 {
				ui.Textf(c, "其中 %d 台正在运行：保存后会标记「待重启生效」，重启实例后才会应用新代理。", runningN).
					FontSize(12).TextColor(t.TextMuted)
			}

			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("clear_proxy"), "清除代理").Clicked() {
					vm.proxyValue = ""
					vm.proxyCleared = true
				}
				if ui.Button(c.Key("cancel_proxy"), "取消").Clicked() {
					vm.showProxy = false
				}
				save := ui.PrimaryButton(c.Key("confirm_proxy"), "保存").Disabled(!vm.proxySaveReady())
				if save.Clicked() {
					val := strings.TrimSpace(vm.proxyValue)
					vm.showProxy = false
					vm.startTask("设置代理", actProxy, targetsOf(targets), true, func(ctx context.Context, tg target) error {
						return vm.svc.SetProxy(ctx, tg.id, val)
					}, nil)
				}
			})
		})
	})
}

func (vm *viewModel) buildShellModal(c *ui.Context) {
	ui.Modal(c, &vm.showShell, func() {
		targets := vm.shellTargets
		modalContent(c, 500).Gap(12).Children(func() {
			ui.Textf(c, "批量执行 ADB Shell 命令（%d 个运行中的实例）", len(targets)).FontSize(16).Bold()
			ui.Text(c, namesPreview(targets, maxNamePreview)).FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Form(c, func() {
				ui.Field(c, "Shell 命令", func() {
					ui.TextInput(c, &vm.shellCmd).Placeholder("例如 input keyevent KEYCODE_HOME")
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_shell"), "取消").Clicked() {
					vm.showShell = false
				}
				if ui.PrimaryButton(c.Key("confirm_shell"), "并发执行").Clicked() {
					cmd := vm.shellCmd
					vm.showShell = false
					p := planAction(actShell, vm.freshInstances(idsOf(targets)), vm.busy)
					if len(p.eligible) == 0 {
						return
					}
					vm.startTask("批量命令", actShell, targetsOf(p.eligible), true, func(ctx context.Context, tg target) error {
						outputs, err := vm.svc.Shell(ctx, []string{tg.id}, cmd)
						vm.post(func() {
							for _, out := range outputs {
								vm.appendLog(fmt.Sprintf("[shell:%s] %s", tg.name, strings.TrimSpace(out)))
							}
						})
						return err
					}, nil)
				}
			})
		})
	})
}

func (vm *viewModel) buildDeleteModal(c *ui.Context) {
	ui.Modal(c, &vm.showDelete, func() {
		t := c.Theme()
		targets := vm.deleteTargets
		runningN := 0
		for _, inst := range targets {
			if inst.State == service.Running || inst.State == service.Starting {
				runningN++
			}
		}
		modalContent(c, 480).Gap(12).Children(func() {
			ui.Textf(c, "确认删除 %d 个实例？", len(targets)).FontSize(16).Bold()
			ui.Text(c, namesPreview(targets, maxNamePreview)).FontSize(13)
			ui.Text(c, "删除后，这些实例里的应用、登录信息和本地数据都会被永久清除，无法恢复。").
				FontSize(12).TextColor(t.Danger)
			if runningN > 0 {
				ui.Textf(c, "其中 %d 台正在运行，会先被停止再删除。", runningN).FontSize(12).TextColor(t.TextMuted)
			}
			ui.Text(c, "删除失败的实例会保持选中，方便你查看原因后重试。").FontSize(12).TextColor(t.TextMuted)

			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_delete"), "取消").Clicked() {
					vm.showDelete = false
				}
				if ui.PrimaryButton(c.Key("confirm_delete"), fmt.Sprintf("确认删除 %d 台", len(targets))).Clicked() {
					vm.showDelete = false
					p := planAction(actDelete, vm.freshInstances(idsOf(targets)), vm.busy)
					if len(p.eligible) == 0 {
						return
					}
					vm.startTask("删除", actDelete, targetsOf(p.eligible), true, func(ctx context.Context, tg target) error {
						return vm.svc.Delete(ctx, tg.id)
					}, func(tk *task) {
						// Deleted instances leave selection and remembered quick targets; failed ones stay selected for retry.
						deletedSet := make(map[string]bool)
						for _, it := range tk.items {
							if it.status == itemOK {
								vm.selected.Remove(it.id)
								deletedSet[it.id] = true
							}
						}
						if len(deletedSet) > 0 && len(vm.lastInstallTargetIDs) > 0 {
							kept := vm.lastInstallTargetIDs[:0]
							for _, id := range vm.lastInstallTargetIDs {
								if !deletedSet[id] {
									kept = append(kept, id)
								}
							}
							vm.lastInstallTargetIDs = kept
						}
					})
				}
			})
		})
	})
}

func (vm *viewModel) buildMixedModal(c *ui.Context) {
	ui.Modal(c, &vm.showMixed, func() {
		t := c.Theme()
		p := vm.mixedPlan
		modalContent(c, 480).Gap(10).Children(func() {
			ui.Textf(c, "「%s」只能对 %d 台实例执行", p.action.label(), len(p.eligible)).FontSize(16).Bold()
			ui.Textf(c, "所选 %d 台中，以下 %d 台当前不可用，将被跳过：", p.total, len(p.skipped)).
				FontSize(12).TextColor(t.TextMuted)
			for i, sk := range p.skipped {
				if i >= maxNamePreview {
					ui.Textf(c, "…另有 %d 台", len(p.skipped)-maxNamePreview).FontSize(12).TextColor(t.TextMuted)
					break
				}
				ui.Textf(c, "· %s — %s", sk.inst.Name, sk.reason).FontSize(12)
			}
			ui.Row(c).Gap(8).Margin(6, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_mixed"), "取消").Clicked() {
					vm.showMixed = false
				}
				if ui.PrimaryButton(c.Key("confirm_mixed"), fmt.Sprintf("仅操作可用的 %d 台", len(p.eligible))).Clicked() {
					vm.showMixed = false
					// Re-check: states may have changed while the dialog was open.
					np := planAction(p.action, vm.freshInstances(idsOf(p.eligible)), vm.busy)
					vm.perform(p.action, np.eligible)
				}
			})
		})
	})
}

func (vm *viewModel) buildLicenseModal(c *ui.Context) {
	ui.Modal(c, &vm.showLicense, func() {
		t := c.Theme()
		modalContent(c, 540).Gap(12).Children(func() {
			ui.Text(c, "Google Android SDK 许可协议确认").FontSize(16).Bold()
			ui.Text(c, "在从 Google 官方服务器下载 Android Emulator、Platform-Tools 或系统镜像（含 Google Play）之前，请阅读并确认以下条款：").
				FontSize(12).TextColor(t.TextMuted)
			ui.Column(c).Padding(10, 12).Radius(6).Background(t.Surface).Gap(6).Children(func() {
				ui.Text(c, "1. 所下载的 Android SDK 组件与系统镜像由 Google LLC 提供，受《Android Software Development Kit License Agreement》约束。").FontSize(12)
				ui.Text(c, "2. VirDeck 仅作为本地环境引导与管理工具，按需从 dl.google.com 拉取官方原版组件并校验 SHA-256 / SHA-1 摘要。").FontSize(12)
				ui.Text(c, "3. 点击「同意并继续下载」即表示您已阅读并同意 Google Android SDK 许可协议，后续下载不再重复弹窗。").FontSize(12)
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_license"), "拒绝并取消").Clicked() {
					vm.showLicense = false
					vm.onLicenseOK = nil
				}
				if ui.PrimaryButton(c.Key("accept_license"), "同意并继续下载").Clicked() {
					st := vm.svc.GetSettings()
					st.LicenseAccepted = true
					_ = vm.svc.UpdateSettings(st)
					vm.showLicense = false
					cb := vm.onLicenseOK
					vm.onLicenseOK = nil
					if cb != nil {
						cb()
					}
				}
			})
		})
	})
}

func kv(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Width(72)
		ui.Text(c, value).FontSize(11).Grow(1).TextAlign(ui.End)
	})
}

func formatStateLabel(st service.State) string {
	switch st {
	case service.Running:
		return "● 运行中"
	case service.Starting:
		return "◐ 启动中"
	case service.Failed:
		return "✖ 异常"
	default:
		return "○ 已停止"
	}
}

func stateColor(t *ui.Theme, st service.State) ui.Color {
	switch st {
	case service.Running:
		return t.Success
	case service.Starting:
		return t.Warning
	case service.Failed:
		return t.Danger
	}
	return t.TextMuted
}

func stateSoftBg(st service.State) ui.Color {
	switch st {
	case service.Running:
		return workbenchPalette.SuccessSoftBg
	case service.Starting:
		return workbenchPalette.WarningSoftBg
	case service.Failed:
		return workbenchPalette.DangerSoftBg
	default:
		return workbenchPalette.StoppedSoftBg
	}
}

func defaultResolutionLabel() string {
	for _, label := range resolutionOptions {
		if strings.HasPrefix(label, config.Values.Create.Resolution+" ") {
			return label
		}
	}
	return resolutionOptions[0]
}
