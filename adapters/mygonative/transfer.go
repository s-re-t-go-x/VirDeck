package mygonative

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"virdeck/config"
	"virdeck/core/service"
)

const transferSendMode = "发送到 Android"

var transferReceiveMode = "接收到 " + hostComputerName(runtime.GOOS)

type transferUIState struct {
	show, busy, picking, listing                                      bool
	epoch                                                             int
	mode, remoteDir, remotePath, localDir, receiveID, common, message string
	targets                                                           []service.Instance
	localPaths                                                        []string
	cancel                                                            context.CancelFunc
	listCancel                                                        context.CancelFunc
	listVersion                                                       int
	listedID, listedPath, listMessage, exportDir                      string
	entries                                                           []service.TransferFile
}

func defaultLocalDownloadDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Downloads")
	}
	return ""
}

func (vm *viewModel) openTransferPage() {
	running := planAction(actTransfer, vm.selectedInsts(), vm.busy).eligible
	if len(running) == 0 {
		running = planAction(actTransfer, vm.instances, vm.busy).eligible
	}
	if len(running) > 0 && (!vm.transfer.show || len(vm.transfer.targets) == 0) {
		vm.openTransfer(running)
		return
	}
	if vm.transfer.localDir == "" {
		vm.transfer.localDir = defaultLocalDownloadDir()
	}
	if vm.transfer.remoteDir == "" {
		vm.transfer.remoteDir = service.DefaultTransferDirectory
	}
	if vm.transfer.remotePath == "" {
		vm.transfer.remotePath = "/sdcard/Download"
	}
	if vm.transfer.mode == "" {
		vm.transfer.mode = transferSendMode
	}
	vm.transfer.show = true
	vm.page = pageTransfer
}

func (vm *viewModel) openTransfer(instances []service.Instance) {
	p := planAction(actTransfer, instances, vm.busy)
	if len(p.eligible) == 0 {
		return
	}
	if vm.transfer.cancel != nil {
		vm.transfer.cancel()
	}
	if vm.transfer.listCancel != nil {
		vm.transfer.listCancel()
	}
	localDir := vm.transfer.localDir
	if localDir == "" {
		localDir = defaultLocalDownloadDir()
	}
	vm.transfer = transferUIState{
		localDir:   localDir,
		show:       true,
		epoch:      vm.transfer.epoch + 1,
		mode:       transferSendMode,
		remoteDir:  service.DefaultTransferDirectory,
		remotePath: "/sdcard/Download",
		receiveID:  p.eligible[0].ID,
		common:     "下载 Download",
		targets:    append([]service.Instance(nil), p.eligible...),
	}
	vm.page = pageTransfer
}

func (vm *viewModel) pickTransferPath(receive bool) {
	s := &vm.transfer
	s.picking = true
	epoch := s.epoch
	win := vm.win
	go func() {
		title := "选择要发送的文件"
		if receive {
			title = "选择电脑接收目录"
		}
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: title, Directory: receive, Multiple: !receive, CreateDirectories: receive})
		vm.post(func() {
			if vm.transfer.epoch != epoch || !vm.transfer.show {
				return
			}
			vm.transfer.picking = false
			if err != nil {
				vm.transfer.message = err.Error()
				vm.appendLog("文件选择失败: " + err.Error())
				return
			}
			if len(paths) == 0 {
				return
			}
			if receive {
				vm.transfer.localDir = paths[0]
			} else {
				vm.transfer.localPaths = append([]string(nil), paths...)
			}
		})
	}()
}

