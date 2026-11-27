package mygonative

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"virdeck/config"
	"virdeck/core/catalog"
	"virdeck/core/service"
)

const (
	envTabImages  = 0
	envTabRuntime = 1
	envTabCleanup = 2
)

func (vm *viewModel) buildImagesPage(c *ui.Context) {
	t := c.Theme()
	checks := buildEnvChecks(vm.env)
	blocking, _ := envIssues(checks)

	ui.Column(c).Grow(1).Children(func() {
		// Page header.
		ui.Row(c).Height(64).Padding(12, 22).Gap(10).BorderWidth(0, 0, 1, 0).
			BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "镜像与环境").FontSize(20).Bold().TextColor(t.Text)
			softBadge(c, envHealthText(vm.envLoaded, checks), vm.healthColor(c), workbenchPalette.SubSurfaceBg)
			ui.Spacer(c)
			if vm.envTab == envTabRuntime {
				missingTools := vm.env.EmulatorPath == "" || vm.env.ADBPath == "" || vm.env.ScrcpyPath == ""
				if ui.Button(c.Key("dl_all_tools"), "一键下载补齐缺失工具").
					Disabled(!missingTools || vm.dlActive).Clicked() {
					vm.startInstallAllBaseTools()
				}
			}
			if ui.Button(c.Key("recheck_env"), "重新检测").Clicked() {
				vm.refreshEnv()
			}
		})

		// Top 3-Tab switcher.
		ui.Row(c).Height(48).Padding(6, 22).Gap(12).BorderWidth(0, 0, 1, 0).
			BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			ui.Tabs(c, &vm.envTab, "系统镜像", "运行环境", "空间清理")
		})

		if vm.dlActive {
			ui.Row(c).Margin(10, 22, 0, 22).Padding(8, 12).Gap(8).Radius(8).
				Background(workbenchPalette.AccentSoftBg).Border(1, workbenchPalette.AccentSoftBorder).
				AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Textf(c, "正在处理 %s (%s)", vm.dlProgress.Name, formatDownloadStage(vm.dlProgress.Stage)).
					FontSize(12).Bold().Grow(1).SingleLine()
				ui.Progress(c, vm.dlProgress.Percent/100.0).Width(160)
				ui.Textf(c, "%.1f%%", vm.dlProgress.Percent).FontSize(12).TextColor(t.Accent)
			})
		}

		if vm.envTab == envTabRuntime && vm.envLoaded && blocking == 0 {
			ui.Row(c).Margin(10, 22, 0, 22).Padding(8, 12).Gap(10).Radius(8).
				Background(workbenchPalette.SuccessSoftBg).AlignItems(ui.Center).Children(func() {
				glyph(c, "check", 15).TextColor(t.Success)
				ui.Text(c, "核心运行环境已就绪，可以下载系统镜像或新建实例。").
					FontSize(12).TextColor(t.Success).Grow(1).SingleLine()
				if quietControl(c, "env_goto_images", "前往系统镜像 →", "").Height(26).Padding(2, 10).
					TextColor(t.Success).Clicked() {
					vm.envTab = envTabImages
				}
			})
		}

		ui.Scroll(c.Key("images_page_scroll")).Grow(1).Padding(16, 22).Gap(12).Children(func() {
			switch vm.envTab {
			case envTabImages:
				vm.buildImageList(c)
			case envTabRuntime:
				vm.buildEnvChecksList(c, checks)
			case envTabCleanup:
				vm.buildCleanupTab(c)
			}
		})
	})
}

func (vm *viewModel) redirectToPureASCIIDataDir(custom bool) {
	if !custom {
		suggested := vm.svc.SuggestPureASCIIDataDir()
		if _, err := vm.svc.SetDataDirOverride(context.Background(), suggested); err != nil {
			vm.showToast("切换纯英文目录失败：" + err.Error())
			return
		}
		vm.showEnvMissing = false
		vm.showToast("✓ 已配置纯英文数据目录 " + suggested + "，重启 VirDeck 后生效")
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            vm.win,
			Title:             "选择纯英文/数字的数据目录",
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		chosen := paths[0]
		vm.post(func() {
			if _, err := vm.svc.SetDataDirOverride(context.Background(), chosen); err != nil {
				vm.showToast("目录不可用：" + err.Error())
				return
			}
			vm.showEnvMissing = false
			vm.showToast("✓ 已配置纯英文数据目录 " + chosen + "，重启 VirDeck 后生效")
		})
	}()
}

