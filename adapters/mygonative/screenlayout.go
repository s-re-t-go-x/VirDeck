package mygonative

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"virdeck/config"
	"virdeck/core/service"
)

const screenScopeAll = "全部已打开投屏"

var screenSizes = []string{"当前大小", "小", "中", "大", "以窗口为基准"}
var screenSizeKeys = []string{"auto", "small", "medium", "large", "reference"}

type screenLayoutState struct {
	show, busy, failed                         bool
	autoFill, rememberPos                      bool
	columns                                    float64
	referenceID                                string
	token                                      uint64
	scope, displayID, size, message            string
	groupID, groupName                         string
	presetName, selectedPreset                 string
	selectedIDs                                []string
	snapshot                                   service.ScreenLayoutSnapshot
}

func (vm *viewModel) screenSelectedScopeLabel() string {
	return fmt.Sprintf("仅选中实例（%d 台）", len(vm.screens.selectedIDs))
}
func (vm *viewModel) screenGroupScopeLabel() string {
	return fmt.Sprintf("当前分组：%s", vm.screens.groupName)
}
func (vm *viewModel) screenScopeOptions() []string {
	opts := []string{screenScopeAll, vm.screenSelectedScopeLabel()}
	if vm.screens.groupID != "" {
		opts = append([]string{vm.screenGroupScopeLabel()}, opts...)
	}
	return opts
}
func (vm *viewModel) openScreenLayout() {
	vm.openScreenLayoutScoped("", "")
}
func (vm *viewModel) openScreenLayoutForGroup(groupID, groupName string) {
	vm.openScreenLayoutScoped(groupID, groupName)
}
func (vm *viewModel) openScreenLayoutScoped(groupID, groupName string) {
	pref := vm.svc.GetSettings().ScreenLayout
	if pref.Size == "" {
		pref.Size = config.Values.ScreenLayout.Size
	}
	scope := screenScopeAll
	if groupID != "" && groupName != "" {
		scope = fmt.Sprintf("当前分组：%s", groupName)
	}
	next := screenLayoutState{
		show:        true,
		token:       vm.screens.token + 1,
		scope:       scope,
		displayID:   pref.DisplayID,
		size:        pref.Size,
		referenceID: pref.ReferenceID,
		columns:     float64(pref.Columns),
		autoFill:    pref.AutoFill,
		rememberPos: pref.RememberPosition,
		groupID:     groupID,
		groupName:   groupName,
	}
	for _, i := range vm.selectedInsts() {
		next.selectedIDs = append(next.selectedIDs, i.ID)
	}
	vm.screens = next
	vm.refreshScreenLayout()
}
func (vm *viewModel) refreshScreenLayout() {
	s := &vm.screens
	s.busy = true
	s.message = ""
	s.failed = false
	s.token++
	token := s.token
	go func() {
		snap, err := vm.svc.InspectScreenLayout(context.Background())
		vm.post(func() {
			s := &vm.screens
			if !s.show || s.token != token {
				return
			}
			s.busy = false
			s.snapshot = snap
			found := false
			for _, d := range snap.Displays {
				if d.ID == s.displayID {
					found = true
					break
				}
			}
			if !found {
				previous := s.displayID
				s.displayID = ""
				for _, d := range snap.Displays {
					if d.Primary {
						s.displayID = d.ID
						break
					}
				}
				if s.displayID == "" && len(snap.Displays) > 0 {
					s.displayID = snap.Displays[0].ID
				}
				if previous != "" {
					s.message = "上次使用的显示器已断开，已选择当前主屏。"
				}
			}
			if s.size == "reference" && s.referenceID != "" {
				found := false
				for _, id := range snap.OpenIDs {
					if id == s.referenceID {
						found = true
						break
					}
				}
				if !found {
					s.referenceID = ""
					s.message = "基准投屏已关闭，请重新选择。"
					s.failed = true
				}
			}
			if snap.Warning != "" {
				s.message = snap.Warning
				s.failed = true
			}
			if err != nil {
				s.failed = true
				s.message = err.Error()
			}
			vm.syncSelectedScreenPreset()
		})
	}()
}

