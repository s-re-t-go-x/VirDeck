package mygonative

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (vm *viewModel) buildLogsPage(c *ui.Context) {
	t := c.Theme()
	filtered := vm.filteredLogLines()

	ui.Column(c).Grow(1).Children(func() {
		// Page header.
		ui.Row(c).Height(64).Padding(12, 22).Gap(10).BorderWidth(0, 0, 1, 0).
			BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "运行日志").FontSize(20).Bold().TextColor(t.Text)
			softBadge(c, logCountLabel(len(vm.logs)), t.TextMuted, workbenchPalette.StoppedSoftBg)
			ui.Spacer(c)
			if ui.Button(c.Key("copy_log"), "复制日志").Disabled(len(vm.logs) == 0).Clicked() {
				vm.writeClipboard(strings.Join(vm.logs, "\n"))
				vm.showToast("已复制全部运行日志到剪贴板")
			}
			if ui.Button(c.Key("clear_log"), "清空日志").Disabled(len(vm.logs) == 0).Clicked() {
				vm.logs = nil
			}
		})

		// Filter + search bar.
		ui.Row(c).Height(48).Padding(6, 22).Gap(10).BorderWidth(0, 0, 1, 0).
			BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			ui.Tabs(c, &vm.logFilter, "全部日志", "仅看错误", "流水线 / 任务")
			ui.Spacer(c)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				glyph(c, "search", 14).TextColor(t.TextMuted)
				ui.TextInput(c, &vm.logQuery).Placeholder("筛选日志关键词…").Width(180).Height(30).FontSize(11)
			})
		})

		if len(filtered) == 0 {
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				glyph(c, "logs", 28).TextColor(t.TextMuted)
				msg := "暂无运行日志，启动实例或执行安装流水线后会在此记录。"
				if len(vm.logs) > 0 {
					msg = "当前筛选条件下无匹配日志。"
				}
				ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted)
			})
			return
		}

		ui.Column(c).Grow(1).Padding(10, 22).Children(func() {
			ui.Column(c).Grow(1).Radius(8).Background(workbenchPalette.SubSurfaceBg).
				Border(1, workbenchPalette.Hairline).Padding(6, 0).Children(func() {
				ui.List(c, &vm.logState, len(filtered), func(i int) {
					if i < 0 || i >= len(filtered) {
						return
					}
					line := filtered[i]
					col := t.Text
					if isErrorLogLine(line) {
						col = t.Danger
					} else if strings.Contains(line, "[流水线]") || strings.Contains(line, "开始任务") || strings.Contains(line, "任务「") {
						col = t.AccentHover
					}
					ui.Text(c, line).FontSize(12).TextColor(col).Padding(4, 14)
				}).Grow(1)
			})
		})
	})
}

func (vm *viewModel) filteredLogLines() []string {
	q := strings.ToLower(strings.TrimSpace(vm.logQuery))
	if vm.logFilter == 0 && q == "" {
		return vm.logs
	}
	var out []string
	for _, line := range vm.logs {
		switch vm.logFilter {
		case 1:
			if !isErrorLogLine(line) {
				continue
			}
		case 2:
			if !strings.Contains(line, "[流水线]") &&
				!strings.Contains(line, "开始任务") &&
				!strings.Contains(line, "任务「") &&
				!strings.Contains(line, "安装") {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(line), q) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func logCountLabel(n int) string {
	return strings.ReplaceAll("N 条", "N", itoaSimple(n))
}

func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
