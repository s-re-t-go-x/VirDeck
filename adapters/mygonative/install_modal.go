package mygonative

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"virdeck/config"
	"virdeck/core/apk"
	"virdeck/core/catalog"
	"virdeck/core/service"
)

type installModalState struct {
	apkPath           string
	meta              service.APKMetadata
	metaErr           string
	inspecting        bool
	inspectSeq        int
	targetIDs         map[string]bool
	launchAndScreen   bool
	grantPermissions  bool
	userToggledLaunch bool
}

func (vm *viewModel) pickAndOpenInstallAPK(preferredIDs []string) {
	if len(vm.instances) == 0 {
		vm.showToast("当前还没有实例，请先新建实例或前往「镜像与环境」下载系统镜像")
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  vm.win,
			Title:   "选择要安装的 APK / 分包文件",
			Filters: []mygo.FileFilter{{Name: "Android 安装包 (*.apk, *.apks, *.xapk)", Extensions: []string{"apk", "apks", "xapk"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		apkPath := paths[0]
		vm.post(func() {
			vm.openInstallModalWithAPK(apkPath, preferredIDs)
		})
	}()
}

// handleDroppedFiles routes workbench file drops to APK installation or file transfer.
func (vm *viewModel) handleDroppedFiles(paths []string, pinnedIDs []string) {
	if len(paths) == 0 {
		return
	}
	var apks, normal []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			vm.showToast("当前暂不支持直接拖入文件夹，请选择 .apk / .apks / .xapk 或普通文件")
			return
		}
		ext := strings.ToLower(filepath.Ext(p))
		switch ext {
		case ".apkm":
			vm.showToast("当前暂不支持 .apkm 格式，请使用 .apk、.apks 或 .xapk")
			return
		case ".apk", ".apks", ".xapk":
			apks = append(apks, p)
		default:
			normal = append(normal, p)
		}
	}
	if len(apks) > 0 && len(normal) > 0 {
		vm.showToast("请将安装包与普通文件分开拖入")
		return
	}
	if len(apks) > 1 {
		vm.showToast("一次仅支持安装一个安装包（.apk / .apks / .xapk），请单个拖入")
		return
	}
	if len(apks) == 1 {
		vm.openInstallModalWithAPK(apks[0], pinnedIDs)
		return
	}
	if len(normal) > 0 {
		vm.routeDroppedFilesToTransfer(normal, pinnedIDs)
	}
}

func (vm *viewModel) routeDroppedFilesToTransfer(files []string, pinnedIDs []string) {
	var targets []service.Instance
	if len(pinnedIDs) > 0 {
		targets = planAction(actTransfer, vm.freshInstances(pinnedIDs), vm.busy).eligible
	}
	if len(targets) == 0 {
		targets = planAction(actTransfer, vm.selectedInsts(), vm.busy).eligible
	}
	if len(targets) == 0 {
		targets = planAction(actTransfer, vm.instances, vm.busy).eligible
	}
	if len(targets) == 0 {
		vm.showToast("请先启动至少一台运行中的实例，再发送普通文件到 Android")
		return
	}
	vm.openTransfer(targets)
	vm.transfer.mode = transferSendMode
	vm.transfer.localPaths = append([]string(nil), files...)
	vm.showToast(fmt.Sprintf("已将 %d 个文件加入「文件传输」待发送队列", len(files)))
}

func (vm *viewModel) openInstallModalWithAPK(apkPath string, preferredIDs []string) {
	if len(vm.instances) == 0 {
		vm.showToast("当前没有可用实例，请先点击「新建实例」或前往「镜像与环境」准备镜像")
		return
	}

	targetSet := make(map[string]bool)
	addValid := func(ids []string) {
		for _, id := range ids {
			if _, ok := vm.findInstance(id); ok {
				targetSet[id] = true
			}
		}
	}
	addValid(preferredIDs)
	if len(targetSet) == 0 {
		addValid(idsOf(vm.selectedInsts()))
	}
	if len(targetSet) == 0 {
		addValid(vm.lastInstallTargetIDs)
	}
	if len(targetSet) == 0 && len(vm.instances) == 1 {
		targetSet[vm.instances[0].ID] = true
	}

	seq := vm.install.inspectSeq + 1
	vm.install = installModalState{
		apkPath:           apkPath,
		inspecting:        true,
		inspectSeq:        seq,
		targetIDs:         targetSet,
		launchAndScreen:   len(targetSet) <= 1,
		grantPermissions:  false, // default OFF per design doc
		userToggledLaunch: false,
	}
	vm.showInstall = true

	svc := vm.svc
	go func() {
		meta, err := svc.InspectAPK(apkPath)
		vm.post(func() {
			if vm.install.inspectSeq != seq {
				return
			}
			vm.install.inspecting = false
			if err != nil {
				vm.install.metaErr = err.Error()
				return
			}
			vm.install.meta = meta
			vm.install.metaErr = ""
		})
	}()
}

func (vm *viewModel) checkInstanceAPKCompat(inst service.Instance, apkABIs []string) (bool, string) {
	if len(apkABIs) == 0 {
		return true, ""
	}
	var imgABI string
	compat := catalog.ARMCompatNone
	parts := strings.Split(inst.SystemImage, ";")
	if len(parts) >= 4 {
		imgABI = parts[3]
		compat = catalog.ImageARMCompat(parts[1], parts[2], parts[3])
	}
	for _, item := range vm.catalog {
		if item.ID == inst.SystemImage {
			if item.ABI != "" {
				imgABI = item.ABI
			}
			if item.ARMCompat != "" {
				compat = item.ARMCompat
			}
			break
		}
	}
	return apk.CheckARMCompatibility(apkABIs, inst.ABIList, imgABI, compat)
}

func (vm *viewModel) buildInstallModal(c *ui.Context) {
	_, h := c.Size()
	ui.Modal(c, &vm.showInstall, func() {
		t := c.Theme()
		s := &vm.install
		if s.targetIDs == nil {
			s.targetIDs = map[string]bool{}
		}

		modalContent(c, 540).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "安装并运行 APK").FontSize(16).Bold()
				ui.Spacer(c)
				if quietControl(c, "install_change_apk", "更换文件", "").Height(28).FontSize(11).Clicked() {
					var kept []string
					for _, inst := range vm.instances {
						if s.targetIDs[inst.ID] {
							kept = append(kept, inst.ID)
						}
					}
					vm.pickAndOpenInstallAPK(kept)
				}
			})

			// Pre-check metadata summary card.
			ui.Column(c).Padding(8, 12).Radius(8).Background(workbenchPalette.SubSurfaceBg).
				Border(1, workbenchPalette.Hairline).Gap(4).Children(func() {
				ui.Text(c, filepath.Base(s.apkPath)).FontSize(13).Bold().Tooltip(s.apkPath).SingleLine()
				if s.inspecting {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c)
						ui.Text(c, "正在离线解析安装包信息…").FontSize(11).TextColor(t.TextMuted)
					})
				} else if s.metaErr != "" {
					ui.Text(c, "离线预检提示："+s.metaErr).FontSize(11).TextColor(t.Warning)
				} else {
					abiText := "纯 Java / Kotlin（全架构兼容）"
					if len(s.meta.ABIs) > 0 {
						abiText = strings.Join(s.meta.ABIs, ", ")
					}
					summary := fmt.Sprintf("包名：%s · 原生库：%s", emptyPlaceholder(s.meta.Package, "未知"), abiText)
					if s.meta.IsSplit {
						summary = fmt.Sprintf("分包格式（%d 个子 APK）· %s", s.meta.SplitCount, summary)
					}
					ui.Text(c, summary).FontSize(11).TextColor(t.TextMuted).SingleLine()
					if s.meta.HasOBB {
						ui.Text(c, "提示：该分包内含 OBB 数据文件，装完如需补充数据可通过「文件传输」推送至 /sdcard/Android/obb/").
							FontSize(10).TextColor(t.Warning)
					}
				}
			})

			ui.Text(c, "选择目标实例（支持已停止或启动中实例，将自动开机并等待就绪）").
				FontSize(12).FontWeight(600)

			var selectedList []service.Instance
			stoppedCount := 0
			activeHostCount := 0
			maxConc := int(vm.settingsConcurrent)
			if maxConc <= 0 {
				maxConc = config.Values.Runtime.MaxConcurrent
			}

			ui.Scroll(c.Key("install_targets")).Height(min(float32(210), max(float32(90), h-360))).Gap(6).Children(func() {
				for _, inst := range vm.instances {
					inst := inst
					if inst.State == service.Running || inst.State == service.Starting {
						activeHostCount++
					}
					checked := s.targetIDs[inst.ID]
					compatOK, compatWarn := vm.checkInstanceAPKCompat(inst, s.meta.ABIs)
					stateHint := "已停止 · 将自动启动"
					switch inst.State {
					case service.Running:
						stateHint = "运行中"
					case service.Starting:
						stateHint = "启动中 · 将等待就绪"
					case service.Failed:
						stateHint = "上次异常 · 将尝试重新启动"
					}
					busyOp := vm.busy[inst.ID]
					disabled := busyOp != "" || (inst.State == service.Failed && inst.Port > 0)

					ui.Column(c).Padding(6, 10).Radius(6).Background(t.Surface).Gap(3).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							if ui.Checkbox(c.Key("install_target_"+inst.ID), &checked, inst.Name).
								Disabled(disabled).Changed() {
								if checked {
									s.targetIDs[inst.ID] = true
								} else {
									delete(s.targetIDs, inst.ID)
								}
								if !s.userToggledLaunch {
									s.launchAndScreen = len(s.targetIDs) <= 1
								}
							}
							ui.Spacer(c)
							ui.Text(c, stateHint).FontSize(11).TextColor(stateColor(t, inst.State)).SingleLine()
						})
						if !inst.Headless {
							ui.Text(c, "自带窗口模式：启动后将直接显示原生窗口（不额外打开 scrcpy）").
								FontSize(10).TextColor(t.TextMuted).MarginStart(24)
						}
						if busyOp != "" {
							ui.Textf(c, "正在执行「%s」", busyOp).FontSize(10).TextColor(t.Warning).MarginStart(24)
						} else if !compatOK {
							ui.Text(c, "⚠ "+compatWarn).FontSize(10).TextColor(t.Warning).MarginStart(24)
						}
					})
					if s.targetIDs[inst.ID] && !disabled {
						selectedList = append(selectedList, inst)
						if inst.State == service.Stopped || inst.State == service.Failed {
							stoppedCount++
						}
					}
				}
			})

			if activeHostCount+stoppedCount > maxConc {
				ui.Textf(c, "⚠ 预计同时运行实例将达到 %d 台，超过并发上限（%d 台），部分停机实例可能因端口/并发上限无法同时启动。",
					activeHostCount+stoppedCount, maxConc).FontSize(11).TextColor(t.Warning)
			}

			ui.Column(c).Gap(4).Children(func() {
				if ui.Checkbox(c.Key("install_opt_launch"), &s.launchAndScreen, "装完自动打开应用并投屏").Changed() {
					s.userToggledLaunch = true
				}
				ui.Checkbox(c.Key("install_opt_grant"), &s.grantPermissions,
					"安装时自动授予常规运行时权限 (-g，不含悬浮窗/所有文件访问等特殊权限)")
			})

			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_install"), "取消").Clicked() {
					vm.showInstall = false
				}
				startLabel := "开始安装"
				if len(selectedList) > 0 {
					startLabel = fmt.Sprintf("开始安装 (%d 台)", len(selectedList))
				}
				if ui.PrimaryButton(c.Key("confirm_install"), startLabel).Disabled(len(selectedList) == 0).Clicked() {
					vm.startInstallPipeline(selectedList, s.apkPath, s.grantPermissions, s.launchAndScreen)
					vm.showInstall = false
				}
			})
		})
	})
}

