package mygonative

import (
	"github.com/egoist/mygo/ui"
	"virdeck/assets"
)

var brandMark = ui.MustParseSVG(assets.IconSVG)

// One palette for the workbench and all of its dialogs.
var workbenchTheme = func() *ui.Theme {
	t := ui.LightTheme()
	t.Background = ui.Hex("#ffffff")
	t.Surface = ui.Hex("#f5f7fb")
	t.SurfaceHover = ui.Hex("#edf0fa")
	t.SurfacePressed = ui.Hex("#e4e8f7")
	t.Border = ui.Hex("#e5eaf2")
	t.Text = ui.Hex("#24364b")
	t.TextMuted = ui.Hex("#7d8ba3")
	t.Accent = ui.Hex("#5963df")
	t.AccentHover = ui.Hex("#4d56cd")
	t.AccentPressed = ui.Hex("#424bb9")
	t.Selection = ui.Hex("#eef0ff")
	t.Focus = ui.Hex("#a1a8ed")
	t.Success = ui.Hex("#438b79")
	t.Warning = ui.Hex("#b08a43")
	t.Danger = ui.Hex("#c46570")
	t.Font, t.FontSize = "Microsoft YaHei UI", 13
	t.Radius = 6
	return t
}()

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
	case actInstallAPK:
		return "download"
	case actShell:
		return "terminal"
	case actClone:
		return "copy"
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
	vm.settingsSavedHint = ""
	vm.showSettings = true
}

func (vm *viewModel) buildSettingsModal(c *ui.Context) {
	ui.Modal(c, &vm.showSettings, func() {
		modalContent(c, 620).Padding(8).Gap(20).Children(func() {
			ui.Row(c).Gap(10).Children(func() { glyph(c, "settings", 20).TextColor(c.Theme().Accent); ui.Text(c, "设置").FontSize(20).Bold() })
			vm.buildSettingsTab(c)
			ui.Row(c).Children(func() {
				ui.Spacer(c)
				if control(c, "close_settings", "关闭设置", "", false).Clicked() {
					vm.showSettings = false
				}
			})
		})
	})
}

func (vm *viewModel) buildHelpModal(c *ui.Context) {
	ui.Modal(c, &vm.showHelp, func() {
		t := c.Theme()
		modalContent(c, 540).Padding(8).Gap(18).Children(func() {
			ui.Text(c, "使用帮助").FontSize(20).Bold()
			ui.Text(c, assets.Product.Name+" "+assets.Product.Version).FontSize(12).TextColor(t.TextMuted)
			for _, section := range [][2]string{
				{"开始使用", "在「镜像与环境」中安装组件和系统镜像，然后新建实例。"},
				{"选择与批量操作", "勾选设备可多选，也可使用 Ctrl / Shift 点击列表。多选后，批量操作会出现在列表上方。"},
				{"后台运行与投屏", "实例启动完成后，点击「投屏」打开操作画面。关闭投屏窗口后，实例仍在后台运行；点击「停止」结束运行。"},
				{"两种代理", "「设置」中的代理用于下载组件；实例详情中的代理用于该设备联网，修改后重启实例生效。"},
			} {
				ui.Column(c).Gap(6).Children(func() {
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
