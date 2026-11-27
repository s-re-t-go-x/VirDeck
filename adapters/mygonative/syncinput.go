package mygonative

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
	"virdeck/core/service"
)

type syncUIState struct {
	show       bool
	busy       bool
	failed     bool
	masterID   string
	slaveIDs   map[string]bool
	jitterPx   float64
	maxDelayMS float64
	textInput  string
	message    string
}

func (vm *viewModel) openSyncModal() {
	status := vm.svc.GetInputSyncStatus()
	vm.syncStatus = status
	s := &vm.syncUI
	s.show = true
	s.message = ""
	s.failed = false

	if status.Active {
		return
	}
	if s.slaveIDs == nil {
		s.slaveIDs = make(map[string]bool)
	}

	var running []service.Instance
	for _, inst := range vm.instances {
		if inst.State == service.Running {
			running = append(running, inst)
		}
	}

	// Prefer currently selected running instance as master if not already set.
	validMaster := false
	for _, inst := range running {
		if inst.ID == s.masterID {
			validMaster = true
			break
		}
	}
	if !validMaster {
		s.masterID = ""
		for _, sel := range vm.selectedInsts() {
			if sel.State == service.Running {
				s.masterID = sel.ID
				break
			}
		}
		if s.masterID == "" && len(running) > 0 {
			s.masterID = running[0].ID
		}
	}

	// Default remaining running instances as slaves on first open.
	if len(s.slaveIDs) == 0 && len(running) > 1 {
		for _, inst := range running {
			if inst.ID != s.masterID {
				s.slaveIDs[inst.ID] = true
			}
		}
	}
	delete(s.slaveIDs, s.masterID)
}

func (vm *viewModel) buildActiveSyncBar(c *ui.Context) {
	if !vm.syncStatus.Active {
		return
	}
	t := c.Theme()
	st := vm.syncStatus
	enabledN := 0
	for _, sl := range st.Slaves {
		if sl.Enabled {
			enabledN++
		}
	}

	bg := workbenchPalette.AccentSoftBg
	statusText := fmt.Sprintf("同步中 · 主控 %s -> %d/%d 台", st.MasterName, enabledN, len(st.Slaves))
	statusColor := t.Accent
	pauseLabel := "暂停同步"
	if st.Paused {
		bg = workbenchPalette.WarningSoftBg
		statusText = fmt.Sprintf("已暂停 · 主控 %s (%d/%d 台)", st.MasterName, enabledN, len(st.Slaves))
		statusColor = t.Warning
		pauseLabel = "恢复同步"
	}

	ui.Row(c).Padding(6, 22).Gap(8).Background(bg).
		BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
		glyph(c, "network", 14).TextColor(statusColor)
		ui.Text(c, statusText).FontSize(11).FontWeight(600).TextColor(statusColor).SingleLine()
		ui.Spacer(c)

		if quietControl(c, "sync_bar_back", "返回", "").Height(26).FontSize(11).Clicked() {
			vm.sendSyncKey("back")
		}
		if quietControl(c, "sync_bar_home", "主页", "").Height(26).FontSize(11).Clicked() {
			vm.sendSyncKey("home")
		}
		if quietControl(c, "sync_bar_recent", "多任务", "").Height(26).FontSize(11).Clicked() {
			vm.sendSyncKey("recent")
		}
		if ui.Button(c.Key("sync_bar_pause"), pauseLabel).Height(26).Padding(2, 10).FontSize(11).
			Tooltip("快捷键 Ctrl+Alt+P").Clicked() {
			vm.toggleSyncPause(!st.Paused)
		}
		if quietControl(c, "sync_bar_manage", "同步面板", "").Height(26).FontSize(11).Clicked() {
			vm.openSyncModal()
		}
		if quietControl(c, "sync_bar_stop", "停止同步", "stop").Height(26).FontSize(11).
			TextColor(t.Danger).Clicked() {
			vm.stopInputSync()
		}
	})
}