func (vm *viewModel) buildEnvChecksList(c *ui.Context, checks []envCheck) {
	t := c.Theme()
	if vm.env.PathWarning != "" {
		ui.Row(c).Padding(8, 12).Radius(8).Gap(8).Wrap().Background(workbenchPalette.DangerSoftBg).
			Border(1, workbenchPalette.DangerSoftBorder).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "! "+vm.env.PathWarning).FontSize(12).TextColor(t.Danger).Grow(1)
			if !vm.env.PathASCIIOK {
				if ui.Button(c.Key("env_redirect_auto"), "一键改用纯英文数据目录").Clicked() {
					vm.redirectToPureASCIIDataDir(false)
				}
				if quietControl(c, "env_redirect_custom", "自选纯英文目录", "").Clicked() {
					vm.redirectToPureASCIIDataDir(true)
				}
			}
		})
	} else if vm.env.EffectiveDataDir != "" && vm.env.EffectiveDataDir != vm.env.DataDir {
		ui.Row(c).Padding(8, 12).Radius(8).Gap(8).Background(workbenchPalette.SuccessSoftBg).
			AlignItems(ui.Center).Children(func() {
			glyph(c, "check", 14).TextColor(t.Success)
			ui.Textf(c, "当前路径含非 ASCII 字符，已通过 Windows 透明映射至纯英文路径：%s", vm.env.EffectiveDataDir).
				FontSize(12).TextColor(t.Success).Grow(1)
		})
	}
	for _, ck := range checks {
		ck := ck
		mark, col := "✓", t.Success
		if !ck.ok {
			mark, col = "!", t.Warning
			if ck.blocking {
				mark, col = "✗", t.Danger
			}
		}
		ui.Column(c).Padding(10, 12).Radius(8).Background(workbenchPalette.SubSurfaceBg).
			Border(1, workbenchPalette.Hairline).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mark).Bold().TextColor(col).Width(16)
				ui.Text(c, ck.name).Bold().FontSize(12).Width(148)
				detail := ck.detail
				if ck.ok && strings.ContainsAny(detail, `\/`) {
					detail = filepath.Clean(detail)
				}
				ui.Text(c, detail).FontSize(12).TextColor(t.TextMuted).Grow(1)
				if ck.toolKey != "" {
					dlLabel := "一键下载"
					if ck.ok {
						dlLabel = "重新下载"
					}
					if ui.Button(c.Key("dl_tool_"+ck.toolKey), dlLabel).Disabled(vm.dlActive).Clicked() {
						toolKey := ck.toolKey
						vm.requireLicense(func() {
							vm.startTask("下载组件 "+toolKey, actCreate, []target{{name: toolKey}}, false, func(ctx context.Context, _ target) error {
								err := vm.svc.EnsureBaseTool(ctx, toolKey)
								vm.refreshEnv()
								return err
							}, func(_ *task) {
								vm.dlActive = false
							})
						})
					}
					if ui.Button(c.Key("imp_tool_"+ck.toolKey), "导入 .zip").Disabled(vm.dlActive).Clicked() {
						toolKey := ck.toolKey
						go func() {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
								Parent:  vm.win,
								Title:   fmt.Sprintf("选择 %s 的离线压缩包", toolKey),
								Filters: []mygo.FileFilter{{Name: "Archive (*.zip, *.tar.gz, *.tgz)", Extensions: []string{"zip", "tar.gz", "tgz", "gz"}}},
							})
							if err != nil || len(paths) == 0 {
								return
							}
							zipPath := paths[0]
							vm.post(func() {
								vm.startTask("导入组件 "+toolKey, actCreate, []target{{name: filepath.Base(zipPath)}}, false, func(ctx context.Context, _ target) error {
									err := vm.svc.ImportBaseToolZip(ctx, toolKey, zipPath)
									vm.refreshEnv()
									return err
								}, func(_ *task) {
									vm.dlActive = false
								})
							})
						}()
					}
				}
			})
			if !ck.ok {
				ui.Text(c, "影响："+ck.impact).FontSize(12).TextColor(col).MarginStart(24)
				ui.Text(c, "处理："+ck.fix).FontSize(12).MarginStart(24)
			}
		})
	}
	if vm.env.DataDir != "" || vm.env.AVDHome != "" {
		ui.Divider(c)
		if vm.env.DataDir != "" {
			vm.buildDirectoryRow(c, "数据目录", vm.env.DataDir)
		}
		if vm.env.EffectiveDataDir != "" && vm.env.EffectiveDataDir != vm.env.DataDir {
			vm.buildDirectoryRow(c, "映射路径", vm.env.EffectiveDataDir)
		}
		if vm.env.AVDHome != "" {
			vm.buildDirectoryRow(c, "AVD 目录", vm.env.AVDHome)
		}
	}
}

