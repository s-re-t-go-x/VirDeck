package mygonative

import (
	"context"
	"errors"
	"fmt"
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

// Presets change video parameters only; the user's sound choice is independent.
func buildProjectionPresets(c *ui.Context, p *projectionForm, disabled, mixed bool, choose func()) {
	t := c.Theme()
	ui.Row(c).Gap(8).Children(func() {
		for i, preset := range config.Values.ProjectionPresets {
			v := preset.Settings
			active := !mixed && int(p.fps) == v.MaxFPS && int(p.size) == v.MaxSize && int(p.rate) == v.BitRateMbps
			b := ui.Button(c.Key(fmt.Sprintf("projection_preset_%d", i)), "").Label(preset.Name).Grow(1).Basis(0).Padding(10).Gap(5).Disabled(disabled)
			if active {
				b = b.Background(t.Selection).BorderColor(t.Accent)
			}
			b.Children(func() {
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Text(c, preset.Name).FontSize(12).Bold().SingleLine()
					ui.Textf(c, "%d FPS · %d px", v.MaxFPS, v.MaxSize).FontSize(10).TextColor(t.TextMuted).SingleLine()
					ui.Textf(c, "%d Mbps · %s", v.BitRateMbps, preset.Hint).FontSize(10).TextColor(t.TextMuted).SingleLine()
				})
			})
			if b.Clicked() {
				audio := p.audio
				*p = projectionFormFrom(v)
				p.audio = audio
				if choose != nil {
					choose()
				}
			}
		}
	})
}
func buildProjectionFields(c *ui.Context, p *projectionForm, disabled bool) {
	ui.Form(c, func() {
		ui.Field(c, "声音", func() { ui.Checkbox(c.Key("projection_audio"), &p.audio, "播放安卓声音").Disabled(disabled) })
		ui.Field(c, "投屏帧率", func() {
			ui.NumberInput(c.Key("projection_fps"), &p.fps, config.MinProjectionFPS, 240, 5).Disabled(disabled)
		})
		ui.Field(c, "投屏最长边", func() {
			ui.NumberInput(c.Key("projection_size"), &p.size, config.MinProjectionSize, 8192, 160).Disabled(disabled)
		})
		ui.Field(c, "投屏码率", func() { ui.NumberInput(c.Key("projection_rate"), &p.rate, 1, 100, 1).Disabled(disabled) })
	})
}

type projectionUIState struct {
	show, inherit, mixed bool
	id, name, error      string
	targets              []service.Instance
	form                 projectionForm
}