func (vm *viewModel) buildSyncModal(c *ui.Context) {
	s := &vm.syncUI
	_, height := c.Size()
	ui.Modal(c, &s.show, func() {
		t := c.Theme()
		modalContent(c, 520).Padding(8).Gap(12).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				glyph(c, "network", 20).TextColor(t.Accent)
				ui.Text(c, "多设备操作同步").FontSize(20).Bold()
				ui.Spacer(c)
				if vm.syncStatus.Active {
					if vm.syncStatus.Paused {
						softBadge(c, "已暂停", t.Warning, workbenchPalette.WarningSoftBg)
					} else {
						softBadge(c, "同步运行中", t.Success, workbenchPalette.SuccessSoftBg)
					}
				}
			})

			bodyH := min(float32(380), max(float32(120), height-235))
			ui.Scroll(c.Key("sync_modal_body")).Height(bodyH).Gap(10).Children(func() {
				st := vm.syncStatus
				if !st.CaptureSupported && st.CaptureHint != "" {
					ui.Row(c).Padding(8, 10).Radius(6).Background(workbenchPalette.WarningSoftBg).
						Border(1, workbenchPalette.WarningSoftEdge).Gap(8).Children(func() {
						glyph(c, "alert", 14).TextColor(t.Warning)
						ui.Text(c, st.CaptureHint).FontSize(11).TextColor(t.Warning)
					})
				} else {
					ui.Text(c, "基于主控 scrcpy 画面比例与被控 pure-control 通道实现，同/异比例自动扣除黑边；支持 Ctrl+Alt+P 一键暂停/恢复。").
						FontSize(11).TextColor(t.TextMuted)
				}

				if st.Active {
					vm.buildActiveSyncModalBody(c, st)
				} else {
					vm.buildSetupSyncModalBody(c)
				}

				if s.message != "" {
					color := t.Success
					if s.failed {
						color = t.Danger
					}
					ui.Text(c, s.message).FontSize(11).TextColor(color)
				}
			})

			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("sync_close"), "关闭").Clicked() {
					s.show = false
				}
				if vm.syncStatus.Active {
					if ui.Button(c.Key("sync_modal_stop"), "停止同步").Disabled(s.busy).TextColor(t.Danger).Clicked() {
						vm.stopInputSync()
					}
					label := "暂停同步"
					if vm.syncStatus.Paused {
						label = "恢复同步"
					}
					if ui.PrimaryButton(c.Key("sync_modal_pause"), label).Disabled(s.busy).Clicked() {
						vm.toggleSyncPause(!vm.syncStatus.Paused)
					}
				} else {
					selectedSlaves := vm.selectedSyncSlaveIDs()
					if ui.PrimaryButton(c.Key("sync_start_btn"), "开始同步操作").
						Disabled(s.busy || s.masterID == "" || len(selectedSlaves) == 0).Clicked() {
						vm.startInputSync(selectedSlaves)
					}
				}
			})
		})
	})
}

