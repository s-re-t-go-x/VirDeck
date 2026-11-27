package mygonative

import (
	"runtime"

	"github.com/egoist/mygo/ui"
	"virdeck/assets"
)

var brandMark = ui.MustParseSVG(assets.IconSVG)

// 「冷雾灰 · 深靛蓝」 (modern-blue) theme for the workbench and all dialogs.
var workbenchTheme = func() *ui.Theme {
	t := ui.LightTheme()
	t.Background = ui.Hex("#ffffff")
	t.Surface = ui.Hex("#f1f4f9")
	t.SurfaceHover = ui.Hex("#e7edf5")
	t.SurfacePressed = ui.Hex("#dde5f0")
	t.Border = ui.Hex("#e2e8f0")
	t.Text = ui.Hex("#1e293b")
	t.TextMuted = ui.Hex("#64748b")
	t.Accent = ui.Hex("#4553cf")
	t.AccentHover = ui.Hex("#3946be")
	t.AccentPressed = ui.Hex("#2f3bab")
	t.Selection = ui.Hex("#eff1fb")
	t.Focus = ui.Hex("#8e97e6")
	t.Success = ui.Hex("#15803d")
	t.Warning = ui.Hex("#b45309")
	t.Danger = ui.Hex("#be123c")
	t.Font, t.FontSize = platformFont(runtime.GOOS), 13
	t.Radius = 7
	return t
}()

// Extended surface, soft-pill, and overlay tokens for the native workbench.
var workbenchPalette = struct {
	ShellBg          ui.Color
	CanvasBg         ui.Color
	SubSurfaceBg     ui.Color
	InteractiveBg    ui.Color
	Hairline         ui.Color
	StrongHairline   ui.Color
	AccentSoftBg     ui.Color
	AccentSoftBorder ui.Color
	SuccessSoftBg    ui.Color
	WarningSoftBg    ui.Color
	WarningSoftEdge  ui.Color
	DangerSoftBg     ui.Color
	DangerSoftBorder ui.Color
	StoppedSoftBg    ui.Color
	FavCardBg        ui.Color
	FavCardBorder    ui.Color
	FavStar          ui.Color
	ToastBg          ui.Color
}{
	ShellBg:          ui.Hex("#f1f4f9"),
	CanvasBg:         ui.Hex("#ffffff"),
	SubSurfaceBg:     ui.Hex("#f8fafc"),
	InteractiveBg:    ui.Hex("#f1f5f9"),
	Hairline:         ui.Hex("#e2e8f0"),
	StrongHairline:   ui.Hex("#cbd5e1"),
	AccentSoftBg:     ui.Hex("#eef0fc"),
	AccentSoftBorder: ui.Hex("#c7ccf2"),
	SuccessSoftBg:    ui.Hex("#e8f7ee"),
	WarningSoftBg:    ui.Hex("#fdf4e3"),
	WarningSoftEdge:  ui.Hex("#f3d9a8"),
	DangerSoftBg:     ui.Hex("#fcebef"),
	DangerSoftBorder: ui.Hex("#f7c4cf"),
	StoppedSoftBg:    ui.Hex("#f1f5f9"),
	FavCardBg:        ui.Hex("#fef9eb"),
	FavCardBorder:    ui.Hex("#f5deb0"),
	FavStar:          ui.Hex("#d97706"),
	ToastBg:          ui.Hex("#1e293b"),
}

