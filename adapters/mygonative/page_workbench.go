package mygonative

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"virdeck/config"
	"virdeck/core/service"
)

func (vm *viewModel) buildWorkbenchPage(c *ui.Context, showDetail bool) {
	t := c.Theme()
	width, _ := c.Size()

	wsCol := ui.Column(c).Grow(1).Children(func() {
		vm.buildWorkbenchHeader(c)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				vm.buildEnvMissingBanner(c)
				vm.buildFilterAndSearchRow(c)
				sel := vm.selectedInsts()
				if len(sel) >= 2 || (len(sel) == 1 && !showDetail) {
					vm.buildTieredActionBar(c, sel)
				}
				vm.buildInstanceTable(c, width)
			})
			if showDetail {
				vm.buildDetail(c)
			}
		})
	})

	// Element-level anti-piercing drag-and-drop: ONLY active when no modal is open.
	if !vm.anyModalOpen() {
		if files := wsCol.DroppedFiles(); len(files) > 0 && !vm.dropHandledFrame {
			vm.dropHandledFrame = true
			vm.handleDroppedFiles(files, nil)
		}
		if wsCol.FileDragOver() {
			ui.Overlay(c, func() {
				ui.Row(c).Absolute().Top(12).Left(max(24, (width-380)/2)).Width(min(380, width-48)).
					Padding(10, 16).Radius(10).Background(workbenchPalette.ToastBg).
					Border(1, t.Accent).Center().Children(func() {
					ui.Text(c, "松手安装 .apk / .apks / .xapk 到实例，或发送普通文件到 Android").
						FontSize(12).FontWeight(600).TextColor(t.Background).SingleLine()
				})
			})
		}
	}
}

func (vm *viewModel) buildWorkbenchHeader(c *ui.Context) {
	t := c.Theme()
	width, _ := c.Size()
	compact := width < 760

	running, stopped := 0, 0
	for _, inst := range vm.instances {
		switch inst.State {
		case service.Running, service.Starting:
			running++
		case service.Stopped:
			stopped++
		}
	}

	title := "实例工作台"
	if vm.groups.active != "" {
		title = vm.activeGroupLabel()
	}

	ui.Row(c).Height(64).Padding(12, 22).Gap(10).BorderWidth(0, 0, 1, 0).
		BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(20).Bold().TextColor(t.Text).SingleLine()
			if !compact {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					softBadge(c, fmt.Sprintf("● %d 运行中", running), t.Success, workbenchPalette.SuccessSoftBg)
					softBadge(c, fmt.Sprintf("○ %d 已停止", stopped), t.TextMuted, workbenchPalette.StoppedSoftBg)
				})
			}
		})
		ui.Spacer(c)

		layoutLabel := "投屏排版"
		apkLabel := "安装 APK"
		syncLabel := "同步操作"
		if compact {
			layoutLabel = "排版"
			apkLabel = "装包"
			syncLabel = "同步"
		}
		if vm.syncStatus.Active {
			if vm.syncStatus.Paused {
				syncLabel = "同步已暂停"
			} else {
				enabledN := 0
				for _, sl := range vm.syncStatus.Slaves {
					if sl.Enabled {
						enabledN++
					}
				}
				syncLabel = fmt.Sprintf("同步中 · %d", enabledN)
			}
		}
		if quietControl(c, "hdr_input_sync", syncLabel, "network").Label("同步操作").
			Tooltip("多设备鼠标与导航控制同步（Windows 全局捕获 / Ctrl+Alt+P 暂停）").Clicked() {
			vm.openSyncModal()
		}
		if quietControl(c, "hdr_screen_layout", layoutLabel, "grid").Label("投屏排版").
			Tooltip("排列已打开的投屏窗口").Clicked() {
			vm.openScreenLayout()
		}
		if quietControl(c, "hdr_install_apk", apkLabel, "package").Label("安装 APK").
			Tooltip("选择 .apk / .apks / .xapk 并一键启动、安装、拉起与投屏").Clicked() {
			vm.pickAndOpenInstallAPK(nil)
		}
		if vm.detailHidden && width >= showDetailAt {
			if quietControl(c, "hdr_detail", "实例详情", "device").Clicked() {
				vm.detailHidden = false
			}
		}
		if control(c, "btn_create", "新建实例", "plus", true).Height(34).Clicked() {
			vm.createProxyReveal = false
			vm.showCreate = true
			vm.createGroupID = vm.groups.active
			if vm.createGroupID == ungroupedFilter {
				vm.createGroupID = ""
			}
		}
	})
}

