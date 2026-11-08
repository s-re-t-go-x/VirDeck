package mygonative

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"virdeck/assets"
	"virdeck/config"
	"virdeck/core/catalog"
	"virdeck/core/service"
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
}

const (
	navWidth       = 176
	detailWidth    = 284
	showNavAt      = 1000 // window width from which the navigation column is shown
	showDetailAt   = 820  // window width from which the detail column is shown
	taskPanelH     = 240
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
		})
		if err := win.SetIcon(assets.IconPNG); err != nil {
			vm.appendLog(fmt.Sprintf("设置窗口图标失败: %v", err))
		}
		vm.bindWindow(win)
	})
	return mygo.App.Run()
}

type viewModel struct {
	svc service.Service
	win *mygo.Window
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
	tasks      []*task
	nextTaskID int
	busy       map[string]string // instance ID -> title of the task running on it
	tasksOpen  bool
	panelTab   int

	// List state.
	tableState ui.ListState
	selected   ui.Selection[string]
	lastRow    int
	logState   ui.ListState
	filterTab  int
	query      string
	prevTab    int
	prevQuery  string

	// Create modal.
	showCreate     bool
	createName     string
	createCount    float64
	createImage    string
	createMemMB    float64
	createCores    float64
	createRes      string
	createProxy    string
	createHeadless bool

	showEdit     bool
	editID       string
	editName     string
	editMemory   float64
	editCores    float64
	editProxy    string
	editHeadless bool
	editImage    string
	editError    string

	// Clone modal.
	showClone  bool
	cloneSrcID string
	cloneCount float64

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

	// Environment & Catalog & Settings modal.
	showEnv      bool
	envTab       int
	showSettings bool
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

	// Global settings form inside the settings tab.
	settingsProxy      string
	settingsConcurrent float64
	settingsSavedHint  string

	// Google Android SDK License consent modal.
	showLicense bool
	onLicenseOK func()
}

func newViewModel(svc service.Service) *viewModel {
	st := svc.GetSettings()
	maxConc := float64(st.MaxConcurrent)
	if maxConc <= 0 {
		maxConc = float64(config.Values.Runtime.MaxConcurrent)
	}
	cat, _ := svc.ListCatalog(context.Background())

	vm := &viewModel{
		svc:                svc,
		busy:               map[string]string{},
		lastRow:            -1,
		prevTab:            0,
		catalog:            cat,
		cleanupDownloads:   true,
		createName:         config.Values.Create.Name,
		createCount:        float64(config.Values.Create.Count),
		createImage:        config.Values.Create.Image,
		createMemMB:        float64(config.Values.Create.MemoryMB),
		createCores:        float64(config.Values.Create.Cores),
		createRes:          defaultResolutionLabel(),
		createHeadless:     config.Values.Create.Headless,
		cloneCount:         float64(config.Values.CloneCount),
		shellCmd:           "getprop ro.build.version.release",
		settingsProxy:      st.DownloadProxy,
		settingsConcurrent: maxConc,
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
	vm.logState.FollowEnd = true
	return vm
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
		return "SHA-1 校验中"
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
			// First-run guide: open it automatically while something essential is missing.
			if blocking, _ := envIssues(buildEnvChecks(env)); blocking > 0 && !vm.envGuideShown {
				vm.envGuideShown = true
				vm.showEnv = true
			}
		})
	}()
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
	vm.logs = append(vm.logs, line)
	if len(vm.logs) > 500 {
		vm.logs = vm.logs[len(vm.logs)-500:]
	}
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
		if !matchesTab(vm.filterTab, inst) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(inst.Name), q) && !strings.Contains(strings.ToLower(inst.ID), q) {
			continue
		}
		vm.visible = append(vm.visible, inst)
	}
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
	case actStart:
		vm.startTask("启动", a, targetsOf(insts), true, func(ctx context.Context, t target) error {
			return vm.svc.Start(ctx, t.id)
		}, nil)
	case actStop:
		vm.startTask("停止", a, targetsOf(insts), true, func(ctx context.Context, t target) error {
			return vm.svc.Stop(ctx, t.id)
		}, nil)
	case actScreen:
		vm.startTask("投屏", a, targetsOf(insts), true, func(ctx context.Context, t target) error {
			return vm.svc.OpenScreen(ctx, t.id)
		}, nil)
	case actInstallAPK:
		go func() {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
				Parent:  vm.win,
				Title:   "选择要批量安装的 APK 文件",
				Filters: []mygo.FileFilter{{Name: "Android Package (*.apk)", Extensions: []string{"apk"}}},
			})
			if err != nil || len(paths) == 0 {
				return
			}
			apk := paths[0]
			vm.post(func() {
				// The state may have changed while the file dialog was open.
				p := planAction(actInstallAPK, vm.freshInstances(ids), vm.busy)
				if len(p.eligible) == 0 {
					return
				}
				vm.startTask("安装 APK", actInstallAPK, targetsOf(p.eligible), true, func(ctx context.Context, t target) error {
					return vm.svc.InstallAPK(ctx, []string{t.id}, apk)
				}, nil)
			})
		}()
	case actShell:
		vm.shellTargets = insts
		vm.showShell = true
	case actProxy:
		vm.openProxy(insts)
	case actDelete:
		vm.deleteTargets = insts
		vm.showDelete = true
	case actClone:
		vm.cloneSrcID = insts[0].ID
		vm.showClone = true
	}
}

// actionButton draws one action button for the selection sel and wires its click.
func (vm *viewModel) actionButton(c *ui.Context, scope string, a action, sel []service.Instance, primary bool) ui.Element {
	p := planAction(a, sel, vm.busy)
	if a == actClone && len(sel) != 1 {
		p = actionPlan{action: a, total: len(sel)}
		if len(sel) > 1 {
			p.skipped = []skippedInstance{{reason: "克隆一次只能选择一个实例"}}
		}
	}
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

// ---------------------------------------------------------------- layout

func (vm *viewModel) view(c *ui.Context) {
	c.SetTheme(workbenchTheme)
	vm.refreshVisible()
	w, _ := c.Size()
	showNav := w >= showNavAt
	showDetail := w >= showDetailAt && !vm.detailHidden
	ui.Row(c).Fill().Background(c.Theme().Background).AlignItems(ui.Stretch).Children(func() {
		if showNav {
			vm.buildNav(c)
		}
		ui.Column(c).Grow(1).Children(func() {
			vm.buildHeader(c, showNav)
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Grow(1).Children(func() { vm.buildCenter(c, showDetail) })
				if showDetail {
					vm.buildDetail(c)
				}
			})
			vm.buildTaskDock(c)
		})
	})
	vm.buildCreateModal(c)
	vm.buildEditModal(c)
	vm.buildCloneModal(c)
	vm.buildProxyModal(c)
	vm.buildShellModal(c)
	vm.buildDeleteModal(c)
	vm.buildMixedModal(c)
	vm.buildEnvModal(c)
	vm.buildSettingsModal(c)
	vm.buildHelpModal(c)
	vm.buildLicenseModal(c)
}