func (vm *viewModel) buildImageList(c *ui.Context) {
	t := c.Theme()

	// Filter pills + search + catalog toolbar.
	ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
		ui.Tabs(c, &vm.imageFilter, "全部", "已安装", "已收藏", "支持 ARM 应用")
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			glyph(c, "search", 14).TextColor(t.TextMuted)
			ui.TextInput(c, &vm.imageQuery).Placeholder("筛选镜像包或名称…").Width(170).Height(30).FontSize(11)
		})
		ui.Spacer(c)
		if ui.Button(c.Key("sync_catalog"), "同步官方列表").Disabled(vm.dlActive).Clicked() {
			vm.startTask("同步官方镜像列表", actCreate, []target{{name: "Google sys-img2-3.xml"}}, false, func(ctx context.Context, _ target) error {
				_, err := vm.svc.SyncCatalog(ctx)
				vm.refreshEnv()
				return err
			}, nil)
		}
		toggleLabel := "+ 自定义镜像"
		if vm.showCustomImg {
			toggleLabel = "收起自定义表单"
		}
		if ui.Button(c.Key("toggle_custom_img"), toggleLabel).Clicked() {
			vm.showCustomImg = !vm.showCustomImg
		}
	})

	ui.Text(c, "点击 ☆ 收藏并置顶（保留 Android 7.1 / 9 兼容项并置顶 Android 11 / 14），可添加备注；支持同步官方列表与导入本地 .zip。").
		FontSize(11).TextColor(t.TextMuted)

	if vm.showCustomImg {
		ui.Column(c).Padding(10, 12).Radius(8).Background(workbenchPalette.SubSurfaceBg).
			Border(1, workbenchPalette.Hairline).Gap(8).Children(func() {
			ui.Text(c, "添加 / 编辑自定义系统镜像").FontSize(12).Bold()
			ui.Form(c, func() {
				ui.Field(c, "镜像包 ID", func() {
					ui.TextInput(c, &vm.customImgID).Placeholder("例如 system-images;android-34;google_apis_playstore;x86_64")
				})
				ui.Field(c, "显示名称", func() {
					ui.TextInput(c, &vm.customImgName).Placeholder("例如 Android 14 企业定制镜像")
				})
				ui.Field(c, "用途说明", func() {
					ui.TextInput(c, &vm.customImgDesc).Placeholder("可选描述")
				})
				ui.Field(c, "下载 URL", func() {
					ui.TextInput(c, &vm.customImgURL).Placeholder("https://.../x86_64-34_r14.zip（若仅离线导入可留空）")
				})
				ui.Field(c, "校验和", func() {
					ui.TextInput(c, &vm.customImgSHA1).Placeholder("可选 64 位 SHA-256 或 40 位 SHA-1")
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c.Key("cancel_custom_img"), "取消").Clicked() {
					vm.showCustomImg = false
				}
				if ui.PrimaryButton(c.Key("save_custom_img"), "保存镜像条目").Disabled(strings.TrimSpace(vm.customImgID) == "").Clicked() {
					checksum := strings.TrimSpace(vm.customImgSHA1)
					item := service.ImageItem{
						ID:          strings.TrimSpace(vm.customImgID),
						Name:        strings.TrimSpace(vm.customImgName),
						Description: strings.TrimSpace(vm.customImgDesc),
						URL:         strings.TrimSpace(vm.customImgURL),
						Source:      catalog.SourceCustom,
					}
					if len(checksum) == 64 {
						item.SHA256 = checksum
					} else {
						item.SHA1 = checksum
					}
					if err := vm.svc.SaveCustomImage(context.Background(), item); err != nil {
						vm.appendLog(fmt.Sprintf("[%s] 保存自定义镜像失败: %s", time.Now().Format("15:04:05"), config.RedactError(err)))
					} else {
						vm.showCustomImg = false
						vm.customImgID, vm.customImgName, vm.customImgDesc, vm.customImgURL, vm.customImgSHA1 = "", "", "", "", ""
						vm.refreshEnv()
					}
				}
			})
		})
	}

	items := vm.catalog
	if len(items) == 0 {
		for _, r := range imageRows(vm.env) {
			src := catalog.SourceLocal
			if r.recommended {
				src = catalog.SourceRecommended
			}
			items = append(items, service.ImageItem{
				ID:        r.pkg,
				Name:      shortImage(r.pkg),
				Source:    src,
				Installed: r.installed,
			})
		}
	}

	q := strings.ToLower(strings.TrimSpace(vm.imageQuery))
	shownCount := 0
	for _, it := range items {
		it := it
		if vm.imageFilter == 1 && !it.Installed {
			continue
		}
		if vm.imageFilter == 2 && !it.Favorite {
			continue
		}
		if vm.imageFilter == 3 && it.ARMCompat != catalog.ARMCompatNative && it.ARMCompat != catalog.ARMCompatTranslation {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) &&
			!strings.Contains(strings.ToLower(it.ID), q) &&
			!strings.Contains(strings.ToLower(it.Note), q) {
			continue
		}
		shownCount++

		keySuffix := it.ID
		badge := "官方"
		badgeCol := t.TextMuted
		switch it.Source {
		case catalog.SourceRecommended:
			badge = "官方"
		case catalog.SourceCustom:
			badge = "自定义"
			badgeCol = t.Warning
		case catalog.SourceLocal:
			badge = "本地"
			badgeCol = t.Success
		}

		rowBg := workbenchPalette.SubSurfaceBg
		rowEdge := workbenchPalette.Hairline
		if it.Favorite {
			rowBg = workbenchPalette.FavCardBg
			rowEdge = workbenchPalette.FavCardBorder
		}

		ui.Column(c).Padding(10, 12).Radius(8).Background(rowBg).Border(1, rowEdge).Gap(6).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				star, starColor := "☆", t.TextMuted
				if it.Favorite {
					star, starColor = "★", workbenchPalette.FavStar
				}
				if ui.Button(c.Key("favorite_"+it.ID), "").Label(star).Children(func() {
					ui.Text(c, star).TextColor(starColor)
				}).Clicked() {
					if vm.saveImageFavorite(it.ID, !it.Favorite, "") {
						if it.Favorite {
							vm.favoriteID = ""
						} else {
							vm.favoriteID, vm.favoriteNote = it.ID, ""
						}
					}
				}
				ui.Text(c, badge).FontSize(11).Bold().TextColor(badgeCol).Width(42)
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Row(c).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
						ui.Text(c, it.Name).FontSize(12).Bold()
						switch it.ARMCompat {
						case catalog.ARMCompatTranslation:
							softBadge(c, "✓ 支持 ARM 转译", t.Success, workbenchPalette.SuccessSoftBg)
						case catalog.ARMCompatNative:
							softBadge(c, "✓ 原生 ARM64", t.Success, workbenchPalette.SuccessSoftBg)
						}
						if !it.Installed && (strings.TrimSpace(it.URL) != "" || it.Source == catalog.SourceCustom) && it.EffectiveChecksum() == "" {
							softBadgeWarnBorder(c, "⚠ 未提供安全校验和", t.Warning, workbenchPalette.WarningSoftBg, workbenchPalette.WarningSoftEdge)
						}
					})
					sub := it.ID
					if it.Description != "" {
						sub = it.Description + " · " + it.ID
					}
					ui.Text(c, sub).FontSize(11).TextColor(t.TextMuted)
					if it.Favorite && it.Note != "" {
						ui.Text(c, "备注："+it.Note).FontSize(11).TextColor(t.Accent)
					}
				})
				if it.Favorite && ui.Button(c.Key("note_"+it.ID), "备注").Clicked() {
					vm.favoriteID, vm.favoriteNote, vm.favoriteError = it.ID, it.Note, ""
				}
				if it.Installed {
					softBadge(c, "✓ 已安装", t.Success, workbenchPalette.SuccessSoftBg)
					if ui.Button(c.Key("del_img_"+keySuffix), "删除镜像").Disabled(vm.dlActive).Clicked() {
						imgID := it.ID
						vm.startTask("删除系统镜像", actDelete, []target{{name: imgID}}, false, func(ctx context.Context, _ target) error {
							err := vm.svc.DeleteSystemImage(ctx, imgID)
							vm.refreshEnv()
							return err
						}, nil)
					}
				} else {
					ui.Text(c, "未安装").FontSize(11).TextColor(t.TextMuted)
					if ui.Button(c.Key("dl_img_"+keySuffix), "下载安装").Disabled(vm.dlActive).Clicked() {
						imgID, imgName := it.ID, it.Name
						vm.requireLicense(func() {
							vm.startTask("下载镜像 "+imgName, actCreate, []target{{name: imgID}}, false, func(ctx context.Context, _ target) error {
								err := vm.svc.DownloadSystemImage(ctx, imgID)
								vm.refreshEnv()
								return err
							}, func(_ *task) {
								vm.dlActive = false
							})
						})
					}
					if ui.Button(c.Key("imp_img_"+keySuffix), "导入 .zip").Disabled(vm.dlActive).Clicked() {
						imgID := it.ID
						go func() {
							paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
								Parent:  vm.win,
								Title:   fmt.Sprintf("选择系统镜像 %s 的本地 .zip 包", imgID),
								Filters: []mygo.FileFilter{{Name: "Zip Archive (*.zip)", Extensions: []string{"zip"}}},
							})
							if err != nil || len(paths) == 0 {
								return
							}
							zipPath := paths[0]
							vm.post(func() {
								vm.startTask("导入镜像 "+imgID, actCreate, []target{{name: filepath.Base(zipPath)}}, false, func(ctx context.Context, _ target) error {
									err := vm.svc.ImportSystemImageZip(ctx, imgID, zipPath)
									vm.refreshEnv()
									return err
								}, func(_ *task) {
									vm.dlActive = false
								})
							})
						}()
					}
				}
				if it.Source == catalog.SourceCustom {
					if ui.Button(c.Key("edit_cust_"+keySuffix), "编辑").Clicked() {
						vm.customImgID = it.ID
						vm.customImgName = it.Name
						vm.customImgDesc = it.Description
						vm.customImgURL = it.URL
						vm.customImgSHA1 = it.EffectiveChecksum()
						vm.showCustomImg = true
					}
					if ui.Button(c.Key("rm_cust_"+keySuffix), "移除条目").Clicked() {
						_ = vm.svc.RemoveCustomImage(context.Background(), it.ID)
						vm.refreshEnv()
					}
				}
			})
			if vm.favoriteID == it.ID {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "收藏备注").FontSize(11)
					ui.TextInput(c.Key("favorite_note_"+it.ID), &vm.favoriteNote).Placeholder("可选备注").Grow(1)
					if ui.Button(c.Key("save_note_"+it.ID), "保存备注").Clicked() {
						if vm.saveImageFavorite(it.ID, true, vm.favoriteNote) {
							vm.favoriteID = ""
						}
					}
					if ui.Button(c.Key("cancel_note_"+it.ID), "取消").Clicked() {
						vm.favoriteID = ""
					}
				})
				if vm.favoriteError != "" {
					ui.Text(c, vm.favoriteError).FontSize(11).TextColor(t.Danger)
				}
			}
		})
	}
	if shownCount == 0 {
		ui.Text(c, "当前筛选条件下没有匹配的系统镜像，可切换上方筛选胶囊或清空关键词。").
			FontSize(12).TextColor(t.TextMuted).Padding(12, 0)
	}
}