func (vm *viewModel) buildEnvMissingBanner(c *ui.Context) {
	if !vm.envLoaded || vm.showEnvMissing {
		return
	}
	blocking, _ := envIssues(buildEnvChecks(vm.env))
	if blocking == 0 {
		return
	}
	t := c.Theme()
	ui.Row(c).Margin(10, 22, 0, 22).Padding(8, 12).Radius(8).
		Background(workbenchPalette.WarningSoftBg).Border(1, workbenchPalette.WarningSoftEdge).
		Gap(10).AlignItems(ui.Center).Children(func() {
		glyph(c, "alert", 15).TextColor(t.Warning)
		msg := fmt.Sprintf("检测到缺少 %d 项核心运行组件，影响创建或启动实例。", blocking)
		if !vm.env.PathASCIIOK {
			msg = "当前数据目录含非 ASCII 字符且未能自动映射，建议一键改用纯英文目录。"
		}
		ui.Text(c, msg).FontSize(12).TextColor(t.Warning).Grow(1).SingleLine()
		if !vm.env.PathASCIIOK {
			if ui.Button(c.Key("banner_redirect_ascii"), "一键改用纯英文数据目录").Height(26).Padding(4, 10).Clicked() {
				vm.redirectToPureASCIIDataDir(false)
			}
		}
		if quietControl(c, "banner_view_env", "查看环境详情", "").Height(26).Padding(4, 10).Clicked() {
			vm.page = pageImages
			vm.envTab = envTabRuntime
			vm.refreshEnv()
		}
		if ui.Button(c.Key("banner_fix_env"), "一键补齐").Height(26).Padding(4, 10).
			Disabled(vm.dlActive).Clicked() {
			vm.startInstallAllBaseTools()
		}
	})
}

func (vm *viewModel) buildFilterAndSearchRow(c *ui.Context) {
	t := c.Theme()
	counts := [4]int{}
	for _, inst := range vm.instances {
		if !vm.inGroup(inst.ID) {
			continue
		}
		counts[0]++
		switch inst.State {
		case service.Running, service.Starting:
			counts[1]++
		case service.Stopped:
			counts[2]++
		case service.Failed:
			counts[3]++
		}
	}

	ui.Row(c).Height(50).Padding(8, 22).Gap(10).BorderWidth(0, 0, 1, 0).
		BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
		ui.Tabs(c, &vm.filterTab,
			fmt.Sprintf("全部 %d", counts[0]),
			fmt.Sprintf("运行中 %d", counts[1]),
			fmt.Sprintf("已停止 %d", counts[2]),
			fmt.Sprintf("异常 %d", counts[3]))
		ui.Spacer(c)
		sortOptions := []string{"默认排序", "按名称", "运行中优先", "最新创建"}
		sortIdx := vm.sortMode
		if sortIdx < 0 || sortIdx >= len(sortOptions) {
			sortIdx = 0
		}
		sortLabel := sortOptions[sortIdx]
		ui.Select(c.Key("inst_sort"), &sortLabel, sortOptions).Label("列表排序").Width(104).Height(30).FontSize(11)
		for i, opt := range sortOptions {
			if sortLabel == opt && vm.sortMode != i {
				vm.sortMode = i
				vm.refreshVisible()
				break
			}
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			glyph(c, "search", 14).TextColor(t.TextMuted)
			ui.TextInput(c, &vm.query).Placeholder("搜索名称、备注或 ID…").Width(156).Height(30).FontSize(11)
		})
	})
	if vm.groups.message != "" && !vm.groups.show {
		color := t.Warning
		if strings.HasPrefix(vm.groups.message, "✓") {
			color = t.Success
		}
		ui.Text(c, vm.groups.message).FontSize(11).TextColor(color).Padding(4, 22, 0, 22)
	}
	if vm.groups.active != "" && vm.groups.active != ungroupedFilter {
		gid := vm.groups.active
		gname := vm.instanceGroupNameByID(gid)
		ui.Row(c).Padding(6, 22).Gap(8).Background(workbenchPalette.SubSurfaceBg).
			BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			glyph(c, "layers", 13).TextColor(t.Accent)
			ui.Textf(c, "分组协作 · %s", gname).FontSize(11).FontWeight(600).TextColor(t.Text).SingleLine()
			ui.Spacer(c)
			if quietControl(c, "grp_collab_start", "启动整组", "play").Height(26).FontSize(11).
				Tooltip("校验并发余量并错峰启动该分组").Clicked() {
				vm.startGroup(gid, gname)
			}
			if quietControl(c, "grp_collab_stop", "停止整组", "stop").Height(26).FontSize(11).
				Tooltip("停止该分组下全部运行中实例").Clicked() {
				vm.stopGroup(gid, gname)
			}
			if quietControl(c, "grp_collab_layout", "分组布局", "grid").Height(26).FontSize(11).
				Tooltip("为该分组管理与保存命名投屏布局").Clicked() {
				vm.openScreenLayoutForGroup(gid, gname)
			}
		})
	}
	vm.buildActiveSyncBar(c)
}