func (vm *viewModel) buildSetupSyncModalBody(c *ui.Context) {
	t := c.Theme()
	s := &vm.syncUI
	if s.slaveIDs == nil {
		s.slaveIDs = make(map[string]bool)
	}

	var running []service.Instance
	for _, inst := range vm.instances {
		if inst.State == service.Running {
			running = append(running, inst)
		}
	}

	if len(running) < 2 {
		ui.Text(c, "需要至少 2 台「运行中」实例才能开启多设备同步。").FontSize(12).TextColor(t.Warning)
		return
	}

	masterLabels := make([]string, len(running))
	currentMaster := ""
	for i, inst := range running {
		masterLabels[i] = fmt.Sprintf("%s (:%d)", inst.Name, inst.Port)
		if inst.ID == s.masterID {
			currentMaster = masterLabels[i]
		}
	}

	ui.Form(c, func() {
		ui.Field(c, "主控设备", func() {
			ui.Select(c.Key("sync_master_select"), &currentMaster, masterLabels).Label("选择主控实例").Disabled(s.busy)
			for i, label := range masterLabels {
				if currentMaster == label && running[i].ID != s.masterID {
					s.masterID = running[i].ID
					delete(s.slaveIDs, s.masterID)
					break
				}
			}
		}).Description("未打开投屏时将自动打开主控 scrcpy 窗口；仅操作主控窗口。")
	})

	ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Text(c, "被控设备（后台无头模式亦可同步）").FontSize(12).FontWeight(600)
		ui.Spacer(c)
		if quietControl(c, "sync_slaves_all", "全选", "").Height(24).FontSize(11).Clicked() {
			for _, inst := range running {
				if inst.ID != s.masterID {
					s.slaveIDs[inst.ID] = true
				}
			}
		}
		if quietControl(c, "sync_slaves_none", "清空", "").Height(24).FontSize(11).Clicked() {
			s.slaveIDs = map[string]bool{}
		}
	})

	ui.Column(c).Gap(4).Children(func() {
		for _, inst := range running {
			if inst.ID == s.masterID {
				continue
			}
			inst := inst
			checked := s.slaveIDs[inst.ID]
			mode := "后台无头"
			if !inst.Headless {
				mode = "自带窗口"
			}
			ui.Row(c).Padding(4, 8).Radius(6).Background(t.Surface).AlignItems(ui.Center).Gap(8).Children(func() {
				if ui.Checkbox(c.Key("sync_slave_"+inst.ID), &checked, inst.Name).Disabled(s.busy).Changed() {
					if checked {
						s.slaveIDs[inst.ID] = true
					} else {
						delete(s.slaveIDs, inst.ID)
					}
				}
				ui.Spacer(c)
				ui.Textf(c, ":%d · %s", inst.Port, mode).FontSize(11).TextColor(t.TextMuted)
			})
		}
	})

	ui.Divider(c)
	ui.Form(c, func() {
		ui.Field(c, "防齐步微延迟", func() {
			ui.NumberInput(c.Key("sync_max_delay"), &s.maxDelayMS, 0, 300, 10).Label("随机延迟毫秒").Disabled(s.busy)
		}).Description("0 = 零延迟实时同步；设为 > 0 ms（如 60）可让各被控机错开响应。")
		ui.Field(c, "随机位置微偏移", func() {
			ui.NumberInput(c.Key("sync_jitter_px"), &s.jitterPx, 0, 10, 1).Label("随机偏移像素").Disabled(s.busy)
		}).Description("0 = 像素级精确对齐；设为 1–10 px 可在点击落点加随机微小偏移。")
	})
}

func (vm *viewModel) buildActiveSyncModalBody(c *ui.Context, st service.InputSyncStatus) {
	t := c.Theme()
	s := &vm.syncUI

	ui.Column(c).Padding(8, 10).Radius(6).Background(workbenchPalette.SubSurfaceBg).
		Border(1, workbenchPalette.Hairline).Gap(4).Children(func() {
		ui.Textf(c, "主控设备：%s", st.MasterName).FontSize(12).FontWeight(600)
		ui.Textf(c, "同步参数：最大延迟 %d ms · 微偏移 ±%d px · 快捷键 Ctrl+Alt+P", st.MaxDelayMS, st.JitterPx).
			FontSize(11).TextColor(t.TextMuted)
	})

	ui.Text(c, "同步导航键与文本广播（跨平台可用）").FontSize(12).FontWeight(600)
	ui.Row(c).Gap(6).Children(func() {
		if ui.Button(c.Key("sync_key_back"), "发送 返回").Disabled(s.busy).Clicked() {
			vm.sendSyncKey("back")
		}
		if ui.Button(c.Key("sync_key_home"), "发送 主页").Disabled(s.busy).Clicked() {
			vm.sendSyncKey("home")
		}
		if ui.Button(c.Key("sync_key_recent"), "发送 多任务").Disabled(s.busy).Clicked() {
			vm.sendSyncKey("recent")
		}
	})
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("sync_text_input"), &s.textInput).Grow(1).
			Placeholder("输入中英文文本或链接，一键同步写入剪贴板并粘贴…").Label("同步发送文本").Disabled(s.busy)
		if ui.Button(c.Key("sync_text_send"), "同步发送").
			Tooltip("通过 scrcpy 剪贴板同步粘贴，支持中英文与链接").
			Disabled(s.busy || s.textInput == "").Clicked() {
			vm.sendSyncText()
		}
	})

	ui.Divider(c)
	ui.Text(c, "被控设备开关（支持单台临时踢出或重新加入）").FontSize(12).FontWeight(600)
	for _, sl := range st.Slaves {
		sl := sl
		enabled := sl.Enabled
		ui.Row(c).Padding(4, 8).Radius(6).Background(t.Surface).AlignItems(ui.Center).Gap(8).Children(func() {
			if ui.Checkbox(c.Key("sync_active_slave_"+sl.InstanceID), &enabled, sl.Name).
				Disabled(!sl.Connected || s.busy).Changed() {
				vm.setSyncSlaveEnabled(sl.InstanceID, enabled)
			}
			ui.Spacer(c)
			if !sl.Connected {
				ui.Text(c, sl.Error).Tooltip(sl.Error).FontSize(10).TextColor(t.Danger).SingleLine()
			} else if sl.Enabled {
				softBadge(c, "同步中", t.Success, workbenchPalette.SuccessSoftBg)
			} else {
				softBadge(c, "已踢出", t.TextMuted, workbenchPalette.StoppedSoftBg)
			}
		})
	}
}

