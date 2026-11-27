package mygonative

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// buildTaskOverlay implements Scheme 1: bottom-right frosted floating task capsule +
// upward floating task card + top-center toast pill.
func (vm *viewModel) buildTaskOverlay(c *ui.Context) {
	t := c.Theme()
	w, h := c.Size()
	lt := vm.latestTask()

	ui.Overlay(c, func() {
		if vm.toastMsg != "" {
			toastW := min(float32(460), max(float32(220), w-40))
			left := max(float32(16), (w-toastW)/2)
			toast := ui.Row(c.Key("toast_pill")).Absolute().Top(14).Left(left).Width(toastW).
				Padding(8, 14).Radius(8).Background(workbenchPalette.ToastBg).
				Shadow(0, 6, 18, 0, ui.RGBA(15, 23, 42, 48)).AlignItems(ui.Center).Gap(8).
				Children(func() {
					ui.Text(c, vm.toastMsg).FontSize(12).TextColor(t.Background).Grow(1)
					ui.Text(c, "✕").FontSize(11).TextColor(t.TextMuted).Shrink(0)
				})
			if toast.Clicked() {
				vm.toastMsg = ""
			}
		}

		// Do not show floating overlay panel over modal dialogs.
		if vm.anyModalOpen() {
			return
		}

		ui.Column(c).Absolute().Bottom(16).Right(18).AlignItems(ui.End).Gap(8).Children(func() {
			if vm.tasksOpen {
				vm.buildFloatingTaskCard(c, w, h)
			}
			vm.buildFloatingTaskCapsule(c, w, lt)
		})
	})
}

func (vm *viewModel) buildFloatingTaskCard(c *ui.Context, winW, winH float32) {
	t := c.Theme()
	cardW := min(float32(390), max(float32(260), winW-32))
	listH := min(float32(280), max(float32(100), winH-210))

	ui.Column(c.Key("floating_task_card")).Width(cardW).Radius(10).
		Background(workbenchPalette.CanvasBg).Border(1, workbenchPalette.StrongHairline).
		Shadow(0, 12, 32, 0, ui.RGBA(15, 23, 42, 36)).Clip().Children(func() {
		ui.Row(c).Padding(10, 14).BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).
			AlignItems(ui.Center).Children(func() {
			ui.Text(c, "任务中心").FontSize(13).Bold().TextColor(t.Text)
			ui.Spacer(c)
			if quietControl(c, "task_card_close", "收起 ▼", "").Height(26).Padding(2, 8).FontSize(11).Clicked() {
				vm.tasksOpen = false
			}
		})

		if vm.dlActive {
			ui.Column(c).Padding(10, 14).Gap(6).Background(workbenchPalette.AccentSoftBg).
				BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Textf(c, "[%s] %s", formatDownloadStage(vm.dlProgress.Stage), vm.dlProgress.Name).
						FontSize(12).Bold().Grow(1).SingleLine()
					ui.Textf(c, "%.0f%%", vm.dlProgress.Percent).FontSize(11).TextColor(t.Accent)
				})
				ui.Progress(c, vm.dlProgress.Percent/100.0)
			})
		}

		ui.Scroll(c.Key("floating_task_scroll")).MaxHeight(listH).Padding(10, 14).Gap(10).Children(func() {
			if len(vm.tasks) == 0 && !vm.dlActive {
				ui.Text(c, "暂无进行中或最近完成的批量任务。").FontSize(12).TextColor(t.TextMuted)
			}
			shown := 0
			for i := len(vm.tasks) - 1; i >= 0 && shown < 6; i-- {
				tk := vm.tasks[i]
				shown++
				_, _, failed := tk.counts()

				ui.Column(c.Key("task_item_"+strconv.Itoa(tk.id))).Padding(8, 10).Radius(8).
					Background(workbenchPalette.SubSurfaceBg).Border(1, workbenchPalette.Hairline).Gap(6).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tk.title).FontSize(12).Bold().SingleLine()
						ui.Text(c, tk.summary()).FontSize(11).TextColor(t.TextMuted).Grow(1).SingleLine()
						if !tk.finished && tk.cancel != nil {
							if quietControl(c, "card_cancel_"+strconv.Itoa(tk.id), "取消", "").
								Height(24).Padding(2, 8).FontSize(11).TextColor(t.Danger).Clicked() {
								tk.cancel()
								tk.canceled = true
							}
						} else if tk.finished && failed > 0 && tk.retryable {
							if ui.Button(c.Key("card_retry_"+strconv.Itoa(tk.id)), "重试失败项").
								Height(24).Padding(2, 8).FontSize(11).Clicked() {
								vm.retryFailed(tk)
							}
						}
					})

					if !tk.finished && len(tk.steps) > 0 {
						for sIdx, step := range tk.steps {
							prefix := "○"
							col := t.TextMuted
							if step.done {
								prefix = "✓"
								col = t.Success
							} else if sIdx == tk.currentStep {
								prefix = "●"
								col = t.Accent
							}
							ui.Textf(c, "%s %d. %s", prefix, sIdx+1, step.label).FontSize(11).TextColor(col).SingleLine()
						}
						if tk.stageText != "" {
							ui.Text(c, tk.stageText).FontSize(11).TextColor(t.Accent).SingleLine()
						}
					} else {
						showAll := !tk.finished || len(tk.items) <= 4
						for _, it := range tk.items {
							if !showAll && it.status != itemFailed {
								continue
							}
							line := fmt.Sprintf("%s %s", statusMark(it.status), it.name)
							col := t.TextMuted
							switch it.status {
							case itemFailed:
								line += "：" + it.err
								col = t.Danger
							case itemOK:
								col = t.Success
							}
							ui.Text(c, line).FontSize(11).TextColor(col)
						}
					}
				})
			}
		})

		ui.Row(c).Padding(8, 14).Background(workbenchPalette.SubSurfaceBg).
			BorderWidth(1, 0, 0, 0).BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "运行日志 (%d 条)", len(vm.logs)).FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			if quietControl(c, "task_open_full_logs", "查看完整运行日志 →", "").Height(26).Padding(2, 8).FontSize(11).
				TextColor(t.AccentHover).Clicked() {
				vm.tasksOpen = false
				vm.page = pageLogs
			}
		})
	})
}

