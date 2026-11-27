package mygonative

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"virdeck/assets"
)

func (vm *viewModel) view(c *ui.Context) {
	c.SetTheme(workbenchTheme)
	vm.syncStartingTimers()
	vm.refreshVisible()
	vm.dropHandledFrame = false

	t := c.Theme()
	w, _ := c.Size()
	showNav := w >= showNavAt
	showDetail := w >= showDetailAt && !vm.detailHidden

	ui.Row(c).Fill().Background(workbenchPalette.ShellBg).AlignItems(ui.Stretch).Children(func() {
		if showNav {
			vm.buildSidebar(c)
		}

		margin := []float32{8, 8, 8, 0}
		if !showNav {
			margin = []float32{8, 8, 8, 8}
		}
		ui.Column(c).Grow(1).Margin(margin...).Radius(10).Background(t.Background).
			Border(1, workbenchPalette.Hairline).Clip().Children(func() {
			if !showNav {
				vm.buildNarrowTopBar(c)
			}
			switch vm.page {
			case pageWorkbench:
				vm.buildWorkbenchPage(c, showDetail)
			case pageTransfer:
				vm.buildTransferPage(c)
			case pageImages:
				vm.buildImagesPage(c)
			case pageLogs:
				vm.buildLogsPage(c)
			case pageSettings:
				vm.buildSettingsPage(c)
			}
		})
	})

	vm.buildTaskOverlay(c)

	vm.buildProjectionSettingsModal(c)
	vm.buildGroupsModal(c)
	vm.buildGroupStartConfirmModal(c)
	vm.buildScreenLayoutModal(c)
	vm.buildSyncModal(c)
	vm.buildLocationModal(c)
	vm.buildAppsModal(c)
	vm.buildBackupModal(c)
	vm.buildRestoreModal(c)
	vm.buildCreateModal(c)
	vm.buildEditModal(c)
	vm.buildInstallModal(c)
	vm.buildEnvMissingModal(c)
	vm.buildProxyModal(c)
	vm.buildShellModal(c)
	vm.buildDeleteModal(c)
	vm.buildMixedModal(c)
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

func (vm *viewModel) buildSidebar(c *ui.Context) {
	t := c.Theme()
	blocking, optional := envIssues(buildEnvChecks(vm.env))
	errCount := vm.errorLogCount()

	ui.Column(c).Width(navWidth).Background(workbenchPalette.ShellBg).Padding(18, 12, 14, 12).Gap(6).Children(func() {
		// Brand header (compact, no Ctrl+K bar).
		ui.Row(c).Gap(10).Padding(2, 6).Children(func() {
			ui.Image(c, brandMark).Width(26).Height(26).Shrink(0)
			ui.Column(c).Gap(1).Children(func() {
				ui.Text(c, assets.Product.Name).FontSize(17).Bold().TextColor(t.Text)
				ui.Text(c, assets.Product.Version).FontSize(10).TextColor(t.TextMuted)
			})
		})

		// Primary route 1: 实例工作台 + embedded group tree.
		vm.sidebarNavItem(c, "nav_workbench", "实例工作台", "device",
			fmt.Sprintf("%d", len(vm.instances)), vm.page == pageWorkbench,
			func() { vm.page = pageWorkbench })

		vm.buildSidebarGroupTree(c)

		// Primary route 2: 文件传输.
		vm.sidebarNavItem(c, "nav_transfer", "文件传输", "folder",
			"", vm.page == pageTransfer,
			func() { vm.openTransferPage() })

		// Primary route 3: 镜像与环境.
		envBadge := ""
		if vm.envLoaded && blocking+optional > 0 {
			envBadge = fmt.Sprintf("缺 %d", blocking+optional)
		}
		vm.sidebarNavItem(c, "nav_env", "镜像与环境", "layers",
			envBadge, vm.page == pageImages,
			func() {
				vm.page = pageImages
				vm.refreshEnv()
			})

		ui.Spacer(c)

		// Footer routes: 运行日志 / 设置 / 使用帮助.
		logBadge := ""
		if errCount > 0 {
			logBadge = fmt.Sprintf("%d", errCount)
		}
		vm.sidebarNavItem(c, "nav_logs", "运行日志", "logs",
			logBadge, vm.page == pageLogs,
			func() { vm.page = pageLogs })

		vm.sidebarNavItem(c, "nav_settings", "设置", "settings",
			"", vm.page == pageSettings,
			func() { vm.openSettings() })

		if quietControl(c, "nav_help", "使用帮助", "help").Height(34).Justify(ui.Start).Clicked() {
			vm.showHelp = true
		}

		// Bottom environment summary card (clickable shortcut to 运行环境 tab).
		envRow := ui.Column(c.Key("nav_env_summary")).Padding(8, 10).Radius(8).
			Background(workbenchPalette.CanvasBg).Border(1, workbenchPalette.Hairline).Gap(3).
			Children(func() {
				ui.Text(c, "●  "+envHealthText(vm.envLoaded, buildEnvChecks(vm.env))).
					FontSize(10).FontWeight(600).TextColor(vm.healthColor(c)).SingleLine()
				accel := "硬件加速待检测"
				if vm.envLoaded {
					if vm.env.AccelOK {
						accel = "硬件加速可用"
					} else {
						accel = "硬件加速未就绪"
					}
				}
				ui.Text(c, accel).FontSize(10).TextColor(t.TextMuted).SingleLine()
			})
		if envRow.Clicked() {
			vm.page = pageImages
			vm.envTab = envTabRuntime
			vm.refreshEnv()
		}
	})
}

func (vm *viewModel) sidebarNavItem(c *ui.Context, key, label, icon, badge string, active bool, onClick func()) {
	t := c.Theme()
	if active {
		card := ui.Row(c.Key(key)).Height(36).Padding(0, 10).Gap(8).Radius(8).
			Background(workbenchPalette.CanvasBg).Border(1, workbenchPalette.Hairline).
			AlignItems(ui.Center).Children(func() {
			ui.Column(c).Width(2).Height(16).Radius(2).Background(t.Accent)
			glyph(c, icon, 15).TextColor(t.AccentHover)
			ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(t.AccentHover).Grow(1).SingleLine()
			if badge != "" {
				softBadge(c, badge, t.AccentHover, workbenchPalette.AccentSoftBg)
			}
		})
		if card.Clicked() && onClick != nil {
			onClick()
		}
		return
	}
	btn := quietControl(c, key, "", "").Label(label).Height(36).Padding(6, 10).Gap(8).
		Justify(ui.Start).Children(func() {
		glyph(c, icon, 15)
		ui.Text(c, label).FontSize(13).Grow(1).SingleLine()
		if badge != "" {
			fg, bg := t.TextMuted, workbenchPalette.StoppedSoftBg
			if key == "nav_logs" {
				fg, bg = t.Danger, workbenchPalette.DangerSoftBg
			} else if key == "nav_env" {
				fg, bg = t.Warning, workbenchPalette.WarningSoftBg
			}
			softBadge(c, badge, fg, bg)
		}
	})
	if btn.Clicked() && onClick != nil {
		onClick()
	}
}

func (vm *viewModel) buildSidebarGroupTree(c *ui.Context) {
	counts := vm.groupCounts()
	ui.Column(c).Padding(0, 0, 4, 14).Gap(2).Children(func() {
		vm.sidebarGroupRow(c, "grp_all", "全部分组", counts[""], "", false)
		for _, g := range vm.groups.snapshot.Groups {
			vm.sidebarGroupRow(c, "grp_"+g.ID, g.Name, counts[g.ID], g.ID, true)
		}
		vm.sidebarGroupRow(c, "grp_ungrouped", "未分组", counts[ungroupedFilter], ungroupedFilter, false)
		if quietControl(c, "sidebar_groups_manage", "管理分组", "").Height(24).Padding(2, 8).FontSize(11).
			Justify(ui.Start).Clicked() {
			vm.openGroups(nil)
		}
	})
}

func (vm *viewModel) sidebarGroupRow(c *ui.Context, key, label string, count int, groupID string, allowStart bool) {
	t := c.Theme()
	active := vm.page == pageWorkbench && vm.groups.active == groupID
	bg := ui.Transparent
	fg := t.TextMuted
	if active {
		bg = workbenchPalette.CanvasBg
		fg = t.AccentHover
	}
	ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		row := ui.Row(c.Key(key)).Grow(1).Height(26).Padding(0, 8).Radius(6).Background(bg).
			AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, label).FontSize(12).TextColor(fg).Grow(1).SingleLine()
			ui.Textf(c, "%d", count).FontSize(10).TextColor(t.TextMuted).Shrink(0)
		})
		if active {
			row = row.Border(1, workbenchPalette.Hairline)
		}
		if row.Clicked() {
			vm.groups.active = groupID
			vm.selected.Clear()
			vm.lastRow = -1
			vm.page = pageWorkbench
			vm.refreshVisible()
		}
		if allowStart && count > 0 {
			if quietControl(c, "grp_start_"+groupID, "", "play").Label("启动整组 "+label).
				Width(24).Height(24).Padding(4).Tooltip("校验并发上限并错峰启动该分组").Clicked() {
				vm.startGroup(groupID, label)
			}
		}
	})
}