var workbenchIcons = func() map[string]*ui.SVG {
	paths := map[string]string{
		"device":   "M7 2h10v20H7z M11 18h2",
		"layers":   "m12 3 9 5-9 5-9-5z M3 12l9 5 9-5 M3 16l9 5 9-5",
		"settings": "M4 7h16 M4 17h16 M9 4v6 M16 14v6",
		"help":     "M9 8a3 3 0 1 1 5 2c-2 1-2 2-2 3 M12 17h.01 M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0",
		"grid":     "M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z",
		"screen":   "M3 4h18v13H3z M8 21h8 M12 17v4",
		"play":     "m8 5 11 7-11 7z",
		"stop":     "M6 6h12v12H6z",
		"plus":     "M12 5v14 M5 12h14",
		"search":   "m16 16 5 5 M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0",
		"close":    "m6 6 12 12 M18 6 6 18",
		"chevron":  "m9 5 7 7-7 7",
		"download": "M12 3v12 m-5-5 5 5 5-5 M4 17v4h16v-4",
		"copy":     "M9 9h12v12H9z M15 9V3H3v12h6",
		"terminal": "m5 7 5 5-5 5 M13 17h6",
		"trash":    "M3 6h18 M9 6V3h6v3 M6 6l1 15h10l1-15 M10 10v7 M14 10v7",
		"network":  "M12 3v7 M4 21v-7h16v7 M12 14v7 M8 3h8v5H8z",
		"volume":   "M11 5 6 9H2v6h4l5 4V5z M15.5 8.5a5 5 0 0 1 0 7 M19 5a10 10 0 0 1 0 14",
		"mute":     "M11 5 6 9H2v6h4l5 4V5z M22 9l-6 6 M16 9l6 6",
		"folder":   "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
		"logs":     "M4 6h16 M4 12h16 M4 18h10",
		"check":    "M20 6 9 17l-5-5",
		"alert":    "M12 9v4 M12 17h.01 M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h16.9a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z",
		"package":  "m16.5 9.4-9-5.19 M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z M3.3 7 12 12l8.7-5 M12 22V12",
		"menu":     "M4 7h16 M4 12h16 M4 17h16",
	}
	icons := make(map[string]*ui.SVG, len(paths))
	for name, path := range paths {
		icons[name] = ui.MustParseSVG([]byte(`<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="` + path + `"/></svg>`))
	}
	return icons
}()

func glyph(c *ui.Context, name string, size float32) ui.Element {
	return ui.Icon(c, workbenchIcons[name]).Width(size).Height(size).Shrink(0)
}

func control(c *ui.Context, key, label, icon string, primary bool) ui.Element {
	var b ui.Element
	if primary {
		b = ui.PrimaryButton(c.Key(key), "")
	} else {
		b = ui.Button(c.Key(key), "")
	}
	b = b.Label(label).Height(34).Padding(8, 12).Gap(7).FontSize(12)
	return b.Children(func() {
		if icon != "" {
			glyph(c, icon, 15)
		}
		if label != "" {
			ui.Text(c, label).SingleLine()
		}
	})
}

func quietControl(c *ui.Context, key, label, icon string) ui.Element {
	// Give the native button a transparent resting face from the start. Setting
	// Background afterwards disables MyGo's own hover/pressed styling.
	theme := c.Theme()
	quiet := *theme
	quiet.Surface, quiet.Border = ui.Transparent, ui.Transparent
	c.SetTheme(&quiet)
	b := control(c, key, label, icon, false)
	c.SetTheme(theme)
	return b.TextColor(theme.TextMuted)
}

func actionIcon(a action) string {
	switch a {
	case actStart:
		return "play"
	case actStop:
		return "stop"
	case actScreen:
		return "screen"
	case actInstallAPK, actTransfer:
		return "download"
	case actShell:
		return "terminal"
	case actDelete:
		return "trash"
	case actProxy:
		return "network"
	}
	return ""
}

func activityStatus(s itemStatus) string {
	switch s {
	case itemOK:
		return "已完成"
	case itemFailed:
		return "失败"
	case itemRunning:
		return "进行中"
	default:
		return "等待中"
	}
}

func (vm *viewModel) openSettings() {
	st := vm.svc.GetSettings()
	vm.settingsProxy, vm.settingsConcurrent = st.DownloadProxy, float64(st.MaxConcurrent)
	vm.settingsProxyReveal = false
	vm.settingsAutoDeleteDownloads = st.AutoDeleteDownloads
	vm.settingsSavedHint = ""
	vm.settingsProjection = projectionFormFrom(st.Projection)
	vm.settingsTab = 0
	vm.page = pageSettings
}

