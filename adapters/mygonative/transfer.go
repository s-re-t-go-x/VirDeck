package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"strings"
	"virdeck/core/service"
)

const transferSendMode = "发送到 Android"
const transferReceiveMode = "接收到 Windows"

type transferUIState struct {
	show, busy, picking                                               bool
	epoch                                                             int
	mode, remoteDir, remotePath, localDir, receiveID, common, message string
	targets                                                           []service.Instance
	localPaths                                                        []string
	cancel                                                            context.CancelFunc
}

func (vm *viewModel) openTransfer(instances []service.Instance) {
	p := planAction(actTransfer, instances, vm.busy)
	if len(p.eligible) == 0 {
		return
	}
	if vm.transfer.cancel != nil {
		vm.transfer.cancel()
	}
	vm.transfer = transferUIState{show: true, epoch: vm.transfer.epoch + 1, mode: transferSendMode, remoteDir: service.DefaultTransferDirectory, remotePath: "/sdcard/Download", receiveID: p.eligible[0].ID, common: "下载 Download", targets: append([]service.Instance(nil), p.eligible...)}
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
	if s.busy || s.picking {
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
	done := func(t *task) {
		cancel()
		if vm.transfer.epoch != epoch {
			return
		}
		vm.transfer.busy = false
		vm.transfer.cancel = nil
		vm.transfer.message = t.summary()
	}
	if s.mode == transferSendMode {
		paths := append([]string(nil), s.localPaths...)
		remote := s.remoteDir
		vm.startTask("发送文件", actTransfer, targetsOf(p.eligible), false, func(_ context.Context, t target) error {
			return vm.svc.SendFiles(ctx, service.SendFilesRequest{IDs: []string{t.id}, LocalPaths: paths, RemoteDir: remote})
		}, done)
	} else {
		remote, local := s.remotePath, s.localDir
		vm.startTask("接收文件", actTransfer, targetsOf(p.eligible), false, func(_ context.Context, t target) error {
			_, err := vm.svc.ReceiveFiles(ctx, t.id, remote, local)
			return err
		}, done)
	}
}

func (vm *viewModel) buildTransferModal(c *ui.Context) {
	s := &vm.transfer
	modal := ui.Modal(c, &s.show, func() {
		theme := c.Theme()
		_, height := c.Size()
		modalContent(c, 540).Padding(8).Gap(12).Children(func() {
			ui.Text(c, "文件传输").FontSize(20).Bold()
			ui.Scroll(c.Key("transfer_body")).Height(min(float32(305), max(float32(100), height-215))).Gap(10).Children(func() {
				ui.Textf(c, "已固定 %d 个目标实例", len(s.targets)).FontSize(12).TextColor(theme.TextMuted)
				ui.Select(c.Key("transfer_mode"), &s.mode, []string{transferSendMode, transferReceiveMode}).Label("传输方向").Disabled(s.busy || s.picking)
				if s.mode == transferSendMode {
					ui.Text(c, "Android 接收目录").FontSize(12)
					ui.TextInput(c.Key("transfer_remote_dir"), &s.remoteDir).Placeholder(service.DefaultTransferDirectory).Disabled(s.busy || s.picking)
					ui.Text(c, "每次发送创建独立子目录，同名文件自动编号。").FontSize(11).TextColor(theme.TextMuted)
					ui.Textf(c, "已选择 %d 个 Windows 文件", len(s.localPaths)).FontSize(12)
					if ui.Button(c.Key("transfer_pick_files"), "选择本地文件").Disabled(s.busy || s.picking).Clicked() {
						vm.pickTransferPath(false)
					}
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
							s.receiveID = s.targets[i].ID
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
								s.remotePath = paths[i]
							}
						}
					}
					ui.TextInput(c.Key("transfer_remote_path"), &s.remotePath).Placeholder("Android 文件或文件夹路径").Disabled(s.busy || s.picking)
					ui.Text(c, "可在 Android 文件管理器确认路径；可直接接收整个常用文件夹。").FontSize(11).TextColor(theme.TextMuted)
					ui.TextInput(c.Key("transfer_local_dir"), &s.localDir).Placeholder("Windows 接收目录").Disabled(s.busy || s.picking)
					if ui.Button(c.Key("transfer_pick_dir"), "选择接收目录").Disabled(s.busy || s.picking).Clicked() {
						vm.pickTransferPath(true)
					}
					ui.Text(c, "每次接收创建独立导出目录，保留已有文件。").FontSize(11).TextColor(theme.TextMuted)
				}
				if s.message != "" {
					ui.Text(c, oneLine(s.message)).FontSize(12).TextColor(theme.TextMuted)
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("transfer_close"), "关闭").Disabled(s.busy || s.picking).Clicked() {
					s.show = false
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
				if ui.PrimaryButton(c.Key("transfer_submit"), label).Disabled(s.busy || s.picking || disabled).Clicked() {
					vm.startTransfer()
				}
			})
		})
	})
	if modal.Dismissed() && (s.busy || s.picking) {
		s.show = true
	}
}
