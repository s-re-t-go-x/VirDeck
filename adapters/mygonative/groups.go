package mygonative

import (
	"context"
	"github.com/egoist/mygo/ui"
	"virdeck/core/service"
)

type groupUIState struct {
	snapshot                      service.GroupSnapshot
	active, editID, name, message string
	show                          bool
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
	vm.groups.message = ""
	vm.refreshGroups()
	vm.refreshVisible()
}
func (vm *viewModel) buildGroupToolbar(c *ui.Context) {
	s := &vm.groups
	ui.Row(c).Padding(4, 24).Gap(8).Wrap().Children(func() {
		labels := []string{"全部分组", "未分组"}
		ids := []string{"", ungroupedFilter}
		current := "全部分组"
		for _, g := range s.snapshot.Groups {
			labels = append(labels, g.Name)
			ids = append(ids, g.ID)
		}
		for i, id := range ids {
			if id == s.active {
				current = labels[i]
			}
		}
		ui.Select(c.Key("group_filter"), &current, labels).Label("分组筛选").Width(168)
		for i, label := range labels {
			if current == label && s.active != ids[i] {
				s.active = ids[i]
				vm.selected.Clear()
				vm.lastRow = -1
				vm.refreshVisible()
				break
			}
		}
		if ui.Button(c.Key("groups_manage"), "管理分组").Clicked() {
			s.show = true
			s.editID = ""
			s.name = ""
			s.message = ""
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
		}).Disabled(len(selected) == 0)
	})
	if s.message != "" && !s.show {
		ui.Text(c, s.message).FontSize(11).TextColor(c.Theme().Warning).Margin(0, 24)
	}
}
func (vm *viewModel) buildGroupsModal(c *ui.Context) {
	s := &vm.groups
	ui.Modal(c, &s.show, func() {
		modalContent(c, 480).Padding(8).Gap(14).Children(func() {
			ui.Text(c, "分组管理").FontSize(20).Bold()
			ui.Text(c, "在工作台筛选分组，选中实例后可移动到分组。删除分组会保留其中的实例。").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Form(c, func() {
				ui.Field(c, "选择分组", func() {
					labels := []string{"新建分组"}
					ids := []string{""}
					current := "新建分组"
					for _, g := range s.snapshot.Groups {
						labels = append(labels, g.Name)
						ids = append(ids, g.ID)
						if g.ID == s.editID {
							current = g.Name
						}
					}
					ui.Select(c.Key("group_edit"), &current, labels).Label("编辑分组选择")
					for i, label := range labels {
						if current == label && ids[i] != s.editID {
							s.editID = ids[i]
							s.name = ""
							if i > 0 {
								s.name = s.snapshot.Groups[i-1].Name
							}
							s.message = ""
							break
						}
					}
				})
				ui.Field(c, "分组名称", func() {
					ui.TextInput(c.Key("group_name"), &s.name).Placeholder("例如：工作、日常、备用").Label("分组名称输入")
				})
			})
			if s.message != "" {
				ui.Text(c, s.message).FontSize(12).TextColor(c.Theme().Warning)
			}
			ui.Row(c).Gap(8).Children(func() {
				if s.editID != "" && ui.Button(c.Key("group_delete"), "删除分组").Clicked() {
					err := vm.svc.DeleteGroup(context.Background(), s.editID)
					vm.groupResult(err)
					if err == nil {
						s.editID = ""
						s.name = ""
					}
				}
				ui.Spacer(c)
				if ui.Button(c.Key("groups_close"), "关闭").Clicked() {
					s.show = false
				}
				label := "创建分组"
				if s.editID != "" {
					label = "保存名称"
				}
				if ui.PrimaryButton(c.Key("group_save"), label).Clicked() {
					if s.editID == "" {
						g, err := vm.svc.CreateGroup(context.Background(), s.name)
						vm.groupResult(err)
						if err == nil {
							s.editID = g.ID
						}
					} else {
						vm.groupResult(vm.svc.RenameGroup(context.Background(), s.editID, s.name))
					}
				}
			})
		})
	})
}