func (vm *viewModel) startTransfer() {
	s := &vm.transfer
	if s.busy || s.picking || s.listing {
		return
	}
	epoch := s.epoch
	ids := idsOf(s.targets)
	if s.mode == transferReceiveMode {
		ids = []string{s.receiveID}
	}
	p := planAction(actTransfer, vm.freshInstances(ids), vm.busy)
	if len(p.eligible) != len(ids) || len(ids) == 0 {
		s.message = "目标状态已变化，请返回工作台或刷新选择运行中的实例"
		return
	}
	if s.mode == transferSendMode && len(s.localPaths) == 0 || s.mode == transferReceiveMode && (s.localDir == "" || s.remotePath == "") {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.busy = true
	s.message = "正在传输，可在右下角悬浮任务胶囊查看进度。"
	s.exportDir = ""
	var resultMu sync.Mutex
	var results []string
	exportDir := ""
	done := func(t *task) {
		cancel()
		if vm.transfer.epoch != epoch {
			return
		}
		vm.transfer.busy = false
		vm.transfer.cancel = nil
		resultMu.Lock()
		defer resultMu.Unlock()
		vm.transfer.message = t.summary() + "\n" + strings.Join(results, "\n")
		vm.transfer.exportDir = exportDir
	}
	if s.mode == transferSendMode {
		paths := append([]string(nil), s.localPaths...)
		remote := s.remoteDir
		vm.startTask("发送文件", actTransfer, targetsOf(p.eligible), false, func(_ context.Context, t target) error {
			destinations, err := vm.svc.SendFiles(ctx, service.SendFilesRequest{IDs: []string{t.id}, LocalPaths: paths, RemoteDir: remote})
			resultMu.Lock()
			if dest := destinations[t.id]; dest != "" {
				results = append(results, t.name+" → "+dest)
			}
			resultMu.Unlock()
			return err
		}, done)
	} else {
		remote, local := s.remotePath, s.localDir
		vm.startTask("接收文件", actTransfer, targetsOf(p.eligible), false, func(_ context.Context, t target) error {
			dest, err := vm.svc.ReceiveFiles(ctx, t.id, remote, local)
			resultMu.Lock()
			exportDir = dest
			if dest != "" {
				results = append(results, "导出目录: "+dest)
			}
			resultMu.Unlock()
			return err
		}, done)
	}
}

// browseTransfer ignores completions from a different session, device or edited path.
func (vm *viewModel) browseTransfer(dir string) {
	s := &vm.transfer
	if s.busy || s.picking {
		return
	}
	if s.listCancel != nil {
		s.listCancel()
	}
	s.remotePath = dir
	s.listVersion++
	version, epoch, id := s.listVersion, s.epoch, s.receiveID
	s.entries = nil
	s.listedPath, s.listMessage = "", "正在读取目录…"
	s.listing = true
	ctx, cancel := context.WithCancel(context.Background())
	s.listCancel = cancel
	go func() {
		defer cancel()
		entries, err := vm.svc.ListTransferFiles(ctx, id, dir)
		vm.post(func() {
			s := &vm.transfer
			if s.epoch != epoch || s.listVersion != version || !s.show {
				return
			}
			s.listing = false
			s.listCancel = nil
			if s.receiveID != id || s.remotePath != dir || s.mode != transferReceiveMode {
				s.listMessage = "路径已变化，请点击读取目录"
				return
			}
			if err != nil {
				s.listMessage = "读取失败: " + err.Error()
				return
			}
			sort.SliceStable(entries, func(i, j int) bool {
				if entries[i].IsDir != entries[j].IsDir {
					return entries[i].IsDir
				}
				return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
			})
			s.entries, s.listedID, s.listedPath = entries, id, dir
			s.listMessage = fmt.Sprintf("%d 项 · 点击文件选中，点击文件夹进入", len(entries))
		})
	}()
}

// buildTransferPage renders the full-page File Transfer view replacing the legacy narrow 540px modal.
func (vm *viewModel) buildTransferPage(c *ui.Context) {
	t := c.Theme()
	s := &vm.transfer
	width, _ := c.Size()
	splitCols := width >= 860

	ui.Column(c).Grow(1).Children(func() {
		// Page header + direction switcher.
		ui.Row(c).Height(64).Padding(12, 22).Gap(10).BorderWidth(0, 0, 1, 0).
			BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			glyph(c, "folder", 18).TextColor(t.Accent)
			ui.Text(c, "文件传输").FontSize(20).Bold().TextColor(t.Text)
			if len(s.targets) > 0 {
				softBadge(c, fmt.Sprintf("目标 %d 台", len(s.targets)), t.AccentHover, workbenchPalette.AccentSoftBg)
			} else {
				softBadge(c, "请先启动至少 1 台实例", t.Warning, workbenchPalette.WarningSoftBg)
			}
			ui.Spacer(c)
			oldMode := s.mode
			ui.Select(c.Key("transfer_mode"), &s.mode, []string{transferSendMode, transferReceiveMode}).
				Label("传输方向").Width(176).Disabled(s.busy || s.picking)
			if oldMode != s.mode {
				if s.mode == transferReceiveMode {
					vm.browseTransfer(s.remotePath)
				} else {
					if s.listCancel != nil {
						s.listCancel()
						s.listCancel = nil
					}
					s.listVersion++
					s.listing = false
				}
			}
			if quietControl(c, "transfer_close", "返回工作台", "").Disabled(s.busy || s.picking).Clicked() {
				if s.listCancel != nil {
					s.listCancel()
				}
				vm.page = pageWorkbench
			}
		})

		ui.Scroll(c.Key("transfer_body")).Grow(1).Padding(16, 22).Gap(12).Children(func() {
			if len(s.targets) == 0 {
				ui.Column(c).Padding(16).Radius(8).Background(workbenchPalette.SubSurfaceBg).
					Border(1, workbenchPalette.Hairline).Gap(8).Children(func() {
					ui.Text(c, "当前没有正在运行的实例").FontSize(14).Bold()
					ui.Text(c, "文件传输需要通过 ADB 与运行中的模拟器通信，请先在「实例工作台」启动实例。").
						FontSize(12).TextColor(t.TextMuted)
				})
				return
			}

			if s.mode == transferSendMode {
				vm.buildTransferSendBody(c, splitCols)
			} else {
				vm.buildTransferReceiveBody(c, splitCols)
			}

			if s.message != "" {
				ui.Column(c).Padding(10, 12).Radius(8).Background(workbenchPalette.SubSurfaceBg).
					Border(1, workbenchPalette.Hairline).Gap(6).Children(func() {
					ui.Text(c, s.message).FontSize(12).TextColor(t.TextMuted)
					if s.exportDir != "" && ui.Button(c.Key("transfer_open_export"), "打开接收文件夹").Clicked() {
						if err := openLocalDir(s.exportDir); err != nil {
							s.message = "打开目录失败: " + config.RedactError(err)
						}
					}
				})
			}
		})

		// Pinned bottom action bar (left-aligned primary action avoids overlapping bottom-right capsule).
		ui.Row(c).Padding(10, 22).Gap(8).Background(workbenchPalette.SubSurfaceBg).
			BorderWidth(1, 0, 0, 0).BorderColor(workbenchPalette.Hairline).AlignItems(ui.Center).Children(func() {
			label := "发送文件"
			disabled := len(s.targets) == 0 || len(s.localPaths) == 0 || strings.TrimSpace(s.remoteDir) == ""
			if s.mode == transferReceiveMode {
				label = "接收文件"
				disabled = len(s.targets) == 0 || s.localDir == "" || s.remotePath == "" || s.receiveID == ""
			}
			if ui.PrimaryButton(c.Key("transfer_submit"), label).
				Disabled(s.busy || s.picking || s.listing || disabled).Clicked() {
				vm.startTransfer()
			}
			if s.busy && ui.Button(c.Key("transfer_cancel"), "取消传输").Clicked() {
				if s.cancel != nil {
					s.cancel()
				}
				s.message = "正在取消传输…"
			}
			ui.Spacer(c)
		})
	})
}

