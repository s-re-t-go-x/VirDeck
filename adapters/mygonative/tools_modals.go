package mygonative

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"virdeck/config"
	"virdeck/core/service"
)

type geoCityPreset struct {
	name     string
	lat, lon string
}

var geoCityPresets = []geoCityPreset{
	{name: "北京", lat: "39.904200", lon: "116.407400"},
	{name: "上海", lat: "31.230400", lon: "121.473700"},
	{name: "东京", lat: "35.676200", lon: "139.650300"},
	{name: "纽约", lat: "40.712800", lon: "-74.006000"},
	{name: "伦敦", lat: "51.507400", lon: "-0.127800"},
	{name: "新加坡", lat: "1.352100", lon: "103.819800"},
}

func (vm *viewModel) openLocationModal(targets []service.Instance) {
	running := make([]service.Instance, 0, len(targets))
	for _, inst := range targets {
		if inst.State == service.Running {
			running = append(running, inst)
		}
	}
	if len(running) == 0 {
		vm.showToast("请先选择至少一台运行中的实例")
		return
	}
	vm.locTargets = running
	if vm.locLat == "" {
		vm.locLat = geoCityPresets[0].lat
	}
	if vm.locLon == "" {
		vm.locLon = geoCityPresets[0].lon
	}
	vm.locError = ""
	vm.locBusy = false
	vm.showLocation = true
}

func (vm *viewModel) buildLocationModal(c *ui.Context) {
	ui.Modal(c, &vm.showLocation, func() {
		t := c.Theme()
		modalContent(c, 500).Gap(12).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				glyph(c, "network", 18).TextColor(t.Accent)
				ui.Text(c, "虚拟定位").FontSize(16).Bold()
			})
			targetDesc := fmt.Sprintf("将对 %d 台运行中实例注入模拟 GPS 坐标", len(vm.locTargets))
			if len(vm.locTargets) == 1 {
				targetDesc = "目标实例：" + vm.locTargets[0].Name
			}
			ui.Text(c, targetDesc).FontSize(12).TextColor(t.TextMuted)

			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "常用城市预设").FontSize(11).TextColor(t.TextMuted)
				ui.Row(c).Gap(6).Wrap().Children(func() {
					for i, preset := range geoCityPresets {
						preset := preset
						if ui.Button(c.Key("geo_preset_"+strconv.Itoa(i)), preset.name).
							Height(26).Padding(2, 10).FontSize(11).Disabled(vm.locBusy).Clicked() {
							vm.locLat = preset.lat
							vm.locLon = preset.lon
							vm.locError = ""
						}
					}
				})
			})

			ui.Form(c, func() {
				ui.Field(c, "纬度 (Lat)", func() {
					ui.TextInput(c.Key("loc_lat"), &vm.locLat).Placeholder("-90 ~ 90，例如 39.904200")
				})
				ui.Field(c, "经度 (Lon)", func() {
					ui.TextInput(c.Key("loc_lon"), &vm.locLon).Placeholder("-180 ~ 180，例如 116.407400")
				})
			})

			if vm.locError != "" {
				ui.Text(c, vm.locError).FontSize(12).TextColor(t.Danger)
			}

			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("loc_cancel"), "取消").Disabled(vm.locBusy).Clicked() {
					vm.showLocation = false
				}
				if ui.PrimaryButton(c.Key("loc_apply"), "应用定位").Disabled(vm.locBusy).Clicked() {
					lat, errLat := strconv.ParseFloat(strings.TrimSpace(vm.locLat), 64)
					lon, errLon := strconv.ParseFloat(strings.TrimSpace(vm.locLon), 64)
					if errLat != nil || errLon != nil || math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(lon) || math.IsInf(lon, 0) {
						vm.locError = "请输入合法的十进制经纬度数值"
						return
					}
					if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
						vm.locError = "纬度范围需为 -90..90，经度范围需为 -180..180"
						return
					}
					ids := idsOf(vm.locTargets)
					vm.locBusy = true
					vm.locError = ""
					go func() {
						err := vm.svc.SetLocation(context.Background(), ids, lat, lon)
						vm.post(func() {
							vm.locBusy = false
							if err != nil {
								vm.locError = config.RedactError(err)
								return
							}
							vm.showLocation = false
							vm.showToast(fmt.Sprintf("已对 %d 台实例应用虚拟定位 (%.4f, %.4f)", len(ids), lat, lon))
						})
					}()
				}
			})
		})
	})
}