func (vm *viewModel) syncSelectedScreenPreset() {
	s := &vm.screens
	if len(s.snapshot.Presets) == 0 {
		s.selectedPreset = ""
		return
	}
	for _, p := range s.snapshot.Presets {
		if p.Name == s.selectedPreset {
			return
		}
	}
	if s.groupID != "" {
		for _, p := range s.snapshot.Presets {
			if p.GroupID == s.groupID {
				s.selectedPreset = p.Name
				return
			}
		}
	}
	s.selectedPreset = s.snapshot.Presets[0].Name
}

func (vm *viewModel) screenLayoutCount() int {
	s := &vm.screens
	if s.scope == screenScopeAll {
		return len(s.snapshot.OpenIDs)
	}
	if s.groupID != "" && s.scope == vm.screenGroupScopeLabel() {
		n := 0
		for _, id := range s.snapshot.OpenIDs {
			if vm.groups.snapshot.Memberships[id] == s.groupID {
				n++
			}
		}
		return n
	}
	selected := map[string]bool{}
	for _, id := range s.selectedIDs {
		selected[id] = true
	}
	n := 0
	for _, id := range s.snapshot.OpenIDs {
		if selected[id] {
			n++
		}
	}
	return n
}
func (vm *viewModel) screenLayoutRequest() service.ScreenLayoutRequest {
	s := &vm.screens
	cols := int(s.columns)
	if cols < 0 {
		cols = 0
	}
	req := service.ScreenLayoutRequest{
		DisplayID:   s.displayID,
		Size:        s.size,
		ReferenceID: s.referenceID,
		Columns:     cols,
	}
	if s.groupID != "" && s.scope == vm.screenGroupScopeLabel() {
		req.GroupID = s.groupID
		return req
	}
	req.SelectedOnly = s.scope != screenScopeAll
	req.IDs = append([]string(nil), s.selectedIDs...)
	return req
}
func (vm *viewModel) runScreenAction(action string, op func() (int, error)) {
	s := &vm.screens
	if s.busy {
		return
	}
	s.busy = true
	s.failed = false
	s.message = ""
	s.token++
	token := s.token
	go func() {
		n, err := op()
		vm.post(func() {
			message := fmt.Sprintf("已%s %d 个投屏窗口", action, n)
			if action == "保存" {
				message = fmt.Sprintf("已保存 %d 个窗口的位置和大小", n)
			}
			if err != nil {
				message += fmt.Sprintf("：%v", err)
			}
			vm.appendLog(message)
			s := &vm.screens
			if !s.show || s.token != token {
				return
			}
			s.busy = false
			s.failed = err != nil
			s.message = message
			if action == "保存" && err == nil {
				s.snapshot.SavedCount = n
			}
		})
	}()
}
func (vm *viewModel) applyScreenLayout() {
	s := &vm.screens
	if s.busy || s.displayID == "" || vm.screenLayoutCount() == 0 || s.size == "reference" && s.referenceID == "" {
		return
	}
	req := vm.screenLayoutRequest()
	vm.runScreenAction("排列", func() (int, error) { return vm.svc.ArrangeScreenLayout(context.Background(), req) })
}