func (vm *viewModel) buildHelpModal(c *ui.Context) {
	ui.Modal(c, &vm.showHelp, func() {
		t := c.Theme()
		modalContent(c, 540).Padding(8).Gap(16).Children(func() {
			ui.Text(c, "使用帮助").FontSize(20).Bold()
			ui.Text(c, assets.Product.Name+" "+assets.Product.Version).FontSize(12).TextColor(t.TextMuted)
			for _, section := range [][2]string{
				{"开始使用", "在左侧「镜像与环境」中下载组件与系统镜像，然后新建实例。"},
				{"统一安装与拖放", "可直接将单个 .apk / .apks / .xapk 拖入工作区或具体设备行安装并自动拉起；拖入普通文件将自动进入「文件传输」待发送队列。"},
				{"后台运行与投屏", "无头模式实例支持按需打开 scrcpy 投屏窗口；关闭投屏窗口后实例仍在后台运行，点击「停止」结束运行。"},
				{"网络代理与桥接", "「设置 → 常规与下载」中的代理用于下载 SDK 与镜像；实例代理支持 http:// 与 socks5://（本地桥接仅覆盖 TCP/HTTP 栈，不接管 UDP/QUIC）。"},
			} {
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, section[0]).FontSize(12).Bold()
					ui.Text(c, section[1]).FontSize(12).TextColor(t.TextMuted)
				})
			}
			if vm.env.DataDir != "" {
				ui.Text(c, "本机数据目录："+vm.env.DataDir).FontSize(11).TextColor(t.TextMuted)
			}
			ui.Row(c).Children(func() {
				ui.Spacer(c)
				if control(c, "close_help", "关闭帮助", "", false).Clicked() {
					vm.showHelp = false
				}
			})
		})
	})
}

func softBadge(c *ui.Context, text string, fg, bg ui.Color) ui.Element {
	return ui.Row(c).Padding(2, 8).Radius(6).Background(bg).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(11).FontWeight(500).TextColor(fg).SingleLine()
	})
}

func softBadgeWarnBorder(c *ui.Context, text string, fg, bg, edge ui.Color) ui.Element {
	return ui.Row(c).Padding(2, 8).Radius(6).Background(bg).Border(1, edge).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(11).FontWeight(500).TextColor(fg).SingleLine()
	})
}

func detailCard(c *ui.Context, children func()) ui.Element {
	return ui.Column(c).Padding(10).Radius(8).Background(workbenchPalette.CanvasBg).
		Border(1, workbenchPalette.Hairline).Gap(5).Children(children)
}

// quietMenu keeps MyGo's hover, pressed, focus and menu handling intact.
func quietMenu(c *ui.Context, key, label string, build func(*ui.Menu)) ui.Element {
	theme := c.Theme()
	quiet := *theme
	quiet.Surface, quiet.Border = ui.Transparent, ui.Transparent
	c.SetTheme(&quiet)
	menu := ui.MenuButton(c.Key(key), label, build)
	c.SetTheme(theme)
	return menu.Label(label).TextColor(theme.TextMuted)
}

// modalContent shares the width budget of MyGo Modal: ten spacing units
// outside the panel and five of inner padding on either side.
func modalContent(c *ui.Context, preferredWidth float32) ui.Element {
	w, _ := c.Size()
	return ui.Column(c).Width(min(preferredWidth, max(0, w-c.Theme().Space(20))))
}

func platformFont(host string) string {
	switch host {
	case "windows":
		return "Microsoft YaHei UI"
	case "darwin":
		return "PingFang SC"
	default:
		return "sans-serif"
	}
}
func hostComputerName(host string) string {
	switch host {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	default:
		return "电脑"
	}
}