func (vm *viewModel) healthColor(c *ui.Context) ui.Color {
	t := c.Theme()
	blocking, optional := envIssues(buildEnvChecks(vm.env))
	switch {
	case !vm.envLoaded:
		return t.TextMuted
	case blocking > 0:
		return t.Danger
	case optional > 0:
		return t.Warning
	}
	return t.Success
}

func (vm *viewModel) buildNav(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(navWidth).Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Padding(28, 12, 18, 12).Gap(8).Children(func() {
		ui.Row(c).Gap(10).Padding(0, 4).Children(func() {
			ui.Image(c, brandMark).Width(30).Height(30).Shrink(0)
			ui.Text(c, assets.Product.Name).FontSize(20).Bold()
		})
		ui.Text(c, "安卓设备管理").FontSize(11).TextColor(t.TextMuted).Padding(0, 4)
		ui.Row(c).Margin(28, 0, 2, 0).Height(42).Padding(0, 12).Gap(10).Radius(7).Background(t.Selection).TextColor(t.Accent).Children(func() {
			glyph(c, "device", 16)
			ui.Text(c, "实例").FontSize(13).Bold()
			ui.Spacer(c)
			ui.Textf(c, "%d", len(vm.instances)).FontSize(11)
		})
		if quietControl(c, "nav_env", "镜像与环境", "layers").Height(42).Justify(ui.Start).Clicked() {
			vm.showEnv, vm.envTab = true, 0
			vm.refreshEnv()
		}
		ui.Spacer(c)
		if quietControl(c, "nav_settings", "设置", "settings").Height(40).Justify(ui.Start).Clicked() {
			vm.openSettings()
		}
		if quietControl(c, "nav_help", "使用帮助", "help").Height(40).Justify(ui.Start).Clicked() {
			vm.showHelp = true
		}
		ui.Divider(c)
		ui.Column(c).Padding(10, 6).Gap(7).Children(func() {
			ui.Text(c, "●  "+envHealthText(vm.envLoaded, buildEnvChecks(vm.env))).FontSize(10).TextColor(vm.healthColor(c))
			accel := "硬件加速待检测"
			if vm.envLoaded {
				if vm.env.AccelOK {
					accel = "硬件加速可用"
				} else {
					accel = "硬件加速需要配置"
				}
			}
			ui.Text(c, accel).FontSize(10).TextColor(t.TextMuted)
		})
		ui.Text(c, assets.Product.Name+" "+assets.Product.Version).FontSize(9).TextColor(t.TextMuted).Padding(10, 6, 0, 6)
	})
}

func (vm *viewModel) buildHeader(c *ui.Context, showNav bool) {
	t := c.Theme()
	running := 0
	for _, inst := range vm.instances {
		if inst.State == service.Running || inst.State == service.Starting {
			running++
		}
	}
	ui.Row(c).Height(106).Padding(24, 28).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "实例工作台").FontSize(24).Bold()
			ui.Textf(c, "%d 台设备，%d 台运行中", len(vm.instances), running).FontSize(12).TextColor(t.TextMuted)
		})
		ui.Spacer(c)
		if !showNav {
			if quietControl(c, "hdr_env", "环境", "layers").Clicked() {
				vm.showEnv, vm.envTab = true, 0
				vm.refreshEnv()
			}
			if quietControl(c, "hdr_settings", "设置", "settings").Clicked() {
				vm.openSettings()
			}
		}
		if vm.detailHidden {
			if quietControl(c, "hdr_detail", "实例详情", "device").Clicked() {
				vm.detailHidden = false
			}
		}
		if control(c, "btn_create", "新建实例", "plus", true).Height(36).Clicked() {
			vm.showCreate = true
		}
	})
}

func (vm *viewModel) buildCenter(c *ui.Context, showDetail bool) {
	t := c.Theme()

	counts := [4]int{len(vm.instances)}
	for _, inst := range vm.instances {
		if inst.State == service.Running || inst.State == service.Starting {
			counts[1]++
		}
		if inst.State == service.Stopped {
			counts[2]++
		}
		if inst.State == service.Failed {
			counts[3]++
		}
	}

	ui.Row(c).Height(62).Padding(10, 24).Gap(12).Children(func() {
		ui.Tabs(c, &vm.filterTab,
			fmt.Sprintf("全部 %d", counts[0]),
			fmt.Sprintf("运行中 %d", counts[1]),
			fmt.Sprintf("已停止 %d", counts[2]),
			fmt.Sprintf("异常 %d", counts[3]))
		ui.Spacer(c)
		ui.Row(c).Gap(8).Children(func() {
			glyph(c, "search", 15).TextColor(t.TextMuted)
			ui.TextInput(c, &vm.query).Placeholder("搜索名称或 ID…").Width(160).Height(30).FontSize(11)
		})
	})
	ui.Divider(c)

	sel := vm.selectedInsts()

	// One instance with the detail column visible is handled there; otherwise show the bar.
	if len(sel) >= 2 || (len(sel) == 1 && !showDetail) {
		vm.buildActionBar(c, sel)
	}
	vm.buildInstanceTable(c)
}

func (vm *viewModel) buildActionBar(c *ui.Context, sel []service.Instance) {
	t := c.Theme()
	ui.Row(c).Padding(10, 24).Gap(8).Wrap().Background(t.Selection).Children(func() {
		if len(sel) == 1 {
			ui.Text(c, sel[0].Name).Bold().FontSize(12)
		} else {
			ui.Textf(c, "已选 %d 台", len(sel)).Bold().FontSize(12)
		}
		vm.actionButton(c, "bar", actStart, sel, true)
		vm.actionButton(c, "bar", actStop, sel, false)
		vm.actionButton(c, "bar", actScreen, sel, false)
		vm.actionButton(c, "bar", actInstallAPK, sel, false)
		vm.actionButton(c, "bar", actShell, sel, false)
		vm.actionButton(c, "bar", actProxy, sel, false)
		if len(sel) == 1 {
			vm.actionButton(c, "bar", actClone, sel, false)
			if ui.Button(c.Key("bar_edit"), "编辑").Disabled(vm.busy[sel[0].ID] != "").Clicked() {
				vm.openEdit(sel[0])
			}
		}
		vm.actionButton(c, "bar", actDelete, sel, false)
	})
}