func (vm *viewModel) buildFloatingTaskCapsule(c *ui.Context, winW float32, lt *task) {
	t := c.Theme()
	compact := winW < 760

	bg := ui.RGBA(255, 255, 255, 242)
	edge := workbenchPalette.Hairline
	var hasFailed bool
	if lt != nil && lt.finished {
		_, _, failed := lt.counts()
		hasFailed = failed > 0
	}
	if hasFailed && !vm.dlActive {
		bg = workbenchPalette.DangerSoftBg
		edge = workbenchPalette.DangerSoftBorder
	}

	ui.Row(c.Key("floating_task_capsule")).Height(38).Padding(4, 14).Gap(8).Radius(19).
		Background(bg).Border(1, edge).Shadow(0, 6, 20, 0, ui.RGBA(15, 23, 42, 26)).
		AlignItems(ui.Center).Children(func() {
		switch {
		case vm.dlActive:
			ui.Spinner(c)
			label := fmt.Sprintf("[%s] %s · %.0f%%", formatDownloadStage(vm.dlProgress.Stage), vm.dlProgress.Name, vm.dlProgress.Percent)
			if compact {
				label = fmt.Sprintf("%s %.0f%%", formatDownloadStage(vm.dlProgress.Stage), vm.dlProgress.Percent)
			}
			if quietControl(c, "capsule_toggle_dl", label, "").Height(28).Padding(2, 6).FontSize(12).
				TextColor(t.AccentHover).MaxWidth(winW / 2).Clicked() {
				vm.tasksOpen = !vm.tasksOpen
			}
			if lt != nil && !lt.finished && lt.cancel != nil {
				if quietControl(c, "capsule_dl_cancel", "取消", "").Height(26).Padding(2, 8).FontSize(11).
					TextColor(t.Danger).Clicked() {
					lt.cancel()
					lt.canceled = true
				}
			}

		case lt != nil && !lt.finished:
			ui.Spinner(c)
			label := lt.title
			if len(lt.steps) > 0 && lt.stageText != "" {
				label = fmt.Sprintf("%s · %d/%d (%d%%)", lt.title, min(lt.currentStep+1, len(lt.steps)), len(lt.steps), lt.progressPct)
			} else if len(lt.items) > 0 {
				done, _, _ := lt.counts()
				label = fmt.Sprintf("%s · %d/%d", lt.title, done, len(lt.items))
			}
			if quietControl(c, "capsule_toggle_running", label, "").Height(28).Padding(2, 6).FontSize(12).
				TextColor(t.AccentHover).Clicked() {
				vm.tasksOpen = !vm.tasksOpen
			}
			if lt.cancel != nil {
				if quietControl(c, "capsule_cancel", "取消", "").Height(26).Padding(2, 8).FontSize(11).
					TextColor(t.Danger).Clicked() {
					lt.cancel()
					lt.canceled = true
				}
			}

		case hasFailed:
			_, _, failed := lt.counts()
			ui.Textf(c, "✖ %s · 失败 %d 项", lt.title, failed).FontSize(12).FontWeight(600).TextColor(t.Danger).SingleLine()
			if quietControl(c, "capsule_why", "查看失败原因", "").Height(26).Padding(2, 8).FontSize(11).
				TextColor(t.Danger).Clicked() {
				vm.tasksOpen = true
			}

		case lt != nil:
			if quietControl(c, "capsule_done", "✓ "+lt.title+" · "+lt.summary(), "").
				Height(28).Padding(2, 6).FontSize(11).Clicked() {
				vm.tasksOpen = !vm.tasksOpen
			}

		default:
			if quietControl(c, "capsule_idle", "任务 · 空闲", "").Height(28).Padding(2, 6).FontSize(11).Clicked() {
				vm.tasksOpen = !vm.tasksOpen
			}
		}
	})
}
