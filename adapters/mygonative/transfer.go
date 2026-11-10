package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"virdeck/core/service"
)

const transferSendMode = "发送到 Android"
const transferReceiveMode = "接收到 Windows"

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
		if home, err := os.UserHomeDir(); err == nil {
			localDir = filepath.Join(home, "Downloads")
		}
	}
	vm.transfer = transferUIState{localDir: localDir, show: true, epoch: vm.transfer.epoch + 1, mode: transferSendMode, remoteDir: service.DefaultTransferDirectory, remotePath: "/sdcard/Download", receiveID: p.eligible[0].ID, common: "下载 Download", targets: append([]service.Instance(nil), p.eligible...)}
}

func (vm *viewModel) pickTransferPath(receive bool) {
	s := &vm.transfer
	s.picking = true
	epoch := s.epoch
	win := vm.win
	go func() {
		title := "选择要发送的文件"
		if receive {
			title = "选择 Windows 接收目录"
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
	if len(p.eligible) != len(ids) {
		s.message = "目标状态已变化，请关闭后重新选择运行中的实例"
		return
	}
	if s.mode == transferSendMode && len(s.localPaths) == 0 || s.mode == transferReceiveMode && (s.localDir == "" || s.remotePath == "") {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.busy = true
	s.message = "正在传输，可在任务面板查看结果。"
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

// browseTransfer ignores completions from a different dialog, device or edited path.
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

func (vm *viewModel) buildTransferModal(c *ui.Context) {
	s := &vm.transfer
	modal := ui.Modal(c, &s.show, func() {
		theme := c.Theme()
		_, height := c.Size()
		modalContent(c, 540).Padding(8).Gap(12).Children(func() {
			ui.Text(c, "文件传输").FontSize(20).Bold()
			ui.Scroll(c.Key("transfer_body")).Height(min(float32(540), max(float32(100), height-170))).Gap(8).Children(func() {
				if s.mode == transferSendMode {
					ui.Textf(c, "已固定 %d 个目标实例", len(s.targets)).FontSize(12).TextColor(theme.TextMuted)
				}
				oldMode := s.mode
				ui.Select(c.Key("transfer_mode"), &s.mode, []string{transferSendMode, transferReceiveMode}).Label("传输方向").Disabled(s.busy || s.picking)
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
				if s.mode == transferSendMode {
					ui.Text(c, "Android 接收目录").FontSize(12)
					ui.TextInput(c.Key("transfer_remote_dir"), &s.remoteDir).Placeholder(service.DefaultTransferDirectory).Disabled(s.busy || s.picking)
					ui.Text(c, "每次发送创建带时间戳的独立子目录，同名文件自动编号；完成后刷新相册。").FontSize(11).TextColor(theme.TextMuted)
					ui.Textf(c, "已选择 %d 个 Windows 文件", len(s.localPaths)).FontSize(12)
					for i, p := range s.localPaths {
						if i == 5 {
							ui.Textf(c, "另有 %d 个文件", len(s.localPaths)-i).FontSize(11)
							break
						}
						ui.Text(c, filepath.Base(p)).FontSize(12).Tooltip(p)
					}
					ui.Row(c).Gap(8).Children(func() {
						if len(s.localPaths) > 0 && ui.Button(c.Key("transfer_clear_files"), "清空已选文件").Disabled(s.busy || s.picking).Clicked() {
							s.localPaths = nil
						}
						if ui.Button(c.Key("transfer_pick_files"), "选择本地文件").Disabled(s.busy || s.picking).Clicked() {
							vm.pickTransferPath(false)
						}
					})
				} else {
					labels := make([]string, len(s.targets))
					current := ""
					for i, target := range s.targets {
						labels[i] = fmt.Sprintf("%s · %s", target.Name, target.ID[:min(6, len(target.ID))])
						if target.ID == s.receiveID {
							current = labels[i]
						}
					}
					ui.Select(c.Key("transfer_receive_target"), &current, labels).Label("接收来源实例").Disabled(s.busy || s.picking)
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
					ui.Select(c.Key("transfer_common"), &s.common, common).Label("常用 Android 文件夹").Disabled(s.busy || s.picking)
					if previous != s.common {
						for i, label := range common {
							if s.common == label {
								vm.browseTransfer(paths[i])
							}
						}
					}
					ui.TextInput(c.Key("transfer_remote_path"), &s.remotePath).Placeholder("Android 文件或文件夹路径").Disabled(s.busy || s.picking)
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
						if ui.Button(c.Key("transfer_browse"), "读取目录").Disabled(s.busy || s.picking || s.listing).Clicked() {
							vm.browseTransfer(activeDir)
						}
						parent := path.Dir(activeDir)
						if ui.Button(c.Key("transfer_parent"), "上一级").Disabled(s.busy || s.picking || activeDir == "/sdcard" || activeDir == "/storage/emulated/0" || activeDir == "/" || activeDir == ".").Clicked() {
							vm.browseTransfer(parent)
						}
					})
					if s.listMessage != "" {
						ui.Text(c, s.listMessage).FontSize(11).TextColor(theme.TextMuted)
					}
					if s.listedID == s.receiveID && (s.listedPath == s.remotePath || path.Dir(s.remotePath) == s.listedPath) {
						ui.Scroll(c.Key("transfer_entries")).Height(130).Gap(4).Children(func() {
							for i, entry := range s.entries {
								selected := path.Join(s.listedPath, entry.Name)
								isChosenFile := !entry.IsDir && s.remotePath == selected
								label := "文件 · " + entry.Name
								if entry.IsDir {
									label = "文件夹 · " + entry.Name
								}
								btn := ui.Button(c.Key(fmt.Sprintf("transfer_entry_%d", i)), label).Label(label).Disabled(s.busy || s.picking || s.listing)
								if isChosenFile {
									btn = btn.Background(theme.Selection).BorderColor(theme.Accent).TextColor(theme.Accent)
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
					ui.Text(c, "接收目标: "+s.remotePath).FontSize(11).Tooltip(s.remotePath)
					ui.TextInput(c.Key("transfer_local_dir"), &s.localDir).Placeholder("Windows 接收目录").Disabled(s.busy || s.picking)
					if ui.Button(c.Key("transfer_pick_dir"), "选择接收目录").Disabled(s.busy || s.picking).Clicked() {
						vm.pickTransferPath(true)
					}
					ui.Text(c, "每次接收创建独立导出目录，保留已有文件。").FontSize(11).TextColor(theme.TextMuted)
				}
				if s.message != "" {
					ui.Text(c, s.message).FontSize(12).TextColor(theme.TextMuted)
					if s.exportDir != "" && ui.Button(c.Key("transfer_open_export"), "打开接收文件夹").Clicked() {
						if err := mygo.Shell.OpenPath(s.exportDir); err != nil {
							s.message = "打开目录失败: " + err.Error()
						}
					}
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("transfer_close"), "关闭").Disabled(s.busy || s.picking).Clicked() {
					s.show = false
					if s.listCancel != nil {
						s.listCancel()
					}
				}
				if s.busy && ui.Button(c.Key("transfer_cancel"), "取消传输").Clicked() {
					if s.cancel != nil {
						s.cancel()
					}
					s.message = "正在取消传输…"
				}
				ui.Spacer(c)
				label := "发送文件"
				disabled := len(s.localPaths) == 0 || strings.TrimSpace(s.remoteDir) == ""
				if s.mode == transferReceiveMode {
					label = "接收文件"
					disabled = s.localDir == "" || s.remotePath == "" || s.receiveID == ""
				}
				if ui.PrimaryButton(c.Key("transfer_submit"), label).Disabled(s.busy || s.picking || s.listing || disabled).Clicked() {
					vm.startTransfer()
				}
			})
		})
	})
	if modal.Dismissed() {
		if s.busy || s.picking {
			s.show = true
		} else if s.listCancel != nil {
			s.listCancel()
		}
	}
}
