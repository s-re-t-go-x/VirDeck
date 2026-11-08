package mygonative

import (
	"context"
	"github.com/egoist/mygo/ui"
	"strings"
	"virdeck/config"
	"virdeck/core/service"
)

type projectionForm struct {
	fps, size, rate float64
	audio           bool
}

func projectionFormFrom(p service.ProjectionSettings) projectionForm {
	p = config.NormalizeProjection(p)
	return projectionForm{fps: float64(p.MaxFPS), size: float64(p.MaxSize), rate: float64(p.BitRateMbps), audio: p.Audio}
}
func (p projectionForm) values() service.ProjectionSettings {
	return service.ProjectionSettings{MaxFPS: int(p.fps), MaxSize: int(p.size), BitRateMbps: int(p.rate), Audio: p.audio}
}
func buildProjectionFields(c *ui.Context, p *projectionForm, disabled bool) {
	ui.Form(c, func() {
		ui.Field(c, "投屏帧率", func() {
			ui.NumberInput(c.Key("projection_fps"), &p.fps, config.MinProjectionFPS, 240, 5).Disabled(disabled)
		}).Description("范围 5–240 FPS；降低后滚动与动画会更不流畅。")
		ui.Field(c, "投屏最长边", func() {
			ui.NumberInput(c.Key("projection_size"), &p.size, config.MinProjectionSize, 8192, 160).Disabled(disabled)
		}).Description("范围 320–8192 像素；按比例缩放，不修改安卓内部的屏幕分辨率。")
		ui.Field(c, "投屏码率", func() { ui.NumberInput(c.Key("projection_rate"), &p.rate, 1, 100, 1).Disabled(disabled) }).Description("范围 1–100 Mbps；码率较低时画面细节会减少。")
		ui.Field(c, "声音", func() { ui.Checkbox(c.Key("projection_audio"), &p.audio, "播放安卓声音").Disabled(disabled) }).Description("通过投屏播放；需要 Android 11 及以上，安卓内音量也需开启。")
	})
}

type projectionUIState struct {
	show, inherit   bool
	id, name, error string
	form            projectionForm
}

func (vm *viewModel) openProjectionSettings(inst service.Instance) {
	p, custom, err := vm.svc.GetProjectionSettings(inst.ID)
	vm.projection = projectionUIState{show: true, id: inst.ID, name: inst.Name, inherit: !custom, form: projectionFormFrom(p)}
	if err != nil {
		vm.projection.error = err.Error()
	}
}
func (vm *viewModel) saveInstanceProjection(reopen bool) {
	s := &vm.projection
	var p *service.ProjectionSettings
	if !s.inherit {
		value := s.form.values()
		p = &value
	}
	if err := vm.svc.SetProjectionSettings(context.Background(), s.id, p); err != nil {
		s.error = err.Error()
		vm.appendLog("保存投屏设置失败：" + err.Error())
		return
	}
	s.error = "✓ 已保存；新开投屏时生效。"
	if reopen {
		id, name := s.id, s.name
		s.show = false
		vm.startTask("重新投屏", actScreen, []target{{id: id, name: name}}, true, func(ctx context.Context, t target) error { return vm.svc.ReopenScreen(ctx, t.id) }, nil)
	}
}
func (vm *viewModel) buildProjectionSettingsModal(c *ui.Context) {
	s := &vm.projection
	_, h := c.Size()
	ui.Modal(c, &s.show, func() {
		modalContent(c, 520).Padding(8).Gap(12).Children(func() {
			ui.Text(c, "投屏设置").FontSize(20).Bold()
			ui.Text(c, s.name).FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Scroll(c).Height(min(350, max(110, h-250))).Gap(12).Children(func() {
				previous := s.inherit
				ui.Checkbox(c, &s.inherit, "跟随全局设置")
				if s.inherit && !previous {
					s.form = projectionFormFrom(vm.svc.GetSettings().Projection)
				}
				buildProjectionFields(c, &s.form, s.inherit)
				if s.error != "" {
					color := c.Theme().Warning
					if strings.HasPrefix(s.error, "✓") {
						color = c.Theme().Success
					}
					ui.Text(c, s.error).FontSize(12).TextColor(color)
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("projection_close"), "关闭").Clicked() {
					s.show = false
				}
				ui.Spacer(c)
				if ui.Button(c.Key("projection_save"), "保存投屏设置").Clicked() {
					vm.saveInstanceProjection(false)
				}
				inst, exists := vm.findInstance(s.id)
				if ui.PrimaryButton(c.Key("projection_reopen"), "保存并重新投屏").Disabled(!exists || inst.State != service.Running || vm.busy[s.id] != "").Clicked() {
					vm.saveInstanceProjection(true)
				}
			})
		})
	})
}