func (vm *viewModel) buildInstanceTable(c *ui.Context) {
	t := c.Theme()
	if len(vm.visible) == 0 {
		ui.Column(c).Grow(1).Center().Gap(12).Children(func() {
			glyph(c, "device", 32).TextColor(t.TextMuted)
			msg := "还没有实例，点击右上角「新建实例」开始。"
			if len(vm.instances) > 0 {
				msg = "没有符合当前筛选的实例。"
			}
			ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted)
		})
		return
	}
	ui.Row(c).Height(34).Padding(0, 24).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		all := len(vm.visible) > 0
		for _, inst := range vm.visible {
			if !vm.selected.Has(inst.ID) {
				all = false
				break
			}
		}
		if ui.Checkbox(c.Key("select_all"), &all, "").Label("全选当前列表").Changed() {
			for _, inst := range vm.visible {
				if all {
					vm.selected.Add(inst.ID)
				} else {
					vm.selected.Remove(inst.ID)
				}
			}
		}
		ui.Text(c, "设备名称").Grow(1).FontSize(10).TextColor(t.TextMuted)
		quietMenu(c, "select_menu", "选择", func(m *ui.Menu) {
			if m.Item("仅选运行中").Chosen() {
				vm.selected.Clear()
				for _, inst := range vm.visible {
					if inst.State == service.Running {
						vm.selected.Add(inst.ID)
					}
				}
			}
			if m.Item("取消选择").Disabled(vm.selected.Len() == 0).Chosen() {
				vm.selected.Clear()
			}
		}).FontSize(10).Height(25).Padding(2, 6)
		ui.Text(c, "状态").Width(104).FontSize(10).TextColor(t.TextMuted)
		ui.Text(c, "资源 / 网络").Width(140).FontSize(10).TextColor(t.TextMuted)
		ui.Column(c).Width(14)
	})
	// The native list still owns Ctrl/Shift selection, focus, keyboard navigation,
	// and virtualization. Only its selected-row palette differs from buttons.
	listTheme := *t
	listTheme.Accent, listTheme.AccentText, listTheme.Radius = t.Selection, t.Text, 0
	// Light selected rows need an explicit inactive fill: the toolkit fallback
	// derives a much darker gray from our dark AccentText.
	listTheme.InactiveAccent = t.Surface.Mix(t.Text, 0.03)
	var list ui.Element
	ui.Themed(c, &listTheme).Grow(1).Background(ui.Transparent).Children(func() {
		list = ui.List(c, &vm.tableState, len(vm.visible), func(row int) {
			ui.Themed(c, t).Background(ui.Transparent).Children(func() {
				it := vm.visible[row]
				on := vm.selected.Has(it.ID)
				edge := ui.Transparent
				if on {
					edge = t.Accent
					if !c.WindowActive() {
						edge = t.TextMuted
					}
				}
				ui.Row(c).Height(78).Padding(14, 24, 14, 24).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					ui.Column(c).Width(3).Height(77).Background(edge).Margin(-14, 0, -14, -24)
					checked := on
					if ui.Checkbox(c.Key("check_"+it.ID), &checked, "").Label("选择 " + it.Name).Changed() {
						if checked {
							vm.selected.Add(it.ID)
							vm.lastRow = row
							vm.detailHidden = false
						} else {
							vm.selected.Remove(it.ID)
						}
					}
					ui.Row(c).Grow(1).Gap(12).Children(func() {
						bg := t.Surface
						color := t.TextMuted
						if on {
							bg = ui.Hex("#e3e7ff")
							color = t.Accent
							if !c.WindowActive() {
								bg = t.SurfacePressed.Mix(t.Surface, 0.6)
								color = t.TextMuted
							}
						}
						ui.Column(c).Width(32).Height(38).Radius(7).Background(bg).Center().Children(func() { glyph(c, "device", 19).TextColor(color) })
						ui.Column(c).Gap(6).Grow(1).Children(func() {
							ui.Text(c, it.Name).FontSize(13).FontWeight(600).SingleLine().TextColor(t.Text)
							ui.Text(c, shortImage(it.SystemImage)).FontSize(10).TextColor(t.TextMuted).SingleLine()
						})
					})
					ui.Column(c).Width(104).Gap(4).Children(func() {
						ui.Text(c, formatStateLabel(it.State)).FontSize(11).TextColor(stateColor(t, it.State)).SingleLine()
						if op := vm.busy[it.ID]; op != "" {
							ui.Text(c, op+"中").FontSize(10).TextColor(t.TextMuted).SingleLine()
						}
					})
					ui.Column(c).Width(140).Gap(6).Children(func() {
						ui.Textf(c, "%.1f GB / %d 核", float64(it.MemoryMB)/1024, it.Cores).FontSize(11).TextColor(t.Text).SingleLine()
						net := "直连网络"
						if it.Proxy != "" {
							net = "已设代理"
						}
						color := t.TextMuted
						if pendingRestart(it) {
							net = "待重启生效"
							color = t.Warning
						}
						ui.Text(c, net).Tooltip(proxyText(it.Proxy)).FontSize(10).TextColor(color).SingleLine()
					})
					glyph(c, "chevron", 14).TextColor(t.TextMuted)
				})
			})
		}).Grow(1).Padding(0).Gap(0)
	})
	if list.Changed() {
		vm.detailHidden = false
	}
}

func (vm *viewModel) buildDetail(c *ui.Context) {
	t := c.Theme()
	sel := vm.selectedInsts()
	ui.Column(c).Width(detailWidth).Background(ui.Hex("#fbfcff")).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		ui.Scroll(c).Grow(1).Padding(24).Gap(12).Children(func() {
			switch {
			case len(sel) == 0:
				ui.Text(c, "实例详情").FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, "在左侧列表中选择一个实例，这里会显示它的状态、配置和可用操作。").FontSize(12).TextColor(t.TextMuted)
			case len(sel) > 1:
				ui.Text(c, "实例详情").FontSize(12).TextColor(t.TextMuted)
				ui.Textf(c, "已选 %d 台实例", len(sel)).FontSize(18).Bold()
				ui.Text(c, "请使用列表上方的批量操作条。选择单个实例可查看详情。").FontSize(12).TextColor(t.TextMuted)
			default:
				vm.buildDetailBody(c, sel[0])
			}
		})
	})
}