func (vm *viewModel) buildNarrowTopBar(c *ui.Context) {
	t := c.Theme()
	pageTitle := map[pageID]string{
		pageWorkbench: "实例工作台",
		pageTransfer:  "文件传输",
		pageImages:    "镜像与环境",
		pageLogs:      "运行日志",
		pageSettings:  "设置",
	}[vm.page]

	ui.Row(c).Height(42).Padding(6, 12).Gap(8).Background(workbenchPalette.SubSurfaceBg).
		BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).Children(func() {
		quietMenu(c, "narrow_nav", "☰ "+pageTitle, func(m *ui.Menu) {
			if m.Item("实例工作台").Chosen() {
				vm.page = pageWorkbench
			}
			if m.Item("文件传输").Chosen() {
				vm.openTransferPage()
			}
			if m.Item("镜像与环境").Chosen() {
				vm.page = pageImages
				vm.refreshEnv()
			}
			if m.Item("运行日志").Chosen() {
				vm.page = pageLogs
			}
			if m.Item("设置").Chosen() {
				vm.openSettings()
			}
			m.Separator()
			if m.Item("使用帮助").Chosen() {
				vm.showHelp = true
			}
		}).FontSize(12).Height(28)

		if vm.page == pageWorkbench {
			quietMenu(c, "narrow_group", "分组: "+vm.activeGroupLabel(), func(m *ui.Menu) {
				if m.Item("全部分组").Chosen() {
					vm.groups.active = ""
					vm.refreshVisible()
				}
				for _, g := range vm.groups.snapshot.Groups {
					g := g
					if m.Item(g.Name).Chosen() {
						vm.groups.active = g.ID
						vm.refreshVisible()
					}
				}
				if m.Item("未分组").Chosen() {
					vm.groups.active = ungroupedFilter
					vm.refreshVisible()
				}
				m.Separator()
				if m.Item("管理分组…").Chosen() {
					vm.openGroups(nil)
				}
			}).FontSize(11).Height(28)
		}

		ui.Spacer(c)
		ui.Text(c, "● "+envHealthText(vm.envLoaded, buildEnvChecks(vm.env))).
			FontSize(10).TextColor(t.TextMuted).SingleLine()
	})
}

func (vm *viewModel) activeGroupLabel() string {
	switch vm.groups.active {
	case "":
		return "全部"
	case ungroupedFilter:
		return "未分组"
	default:
		for _, g := range vm.groups.snapshot.Groups {
			if g.ID == vm.groups.active {
				return g.Name
			}
		}
		return "全部"
	}
}