func (vm *viewModel) runScreenshots(targets []service.Instance) {
	running := make([]service.Instance, 0, len(targets))
	for _, inst := range targets {
		if inst.State == service.Running {
			running = append(running, inst)
		}
	}
	if len(running) == 0 {
		vm.showToast("没有可截图的运行中实例")
		return
	}
	ids := idsOf(running)
	go func() {
		paths, err := vm.svc.CaptureScreenshots(context.Background(), ids, "")
		vm.post(func() {
			if err != nil && len(paths) == 0 {
				vm.showToast("截图失败：" + config.RedactError(err))
				return
			}
			if err != nil {
				vm.showToast(fmt.Sprintf("已保存 %d 张截图，部分失败：%s", len(paths), config.RedactError(err)))
				return
			}
			vm.showToast(fmt.Sprintf("已保存 %d 张截图（screenshots 目录）", len(paths)))
		})
	}()
}

func (vm *viewModel) openAppsModal(inst service.Instance) {
	vm.appsSeq++
	vm.appsTarget = inst
	vm.appsQuery = ""
	vm.appsThirdPartyOnly = true
	vm.appsList = nil
	vm.appsError = ""
	vm.appsNotice = ""
	vm.appsConfirmPkg = ""
	vm.appsConfirmOp = ""
	vm.appsBusy = false
	vm.showApps = true
	vm.refreshAppsList()
}

func (vm *viewModel) refreshAppsList() {
	targetID := vm.appsTarget.ID
	if targetID == "" {
		return
	}
	vm.appsSeq++
	seq := vm.appsSeq
	thirdOnly := vm.appsThirdPartyOnly
	vm.appsLoading = true
	vm.appsError = ""
	go func() {
		list, err := vm.svc.ListInstalledApps(context.Background(), targetID, thirdOnly)
		vm.post(func() {
			if vm.appsSeq != seq || vm.appsTarget.ID != targetID || !vm.showApps {
				return
			}
			vm.appsLoading = false
			if err != nil {
				vm.appsError = config.RedactError(err)
				return
			}
			vm.appsList = list
		})
	}()
}

func (vm *viewModel) execAppOp(pkg, op, doneMsg string) {
	targetID := vm.appsTarget.ID
	if targetID == "" || vm.appsBusy {
		return
	}
	vm.appsBusy = true
	vm.appsError = ""
	vm.appsNotice = ""
	vm.appsConfirmPkg = ""
	vm.appsConfirmOp = ""
	go func() {
		err := vm.svc.ManageInstalledApp(context.Background(), targetID, pkg, op)
		vm.post(func() {
			vm.appsBusy = false
			if err != nil {
				vm.appsError = config.RedactError(err)
				return
			}
			vm.appsNotice = doneMsg
			if op == "uninstall" {
				vm.refreshAppsList()
			}
		})
	}()
}