func (vm *viewModel) openProjectionSettings(inst service.Instance) {
	vm.openProjectionBatch([]service.Instance{inst})
}
func (vm *viewModel) openProjectionBatch(instances []service.Instance) {
	if len(instances) == 0 {
		return
	}
	p, custom, err := vm.svc.GetProjectionSettings(instances[0].ID)
	s := projectionUIState{show: true, id: instances[0].ID, name: instances[0].Name, targets: append([]service.Instance(nil), instances...), inherit: !custom, form: projectionFormFrom(p)}
	if err != nil {
		s.error = err.Error()
	}
	for _, inst := range instances[1:] {
		other, over, e := vm.svc.GetProjectionSettings(inst.ID)
		if e != nil {
			s.error = e.Error()
		}
		if over != custom || other != p {
			s.mixed = true
		}
	}
	if len(instances) > 1 {
		s.name = fmt.Sprintf("已选 %d 台实例", len(instances))
	}
	vm.projection = s
}
func (vm *viewModel) saveInstanceProjection(reopen bool) {
	s := &vm.projection
	if s.mixed {
		s.error = "请先选择统一预设，或明确选择跟随全局设置。"
		return
	}
	var value *service.ProjectionSettings
	if !s.inherit {
		v := s.form.values()
		value = &v
	}
	var errs []error
	var targets []target
	for _, original := range s.targets {
		inst, exists := vm.findInstance(original.ID)
		if !exists {
			errs = append(errs, fmt.Errorf("%s：实例已不存在", original.Name))
			continue
		}
		if vm.busy[inst.ID] != "" {
			errs = append(errs, fmt.Errorf("%s：正在执行其他操作", inst.Name))
			continue
		}
		if err := vm.svc.SetProjectionSettings(context.Background(), inst.ID, value); err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", inst.Name, err))
			continue
		}
		if reopen && inst.State == service.Running {
			targets = append(targets, target{id: inst.ID, name: inst.Name})
		}
	}
	s.error = "✓ 已保存；下次打开投屏时生效。"
	if len(errs) > 0 {
		s.error = "部分保存失败：" + errors.Join(errs...).Error()
		vm.appendLog(s.error)
	}
	if len(targets) > 0 {
		if len(errs) == 0 {
			s.show = false
		}
		vm.startTask("重新投屏", actScreen, targets, true, func(ctx context.Context, t target) error { return vm.svc.ReopenScreen(ctx, t.id) }, nil)
	}
}
func (vm *viewModel) buildProjectionSettingsModal(c *ui.Context) {
	s := &vm.projection
	_, h := c.Size()
	ui.Modal(c, &s.show, func() {
		modalContent(c, 560).Padding(8).Gap(12).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				glyph(c, "screen", 20).TextColor(c.Theme().Accent)
				ui.Text(c, "投屏设置").FontSize(20).Bold()
			})
			ui.Text(c, s.name).FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Scroll(c).Height(min(float32(430), max(float32(110), h-240))).Gap(12).Children(func() {
				buildProjectionPresets(c, &s.form, vm.settingsRefreshing, s.mixed, func() { s.inherit = false; s.mixed = false; s.error = "" })
				previous := s.inherit
				inherit := ui.Checkbox(c, &s.inherit, "跟随全局设置")
				if inherit.Changed() {
					s.mixed = false
					s.error = ""
				}
				if s.inherit && !previous {
					s.form = projectionFormFrom(vm.svc.GetSettings().Projection)
				}
				if s.mixed {
					ui.Text(c, "所选配置不同；选择预设或勾选跟随全局后统一保存。").FontSize(11).TextColor(c.Theme().Warning)
				}
				buildProjectionFields(c, &s.form, s.inherit || s.mixed)
				ui.Text(c, "仅调整投屏编码；不改变安卓分辨率。声音需要 Android 11+。").FontSize(11).TextColor(c.Theme().TextMuted)
				if s.error != "" {
					color := c.Theme().Warning
					if strings.HasPrefix(s.error, "✓") {
						color = c.Theme().Success
					}
					ui.Text(c, s.error).FontSize(12).TextColor(color)
				}
			})
			ui.Row(c).Gap(8).Wrap().Children(func() {
				if ui.Button(c.Key("projection_close"), "关闭").Clicked() {
					s.show = false
				}
				ui.Spacer(c)
				if ui.Button(c.Key("projection_save"), "保存投屏设置").Disabled(s.mixed).Clicked() {
					vm.saveInstanceProjection(false)
				}
				canReopen := false
				for _, v := range s.targets {
					if inst, ok := vm.findInstance(v.ID); ok && inst.State == service.Running && vm.busy[v.ID] == "" {
						canReopen = true
					}
				}
				if ui.PrimaryButton(c.Key("projection_reopen"), "保存并重新投屏").Disabled(!canReopen || s.mixed).Clicked() {
					vm.saveInstanceProjection(true)
				}
			})
		})
	})
}
func projectionSummary(p service.ProjectionSettings, custom bool) string {
	source := "默认"
	if custom {
		source = "独立"
	}
	sound := "静音"
	if p.Audio {
		sound = "有声"
	}
	return fmt.Sprintf("%s · %d FPS · %dp · %s", source, p.MaxFPS, p.MaxSize, sound)
}
func (vm *viewModel) detailProjectionSummary(inst service.Instance) string {
	p, custom, err := vm.svc.GetProjectionSettings(inst.ID)
	if err != nil {
		return "读取失败"
	}
	return projectionSummary(p, custom)
}
func (vm *viewModel) buildDetailAudioButton(c *ui.Context, inst service.Instance) {
	p, _, err := vm.svc.GetProjectionSettings(inst.ID)
	text := "静音"
	a11y := "开启声音"
	icon := "mute"
	tip := "当前投屏静音，点击开启声音"
	if err == nil && p.Audio {
		text = "有声"
		a11y = "关闭声音"
		icon = "volume"
		tip = "当前投屏已开启声音，点击切换为静音"
	}
	if control(c, "detail_audio_toggle", text, icon, false).Label(a11y).Tooltip(tip).Disabled(err != nil || vm.busy[inst.ID] != "" || vm.settingsRefreshing).Clicked() {
		vm.toggleProjectionAudio(inst)
	}
}
func (vm *viewModel) toggleProjectionAudio(inst service.Instance) {
	if vm.busy[inst.ID] != "" {
		return
	}
	vm.startTask("切换投屏声音", actScreen, []target{{id: inst.ID, name: inst.Name}}, false, func(ctx context.Context, t target) error {
		p, _, err := vm.svc.GetProjectionSettings(t.id)
		if err != nil {
			return err
		}
		p.Audio = !p.Audio
		var targetCfg *service.ProjectionSettings
		if config.NormalizeProjection(p) != config.NormalizeProjection(vm.svc.GetSettings().Projection) {
			targetCfg = &p
		}
		if err = vm.svc.SetProjectionSettings(ctx, t.id, targetCfg); err != nil {
			return err
		}
		snapshot, err := vm.svc.InspectScreenLayout(ctx)
		if err != nil {
			return fmt.Errorf("声音设置已保存，读取投屏窗口失败：%w", err)
		}
		for _, id := range snapshot.OpenIDs {
			if id == t.id {
				return vm.svc.ReopenScreen(ctx, id)
			}
		}
		return nil
	}, nil)
}
func (vm *viewModel) saveGlobalAndReopen() {
	if vm.settingsRefreshing || !vm.saveGlobalSettings() {
		return
	}
	vm.settingsRefreshing = true
	vm.settingsSavedHint = "✓ 已保存，正在查找跟随全局的投屏…"
	go func() {
		snap, err := vm.svc.InspectScreenLayout(context.Background())
		vm.post(func() {
			if err != nil {
				vm.settingsRefreshing = false
				vm.settingsSavedHint = "已保存，但读取投屏失败：" + err.Error()
				vm.appendLog(vm.settingsSavedHint)
				return
			}
			settings := vm.svc.GetSettings()
			var targets []target
			skipped := 0
			for _, id := range snap.OpenIDs {
				if _, custom := settings.ProjectionOverrides[id]; custom {
					continue
				}
				inst, ok := vm.findInstance(id)
				if !ok || inst.State != service.Running {
					continue
				}
				if vm.busy[id] != "" {
					skipped++
					continue
				}
				targets = append(targets, target{id: id, name: inst.Name})
			}
			if len(targets) == 0 {
				vm.settingsRefreshing = false
				vm.settingsSavedHint = fmt.Sprintf("✓ 已保存；没有可重开的全局投屏，忙碌跳过 %d 台。", skipped)
				return
			}
			vm.settingsSavedHint = fmt.Sprintf("✓ 已保存；正在重开 %d 台投屏，忙碌跳过 %d 台。", len(targets), skipped)
			vm.startTask("应用全局投屏设置", actScreen, targets, true, func(ctx context.Context, t target) error {
				_, custom, e := vm.svc.GetProjectionSettings(t.id)
				if e != nil {
					return e
				}
				if custom {
					return nil
				}
				return vm.svc.ReopenScreen(ctx, t.id)
			}, func(t *task) {
				vm.settingsRefreshing = false
				vm.settingsSavedHint = "✓ 全局设置已保存；" + t.summary()
				if len(t.failedItems()) > 0 {
					vm.settingsSavedHint = "已保存，部分投屏重开失败；请查看任务与日志。"
				}
			})
		})
	}()
}
