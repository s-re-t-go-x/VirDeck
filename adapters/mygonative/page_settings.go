package mygonative

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"virdeck/config"
)

func (vm *viewModel) saveGlobalSettings() bool {
	st := vm.svc.GetSettings()
	st.DownloadProxy = strings.TrimSpace(vm.settingsProxy)
	st.MaxConcurrent = int(vm.settingsConcurrent)
	st.AutoDeleteDownloads = vm.settingsAutoDeleteDownloads
	st.Projection = vm.settingsProjection.values()
	if err := vm.svc.UpdateSettings(st); err != nil {
		vm.settingsSavedHint = "保存失败：" + config.RedactError(err)
		vm.appendLog(vm.settingsSavedHint)
		return false
	}
	vm.settingsSavedHint = "✓ 已保存；投屏参数在新开或重新投屏时生效。"
	vm.appendLog("全局设置已保存")
	return true
}

func (vm *viewModel) buildSettingsPage(c *ui.Context) {
	t := c.Theme()

	ui.Column(c).Grow(1).Children(func() {
		ui.Row(c).Height(64).Padding(12, 22).Gap(10).BorderWidth(0, 0, 1, 0).
			BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			glyph(c, "settings", 18).TextColor(t.Accent)
			ui.Text(c, "设置").FontSize(20).Bold().TextColor(t.Text)
			ui.Spacer(c)
			if quietControl(c, "settings_back_workbench", "返回工作台", "").Clicked() {
				vm.page = pageWorkbench
			}
		})

		ui.Row(c).Height(48).Padding(6, 22).BorderWidth(0, 0, 1, 0).
			BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			ui.Tabs(c, &vm.settingsTab, "常规与下载", "投屏与声音")
		})

		ui.Scroll(c.Key("settings_page_scroll")).Grow(1).Padding(16, 22).Gap(14).Children(func() {
			vm.buildSettingsTab(c)
		})

		ui.Row(c).Padding(10, 22).Gap(8).Wrap().Background(workbenchPalette.SubSurfaceBg).
			BorderWidth(1, 0, 0, 0).BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c.Key("save_settings"), "保存全局设置").
				Disabled(vm.settingsRefreshing).Clicked() {
				vm.saveGlobalSettings()
			}
			if vm.settingsTab == 1 && ui.Button(c.Key("save_settings_reopen"), "保存并重开已打开投屏").
				Disabled(vm.settingsRefreshing).Clicked() {
				vm.saveGlobalAndReopen()
			}
			if vm.settingsSavedHint != "" {
				color := t.Warning
				if strings.HasPrefix(vm.settingsSavedHint, "✓") {
					color = t.Success
				}
				ui.Text(c, vm.settingsSavedHint).FontSize(12).TextColor(color)
			}
			ui.Spacer(c)
		})
	})
}

func (vm *viewModel) buildSettingsTab(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).MaxWidth(680).Gap(12).Children(func() {
		if vm.settingsTab == 0 {
			ui.Column(c).Padding(14).Radius(8).Background(workbenchPalette.SubSurfaceBg).
				Border(1, workbenchPalette.Hairline).Children(func() {
				ui.Form(c, func() {
					ui.Field(c, "下载代理", func() {
						proxyTextInput(c, "settings_proxy", &vm.settingsProxy, &vm.settingsProxyReveal, "可选，HTTP / SOCKS5 代理")
					}).Description("仅用于下载 SDK 与系统镜像；各实例的联网代理请在实例配置中单独设置。")
					ui.Field(c, "最大运行实例数", func() {
						ui.NumberInput(c, &vm.settingsConcurrent, 1, 16, 1)
					}).Description("限制同时启动与运行的实例上限，避免宿主机内存耗尽。")
					ui.Field(c, "下载清理", func() {
						ui.Checkbox(c.Key("settings_auto_delete"), &vm.settingsAutoDeleteDownloads, "安装成功后自动删除下载压缩包")
					}).Description("仅清理在线下载的组件或镜像压缩包，不影响本地手动导入的文件。")
				})
			})
		} else {
			ui.Column(c).Padding(14).Radius(8).Background(workbenchPalette.SubSurfaceBg).
				Border(1, workbenchPalette.Hairline).Gap(10).Children(func() {
				buildProjectionPresets(c, &vm.settingsProjection, vm.settingsRefreshing, false, nil)
				buildProjectionFields(c, &vm.settingsProjection, vm.settingsRefreshing)
				ui.Text(c, "默认所有实例跟随此全局设置；单台实例可在「投屏设置」中独立覆盖。").
					FontSize(12).TextColor(t.TextMuted)
			})
		}
	})
}