func (vm *viewModel) buildDetailBody(c *ui.Context, inst service.Instance) {
	t := c.Theme()
	one := []service.Instance{inst}

	ui.Row(c).Children(func() {
		ui.Text(c, "实例详情").FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		if quietControl(c, "close_detail", "", "close").Label("收起实例详情").Width(24).Height(24).Padding(3).Clicked() {
			vm.detailHidden = true
		}
	})
	ui.Text(c, inst.Name).FontSize(23).FontWeight(600)
	ui.Text(c, formatStateLabel(inst.State)).FontSize(12).TextColor(stateColor(t, inst.State))
	ui.Text(c, shortImage(inst.SystemImage)).FontSize(10).TextColor(t.TextMuted)
	if op := vm.busy[inst.ID]; op != "" {
		ui.Row(c).Gap(6).Children(func() {
			ui.Spinner(c)
			ui.Textf(c, "正在执行「%s」", op).FontSize(12).TextColor(t.TextMuted)
		})
	}

	ui.Row(c).Gap(6).Wrap().Margin(6, 0, 0, 0).Children(func() {
		if inst.State == service.Running || inst.State == service.Starting || (inst.State == service.Failed && inst.Port > 0) {
			vm.actionButton(c, "det", actScreen, one, true).Grow(1)
			vm.actionButton(c, "det", actStop, one, false)
		} else {
			vm.actionButton(c, "det", actStart, one, true).Grow(1)
		}
	})
	quietMenu(c, "detail_more", "更多操作", func(m *ui.Menu) {
		if m.Item("编辑").Disabled(vm.busy[inst.ID] != "").Chosen() {
			vm.openEdit(inst)
		}
		for _, a := range []action{actInstallAPK, actShell, actClone, actDelete} {
			p := planAction(a, one, vm.busy)
			if m.Item(a.label()).Disabled(p.disabledHint() != "").Chosen() {
				vm.invoke(a, p)
			}
		}
	}).FontSize(11).Height(28).AlignSelf(ui.Start)

	ui.Divider(c)
	ui.Text(c, "设备配置").FontSize(12).Bold().Margin(8, 0, 4, 0)
	kv(c, "系统镜像", shortImage(inst.SystemImage))
	kv(c, "内存 / CPU", fmt.Sprintf("%.1f GB / %d 核", float64(inst.MemoryMB)/1024, inst.Cores))
	mode := "独立窗口"
	if inst.Headless {
		mode = "后台运行（无窗口）"
	}
	kv(c, "显示模式", mode)
	if inst.State == service.Running || inst.State == service.Starting {
		ui.Text(c, "以上为已保存配置；内存、CPU 和运行模式的修改在下次启动生效。").FontSize(11).TextColor(t.TextMuted)
	}
	serial := "—"
	if inst.Port > 0 {
		serial = fmt.Sprintf("emulator-%d", inst.Port)
	}
	kv(c, "ADB 序列号", serial)
	kv(c, "实例 ID", inst.ID)

	ui.Divider(c)
	ui.Row(c).Children(func() {
		ui.Text(c, "网络代理").FontSize(12).Bold()
		ui.Spacer(c)
		vm.actionButton(c, "det", actProxy, one, false)
	})
	kv(c, "已保存", proxyText(inst.Proxy))
	if inst.State == service.Running || inst.State == service.Starting {
		kv(c, "当前生效", proxyText(inst.AppliedProxy))
		if pendingRestart(inst) {
			ui.Text(c, "待重启生效：保存的代理与正在运行的实例不同，重启该实例后才会应用。").
				FontSize(12).TextColor(t.Warning)
		}
	}

	ui.Divider(c)
	ui.Text(c, "最近活动").FontSize(12).Bold().Margin(8, 0, 4, 0)
	shown := 0
	for i := len(vm.tasks) - 1; i >= 0 && shown < 3; i-- {
		tk := vm.tasks[i]
		for _, item := range tk.items {
			if item.id != inst.ID {
				continue
			}
			color := t.TextMuted
			if item.status == itemFailed {
				color = t.Danger
			} else if item.status == itemOK {
				color = t.Success
			}
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, "●").FontSize(8).TextColor(color)
				ui.Column(c).Gap(5).Children(func() {
					ui.Text(c, tk.title+" · "+activityStatus(item.status)).FontSize(11).TextColor(color)
					ui.Text(c, tk.started.Format("01-02 15:04")).FontSize(10).TextColor(t.TextMuted)
				})
			})
			shown++
			break
		}
	}
	if shown == 0 {
		ui.Text(c, "暂无操作记录").FontSize(11).TextColor(t.TextMuted)
	}

	if inst.LastError != "" {
		ui.Divider(c)
		ui.Text(c, "最近错误").FontSize(12).Bold().TextColor(t.Danger)
		ui.Text(c, inst.LastError).FontSize(11).TextColor(t.TextMuted)
	}
}

func kv(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Width(72)
		ui.Text(c, value).FontSize(11).Grow(1).TextAlign(ui.End)
	})
}

// ---------------------------------------------------------------- task dock

func (vm *viewModel) buildTaskDock(c *ui.Context) {
	t := c.Theme()
	w, _ := c.Size()
	compact := w < 900
	ui.Divider(c)
	if vm.tasksOpen {
		vm.buildTaskPanel(c)
		ui.Divider(c)
	}
	ui.Row(c).Height(46).Padding(6, 24).Gap(10).Background(t.Background).Children(func() {
		if vm.dlActive {
			stageLabel := formatDownloadStage(vm.dlProgress.Stage)
			ui.Spinner(c)
			ui.Textf(c, "[%s] %s", stageLabel, vm.dlProgress.Name).FontSize(12).Bold().SingleLine().MaxWidth(w / 2)
			if !compact {
				ui.Progress(c, vm.dlProgress.Percent/100.0).Width(140)
			}
			ui.Textf(c, "%.1f%%", vm.dlProgress.Percent).FontSize(12).TextColor(t.Accent)
		} else {
			lt := vm.latestTask()
			if lt == nil {
				glyph(c, "download", 16).TextColor(t.TextMuted)
				ui.Text(c, "暂无进行中的任务").FontSize(11).TextColor(t.TextMuted)
			} else {
				_, _, failed := lt.counts()
				ui.Text(c, lt.title).FontSize(12).Bold().SingleLine().Shrink(0)
				summary := lt.summary()
				if compact {
					done, _, _ := lt.counts()
					summary = fmt.Sprintf("完成 %d/%d · 失败 %d", done, len(lt.items), failed)
				}
				ui.Text(c, summary).FontSize(11).TextColor(t.TextMuted).SingleLine()
				if !compact {
					ui.Progress(c, lt.progress()).Width(120)
				}
				if !lt.finished {
					ui.Spinner(c)
				} else if failed > 0 {
					if lt.retryable {
						if ui.Button(c.Key("dock_retry"), "重试失败项").Clicked() {
							vm.retryFailed(lt)
						}
					}
					if !compact && ui.Button(c.Key("dock_why"), "查看失败原因").Clicked() {
						vm.tasksOpen, vm.panelTab = true, 0
					}
				}
			}
		}
		ui.Spacer(c)
		label := "任务与日志"
		if vm.tasksOpen {
			label = "收起 ▼"
		}
		if quietControl(c, "dock_toggle", label, "").Clicked() {
			vm.tasksOpen = !vm.tasksOpen
		}
	})
}