func (vm *viewModel) buildTieredActionBar(c *ui.Context, sel []service.Instance) {
	t := c.Theme()
	ui.Row(c).Padding(8, 22).Gap(8).Wrap().Background(t.Selection).
		BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
		if len(sel) == 1 {
			ui.Text(c, sel[0].Name).Bold().FontSize(12)
		} else {
			ui.Textf(c, "已选 %d 台", len(sel)).Bold().FontSize(12)
		}

		// Direct high-frequency actions.
		vm.actionButton(c, "bar", actStart, sel, true)
		vm.actionButton(c, "bar", actStop, sel, false)
		vm.actionButton(c, "bar", actScreen, sel, false)

		// Group move menu.
		selIDs := idsOf(sel)
		quietMenu(c, "bar_group_move", "移至分组", func(m *ui.Menu) {
			if m.Item("未分组").Chosen() {
				vm.groupResult(vm.svc.AssignGroup(context.Background(), selIDs, ""))
			}
			for _, g := range vm.groups.snapshot.Groups {
				g := g
				if m.Item(g.Name).Chosen() {
					vm.groupResult(vm.svc.AssignGroup(context.Background(), selIDs, g.ID))
				}
			}
			m.Separator()
			if m.Item("+ 新建分组并移入").Chosen() {
				vm.openGroups(selIDs)
			}
		}).Height(32).FontSize(12)

		runningSel := make([]service.Instance, 0, len(sel))
		for _, inst := range sel {
			if inst.State == service.Running {
				runningSel = append(runningSel, inst)
			}
		}

		// Tools dropdown menu.
		quietMenu(c, "bar_tools", "工具", func(m *ui.Menu) {
			for _, a := range []action{actInstallAPK, actTransfer, actShell} {
				p := planAction(a, sel, vm.busy)
				if m.Item(p.buttonLabel(a.label())).Disabled(p.disabledHint() != "").Chosen() {
					vm.invoke(a, p)
				}
			}
			m.Separator()
			if m.Item("一键截图").Disabled(len(runningSel) == 0).Chosen() {
				vm.runScreenshots(runningSel)
			}
			if m.Item("应用管理").Disabled(len(sel) != 1 || sel[0].State != service.Running || vm.busy[sel[0].ID] != "").Chosen() {
				vm.openAppsModal(sel[0])
			}
		}).Height(32).FontSize(12)

		// Config dropdown menu.
		availableProj := len(sel) > 0 && !vm.settingsRefreshing
		for _, inst := range sel {
			if vm.busy[inst.ID] != "" {
				availableProj = false
			}
		}
		quietMenu(c, "bar_config", "配置", func(m *ui.Menu) {
			pProxy := planAction(actProxy, sel, vm.busy)
			if m.Item(pProxy.buttonLabel(actProxy.label())).Disabled(pProxy.disabledHint() != "").Chosen() {
				vm.invoke(actProxy, pProxy)
			}
			if m.Item("投屏设置").Disabled(!availableProj).Chosen() {
				vm.openProjectionBatch(sel)
			}
			if m.Item("虚拟定位").Disabled(len(runningSel) == 0).Chosen() {
				vm.openLocationModal(runningSel)
			}
		}).Height(32).FontSize(12)

		if len(sel) == 1 {
			if ui.Button(c.Key("bar_edit"), "编辑").Height(32).Disabled(vm.busy[sel[0].ID] != "").Clicked() {
				vm.openEdit(sel[0])
			}
		}

		vm.actionButton(c, "bar", actDelete, sel, false).TextColor(t.Danger)
	})
}