func (vm *viewModel) buildTransferSendBody(c *ui.Context, splitCols bool) {
	t := c.Theme()
	s := &vm.transfer

	leftPanel := func() {
		ui.Column(c).Grow(1).Padding(14).Radius(8).Background(workbenchPalette.SubSurfaceBg).
			Border(1, workbenchPalette.Hairline).Gap(10).Children(func() {
			ui.Text(c, "发送配置").FontSize(13).Bold()
			ui.Textf(c, "已固定 %d 个目标实例：%s", len(s.targets), namesPreview(s.targets, 4)).
				FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "Android 接收目录").FontSize(12)
			ui.TextInput(c.Key("transfer_remote_dir"), &s.remoteDir).
				Placeholder(service.DefaultTransferDirectory).Disabled(s.busy || s.picking)
			ui.Text(c, "每次发送会创建带时间戳的独立子目录，同名文件自动编号；完成后自动刷新媒体库。").
				FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("transfer_pick_files"), "选择本地文件").Disabled(s.busy || s.picking).Clicked() {
					vm.pickTransferPath(false)
				}
				if len(s.localPaths) > 0 && ui.Button(c.Key("transfer_clear_files"), "清空已选文件").
					Disabled(s.busy || s.picking).Clicked() {
					s.localPaths = nil
				}
			})
		})
	}

	rightPanel := func() {
		dropZone := ui.Column(c).Grow(1).Padding(14).Radius(8).Background(workbenchPalette.SubSurfaceBg).
			Border(1, workbenchPalette.Hairline).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "待发送队列").FontSize(13).Bold()
				ui.Spacer(c)
				ui.Textf(c, "已选择 %d 个本地文件", len(s.localPaths)).FontSize(11).TextColor(t.TextMuted)
			})
			if len(s.localPaths) == 0 {
				ui.Text(c, "支持将桌面/资源管理器中的普通文件直接拖放至此队列，或点击左侧「选择本地文件」。").
					FontSize(12).TextColor(t.TextMuted)
			} else {
				for _, p := range s.localPaths {
					ui.Row(c).Padding(6, 8).Radius(6).Background(workbenchPalette.CanvasBg).
						Border(1, workbenchPalette.Hairline).Gap(8).Children(func() {
						ui.Text(c, filepath.Base(p)).FontSize(12).Grow(1).SingleLine().Tooltip(p)
					})
				}
			}
		})
		if !vm.anyModalOpen() {
			if dropped := dropZone.DroppedFiles(); len(dropped) > 0 {
				vm.handleDroppedFiles(dropped, idsOf(s.targets))
			}
			if dropZone.FileDragOver() {
				dropZone.Background(workbenchPalette.AccentSoftBg)
			}
		}
	}

	if splitCols {
		ui.Row(c).Gap(12).AlignItems(ui.Stretch).Children(func() {
			leftPanel()
			rightPanel()
		})
	} else {
		ui.Column(c).Gap(10).Children(func() {
			leftPanel()
			rightPanel()
		})
	}
}