func (vm *viewModel) buildTaskPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Height(taskPanelH).Background(t.Surface).Children(func() {
		ui.Row(c).Padding(4, 12).Children(func() {
			ui.Tabs(c, &vm.panelTab, "任务", "运行日志")
			ui.Spacer(c)
			if vm.panelTab == 1 {
				if ui.Button(c.Key("clear_log"), "清空日志").Disabled(len(vm.logs) == 0).Clicked() {
					vm.logs = nil
				}
			}
		})
		ui.Divider(c)
		if vm.panelTab == 0 {
			vm.buildTaskList(c)
			return
		}
		ui.List(c, &vm.logState, len(vm.logs), func(i int) {
			if i >= 0 && i < len(vm.logs) {
				ui.Text(c, vm.logs[i]).FontSize(12).Padding(3, 16)
			}
		}).Grow(1)
	})
}

func (vm *viewModel) buildTaskList(c *ui.Context) {
	t := c.Theme()
	if len(vm.tasks) == 0 {
		ui.Text(c, "还没有执行过批量任务。").FontSize(12).TextColor(t.TextMuted).Padding(12, 16)
		return
	}
	ui.Scroll(c).Grow(1).Padding(8, 16).Gap(10).Children(func() {
		shown := 0
		for i := len(vm.tasks) - 1; i >= 0 && shown < 8; i-- {
			tk := vm.tasks[i]
			shown++
			_, _, failed := tk.counts()
			ui.Column(c).Gap(3).Children(func() {
				ui.Row(c).Gap(10).Children(func() {
					ui.Text(c, tk.title).Bold().FontSize(12)
					ui.Text(c, tk.summary()).FontSize(12).TextColor(t.TextMuted)
					if !tk.finished {
						ui.Text(c, "进行中").FontSize(12).TextColor(t.Accent)
					}
					if tk.finished && failed > 0 && tk.retryable {
						if ui.Button(c.Key("retry_"+strconv.Itoa(tk.id)), "重试失败项").Clicked() {
							vm.retryFailed(tk)
						}
					}
				})
				showAll := !tk.finished || len(tk.items) <= 6
				for _, it := range tk.items {
					if !showAll && it.status != itemFailed {
						continue
					}
					line := fmt.Sprintf("%s %s", statusMark(it.status), it.name)
					col := t.TextMuted
					switch it.status {
					case itemFailed:
						line += "：" + it.err
						col = t.Danger
					case itemOK:
						col = t.Success
					}
					ui.Text(c, line).FontSize(11).TextColor(col)
				}
			})
		}
	})
}

// ---------------------------------------------------------------- modals

func (vm *viewModel) buildCreateModal(c *ui.Context) {
	ui.Modal(c, &vm.showCreate, func() {
		t := c.Theme()
		modalContent(c, 560).Gap(12).Children(func() {
			ui.Text(c, "新建干净安卓实例").FontSize(16).Bold()
			ui.Text(c, "纯 Go 零 Java 引擎直接生成独立 config.ini，首次启动自动初始化独立 userdata 与加密密钥，彻底隔离设备指纹与 GSF ID。").
				FontSize(12).TextColor(t.TextMuted)

			options := imageChoices(vm.env, vm.catalog...)
			for i, pkg := range options {
				options[i] = imageChoiceLabel(vm.env, pkg)
			}
			imgInstalled := imageInstalled(vm.env, vm.createImage)

			ui.Form(c, func() {
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
				ui.Field(c, "内存 (MB)", func() {
					ui.NumberInput(c, &vm.createMemMB, 1024, 16384, 512)
				})
				ui.Field(c, "CPU 核数", func() {
					ui.NumberInput(c, &vm.createCores, 1, 8, 1)
				})
				ui.Field(c, "HTTP 代理", func() {
					ui.TextInput(c, &vm.createProxy).Placeholder("可选，如 http://127.0.0.1:7890")
				})
				ui.Field(c, "运行模式", func() {
					ui.Checkbox(c, &vm.createHeadless, "默认无头运行 (-no-window -no-snapshot)，需要看画面时用 scrcpy 投屏")
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
							vm.showEnv = true
							vm.envTab = 1
							vm.refreshEnv()
						}
					})
				}
			}

			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_create"), "取消").Clicked() {
					vm.showCreate = false
				}
				if ui.PrimaryButton(c.Key("confirm_create"), "立即创建").Clicked() {
					res := "720p"
					if strings.HasPrefix(vm.createRes, "1080p") {
						res = "1080p"
					}
					spec := service.CreateSpec{
						NamePrefix:  vm.createName,
						Count:       int(vm.createCount),
						SystemImage: vm.createImage,
						MemoryMB:    int(vm.createMemMB),
						Cores:       int(vm.createCores),
						Resolution:  res,
						Proxy:       vm.createProxy,
						Headless:    vm.createHeadless,
					}
					vm.showCreate = false
					name := fmt.Sprintf("%s ×%d", strings.TrimSpace(spec.NamePrefix), spec.Count)
					vm.startTask("创建实例", actCreate, []target{{name: name}}, false, func(ctx context.Context, _ target) error {
						_, err := vm.svc.Create(ctx, spec)
						return err
					}, nil)
				}
			})
		})
	})
}