func (vm *viewModel) selectedSyncSlaveIDs() []string {
	var out []string
	for _, inst := range vm.instances {
		if inst.State == service.Running && inst.ID != vm.syncUI.masterID && vm.syncUI.slaveIDs[inst.ID] {
			out = append(out, inst.ID)
		}
	}
	return out
}

func (vm *viewModel) startInputSync(slaveIDs []string) {
	s := &vm.syncUI
	if s.busy {
		return
	}
	s.busy = true
	s.failed = false
	s.message = "正在建立 scrcpy 纯控制通道…"
	cfg := service.InputSyncConfig{
		MasterID:   s.masterID,
		SlaveIDs:   append([]string(nil), slaveIDs...),
		JitterPx:   int(s.jitterPx),
		MaxDelayMS: int(s.maxDelayMS),
	}
	go func() {
		st, err := vm.svc.StartInputSync(context.Background(), cfg)
		vm.post(func() {
			s.busy = false
			if err != nil {
				s.failed = true
				s.message = err.Error()
				return
			}
			vm.syncStatus = st
			s.failed = false
			s.message = fmt.Sprintf("✓ 已开启多设备同步（%d 台被控就绪）", len(st.Slaves))
		})
	}()
}

func (vm *viewModel) stopInputSync() {
	s := &vm.syncUI
	if s.busy {
		return
	}
	s.busy = true
	go func() {
		err := vm.svc.StopInputSync(context.Background())
		vm.post(func() {
			s.busy = false
			if err != nil {
				s.failed = true
				s.message = err.Error()
				return
			}
			vm.syncStatus = vm.svc.GetInputSyncStatus()
			s.failed = false
			s.message = "已停止多设备操作同步"
		})
	}()
}

func (vm *viewModel) toggleSyncPause(paused bool) {
	st, err := vm.svc.SetInputSyncPaused(paused)
	if err != nil {
		vm.showToast(err.Error())
		return
	}
	vm.syncStatus = st
}

func (vm *viewModel) setSyncSlaveEnabled(id string, enabled bool) {
	st, err := vm.svc.SetInputSyncSlaveEnabled(id, enabled)
	if err != nil {
		vm.showToast(err.Error())
		return
	}
	vm.syncStatus = st
}

func (vm *viewModel) sendSyncKey(key string) {
	go func() {
		if err := vm.svc.BroadcastSyncKey(context.Background(), key); err != nil {
			vm.post(func() {
				vm.syncUI.failed = true
				vm.syncUI.message = err.Error()
				vm.showToast(err.Error())
			})
		}
	}()
}

func (vm *viewModel) sendSyncText() {
	txt := vm.syncUI.textInput
	if txt == "" {
		return
	}
	go func() {
		err := vm.svc.BroadcastSyncText(context.Background(), txt)
		vm.post(func() {
			if err != nil {
				vm.syncUI.failed = true
				vm.syncUI.message = err.Error()
				vm.showToast(err.Error())
				return
			}
			vm.syncUI.textInput = ""
			vm.syncUI.failed = false
			vm.syncUI.message = "✓ 文本已同步发送"
		})
	}()
}
