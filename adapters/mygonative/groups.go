package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
	"virdeck/core/service"
)

type groupUIState struct {
	snapshot                      service.GroupSnapshot
	active, editID, name, message string
	show                          bool
	editName                      string
	moveIDs                       []string
	pendingGroupID                string
}

const ungroupedFilter = "__ungrouped"

func (vm *viewModel) refreshGroups() {
	data, err := vm.svc.ListGroups(context.Background())
	if err != nil {
		vm.groups.message = "读取分组失败：" + err.Error()
		vm.appendLog(vm.groups.message)
		return
	}
	vm.groups.snapshot = data
	if vm.groups.active != "" && vm.groups.active != ungroupedFilter {
		found := false
		for _, g := range data.Groups {
			if g.ID == vm.groups.active {
				found = true
			}
		}
		if !found {
			vm.groups.active = ""
		}
	}
}
func (vm *viewModel) inGroup(id string) bool {
	g := vm.groups.active
	if g == "" {
		return true
	}
	member := vm.groups.snapshot.Memberships[id]
	if g == ungroupedFilter {
		return member == ""
	}
	return member == g
}
func (vm *viewModel) groupResult(err error) {
	if err != nil {
		vm.groups.message = err.Error()
		vm.appendLog("分组操作失败：" + err.Error())
		return
	}
	vm.groups.message = "✓ 分组已更新"
	vm.refreshGroups()
	vm.refreshVisible()
}
func (vm *viewModel) groupCounts() map[string]int {
	counts := map[string]int{"": len(vm.instances)}
	for _, inst := range vm.instances {
		g := vm.groups.snapshot.Memberships[inst.ID]
		if g == "" {
			g = ungroupedFilter
		}
		counts[g]++
	}
	return counts
}
func (vm *viewModel) instanceGroupName(id string) string {
	for _, g := range vm.groups.snapshot.Groups {
		if g.ID == vm.groups.snapshot.Memberships[id] {
			return g.Name
		}
	}
	return "未分组"
}
func (vm *viewModel) groupPicker(c *ui.Context, key string, id *string) {
	labels := []string{"未分组"}
	ids := []string{""}
	current := "未分组"
	for _, g := range vm.groups.snapshot.Groups {
		labels = append(labels, g.Name)
		ids = append(ids, g.ID)
		if *id == g.ID {
			current = g.Name
		}
	}
	ui.Select(c.Key(key), &current, labels).Label("所属分组")
	for i, label := range labels {
		if current == label {
			*id = ids[i]
			break
		}
	}
}
func (vm *viewModel) openGroups(moveIDs []string) {
	s := &vm.groups
	s.show = true
	s.editID = ""
	s.editName = ""
	s.name = ""
	s.message = ""
	s.pendingGroupID = ""
	s.moveIDs = append([]string(nil), moveIDs...)
}
func (vm *viewModel) createGroupFromForm() {
	s := &vm.groups
	id := s.pendingGroupID
	if id == "" {
		g, err := vm.svc.CreateGroup(context.Background(), s.name)
		if err != nil {
			vm.groupResult(err)
			return
		}
		id = g.ID
		s.name = ""
	}
	if len(s.moveIDs) > 0 {
		s.pendingGroupID = id
		if err := vm.svc.AssignGroup(context.Background(), s.moveIDs, id); err != nil {
			vm.refreshGroups()
			s.message = "分组已创建，但移入失败：" + err.Error() + "。可重试移入。"
			vm.appendLog(s.message)
			return
		}
	}
	moved := len(s.moveIDs)
	s.pendingGroupID = ""
	s.moveIDs = nil
	vm.groupResult(nil)
	s.message = "✓ 已创建分组"
	if moved > 0 {
		s.message = fmt.Sprintf("✓ 已创建分组并移入 %d 台实例", moved)
	}
}
func (vm *viewModel) buildGroupToolbar(c *ui.Context) {
	s := &vm.groups
	counts := vm.groupCounts()
	ui.Row(c).Padding(4, 24).Gap(8).Wrap().Children(func() {
		labels := []string{fmt.Sprintf("全部分组 (%d)", counts[""]), fmt.Sprintf("未分组 (%d)", counts[ungroupedFilter])}
		ids := []string{"", ungroupedFilter}
		current := labels[0]
		for _, g := range s.snapshot.Groups {
			labels = append(labels, fmt.Sprintf("%s (%d)", g.Name, counts[g.ID]))
			ids = append(ids, g.ID)
		}
		for i, id := range ids {
			if id == s.active {
				current = labels[i]
			}
		}
		ui.Select(c.Key("group_filter"), &current, labels).Label("分组筛选").Width(190)
		for i, label := range labels {
			if current == label && s.active != ids[i] {
				s.active = ids[i]
				vm.selected.Clear()
				vm.lastRow = -1
				vm.refreshVisible()
				break
			}
		}
		if quietControl(c, "groups_manage", "管理分组", "layers").Clicked() {
			vm.openGroups(nil)
		}
		selected := idsOf(vm.selectedInsts())
		quietMenu(c, "group_move", "移动到分组", func(m *ui.Menu) {
			if m.Item("未分组").Chosen() {
				vm.groupResult(vm.svc.AssignGroup(context.Background(), selected, ""))
			}
			for _, g := range s.snapshot.Groups {
				if m.Item(g.Name).Chosen() {
					vm.groupResult(vm.svc.AssignGroup(context.Background(), selected, g.ID))
				}
			}
			m.Separator()
			if m.Item("+ 新建分组并移入").Chosen() {
				vm.openGroups(selected)
			}
		}).Disabled(len(selected) == 0)
	})
	if s.message != "" && !s.show {
		color := c.Theme().Warning
		if strings.HasPrefix(s.message, "✓") {
			color = c.Theme().Success
		}
		ui.Text(c, s.message).FontSize(11).TextColor(color).Margin(0, 24)
	}
}
func (vm *viewModel) buildGroupDetail(c *ui.Context, inst service.Instance) {
	t := c.Theme()
	hasGroup := vm.groups.snapshot.Memberships[inst.ID] != ""
	label := vm.instanceGroupName(inst.ID)
	color := t.TextMuted
	if hasGroup {
		color = t.Accent
	}
	quietMenu(c, "detail_group_"+inst.ID, label, func(m *ui.Menu) {
		if m.Item("未分组").Chosen() {
			vm.groupResult(vm.svc.AssignGroup(context.Background(), []string{inst.ID}, ""))
		}
		for _, g := range vm.groups.snapshot.Groups {
			if m.Item(g.Name).Chosen() {
				vm.groupResult(vm.svc.AssignGroup(context.Background(), []string{inst.ID}, g.ID))
			}
		}
		m.Separator()
		if m.Item("+ 新建分组并移入").Chosen() {
			vm.openGroups([]string{inst.ID})
		}
	}).Label("所属分组").FontSize(11).Height(22).Padding(2, 8).Radius(4).Background(t.Surface).TextColor(color)
}
func (vm *viewModel) buildGroupsModal(c *ui.Context) {
	s := &vm.groups
	_, h := c.Size()
	ui.Modal(c, &s.show, func() {
		t := c.Theme()
		modalContent(c, 560).Padding(8).Gap(12).Children(func() {
			ui.Row(c).Gap(8).Children(func() { glyph(c, "layers", 20).TextColor(t.Accent); ui.Text(c, "分组管理").FontSize(20).Bold() })
			ui.Text(c, "按用途整理设备。删除分组只移除归属，保留实例与数据。").FontSize(11).TextColor(t.TextMuted)
			counts := vm.groupCounts()
			listHeight := float32(max(1, len(s.snapshot.Groups)) * 60)
			if s.editID != "" {
				listHeight += 44
			}
			ui.Scroll(c.Key("group_list")).Height(min(listHeight, float32(260), max(float32(80), h-315))).Gap(6).Children(func() {
				if len(s.snapshot.Groups) == 0 {
					ui.Text(c, "还没有分组，在下方创建第一个分组。").FontSize(12).TextColor(t.TextMuted)
				}
				for _, g := range s.snapshot.Groups {
					ui.Column(c.Key("group_row_" + g.ID)).Padding(10).Gap(8).Background(t.Surface).Radius(6).Children(func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, g.Name).Bold().FontSize(12).Grow(1).SingleLine().Tooltip(g.Name)
							ui.Textf(c, "%d 台", counts[g.ID]).FontSize(11).TextColor(t.TextMuted)
							if quietControl(c, "group_edit_"+g.ID, "重命名", "").Clicked() {
								s.editID = g.ID
								s.editName = g.Name
								s.message = ""
							}
							if quietControl(c, "group_delete_"+g.ID, "删除分组", "trash").TextColor(t.Danger).Clicked() {
								if err := vm.svc.DeleteGroup(context.Background(), g.ID); err != nil {
									vm.groupResult(err)
								} else {
									vm.groupResult(nil)
									s.message = "✓ 已删除分组，实例已移至未分组"
									if s.pendingGroupID == g.ID {
										s.pendingGroupID = ""
										s.name = ""
									}
									if s.editID == g.ID {
										s.editID = ""
									}
								}
							}
						})
						if s.editID == g.ID {
							ui.Row(c).Gap(8).Children(func() {
								ui.TextInput(c.Key("group_edit_name_"+g.ID), &s.editName).Grow(1).Label("重命名分组")
								if ui.Button(c.Key("group_rename_save_"+g.ID), "保存名称").Disabled(strings.TrimSpace(s.editName) == "").Clicked() {
									if err := vm.svc.RenameGroup(context.Background(), g.ID, s.editName); err != nil {
										vm.groupResult(err)
									} else {
										vm.groupResult(nil)
										s.message = "✓ 分组已重命名"
										s.editID = ""
									}
								}
								if quietControl(c, "group_edit_cancel_"+g.ID, "取消", "").Clicked() {
									s.editID = ""
								}
							})
						}
					})
				}
			})
			ui.Divider(c)
			title := "新建分组"
			if len(s.moveIDs) > 0 {
				title = fmt.Sprintf("新建分组并移入 %d 台实例", len(s.moveIDs))
			}
			ui.Text(c, title).FontSize(12).Bold()
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c.Key("group_name"), &s.name).Grow(1).Placeholder("例如：工作、日常、备用").Label("分组名称输入").Disabled(s.pendingGroupID != "")
				label := "创建分组"
				if s.pendingGroupID != "" {
					label = "重试移入"
				}
				if ui.PrimaryButton(c.Key("group_save"), label).Disabled(strings.TrimSpace(s.name) == "" && s.pendingGroupID == "").Clicked() {
					vm.createGroupFromForm()
				}
			})
			if s.message != "" {
				color := t.Warning
				if strings.HasPrefix(s.message, "✓") {
					color = t.Success
				}
				ui.Text(c, s.message).FontSize(11).TextColor(color).SingleLine().Tooltip(s.message)
			}
			ui.Row(c).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("groups_close"), "关闭").Clicked() {
					s.show = false
				}
			})
		})
	})
}