func (vm *viewModel) buildCloneModal(c *ui.Context) {
	ui.Modal(c, &vm.showClone, func() {
		src, _ := vm.findInstance(vm.cloneSrcID)
		modalContent(c, 440).Gap(12).Children(func() {
			ui.Text(c, "克隆实例（可选母版复制）").FontSize(16).Bold()
			ui.Text(c, "注意：母版若已开机登录过，克隆体会共享 ANDROID_ID 与 GSF ID。建议仅对未登录账号的基础母版使用。").
				FontSize(12).TextColor(c.Theme().TextMuted)

			ui.Form(c, func() {
				ui.Field(c, "源实例", func() {
					ui.Text(c, src.Name+"  ("+vm.cloneSrcID+")").Bold()
				})
				ui.Field(c, "克隆数量", func() {
					ui.NumberInput(c, &vm.cloneCount, 1, 20, 1)
				})
			})

			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_clone"), "取消").Clicked() {
					vm.showClone = false
				}
				if ui.PrimaryButton(c.Key("confirm_clone"), "开始克隆").Clicked() {
					srcID, cnt := vm.cloneSrcID, int(vm.cloneCount)
					vm.showClone = false
					if inst, ok := vm.findInstance(srcID); ok {
						vm.startTask("克隆", actClone, targetsOf([]service.Instance{inst}), true, func(ctx context.Context, t target) error {
							_, err := vm.svc.Clone(ctx, t.id, cnt)
							return err
						}, nil)
					}
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
			ui.Textf(c, "设置 HTTP 代理（共 %d 个实例）", len(targets)).FontSize(16).Bold()
			ui.Text(c, "模拟器启动时会携带 -http-proxy 与 -dns-server 参数。").FontSize(12).TextColor(t.TextMuted)

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
					ui.TextInput(c, &vm.proxyValue).Placeholder("http://127.0.0.1:7890")
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
						// Deleted instances leave the selection; failed ones stay selected for a retry.
						for _, it := range tk.items {
							if it.status == itemOK {
								vm.selected.Remove(it.id)
							}
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
				ui.Text(c, "2. VirDeck 仅作为本地环境引导与管理工具，按需从 dl.google.com 拉取官方原版组件并校验 SHA-1 摘要。").FontSize(12)
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

func (vm *viewModel) buildEnvModal(c *ui.Context) {
	_, h := c.Size()
	ui.Modal(c, &vm.showEnv, func() {
		t := c.Theme()
		checks := buildEnvChecks(vm.env)
		modalContent(c, 680).Gap(10).Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, "镜像与环境管理").FontSize(16).Bold()
				ui.Spacer(c)
				ui.Text(c, envHealthText(vm.envLoaded, checks)).FontSize(12).TextColor(vm.healthColor(c))
			})
			ui.Tabs(c, &vm.envTab, "运行环境", "系统镜像", "空间清理")
			ui.Divider(c)

			if vm.dlActive {
				ui.Row(c).Padding(6, 10).Gap(8).Radius(6).Background(t.Selection).Children(func() {
					ui.Spinner(c)
					ui.Textf(c, "正在处理 %s (%s)", vm.dlProgress.Name, formatDownloadStage(vm.dlProgress.Stage)).FontSize(12).Bold()
					ui.Spacer(c)
					ui.Progress(c, vm.dlProgress.Percent/100.0).Width(160)
					ui.Textf(c, "%.1f%%", vm.dlProgress.Percent).FontSize(12).TextColor(t.Accent)
				})
			}

			ui.Scroll(c).Height(min(360, h-260)).Gap(10).Children(func() {
				switch vm.envTab {
				case 0:
					vm.buildEnvChecksList(c, checks)
				case 1:
					vm.buildImageList(c)
				case 2:
					vm.buildCleanupTab(c)
				}
			})

			ui.Row(c).Gap(8).Children(func() {
				if vm.envTab == 0 {
					missingTools := vm.env.EmulatorPath == "" || vm.env.ADBPath == "" || vm.env.ScrcpyPath == ""
					if ui.Button(c.Key("dl_all_tools"), "一键下载补齐缺失工具").Disabled(!missingTools || vm.dlActive).Clicked() {
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
				}
				ui.Spacer(c)
				if ui.Button(c.Key("recheck_env"), "重新检测").Clicked() {
					vm.refreshEnv()
				}
				if ui.PrimaryButton(c.Key("close_env"), "关闭").Clicked() {
					vm.showEnv = false
				}
			})
		})
	})
}

func (vm *viewModel) buildEnvChecksList(c *ui.Context, checks []envCheck) {
	t := c.Theme()
	if vm.env.PathWarning != "" {
		ui.Row(c).Padding(8, 10).Radius(6).Background(t.Selection).Children(func() {
			ui.Text(c, "! "+vm.env.PathWarning).FontSize(12).TextColor(t.Danger)
		})
	}
	for _, ck := range checks {
		ck := ck
		mark, col := "✓", t.Success
		if !ck.ok {
			mark, col = "!", t.Warning
			if ck.blocking {
				mark, col = "✗", t.Danger
			}
		}
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, mark).Bold().TextColor(col).Width(16)
				ui.Text(c, ck.name).Bold().FontSize(12).Width(130)
				detail := ck.detail
				if ck.ok && strings.ContainsAny(detail, `\/`) {
					detail = filepath.Clean(detail)
				}
				ui.Text(c, detail).FontSize(12).TextColor(t.TextMuted).Grow(1)
				if ck.toolKey != "" {
					dlLabel := "一键下载"
					if ck.ok {
						dlLabel = "重新下载"
					}
					if ui.Button(c.Key("dl_tool_"+ck.toolKey), dlLabel).Disabled(vm.dlActive).Clicked() {
						toolKey := ck.toolKey
						vm.requireLicense(func() {
							vm.startTask("下载组件 "+toolKey, actCreate, []target{{name: toolKey}}, false, func(ctx context.Context, _ target) error {
								err := vm.svc.EnsureBaseTool(ctx, toolKey)
								vm.refreshEnv()
								return err
							}, func(_ *task) {
								vm.dlActive = false
							})
						})
					}
					if ui.Button(c.Key("imp_tool_"+ck.toolKey), "导入 .zip").Disabled(vm.dlActive).Clicked() {
						toolKey := ck.toolKey
						go func() {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
								Parent:  vm.win,
								Title:   fmt.Sprintf("选择 %s 的离线 .zip 压缩包", toolKey),
								Filters: []mygo.FileFilter{{Name: "Zip Archive (*.zip)", Extensions: []string{"zip"}}},
							})
							if err != nil || len(paths) == 0 {
								return
							}
							zipPath := paths[0]
							vm.post(func() {
								vm.startTask("导入组件 "+toolKey, actCreate, []target{{name: filepath.Base(zipPath)}}, false, func(ctx context.Context, _ target) error {
									err := vm.svc.ImportBaseToolZip(ctx, toolKey, zipPath)
									vm.refreshEnv()
									return err
								}, func(_ *task) {
									vm.dlActive = false
								})
							})
						}()
					}
				}
			})
			if !ck.ok {
				ui.Text(c, "影响："+ck.impact).FontSize(12).TextColor(col).MarginStart(24)
				ui.Text(c, "处理："+ck.fix).FontSize(12).MarginStart(24)
			}
		})
	}
	if vm.env.DataDir != "" || vm.env.AVDHome != "" {
		ui.Divider(c)
		if vm.env.DataDir != "" {
			vm.buildDirectoryRow(c, "数据目录", vm.env.DataDir)
		}
		if vm.env.AVDHome != "" {
			vm.buildDirectoryRow(c, "AVD 目录", vm.env.AVDHome)
		}
	}
}

