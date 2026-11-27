package mygonative

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"virdeck/config"
	"virdeck/core/service"
)

type perfPreset struct {
	label  string
	cores  float64
	memMB  float64
}

var perfPresets = []perfPreset{
	{label: "省资源 (2核 · 2G)", cores: 2, memMB: 2048},
	{label: "均衡 (3核 · 2G)", cores: 3, memMB: 2048},
	{label: "流畅 (4核 · 4G)", cores: 4, memMB: 4096},
}

func renderPerfPresets(c *ui.Context, scope string, cores, memMB *float64) {
	ui.Row(c).Gap(6).Wrap().Children(func() {
		for i, p := range perfPresets {
			p := p
			if ui.Button(c.Key(scope+"_perf_"+string(rune('0'+i))), p.label).
				Height(26).Padding(2, 8).FontSize(11).Clicked() {
				*cores = p.cores
				*memMB = p.memMB
			}
		}
	})
}

var gpuModeOptions = []string{
	"自动选择（推荐）",
	"硬件加速 (host)",
	"兼容模式 (ANGLE)",
	"软件渲染 (SwiftShader)",
}

func gpuModeLabel(raw string) string {
	switch strings.TrimSpace(raw) {
	case "host":
		return gpuModeOptions[1]
	case "angle_indirect":
		return gpuModeOptions[2]
	case "swiftshader_indirect", "swiftshader":
		return gpuModeOptions[3]
	default:
		return gpuModeOptions[0]
	}
}

func parseGPUModeChoice(label string) string {
	switch label {
	case gpuModeOptions[1]:
		return "host"
	case gpuModeOptions[2]:
		return "angle_indirect"
	case gpuModeOptions[3]:
		return "swiftshader_indirect"
	default:
		return ""
	}
}

func proxyTextInput(c *ui.Context, key string, value *string, reveal *bool, placeholder string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		input := ui.TextInput(c.Key(key), value).Placeholder(placeholder).Grow(1)
		if !*reveal {
			input.Password()
		}
		toggleLabel := "显示"
		if *reveal {
			toggleLabel = "隐藏"
		}
		if ui.Button(c.Key(key+"_toggle"), toggleLabel).Height(30).Padding(2, 10).FontSize(11).Clicked() {
			*reveal = !*reveal
		}
	})
}

func (vm *viewModel) openEdit(inst service.Instance) {
	vm.editID, vm.editName, vm.editError = inst.ID, inst.Name, ""
	vm.editMemory, vm.editCores = float64(inst.MemoryMB), float64(inst.Cores)
	vm.editRes = resolutionChoiceLabel(inst.Resolution)
	vm.editProxy, vm.editHeadless, vm.editImage = inst.Proxy, inst.Headless, inst.SystemImage
	vm.editProxyReveal = false
	vm.editGPUMode = gpuModeLabel(inst.GPUMode)
	vm.editNote = inst.Note
	vm.editQuickBoot = inst.QuickBoot
	vm.editSnapHint = ""
	vm.showEdit = true
}

func (vm *viewModel) buildEditModal(c *ui.Context) {
	_, h := c.Size()
	ui.Modal(c, &vm.showEdit, func() {
		modalContent(c, 520).Gap(12).Children(func() {
			ui.Text(c, "编辑实例").FontSize(16).Bold()
			ui.Text(c, "名称与备注立即生效；内存、CPU、GPU、分辨率、代理、运行模式与快速启动在下次启动时应用。").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Scroll(c.Key("edit_fields")).Height(min(500, max(130, h-220))).Gap(10).Children(func() {
				ui.Form(c, func() {
					ui.Field(c, "实例名称", func() { ui.TextInput(c.Key("edit_name"), &vm.editName).Placeholder("支持中文") })
					ui.Field(c, "实例备注", func() { ui.TextInput(c.Key("edit_note"), &vm.editNote).Placeholder("可选，用途或账号标签（最多 80 字）") })
					ui.Field(c, "分辨率", func() { ui.Combobox(c.Key("edit_res"), &vm.editRes, resolutionOptions) })
					ui.Field(c, "性能预设", func() { renderPerfPresets(c, "edit", &vm.editCores, &vm.editMemory) })
					ui.Field(c, "内存 (MB)", func() { ui.NumberInput(c.Key("edit_memory"), &vm.editMemory, 1024, 16384, 512) })
					ui.Field(c, "CPU 核数", func() { ui.NumberInput(c.Key("edit_cores"), &vm.editCores, 1, 8, 1) })
					ui.Field(c, "GPU 渲染模式", func() {
						if vm.editGPUMode == "" {
							vm.editGPUMode = gpuModeOptions[0]
						}
						ui.Select(c.Key("edit_gpu"), &vm.editGPUMode, gpuModeOptions).Label("选择 GPU 渲染模式")
					})
					ui.Field(c, "网络代理", func() {
						proxyTextInput(c, "edit_proxy", &vm.editProxy, &vm.editProxyReveal, "留空直连，支持 http:// 或 socks5://")
					})
					ui.Field(c, "运行模式", func() { ui.Checkbox(c.Key("edit_headless"), &vm.editHeadless, "后台运行（无窗口）") })
					ui.Field(c, "快速启动", func() {
						ui.Column(c).Gap(6).Children(func() {
							ui.Checkbox(c.Key("edit_quickboot"), &vm.editQuickBoot, "快速启动（保存快照，参数变更后自动冷启动一次）")
							if vm.editQuickBoot {
								if ui.Button(c.Key("edit_clear_snapshot"), "清除快照 / 下次冷启动").Clicked() {
									if err := vm.svc.ClearInstanceSnapshot(context.Background(), vm.editID); err != nil {
										vm.editError = config.RedactError(err)
									} else {
										vm.editError = ""
										vm.editSnapHint = "已清除快照，下次启动将冷启动自愈。"
									}
								}
							}
						})
					})
					ui.Field(c, "系统镜像", func() { ui.Text(c, shortImage(vm.editImage)).FontSize(11).TextColor(c.Theme().TextMuted) }).Description("已有实例保持原镜像和数据。")
				})
			})
			if vm.editSnapHint != "" {
				ui.Text(c, vm.editSnapHint).FontSize(12).TextColor(c.Theme().Success)
			}
			if vm.editError != "" {
				ui.Text(c, vm.editError).FontSize(12).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("edit_cancel"), "取消").Clicked() {
					vm.showEdit = false
				}
				if ui.PrimaryButton(c.Key("edit_save"), "保存更改").Disabled(strings.TrimSpace(vm.editName) == "").Clicked() {
					spec := service.InstanceSettings{
						Name:       vm.editName,
						MemoryMB:   int(vm.editMemory),
						Cores:      int(vm.editCores),
						Resolution: parseResolutionChoice(vm.editRes),
						Proxy:      vm.editProxy,
						GPUMode:    parseGPUModeChoice(vm.editGPUMode),
						Note:       vm.editNote,
						Headless:   vm.editHeadless,
						QuickBoot:  vm.editQuickBoot,
					}
					if err := vm.svc.EditInstance(context.Background(), vm.editID, spec); err != nil {
						vm.editError = config.RedactError(err)
					} else {
						vm.instances, _ = vm.svc.List(context.Background())
						vm.showEdit = false
					}
				}
			})
		})
	})
}