func (vm *viewModel) buildAppsModal(c *ui.Context) {
	_, h := c.Size()
	ui.Modal(c, &vm.showApps, func() {
		t := c.Theme()
		modalContent(c, 560).Gap(10).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				glyph(c, "device", 18).TextColor(t.Accent)
				ui.Text(c, "应用管理").FontSize(16).Bold()
			})
			ui.Text(c, "目标实例："+vm.appsTarget.Name).FontSize(12).TextColor(t.TextMuted)

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("apps_query"), &vm.appsQuery).Placeholder("按包名搜索，例如 com.example").Grow(1)
				if ui.Checkbox(c.Key("apps_third_only"), &vm.appsThirdPartyOnly, "仅第三方应用").Disabled(vm.appsLoading || vm.appsBusy).Changed() {
					vm.refreshAppsList()
				}
				if ui.Button(c.Key("apps_refresh"), "刷新").Height(30).Disabled(vm.appsLoading || vm.appsBusy).Clicked() {
					vm.refreshAppsList()
				}
			})

			if vm.appsConfirmPkg != "" {
				actionText := "清除数据"
				if vm.appsConfirmOp == "uninstall" {
					actionText = "卸载"
				}
				ui.Row(c).Padding(8, 10).Gap(8).Radius(6).Background(t.Selection).AlignItems(ui.Center).Children(func() {
					ui.Textf(c, "确认对 %s 执行「%s」？", vm.appsConfirmPkg, actionText).FontSize(12).Grow(1)
					if ui.Button(c.Key("apps_confirm_ok"), "确认执行").TextColor(t.Danger).Height(26).Disabled(vm.appsBusy).Clicked() {
						vm.execAppOp(vm.appsConfirmPkg, vm.appsConfirmOp, fmt.Sprintf("已对 %s 执行%s", vm.appsConfirmPkg, actionText))
					}
					if ui.Button(c.Key("apps_confirm_cancel"), "取消").Height(26).Disabled(vm.appsBusy).Clicked() {
						vm.appsConfirmPkg = ""
						vm.appsConfirmOp = ""
					}
				})
			}

			q := strings.ToLower(strings.TrimSpace(vm.appsQuery))
			filtered := make([]service.InstalledApp, 0, len(vm.appsList))
			for _, app := range vm.appsList {
				if q == "" || strings.Contains(strings.ToLower(app.PackageName), q) {
					filtered = append(filtered, app)
				}
			}

			ui.Scroll(c.Key("apps_scroll")).Height(min(280, max(120, h-320))).Gap(6).Children(func() {
				if vm.appsLoading {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c)
						ui.Text(c, "正在读取已安装应用列表…").FontSize(12).TextColor(t.TextMuted)
					})
					return
				}
				if len(filtered) == 0 {
					ui.Text(c, "没有匹配的应用包名").FontSize(12).TextColor(t.TextMuted)
					return
				}
				for _, app := range filtered {
					app := app
					ui.Row(c).Padding(6, 8).Gap(6).Radius(6).BorderWidth(0, 0, 1, 0).BorderColor(workbenchPalette.Hairline).
						AlignItems(ui.Center).Children(func() {
						ui.Text(c, app.PackageName).FontSize(12).Grow(1).SingleLine()
						if ui.Button(c.Key("app_launch_"+app.PackageName), "启动").Height(26).Padding(2, 8).FontSize(11).Disabled(vm.appsBusy).Clicked() {
							vm.execAppOp(app.PackageName, "launch", "已启动 "+app.PackageName)
						}
						if ui.Button(c.Key("app_stop_"+app.PackageName), "强停").Height(26).Padding(2, 8).FontSize(11).Disabled(vm.appsBusy).Clicked() {
							vm.execAppOp(app.PackageName, "stop", "已强停 "+app.PackageName)
						}
						if ui.Button(c.Key("app_clear_"+app.PackageName), "清数据").Height(26).Padding(2, 8).FontSize(11).Disabled(vm.appsBusy).Clicked() {
							vm.appsConfirmPkg = app.PackageName
							vm.appsConfirmOp = "clear"
						}
						if ui.Button(c.Key("app_uninst_"+app.PackageName), "卸载").Height(26).Padding(2, 8).FontSize(11).TextColor(t.Danger).Disabled(vm.appsBusy).Clicked() {
							vm.appsConfirmPkg = app.PackageName
							vm.appsConfirmOp = "uninstall"
						}
					})
				}
			})

			if vm.appsError != "" {
				ui.Text(c, vm.appsError).FontSize(12).TextColor(t.Danger)
			} else if vm.appsNotice != "" {
				ui.Text(c, vm.appsNotice).FontSize(12).TextColor(t.Success)
			}

			ui.Row(c).Children(func() {
				ui.Textf(c, "共 %d 个应用", len(filtered)).FontSize(11).TextColor(t.TextMuted)
				ui.Spacer(c)
				if ui.Button(c.Key("apps_close"), "关闭").Clicked() {
					vm.showApps = false
				}
			})
		})
	})
}

func (vm *viewModel) runDiskClean(inst service.Instance) {
	if vm.busy[inst.ID] != "" {
		vm.showToast("实例当前有其他任务正在执行")
		return
	}
	vm.busy[inst.ID] = "磁盘清理"
	go func() {
		res, err := vm.svc.CleanInstanceDisk(context.Background(), inst.ID)
		vm.post(func() {
			delete(vm.busy, inst.ID)
			if err != nil {
				vm.showToast("磁盘清理失败：" + err.Error())
				return
			}
			vm.showToast(res.Message)
		})
	}()
}

func (vm *viewModel) pickBackupSave(defaultName string) (string, bool) {
	if vm.pickBackupSavePath != nil {
		return vm.pickBackupSavePath(defaultName)
	}
	out, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:            vm.win,
		Title:             "导出实例备份 (.zip)",
		DefaultPath:       defaultName,
		Filters:           []mygo.FileFilter{{Name: "VirDeck 实例备份 (*.zip)", Extensions: []string{"zip"}}},
		CreateDirectories: true,
	})
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return out, true
}

func (vm *viewModel) pickBackupOpen() (string, bool) {
	if vm.pickBackupOpenPath != nil {
		return vm.pickBackupOpenPath()
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:  vm.win,
		Title:   "选择要还原的实例备份 (.zip)",
		Filters: []mygo.FileFilter{{Name: "VirDeck 实例备份 (*.zip)", Extensions: []string{"zip"}}},
	})
	if err != nil || len(paths) == 0 || strings.TrimSpace(paths[0]) == "" {
		return "", false
	}
	return paths[0], true
}

func sanitizeBackupFileStem(name, fallback string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	stem := strings.Trim(b.String(), ". ")
	if stem == "" || stem == "." || stem == ".." {
		stem = strings.Trim(strings.TrimSpace(fallback), ". ")
	}
	if stem == "" {
		return "instance"
	}
	return stem
}