func (vm *viewModel) runScreenPresetAction(actionLabel string, op func() (string, error)) {
	s := &vm.screens
	if s.busy {
		return
	}
	s.busy = true
	s.failed = false
	s.message = ""
	s.token++
	token := s.token
	go func() {
		msg, err := op()
		var presets []service.ScreenLayoutPresetSummary
		if snap, snapErr := vm.svc.InspectScreenLayout(context.Background()); snapErr == nil {
			presets = snap.Presets
		}
		vm.post(func() {
			s := &vm.screens
			if err != nil {
				msg = fmt.Sprintf("%s失败：%v", actionLabel, err)
			}
			vm.appendLog(msg)
			if !s.show || s.token != token {
				return
			}
			s.busy = false
			s.failed = err != nil
			s.message = msg
			if err == nil && presets != nil {
				s.snapshot.Presets = presets
				if actionLabel == "保存快照" {
					s.presetName = ""
				}
				vm.syncSelectedScreenPreset()
			}
		})
	}()
}
func (vm *viewModel) setScreenAutoFill(previous bool) {
	s := &vm.screens
	enabled := s.autoFill
	s.busy = true
	s.failed = false
	s.message = ""
	s.token++
	token := s.token
	go func() {
		err := vm.svc.SetScreenAutoFill(context.Background(), enabled)
		vm.post(func() {
			s := &vm.screens
			if !s.show || s.token != token {
				return
			}
			s.busy = false
			s.failed = err != nil
			if err != nil {
				s.autoFill = previous
				s.message = err.Error()
			} else if enabled {
				s.message = "已开启；完成一次排版后，新投屏将自动补位。"
			} else {
				s.message = "已关闭自动补位。"
			}
		})
	}()
}