func (vm *viewModel) buildImageList(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, "点击 ☆ 收藏并置顶，可添加备注；支持同步官方列表及导入本地 .zip。").
			FontSize(11).TextColor(t.TextMuted).Grow(1)
		if ui.Button(c.Key("sync_catalog"), "同步官方列表").Disabled(vm.dlActive).Clicked() {
			vm.startTask("同步官方镜像列表", actCreate, []target{{name: "Google sys-img2-3.xml"}}, false, func(ctx context.Context, _ target) error {
				_, err := vm.svc.SyncCatalog(ctx)
				vm.refreshEnv()
				return err
			}, nil)
		}
		toggleLabel := "+ 自定义镜像"
		if vm.showCustomImg {
			toggleLabel = "收起自定义表单"
		}
		if ui.Button(c.Key("toggle_custom_img"), toggleLabel).Clicked() {
			vm.showCustomImg = !vm.showCustomImg
		}
	})

	if vm.showCustomImg {
		ui.Column(c).Padding(10, 12).Radius(6).Background(t.Surface).Gap(8).Children(func() {
			ui.Text(c, "添加 / 编辑自定义系统镜像").FontSize(12).Bold()
			ui.Form(c, func() {
				ui.Field(c, "镜像包 ID", func() {
					ui.TextInput(c, &vm.customImgID).Placeholder("例如 system-images;android-34;google_apis_playstore;x86_64")
				})
				ui.Field(c, "显示名称", func() {
					ui.TextInput(c, &vm.customImgName).Placeholder("例如 Android 14 企业定制镜像")
				})
				ui.Field(c, "用途说明", func() {
					ui.TextInput(c, &vm.customImgDesc).Placeholder("可选描述")
				})
				ui.Field(c, "下载 URL", func() {
					ui.TextInput(c, &vm.customImgURL).Placeholder("https://.../x86_64-34_r14.zip（若仅离线导入可留空）")
				})
				ui.Field(c, "SHA-1 校验", func() {
					ui.TextInput(c, &vm.customImgSHA1).Placeholder("可选 40 位十六进制 SHA-1")
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_custom_img"), "取消").Clicked() {
					vm.showCustomImg = false
				}
				if ui.PrimaryButton(c.Key("save_custom_img"), "保存镜像条目").Disabled(strings.TrimSpace(vm.customImgID) == "").Clicked() {
					item := service.ImageItem{
						ID:          strings.TrimSpace(vm.customImgID),
						Name:        strings.TrimSpace(vm.customImgName),
						Description: strings.TrimSpace(vm.customImgDesc),
						URL:         strings.TrimSpace(vm.customImgURL),
						SHA1:        strings.TrimSpace(vm.customImgSHA1),
						Source:      catalog.SourceCustom,
					}
					if err := vm.svc.SaveCustomImage(context.Background(), item); err != nil {
						vm.appendLog(fmt.Sprintf("[%s] 保存自定义镜像失败: %v", time.Now().Format("15:04:05"), err))
					} else {
						vm.showCustomImg = false
						vm.customImgID, vm.customImgName, vm.customImgDesc, vm.customImgURL, vm.customImgSHA1 = "", "", "", "", ""
						vm.refreshEnv()
					}
				}
			})
		})
	}

	items := vm.catalog
	if len(items) == 0 {
		// Fallback to built-in rows if catalog was not loaded yet.
		for _, r := range imageRows(vm.env) {
			src := catalog.SourceLocal
			if r.recommended {
				src = catalog.SourceRecommended
			}
			items = append(items, service.ImageItem{
				ID:        r.pkg,
				Name:      shortImage(r.pkg),
				Source:    src,
				Installed: r.installed,
			})
		}
	}

	for _, it := range items {
		it := it
		keySuffix := it.ID
		badge := "官方"
		badgeCol := t.TextMuted
		switch it.Source {
		case catalog.SourceRecommended:
			badge = "官方"
		case catalog.SourceCustom:
			badge = "自定义"
			badgeCol = t.Warning
		case catalog.SourceLocal:
			badge = "本地"
			badgeCol = t.Success
		}

		rowBackground := t.Surface
		favoriteColor := ui.Hex("#E9A323")
		if it.Favorite {
			rowBackground = t.Surface.Mix(favoriteColor, 0.065)
		}
		ui.Column(c).Padding(6, 8).Radius(6).Background(rowBackground).Gap(4).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				star, starColor := "☆", t.TextMuted
				if it.Favorite {
					star, starColor = "★", favoriteColor
				}
				if ui.Button(c.Key("favorite_"+it.ID), "").Label(star).Children(func() {
					ui.Text(c, star).TextColor(starColor)
				}).Clicked() {
					if vm.saveImageFavorite(it.ID, !it.Favorite, "") {
						if it.Favorite {
							vm.favoriteID = ""
						} else {
							vm.favoriteID, vm.favoriteNote = it.ID, ""
						}
					}
				}
				ui.Text(c, badge).FontSize(11).Bold().TextColor(badgeCol).Width(36)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, it.Name).FontSize(12).Bold()
					sub := it.ID
					if it.Description != "" {
						sub = it.Description + "  ·  " + it.ID
					}
					ui.Text(c, sub).FontSize(11).TextColor(t.TextMuted)
					if it.Favorite && it.Note != "" {
						ui.Text(c, "备注："+it.Note).FontSize(11).TextColor(t.Accent)
					}
				})
				if it.Favorite && ui.Button(c.Key("note_"+it.ID), "备注").Clicked() {
					vm.favoriteID, vm.favoriteNote, vm.favoriteError = it.ID, it.Note, ""
				}
				if it.Installed {
					ui.Text(c, "✓ 已安装").FontSize(11).Bold().TextColor(t.Success)
					if ui.Button(c.Key("del_img_"+keySuffix), "删除镜像").Disabled(vm.dlActive).Clicked() {
						imgID := it.ID
						vm.startTask("删除系统镜像", actDelete, []target{{name: imgID}}, false, func(ctx context.Context, _ target) error {
							err := vm.svc.DeleteSystemImage(ctx, imgID)
							vm.refreshEnv()
							return err
						}, nil)
					}
				} else {
					ui.Text(c, "未安装").FontSize(11).TextColor(t.TextMuted)
					if ui.Button(c.Key("dl_img_"+keySuffix), "下载安装").Disabled(vm.dlActive).Clicked() {
						imgID, imgName := it.ID, it.Name
						vm.requireLicense(func() {
							vm.startTask("下载镜像 "+imgName, actCreate, []target{{name: imgID}}, false, func(ctx context.Context, _ target) error {
								err := vm.svc.DownloadSystemImage(ctx, imgID)
								vm.refreshEnv()
								return err
							}, func(_ *task) {
								vm.dlActive = false
							})
						})
					}
					if ui.Button(c.Key("imp_img_"+keySuffix), "导入 .zip").Disabled(vm.dlActive).Clicked() {
						imgID := it.ID
						go func() {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
								Parent:  vm.win,
								Title:   fmt.Sprintf("选择系统镜像 %s 的本地 .zip 包", imgID),
								Filters: []mygo.FileFilter{{Name: "Zip Archive (*.zip)", Extensions: []string{"zip"}}},
							})
							if err != nil || len(paths) == 0 {
								return
							}
							zipPath := paths[0]
							vm.post(func() {
								vm.startTask("导入镜像 "+imgID, actCreate, []target{{name: filepath.Base(zipPath)}}, false, func(ctx context.Context, _ target) error {
									err := vm.svc.ImportSystemImageZip(ctx, imgID, zipPath)
									vm.refreshEnv()
									return err
								}, func(_ *task) {
									vm.dlActive = false
								})
							})
						}()
					}
				}
				if it.Source == catalog.SourceCustom {
					if ui.Button(c.Key("edit_cust_"+keySuffix), "编辑").Clicked() {
						vm.customImgID = it.ID
						vm.customImgName = it.Name
						vm.customImgDesc = it.Description
						vm.customImgURL = it.URL
						vm.customImgSHA1 = it.SHA1
						vm.showCustomImg = true
					}
					if ui.Button(c.Key("rm_cust_"+keySuffix), "移除条目").Clicked() {
						_ = vm.svc.RemoveCustomImage(context.Background(), it.ID)
						vm.refreshEnv()
					}
				}
			})
			if vm.favoriteID == it.ID {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "收藏备注").FontSize(11)
					ui.TextInput(c.Key("favorite_note_"+it.ID), &vm.favoriteNote).Placeholder("可选备注").Grow(1)
					if ui.Button(c.Key("save_note_"+it.ID), "保存备注").Clicked() {
						if vm.saveImageFavorite(it.ID, true, vm.favoriteNote) {
							vm.favoriteID = ""
						}
					}
					if ui.Button(c.Key("cancel_note_"+it.ID), "取消").Clicked() {
						vm.favoriteID = ""
					}
				})
				if vm.favoriteError != "" {
					ui.Text(c, vm.favoriteError).FontSize(11).TextColor(t.Danger)
				}
			}
		})
	}
}

