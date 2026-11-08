package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"virdeck/core/service"
)

func cacheSize(n int64) string {
	const mb = 1024 * 1024
	if n >= 1024*mb {
		return fmt.Sprintf("%.2f GB", float64(n)/(1024*mb))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/mb)
}

func (vm *viewModel) buildCleanupTab(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "扫描后按需清理，已安装组件、系统镜像和实例应用数据保持不变。").FontSize(12).TextColor(t.TextMuted)
	if ui.Button(c.Key("scan_cleanup"), "扫描可清理文件").Disabled(vm.cleanupBusy).Clicked() {
		vm.cleanupBusy, vm.cleanupMessage = true, "正在扫描…"
		vm.startTask("扫描缓存", actCreate, []target{{name: "下载与 AVD 缓存"}}, false, func(ctx context.Context, _ target) error {
			report, err := vm.svc.ScanCleanup(ctx)
			vm.post(func() {
				if err != nil {
					vm.cleanupMessage = "扫描失败：" + err.Error()
					return
				}
				vm.cleanupReport, vm.cleanupScanned, vm.cleanupMessage = report, true, ""
				if report.AVDBlocked {
					vm.cleanupAVD = false
				}
			})
			return err
		}, func(_ *task) { vm.cleanupBusy = false })
	}
	if vm.cleanupScanned {
		row := func(key, label, detail string, selected *bool, size service.CleanupSize, disabled bool) {
			ui.Column(c).Padding(8, 10).Gap(3).Radius(6).Background(t.Surface).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Checkbox(c.Key(key), selected, label).Disabled(disabled || vm.cleanupBusy)
					ui.Spacer(c)
					ui.Textf(c, "%d 个文件 · %s", size.Files, cacheSize(size.Bytes)).FontSize(11).TextColor(t.TextMuted)
				})
				ui.Text(c, detail).FontSize(11).TextColor(t.TextMuted)
			})
		}
		row("clean_downloads", "下载压缩包", "删除 downloads 内已完成的 .zip；重新安装时需重新下载。", &vm.cleanupDownloads, vm.cleanupReport.Downloads, false)
		row("clean_partials", "未完成下载", "删除 .part / .meta；下次下载将从头开始。", &vm.cleanupPartials, vm.cleanupReport.Partials, false)
		detail := "仅清理 cache.img 缓存分区及其差分文件，启动时重新生成。"
		if vm.cleanupReport.AVDBlocked {
			detail = vm.cleanupReport.AVDBlockReason
			if detail == "" {
				detail = "请先停止全部实例后重新扫描。"
			}
		}
		row("clean_avd", "AVD 缓存分区", detail, &vm.cleanupAVD, vm.cleanupReport.AVDCache, vm.cleanupReport.AVDBlocked)
		ui.Text(c, "显示文件逻辑大小；稀疏磁盘的实际占用可能更小。此操作不压缩用户磁盘。").FontSize(11).TextColor(t.TextMuted)
		selected := vm.cleanupDownloads && vm.cleanupReport.Downloads.Files > 0 || vm.cleanupPartials && vm.cleanupReport.Partials.Files > 0 || vm.cleanupAVD && vm.cleanupReport.AVDCache.Files > 0
		if ui.Button(c.Key("run_cleanup"), "清理选中项").Disabled(vm.cleanupBusy || vm.dlActive || !selected).Clicked() {
			opts := service.CleanupOptions{Downloads: vm.cleanupDownloads, Partials: vm.cleanupPartials, AVDCache: vm.cleanupAVD}
			vm.cleanupBusy, vm.cleanupMessage = true, "正在清理…"
			vm.startTask("清理缓存", actDelete, []target{{name: "选中缓存项"}}, false, func(ctx context.Context, _ target) error {
				removed, err := vm.svc.CleanCaches(ctx, opts)
				report, scanErr := vm.svc.ScanCleanup(ctx)
				vm.post(func() {
					vm.cleanupMessage = fmt.Sprintf("已清理 %d 个文件，累计文件大小 %s。", removed.Files, cacheSize(removed.Bytes))
					if err != nil {
						vm.cleanupMessage += " " + err.Error()
					}
					if scanErr == nil {
						vm.cleanupReport = report
					} else {
						vm.cleanupScanned = false
					}
				})
				return err
			}, func(_ *task) { vm.cleanupBusy = false })
		}
	}
	if vm.cleanupMessage != "" {
		ui.Text(c, vm.cleanupMessage).FontSize(12).TextColor(t.Accent)
	}
	ui.Text(c, "日志现状：界面日志只保存在内存中，当前没有日志文件。").FontSize(11).TextColor(t.TextMuted)
}
