package mygonative

import (
	"context"
	"github.com/egoist/mygo/ui"
	"strings"
	"virdeck/core/service"
)

func (vm *viewModel) openEdit(inst service.Instance) {
	vm.editID, vm.editName, vm.editError = inst.ID, inst.Name, ""
	vm.editMemory, vm.editCores = float64(inst.MemoryMB), float64(inst.Cores)
	vm.editProxy, vm.editHeadless, vm.editImage = inst.Proxy, inst.Headless, inst.SystemImage
	vm.showEdit = true
}

func (vm *viewModel) buildEditModal(c *ui.Context) {
	_, h := c.Size()
	ui.Modal(c, &vm.showEdit, func() {
		modalContent(c, 520).Gap(12).Children(func() {
			ui.Text(c, "编辑实例").FontSize(16).Bold()
			ui.Text(c, "名称立即生效；内存、CPU、代理和运行模式在下次启动时应用。").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Scroll(c).Height(min(320, h-200)).Gap(10).Children(func() {
				ui.Form(c, func() {
					ui.Field(c, "实例名称", func() { ui.TextInput(c.Key("edit_name"), &vm.editName).Placeholder("支持中文") })
					ui.Field(c, "内存 (MB)", func() { ui.NumberInput(c.Key("edit_memory"), &vm.editMemory, 1024, 16384, 512) })
					ui.Field(c, "CPU 核数", func() { ui.NumberInput(c.Key("edit_cores"), &vm.editCores, 1, 8, 1) })
					ui.Field(c, "HTTP 代理", func() { ui.TextInput(c.Key("edit_proxy"), &vm.editProxy).Placeholder("留空使用直连") })
					ui.Field(c, "运行模式", func() { ui.Checkbox(c.Key("edit_headless"), &vm.editHeadless, "后台运行（无窗口）") })
					ui.Field(c, "系统镜像", func() { ui.Text(c, shortImage(vm.editImage)).FontSize(11).TextColor(c.Theme().TextMuted) }).Description("已有实例保持原镜像和数据。")
				})
			})
			if vm.editError != "" {
				ui.Text(c, vm.editError).FontSize(12).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("edit_cancel"), "取消").Clicked() {
					vm.showEdit = false
				}
				if ui.PrimaryButton(c.Key("edit_save"), "保存更改").Disabled(strings.TrimSpace(vm.editName) == "").Clicked() {
					spec := service.InstanceSettings{Name: vm.editName, MemoryMB: int(vm.editMemory), Cores: int(vm.editCores), Proxy: vm.editProxy, Headless: vm.editHeadless}
					if err := vm.svc.EditInstance(context.Background(), vm.editID, spec); err != nil {
						vm.editError = err.Error()
					} else {
						vm.instances, _ = vm.svc.List(context.Background())
						vm.showEdit = false
					}
				}
			})
		})
	})
}