func (vm *viewModel) buildTransferReceiveBody(c *ui.Context, splitCols bool) {
	t := c.Theme()
	s := &vm.transfer

	leftPanel := func() {
		ui.Column(c).Width(280).Shrink(0).Padding(14).Radius(8).Background(workbenchPalette.SubSurfaceBg).
			Border(1, workbenchPalette.Hairline).Gap(10).Children(func() {
			ui.Text(c, "导出设置").FontSize(13).Bold()
			labels := make([]string, len(s.targets))
			current := ""
			for i, target := range s.targets {
				labels[i] = fmt.Sprintf("%s · %s", target.Name, target.ID[:min(6, len(target.ID))])
				if target.ID == s.receiveID {
					current = labels[i]
				}
			}
			ui.Select(c.Key("transfer_receive_target"), &current, labels).
				Label("接收来源实例").Disabled(s.busy || s.picking)
			for i, label := range labels {
				if label == current {
					if s.receiveID != s.targets[i].ID {
						s.receiveID = s.targets[i].ID
						vm.browseTransfer(s.remotePath)
					}
					break
				}
			}

			previous := s.common
			common := []string{"下载 Download", "图片 Pictures", "相机 DCIM", "文档 Documents", "音乐 Music"}
			paths := []string{"/sdcard/Download", "/sdcard/Pictures", "/sdcard/DCIM", "/sdcard/Documents", "/sdcard/Music"}
			ui.Select(c.Key("transfer_common"), &s.common, common).
				Label("常用 Android 文件夹").Disabled(s.busy || s.picking)
			if previous != s.common {
				for i, label := range common {
					if s.common == label {
						vm.browseTransfer(paths[i])
					}
				}
			}

			ui.Text(c, "电脑接收目录").FontSize(12)
			ui.TextInput(c.Key("transfer_local_dir"), &s.localDir).
				Placeholder("电脑接收目录").Disabled(s.busy || s.picking)
			if ui.Button(c.Key("transfer_pick_dir"), "选择接收目录").Disabled(s.busy || s.picking).Clicked() {
				vm.pickTransferPath(true)
			}
			ui.Text(c, "每次接收创建独立导出目录，保留已有文件。").FontSize(11).TextColor(t.TextMuted)
		})
	}

	rightPanel := func() {
		ui.Column(c).Grow(1).Padding(14).Radius(8).Background(workbenchPalette.SubSurfaceBg).
			Border(1, workbenchPalette.Hairline).Gap(8).Children(func() {
			ui.Text(c, "Android 设备文件浏览器").FontSize(13).Bold()
			ui.TextInput(c.Key("transfer_remote_path"), &s.remotePath).
				Placeholder("Android 文件或文件夹路径").Disabled(s.busy || s.picking)
			activeDir := s.remotePath
			if s.listedID == s.receiveID && s.listedPath != "" && path.Dir(s.remotePath) == s.listedPath {
				for _, entry := range s.entries {
					if !entry.IsDir && path.Join(s.listedPath, entry.Name) == s.remotePath {
						activeDir = s.listedPath
						break
					}
				}
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("transfer_browse"), "读取目录").
					Disabled(s.busy || s.picking || s.listing).Clicked() {
					vm.browseTransfer(activeDir)
				}
				parent := path.Dir(activeDir)
				if ui.Button(c.Key("transfer_parent"), "上一级").
					Disabled(s.busy || s.picking || activeDir == "/sdcard" || activeDir == "/storage/emulated/0" || activeDir == "/" || activeDir == ".").Clicked() {
					vm.browseTransfer(parent)
				}
			})
			if s.listMessage != "" {
				ui.Text(c, s.listMessage).FontSize(11).TextColor(t.TextMuted)
			}
			if s.listedID == s.receiveID && (s.listedPath == s.remotePath || path.Dir(s.remotePath) == s.listedPath) {
				ui.Scroll(c.Key("transfer_entries")).Height(220).Gap(4).Children(func() {
					for i, entry := range s.entries {
						selected := path.Join(s.listedPath, entry.Name)
						isChosenFile := !entry.IsDir && s.remotePath == selected
						label := "文件 · " + entry.Name
						if entry.IsDir {
							label = "文件夹 · " + entry.Name
						}
						btn := ui.Button(c.Key(fmt.Sprintf("transfer_entry_%d", i)), label).
							Label(label).Disabled(s.busy || s.picking || s.listing)
						if isChosenFile {
							btn = btn.Background(t.Selection).BorderColor(t.Accent).TextColor(t.Accent)
						}
						if btn.Clicked() {
							if entry.IsDir {
								vm.browseTransfer(selected)
							} else if isChosenFile {
								s.remotePath = s.listedPath
							} else {
								s.remotePath = selected
							}
						}
					}
				})
			}
			ui.Text(c, "接收目标: "+s.remotePath).FontSize(11).TextColor(t.TextMuted).Tooltip(s.remotePath)
		})
	}

	if splitCols {
		ui.Row(c).Gap(12).AlignItems(ui.Stretch).Children(func() {
			leftPanel()
			rightPanel()
		})
	} else {
		ui.Column(c).Gap(10).Children(func() {
			leftPanel()
			rightPanel()
		})
	}
}