func (vm *viewModel) openBackupModal(inst service.Instance) {
	if vm.busy[inst.ID] != "" {
		vm.showToast("实例当前有其他任务正在执行")
		return
	}
	vm.backupTarget = inst
	vm.backupIncludeProxy = false
	vm.showBackup = true
}

func (vm *viewModel) buildBackupModal(c *ui.Context) {
	ui.Modal(c, &vm.showBackup, func() {
		t := c.Theme()
		modalContent(c, 480).Gap(12).Children(func() {
			ui.Text(c, "备份实例").FontSize(16).Bold()
			ui.Text(c, "目标实例："+vm.backupTarget.Name).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "默认不导出网络代理配置，以免分享备份时意外泄露代理账号或密码。").FontSize(12).TextColor(t.TextMuted)
			ui.Checkbox(c.Key("backup_include_proxy"), &vm.backupIncludeProxy, "包含网络代理配置（含敏感凭据，分享备份时请勿勾选）")
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("backup_cancel"), "取消").Clicked() {
					vm.showBackup = false
				}
				if ui.PrimaryButton(c.Key("backup_confirm"), "选择保存位置…").Clicked() {
					target := vm.backupTarget
					includeProxy := vm.backupIncludeProxy
					vm.showBackup = false
					vm.runBackupInstance(target, includeProxy)
				}
			})
		})
	})
}

func (vm *viewModel) openRestoreModal() {
	vm.restoreName = ""
	vm.restoreIncludeProxy = false
	vm.showRestore = true
}

func (vm *viewModel) buildRestoreModal(c *ui.Context) {
	ui.Modal(c, &vm.showRestore, func() {
		t := c.Theme()
		modalContent(c, 480).Gap(12).Children(func() {
			ui.Text(c, "从备份还原").FontSize(16).Bold()
			ui.Text(c, "默认忽略备份包内的代理设置，避免导入第三方备份时流量被劫持。").FontSize(12).TextColor(t.TextMuted)
			ui.Form(c, func() {
				ui.Field(c, "新实例名称", func() {
					ui.TextInput(c.Key("restore_name"), &vm.restoreName).Placeholder("可选，留空自动生成")
				})
			})
			ui.Checkbox(c.Key("restore_include_proxy"), &vm.restoreIncludeProxy, "继承备份中的网络代理配置（仅限本人可信备份）")
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("restore_cancel"), "取消").Clicked() {
					vm.showRestore = false
				}
				if ui.PrimaryButton(c.Key("restore_confirm"), "选择备份文件…").Clicked() {
					name := strings.TrimSpace(vm.restoreName)
					includeProxy := vm.restoreIncludeProxy
					vm.showRestore = false
					vm.runRestoreInstance(name, includeProxy)
				}
			})
		})
	})
}

func (vm *viewModel) runBackupInstance(inst service.Instance, includeProxy bool) {
	if vm.busy[inst.ID] != "" {
		vm.showToast("实例当前有其他任务正在执行")
		return
	}
	defaultName := sanitizeBackupFileStem(inst.Name, inst.ID) + "_backup.zip"
	vm.busy[inst.ID] = "备份实例"
	go func() {
		destZip, ok := vm.pickBackupSave(defaultName)
		if !ok {
			vm.post(func() {
				delete(vm.busy, inst.ID)
			})
			return
		}
		res, err := vm.svc.BackupInstanceWithOptions(context.Background(), inst.ID, destZip, service.BackupOptions{IncludeProxy: includeProxy})
		vm.post(func() {
			delete(vm.busy, inst.ID)
			if err != nil {
				vm.showToast("备份失败：" + config.RedactError(err))
				return
			}
			vm.showToast(fmt.Sprintf("已备份至 %s (%.1f MB)", filepath.Base(res.Path), float64(res.SizeBytes)/(1024*1024)))
		})
	}()
}

func (vm *viewModel) runRestoreInstance(nameOverride string, includeProxy bool) {
	go func() {
		srcZip, ok := vm.pickBackupOpen()
		if !ok {
			return
		}
		inst, err := vm.svc.RestoreInstanceWithOptions(context.Background(), srcZip, nameOverride, service.RestoreOptions{IncludeProxy: includeProxy})
		if err != nil {
			vm.post(func() {
				vm.showToast("还原备份失败：" + config.RedactError(err))
			})
			return
		}
		all, _ := vm.svc.List(context.Background())
		vm.post(func() {
			if len(all) > 0 {
				vm.instances = all
			} else {
				vm.instances = append(vm.instances, inst)
			}
			vm.refreshGroups()
			vm.refreshVisible()
			vm.selected.Clear()
			vm.selected.Add(inst.ID)
			vm.showToast("已还原实例：" + inst.Name)
		})
	}()
}