func (vm *viewModel) startInstallPipeline(targets []service.Instance, apkPath string, grantPerms, launchAndScreen bool) {
	vm.lastInstallTargetIDs = idsOf(targets)
	steps := []taskStep{
		{label: "启动与引导就绪"},
		{label: "安装 APK"},
	}
	if launchAndScreen {
		steps = append(steps, taskStep{label: "拉起应用"}, taskStep{label: "打开投屏"})
	}
	tk := vm.startTask("安装应用", actInstallAPK, targetsOf(targets), false, func(ctx context.Context, tg target) error {
		return vm.svc.InstallApp(ctx, service.InstallAppRequest{
			InstanceIDs:      []string{tg.id},
			APKPath:          apkPath,
			GrantPermissions: grantPerms,
			LaunchApp:        launchAndScreen,
			OpenScreen:       launchAndScreen,
		})
	}, nil)
	if tk != nil {
		tk.steps = steps
		tk.stageText = "准备安装 " + filepath.Base(apkPath)
	}
}

func (vm *viewModel) launchRecentApp(inst service.Instance, app service.RecentApp) {
	if vm.busy[inst.ID] != "" {
		return
	}
	vm.startTask("启动应用", actInstallAPK, targetsOf([]service.Instance{inst}), false, func(ctx context.Context, tg target) error {
		return vm.svc.LaunchApp(ctx, tg.id, app.Package, app.Component)
	}, nil)
}

func emptyPlaceholder(val, fallback string) string {
	if strings.TrimSpace(val) == "" {
		return fallback
	}
	return val
}