func (vm *viewModel) saveImageFavorite(id string, selected bool, note string) bool {
	if err := vm.svc.SetImageFavorite(context.Background(), id, selected, note); err != nil {
		vm.favoriteError = "保存收藏失败：" + err.Error()
		vm.appendLog(vm.favoriteError)
		return false
	}
	vm.favoriteError = ""
	vm.catalog, _ = vm.svc.ListCatalog(context.Background())
	return true
}

func (vm *viewModel) buildDirectoryRow(c *ui.Context, label, path string) {
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted).Width(72)
		ui.Text(c, filepath.Clean(path)).FontSize(11).Grow(1)
		if ui.Button(c.Key("open_dir_"+label), "打开目录").Clicked() {
			if err := mygo.Shell.OpenPath(filepath.Clean(path)); err != nil {
				vm.appendLog(fmt.Sprintf("打开%s失败: %v", label, err))
			}
		}
	})
}

func (vm *viewModel) buildSettingsTab(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(12).Children(func() {
		ui.Text(c, "管理组件下载使用的网络，以及同时运行的实例数量。").
			FontSize(12).TextColor(t.TextMuted)
		ui.Form(c, func() {
			ui.Field(c, "下载代理", func() {
				ui.TextInput(c, &vm.settingsProxy).Placeholder("可选，如 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080")
			}).Description("用于从 dl.google.com 或 GitHub 下载 SDK 工具与系统镜像；留空则使用系统默认网络")
			ui.Field(c, "最大运行实例数", func() {
				ui.NumberInput(c, &vm.settingsConcurrent, 1, 16, 1)
			}).Description("限制同时运行的实例数量，避免占用过多内存。")
		})
		if vm.settingsSavedHint != "" {
			ui.Text(c, vm.settingsSavedHint).FontSize(12).TextColor(t.Success)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c.Key("save_settings"), "保存全局设置").Clicked() {
				st := vm.svc.GetSettings()
				st.DownloadProxy = strings.TrimSpace(vm.settingsProxy)
				st.MaxConcurrent = int(vm.settingsConcurrent)
				if err := vm.svc.UpdateSettings(st); err != nil {
					vm.settingsSavedHint = "保存失败: " + err.Error()
				} else {
					vm.settingsSavedHint = "✓ 已保存并立即生效"
					vm.appendLog(fmt.Sprintf("[%s] 已更新全局设置: 并发=%d, 下载代理=%s",
						time.Now().Format("15:04:05"), st.MaxConcurrent, proxyText(st.DownloadProxy)))
				}
			}
		})
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

func defaultResolutionLabel() string {
	for _, label := range resolutionOptions {
		if strings.HasPrefix(label, config.Values.Create.Resolution+" ") {
			return label
		}
	}
	return resolutionOptions[0]
}