func (vm *viewModel) setScreenRememberPosition(previous bool) {
	s := &vm.screens
	enabled := s.rememberPos
	s.busy = true
	s.failed = false
	s.message = ""
	s.token++
	token := s.token
	go func() {
		err := vm.svc.SetScreenRememberPosition(context.Background(), enabled)
		vm.post(func() {
			s := &vm.screens
			if !s.show || s.token != token {
				return
			}
			s.busy = false
			s.failed = err != nil
			if err != nil {
				s.rememberPos = previous
				s.message = err.Error()
			} else if enabled {
				s.message = "已开启：关闭或停机时将记住各实例最后投屏窗口位置。"
			} else {
				s.message = "已关闭记住上次窗口位置。"
			}
		})
	}()
}
func (vm *viewModel) buildScreenLayoutModal(c *ui.Context) {
	s := &vm.screens
	ui.Modal(c, &s.show, func() {
		t := c.Theme()
		_, height := c.Size()
		bodyHeight := float32(410)
		if s.size == "reference" {
			bodyHeight += 44
		}
		if len(s.snapshot.Presets) > 0 {
			bodyHeight += 42
		}
		if s.message != "" {
			bodyHeight += 36
		}
		bodyHeight = min(bodyHeight, max(100, height-235))
		modalContent(c, 520).Padding(8).Gap(12).Children(func() {
			ui.Row(c).Gap(10).Children(func() { glyph(c, "grid", 20).TextColor(t.Accent); ui.Text(c, "投屏排版").FontSize(20).Bold() })
			ui.Scroll(c.Key("screen_layout_body")).Height(bodyHeight).Gap(12).Children(func() {
				ui.Text(c, "保持画面比例；空间不足的窗口保持原位。").FontSize(12).TextColor(t.TextMuted)
				ui.Form(c, func() {
					ui.Field(c, "排列范围", func() {
						ui.Select(c.Key("screen_scope"), &s.scope, vm.screenScopeOptions()).Label("投屏排列范围").Disabled(s.busy)
					})
					ui.Field(c, "显示器", func() {
						labels := make([]string, len(s.snapshot.Displays))
						current := ""
						for i, d := range s.snapshot.Displays {
							primary := ""
							if d.Primary {
								primary = "（主屏）"
							}
							labels[i] = fmt.Sprintf("显示器 %d%s · %d × %d", i+1, primary, d.WorkArea.Width, d.WorkArea.Height)
							if d.ID == s.displayID {
								current = labels[i]
							}
						}
						ui.Select(c.Key("screen_display"), &current, labels).Label("排列显示器").Placeholder("暂无可用显示器").Disabled(s.busy || s.size == "reference")
						for i, label := range labels {
							if current == label {
								s.displayID = s.snapshot.Displays[i].ID
								break
							}
						}
					})
					ui.Field(c, "窗口大小", func() {
						label := screenSizes[0]
						for i, key := range screenSizeKeys {
							if key == s.size {
								label = screenSizes[i]
							}
						}
						ui.Select(c.Key("screen_size"), &label, screenSizes).Label("投屏窗口大小").Disabled(s.busy)
						for i, option := range screenSizes {
							if label == option {
								s.size = screenSizeKeys[i]
								break
							}
						}
					})
					if s.size == "reference" {
						ui.Field(c, "基准窗口", func() {
							labels := make([]string, len(s.snapshot.Windows))
							counts := map[string]int{}
							for _, w := range s.snapshot.Windows {
								counts[w.Name]++
							}
							current := ""
							for i, w := range s.snapshot.Windows {
								labels[i] = w.Name
								if counts[w.Name] > 1 {
									labels[i] += " · " + w.InstanceID[:min(6, len(w.InstanceID))]
								}
								if w.InstanceID == s.referenceID {
									current = labels[i]
								}
							}
							ui.Select(c.Key("screen_reference"), &current, labels).Label("基准投屏选择").Placeholder("选择已打开的投屏").Disabled(s.busy)
							for i, label := range labels {
								if current == label {
									s.referenceID = s.snapshot.Windows[i].InstanceID
									if id := s.snapshot.Windows[i].DisplayID; id != "" {
										s.displayID = id
									}
									break
								}
							}
						}).Description("基准窗口保持不动，其他窗口沿用它的画面高度。")
					} else {
						ui.Field(c, "每行窗口数", func() {
							ui.NumberInput(c.Key("screen_columns"), &s.columns, 0, 8, 1).Label("每行窗口个数").Disabled(s.busy)
						}).Description("0 = 按窗口大小自动计算；1–8 = 固定每行排列个数")
					}
				})
				previous := s.autoFill
				ui.Checkbox(c.Key("screen_auto_fill"), &s.autoFill, "新投屏自动补位").Disabled(s.busy)
				if previous != s.autoFill {
					vm.setScreenAutoFill(previous)
				}
				prevRem := s.rememberPos
				ui.Checkbox(c.Key("screen_remember_pos"), &s.rememberPos, "记住每个实例上次投屏窗口位置").Disabled(s.busy)
				if prevRem != s.rememberPos {
					vm.setScreenRememberPosition(prevRem)
				}

				ui.Textf(c, "已保存 %d 个窗口；再次保存将替换已存布局。", s.snapshot.SavedCount).FontSize(11).TextColor(t.TextMuted)
				ui.Divider(c)
				ui.Text(c, "命名布局快照（可按分组分别保存与恢复）").FontSize(11).FontWeight(600).TextColor(t.Text)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c.Key("screen_preset_name"), &s.presetName).Grow(1).
						Placeholder("输入快照名称（如：社媒竖排）").Label("布局快照名称").Disabled(s.busy)
					if ui.Button(c.Key("screen_preset_save"), "保存快照").
						Disabled(s.busy || strings.TrimSpace(s.presetName) == "" || vm.screenLayoutCount() == 0).Clicked() {
						req := vm.screenLayoutRequest()
						name := strings.TrimSpace(s.presetName)
						req.PresetName = name
						if req.GroupID == "" && vm.groups.active != "" && vm.groups.active != ungroupedFilter {
							req.GroupID = vm.groups.active
						}
						s.selectedPreset = name
						vm.runScreenPresetAction("保存快照", func() (string, error) {
							n, err := vm.svc.SaveScreenLayout(context.Background(), req)
							if err != nil {
								return "", err
							}
							return fmt.Sprintf("已保存命名快照「%s」（%d 个窗口）", name, n), nil
						})
					}
				})
				if len(s.snapshot.Presets) > 0 {
					presetLabels := make([]string, len(s.snapshot.Presets))
					currentLabel := ""
					for i, p := range s.snapshot.Presets {
						label := fmt.Sprintf("%s (%d 窗口)", p.Name, p.WindowCount)
						if p.GroupID != "" {
							label = fmt.Sprintf("%s · %s (%d 窗口)", p.Name, vm.instanceGroupNameByID(p.GroupID), p.WindowCount)
						}
						presetLabels[i] = label
						if p.Name == s.selectedPreset {
							currentLabel = label
						}
					}
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Select(c.Key("screen_preset_select"), &currentLabel, presetLabels).
							Label("选择命名布局快照").Grow(1).Disabled(s.busy)
						for i, label := range presetLabels {
							if currentLabel == label {
								s.selectedPreset = s.snapshot.Presets[i].Name
								break
							}
						}
						if ui.Button(c.Key("screen_preset_restore"), "恢复快照").
							Disabled(s.busy || s.selectedPreset == "" || vm.screenLayoutCount() == 0).Clicked() {
							req := vm.screenLayoutRequest()
							name := s.selectedPreset
							req.PresetName = name
							vm.runScreenPresetAction("恢复快照", func() (string, error) {
								n, err := vm.svc.RestoreScreenLayout(context.Background(), req)
								if err != nil {
									return "", err
								}
								return fmt.Sprintf("已按快照「%s」恢复 %d 个窗口", name, n), nil
							})
						}
						if quietControl(c, "screen_preset_delete", "删除快照", "trash").
							TextColor(t.Danger).Disabled(s.busy || s.selectedPreset == "").Clicked() {
							name := s.selectedPreset
							vm.runScreenPresetAction("删除快照", func() (string, error) {
								if err := vm.svc.DeleteScreenLayoutPreset(context.Background(), name); err != nil {
									return "", err
								}
								return fmt.Sprintf("已删除布局快照「%s」", name), nil
							})
						}
					})
				}
				if s.busy {
					ui.Text(c, "正在处理投屏窗口…").FontSize(12).TextColor(t.TextMuted)
				} else if n := vm.screenLayoutCount(); n == 0 {
					ui.Text(c, "此范围内没有已打开的投屏，请先打开投屏。").FontSize(12).TextColor(t.TextMuted)
				} else {
					ui.Textf(c, "范围内有 %d 个已打开投屏。", n).FontSize(12).TextColor(t.TextMuted)
				}
				if s.message != "" {
					color := t.Success
					if s.failed {
						color = t.Warning
					}
					ui.Text(c, s.message).FontSize(12).TextColor(color)
				}
			})
			ui.Row(c).Gap(6).Children(func() {
				if ui.Button(c.Key("screen_save"), "保存当前布局").Disabled(s.busy || vm.screenLayoutCount() == 0).Clicked() {
					req := vm.screenLayoutRequest()
					vm.runScreenAction("保存", func() (int, error) { return vm.svc.SaveScreenLayout(context.Background(), req) })
				}
				if ui.Button(c.Key("screen_restore"), "恢复已存布局").Disabled(s.busy || s.snapshot.SavedCount == 0 || vm.screenLayoutCount() == 0).Clicked() {
					req := vm.screenLayoutRequest()
					vm.runScreenAction("恢复", func() (int, error) { return vm.svc.RestoreScreenLayout(context.Background(), req) })
				}
				if ui.Button(c.Key("screen_refresh"), "刷新").Disabled(s.busy).Clicked() {
					vm.refreshScreenLayout()
				}
				ui.Spacer(c)
				if ui.Button(c.Key("screen_close"), "关闭").Clicked() {
					s.show = false
				}
				if ui.PrimaryButton(c.Key("screen_apply"), "一键排列").Disabled(s.busy || vm.screenLayoutCount() == 0 || s.displayID == "" || s.size == "reference" && s.referenceID == "").Clicked() {
					vm.applyScreenLayout()
				}
			})
		})
	})
}