func (vm *viewModel) buildInstanceTable(c *ui.Context, winWidth float32) {
	t := c.Theme()
	if len(vm.visible) == 0 {
		ui.Column(c).Grow(1).Center().Gap(12).Children(func() {
			glyph(c, "device", 32).TextColor(t.TextMuted)
			msg := "还没有实例，点击右上角「新建实例」开始。"
			if len(vm.instances) > 0 {
				msg = "没有符合当前筛选的实例。"
			}
			ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted)
			if len(vm.instances) == 0 {
				if control(c, "empty_create", "新建实例", "plus", true).Height(34).Clicked() {
					vm.createProxyReveal = false
					vm.showCreate = true
				}
			}
		})
		return
	}

	ui.Row(c).Height(34).Padding(0, 22).Gap(14).Background(workbenchPalette.SubSurfaceBg).
		BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
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
			m.Separator()
			if m.Item("从备份还原…").Chosen() {
				vm.openRestoreModal()
			}
		}).FontSize(10).Height(25).Padding(2, 6)
		ui.Text(c, "状态").Width(112).FontSize(10).TextColor(t.TextMuted)
		if winWidth >= 680 {
			ui.Text(c, "规格 / 端口").Width(148).FontSize(10).TextColor(t.TextMuted)
		}
		ui.Column(c).Width(14)
	})

	listTheme := *t
	listTheme.Accent, listTheme.AccentText, listTheme.Radius = t.Selection, t.Text, 0
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
				rowEl := ui.Row(c).Height(64).Padding(10, 22).Gap(14).
					BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).
					AlignItems(ui.Center).Children(func() {
					ui.Column(c).Width(3).Height(63).Background(edge).Margin(-10, 0, -10, -22)
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
					ui.Row(c).Grow(1).Gap(10).AlignItems(ui.Center).Children(func() {
						bg := workbenchPalette.InteractiveBg
						iconCol := t.TextMuted
						if on {
							bg = workbenchPalette.AccentSoftBg
							iconCol = t.Accent
							if !c.WindowActive() {
								bg = t.SurfacePressed.Mix(t.Surface, 0.6)
								iconCol = t.TextMuted
							}
						}
						ui.Column(c).Width(30).Height(34).Radius(7).Background(bg).Center().Children(func() {
							glyph(c, "device", 17).TextColor(iconCol)
						})
						ui.Column(c).Gap(3).Grow(1).Children(func() {
							ui.Text(c, it.Name).FontSize(13).FontWeight(600).SingleLine().TextColor(t.Text)
							ui.Row(c).Gap(5).Children(func() {
								if vm.groups.active == "" && vm.groups.snapshot.Memberships[it.ID] != "" {
									ui.Text(c, vm.instanceGroupName(it.ID)).FontSize(10).TextColor(t.Accent).SingleLine().Shrink(0)
									ui.Text(c, "·").FontSize(10).TextColor(t.TextMuted).Shrink(0)
								}
								meta := shortImage(it.SystemImage)
								if strings.TrimSpace(it.Resolution) != "" && it.Resolution != "720p" {
									meta += " · " + strings.ToUpper(it.Resolution)
								}
								if strings.TrimSpace(it.Note) != "" {
									meta += " · " + oneLine(it.Note)
								}
								ui.Text(c, meta).FontSize(10).TextColor(t.TextMuted).SingleLine()
							})
						})
					})

					ui.Column(c).Width(112).Gap(3).Children(func() {
						redactedLastErr := config.RedactText(it.LastError)
						stateEl := softBadge(c, vm.instanceStateLabel(it), stateColor(t, it.State), stateSoftBg(it.State))
						if redactedLastErr != "" {
							stateEl.Tooltip(redactedLastErr)
						}
						if op := vm.busy[it.ID]; op != "" {
							ui.Text(c, op+"中").FontSize(10).TextColor(t.Accent).SingleLine()
						} else if it.State == service.Starting {
							ui.Text(c, "等待桌面就绪").FontSize(10).TextColor(t.TextMuted).SingleLine()
						} else if it.State == service.Failed && redactedLastErr != "" {
							ui.Text(c, oneLine(redactedLastErr)).Tooltip(redactedLastErr).FontSize(10).TextColor(t.Danger).SingleLine()
						}
					})

					if winWidth >= 680 {
						ui.Column(c).Width(148).Gap(3).Children(func() {
							portStr := ""
							if it.Port > 0 {
								portStr = fmt.Sprintf(" · :%d", it.Port)
							}
							ui.Textf(c, "%.1fG / %d核%s", float64(it.MemoryMB)/1024, it.Cores, portStr).
								FontSize(11).TextColor(t.Text).SingleLine()
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
					}
					if it.State == service.Running || it.State == service.Starting {
						if quietControl(c, "row_quick_"+it.ID, "", "screen").Label("投屏 "+it.Name).
							Width(28).Height(28).Padding(5).Disabled(vm.busy[it.ID] != "").
							Tooltip("快速打开投屏").Clicked() {
							vm.perform(actScreen, []service.Instance{it})
						}
					} else {
						if quietControl(c, "row_quick_"+it.ID, "", "play").Label("启动 "+it.Name).
							Width(28).Height(28).Padding(5).Disabled(vm.busy[it.ID] != "").
							Tooltip("快速启动实例").Clicked() {
							vm.perform(actStart, []service.Instance{it})
						}
					}
					glyph(c, "chevron", 14).TextColor(t.TextMuted)
				})

				// High-priority row-level drop target when no modal is open.
				if !vm.anyModalOpen() {
					if files := rowEl.DroppedFiles(); len(files) > 0 {
						vm.dropHandledFrame = true
						vm.handleDroppedFiles(files, []string{it.ID})
					}
					if rowEl.FileDragOver() {
						rowEl.Background(workbenchPalette.AccentSoftBg)
					}
				}
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
	ui.Column(c).Width(detailWidth).Background(workbenchPalette.SubSurfaceBg).
		BorderWidth(0, 0, 0, 1).BorderColor(workbenchPalette.Hairline).Children(func() {
		ui.Scroll(c).Grow(1).Padding(12).Gap(8).Children(func() {
			switch {
			case len(sel) == 0:
				detailCard(c, func() {
					ui.Text(c, "实例详情").FontSize(12).FontWeight(600).TextColor(t.Text)
					ui.Text(c, "在左侧列表中选择一个实例，这里会显示它的状态、配置、代理和最近运行应用。").
						FontSize(12).TextColor(t.TextMuted)
				})
			case len(sel) > 1:
				detailCard(c, func() {
					ui.Text(c, "实例详情").FontSize(12).FontWeight(600).TextColor(t.Text)
					ui.Textf(c, "已选 %d 台实例", len(sel)).FontSize(18).Bold()
					ui.Text(c, "请使用列表上方的批量操作条。选择单个实例可查看详细卡片。").
						FontSize(12).TextColor(t.TextMuted)
				})
			default:
				vm.buildDetailBody(c, sel[0])
			}
		})
	})
}

func (vm *viewModel) buildDetailBody(c *ui.Context, inst service.Instance) {
	t := c.Theme()
	one := []service.Instance{inst}

	// Card 1: 设备概览与主操作卡.
	detailCard(c, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "实例详情").FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			if quietControl(c, "close_detail", "", "close").Label("收起实例详情").Width(24).Height(24).Padding(3).Clicked() {
				vm.detailHidden = true
			}
		})
		ui.Text(c, inst.Name).FontSize(18).FontWeight(600).TextColor(t.Text)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			stateEl := softBadge(c, vm.instanceStateLabel(inst), stateColor(t, inst.State), stateSoftBg(inst.State))
			if inst.LastError != "" {
				stateEl.Tooltip(config.RedactText(inst.LastError))
			}
			vm.buildGroupDetail(c, inst)
		})
		if inst.State == service.Starting {
			ui.Text(c, "正在引导 Android 系统，等待桌面就绪…").FontSize(11).TextColor(t.TextMuted)
		}
		if op := vm.busy[inst.ID]; op != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Textf(c, "正在执行「%s」", op).FontSize(12).TextColor(t.Accent)
			})
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if inst.State == service.Running || inst.State == service.Starting || (inst.State == service.Failed && inst.Port > 0) {
				vm.actionButton(c, "det", actScreen, one, true).Grow(1)
				vm.buildDetailAudioButton(c, inst)
				vm.actionButton(c, "det", actStop, one, false).TextColor(t.Danger).BorderColor(t.Danger)
			} else {
				vm.actionButton(c, "det", actStart, one, true).Grow(1)
			}
		})
		quietMenu(c, "detail_more", "更多操作", func(m *ui.Menu) {
			if m.Item("编辑").Disabled(vm.busy[inst.ID] != "").Chosen() {
				vm.openEdit(inst)
			}
			if m.Item("投屏设置").Disabled(vm.busy[inst.ID] != "").Chosen() {
				vm.openProjectionSettings(inst)
			}
			if m.Item("复制 ADB 命令").Chosen() {
				vm.writeClipboard(adbCommandForInstance(inst, vm.svc.GetSettings().ADBServerPort))
				vm.showToast("已复制 ADB 连接命令")
			}
			m.Separator()
			isBusy := vm.busy[inst.ID] != ""
			isRunning := inst.State == service.Running
			isStopped := inst.State == service.Stopped || (inst.State == service.Failed && inst.Port == 0)
			if m.Item("应用管理").Disabled(!isRunning || isBusy).Chosen() {
				vm.openAppsModal(inst)
			}
			if m.Item("虚拟定位").Disabled(!isRunning || isBusy).Chosen() {
				vm.openLocationModal(one)
			}
			if m.Item("一键截图").Disabled(!isRunning || isBusy).Chosen() {
				vm.runScreenshots(one)
			}
			if m.Item("磁盘清理").Disabled((!isRunning && !isStopped) || isBusy).Chosen() {
				vm.runDiskClean(inst)
			}
			if m.Item("备份实例").Disabled(!isStopped || isBusy).Chosen() {
				vm.openBackupModal(inst)
			}
			m.Separator()
			for _, a := range []action{actInstallAPK, actShell, actTransfer, actDelete} {
				p := planAction(a, one, vm.busy)
				if m.Item(a.label()).Disabled(p.disabledHint() != "").Chosen() {
					vm.invoke(a, p)
				}
			}
		}).FontSize(11).Height(28).AlignSelf(ui.Start)

		if inst.LastError != "" {
			redactedLastErr := config.RedactText(inst.LastError)
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "最近错误").FontSize(12).Bold().TextColor(t.Danger)
				ui.Spacer(c)
				if quietControl(c, "copy_last_error", "复制错误", "copy").Height(26).Padding(4, 8).FontSize(11).Clicked() {
					vm.writeClipboard(redactedLastErr)
				}
			})
			ui.Text(c, redactedLastErr).FontSize(11).TextColor(t.TextMuted)
		}
	})

	// Card 2: 设备配置卡.
	detailCard(c, func() {
		ui.Text(c, "设备配置").FontSize(12).Bold()
		kv(c, "系统镜像", shortImage(inst.SystemImage))
		storageGB := inst.StorageGB
		if storageGB <= 0 {
			storageGB = 6
		}
		kv(c, "硬件资源", fmt.Sprintf("%.1f GB / %d 核 / %dG", float64(inst.MemoryMB)/1024, inst.Cores, storageGB))
		kv(c, "屏幕形态", resolutionChoiceLabel(inst.Resolution))
		kv(c, "GPU 渲染", gpuModeLabel(inst.GPUMode))
		if strings.TrimSpace(inst.Note) != "" {
			kv(c, "实例备注", inst.Note)
		}
		bootMode := "冷启动"
		if inst.QuickBoot {
			bootMode = "快速启动快照"
			if inst.ForceColdNext {
				bootMode = "快速启动（下次冷启）"
			}
		}
		kv(c, "启动方式", bootMode)
		if inst.QuickBoot && inst.State == service.Stopped {
			if quietControl(c, "det_clear_snap_"+inst.ID, "清除快照 / 下次冷启动", "").Height(26).Padding(2, 8).FontSize(11).Clicked() {
				if err := vm.svc.ClearInstanceSnapshot(context.Background(), inst.ID); err != nil {
					vm.showToast("清除快照失败: " + err.Error())
				} else {
					vm.instances, _ = vm.svc.List(context.Background())
					vm.showToast("已清除快照，下次启动将冷启动自愈")
				}
			}
		}
		mode := "自带窗口"
		if inst.Headless {
			mode = "后台无头"
		}
		kv(c, "显示模式", mode)
		kv(c, "投屏配置", vm.detailProjectionSummary(inst))
		serial := "—"
		if inst.Port > 0 {
			serial = fmt.Sprintf("emulator-%d", inst.Port)
		}
		kv(c, "ADB 序列号", serial)
		kv(c, "实例 ID", inst.ID)
	})

	// Card 3: 网络代理卡.
	detailCard(c, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "网络代理").FontSize(12).Bold()
			ui.Spacer(c)
			vm.actionButton(c, "det", actProxy, one, false).Height(28).FontSize(11)
		})
		kv(c, "已保存", proxyText(inst.Proxy))
		if inst.State == service.Running || inst.State == service.Starting {
			kv(c, "当前生效", proxyText(inst.AppliedProxy))
			if pendingRestart(inst) {
				ui.Text(c, "待重启生效：保存的代理与运行中实例不一致，重启后生效。").
					FontSize(11).TextColor(t.Warning)
			}
		}
		ui.Text(c, "本地 SOCKS5 桥接仅覆盖 TCP/HTTP 栈（UDP/QUIC 需安卓内 VPN 工具）。").
			FontSize(10).TextColor(t.TextMuted)
	})

	// Card 4: 快捷应用 / 最近运行卡.
	detailCard(c, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "最近运行应用").FontSize(12).Bold()
			ui.Spacer(c)
			if quietControl(c, "det_install_apk", "装包", "package").Height(26).Padding(2, 8).FontSize(11).Clicked() {
				vm.pickAndOpenInstallAPK([]string{inst.ID})
			}
		})
		if len(inst.RecentApps) == 0 {
			ui.Text(c, "通过安装流水线拉起应用后，会在此沉淀一键直达入口。").
				FontSize(11).TextColor(t.TextMuted)
		} else {
			for i, app := range inst.RecentApps {
				app := app
				ui.Row(c.Key(fmt.Sprintf("recent_app_%s_%d", inst.ID, i))).
					Padding(6, 8).Radius(6).Background(workbenchPalette.SubSurfaceBg).
					Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Text(c, app.Package).FontSize(11).FontWeight(500).SingleLine().Tooltip(app.Component)
						if app.Component != "" {
							ui.Text(c, app.Component).FontSize(10).TextColor(t.TextMuted).SingleLine()
						}
					})
					if ui.Button(c.Key(fmt.Sprintf("launch_recent_%s_%d", inst.ID, i)), "启动").
						Height(26).Padding(2, 8).FontSize(11).
						Disabled(inst.State != service.Running || vm.busy[inst.ID] != "").Clicked() {
						vm.launchRecentApp(inst, app)
					}
				})
			}
		}
	})
}