func (vm *viewModel) saveImageFavorite(id string, selected bool, note string) bool {
	if err := vm.svc.SetImageFavorite(context.Background(), id, selected, note); err != nil {
		vm.favoriteError = "保存收藏失败：" + err.Error()
		vm.appendLog(vm.favoriteError)
		return false
	}
	vm.favoriteError = ""
	vm.catalog, _ = vm.svc.ListCatalog(context.Background())
	return true
}

func (vm *viewModel) buildDirectoryRow(c *ui.Context, label, path string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted).Width(72)
		ui.Text(c, filepath.Clean(path)).FontSize(11).Grow(1)
		if ui.Button(c.Key("open_dir_"+label), "打开目录").Clicked() {
			if err := openLocalDir(path); err != nil {
				vm.appendLog(fmt.Sprintf("打开%s失败: %s", label, config.RedactError(err)))
			}
		}
	})
}

// buildEnvMissingModal is the lightweight first-run/missing-env decision card (Modal #9).
func (vm *viewModel) buildEnvMissingModal(c *ui.Context) {
	ui.Modal(c, &vm.showEnvMissing, func() {
		t := c.Theme()
		checks := buildEnvChecks(vm.env)
		blocking, _ := envIssues(checks)

		modalContent(c, 460).Gap(12).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "检测到运行环境不完整").FontSize(16).Bold()
				softBadgeWarnBorder(c, fmt.Sprintf("缺少 %d 项", max(1, blocking)),
					t.Warning, workbenchPalette.WarningSoftBg, workbenchPalette.WarningSoftEdge)
			})
			ui.Text(c, "缺失核心组件会影响新建或启动模拟器实例。建议点击下方一键补齐，后台自动校验并安装：").
				FontSize(12).TextColor(t.TextMuted)

			ui.Column(c).Padding(8, 10).Radius(8).Background(workbenchPalette.SubSurfaceBg).
				Border(1, workbenchPalette.Hairline).Gap(6).Children(func() {
				listed := 0
				for _, ck := range checks {
					if ck.ok || !ck.blocking {
						continue
					}
					listed++
					ui.Row(c).Gap(6).Children(func() {
						ui.Text(c, "✗").FontSize(12).TextColor(t.Danger).Width(14)
						ui.Text(c, ck.name).FontSize(12).Bold().Width(116)
						ui.Text(c, ck.impact).FontSize(11).TextColor(t.TextMuted).Grow(1)
					})
				}
				if listed == 0 {
					ui.Text(c, "请前往「镜像与环境」检查可选组件（如 scrcpy）与路径权限。").
						FontSize(12).TextColor(t.TextMuted)
				}
			})

			ui.Row(c).Gap(8).Wrap().Children(func() {
				if quietControl(c, "env_miss_dismiss", "暂不处理", "").Clicked() {
					vm.showEnvMissing = false
				}
				ui.Spacer(c)
				if !vm.env.PathASCIIOK {
					if ui.Button(c.Key("env_miss_redirect_ascii"), "一键改用纯英文数据目录").Clicked() {
						vm.redirectToPureASCIIDataDir(false)
					}
				}
				if ui.Button(c.Key("env_miss_manual"), "去环境页手动处理").Clicked() {
					vm.showEnvMissing = false
					vm.page = pageImages
					vm.envTab = envTabRuntime
					vm.refreshEnv()
				}
				if ui.PrimaryButton(c.Key("env_miss_auto"), "一键补齐基础环境").Disabled(vm.dlActive).Clicked() {
					vm.showEnvMissing = false
					vm.startInstallAllBaseTools()
				}
			})
		})
	})
}
