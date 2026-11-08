package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"virdeck/core/service"
)

type transferTestService struct {
	service.Service
	sent     chan service.SendFilesRequest
	received chan string
}

func (s *transferTestService) SendFiles(_ context.Context, r service.SendFilesRequest) error {
	s.sent <- r
	return nil
}
func (s *transferTestService) ReceiveFiles(_ context.Context, id, remote, local string) (string, error) {
	s.received <- id + "|" + remote + "|" + local
	return local + "/unique", nil
}

func TestTransferModalFreezesTargetsAndSendsFromLiveForm(t *testing.T) {
	for _, size := range [][2]int{{610, 480}, {1180, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			vm := newTestVM(t)
			svc := &transferTestService{Service: vm.svc, sent: make(chan service.SendFilesRequest, 1)}
			vm.svc = svc
			posts := make(chan func(), 16)
			vm.post = func(f func()) { posts <- f }
			selected := []service.Instance{{ID: "a1b2c3d4", Name: "手机一", State: service.Running}}
			vm.openTransfer(selected)
			selected[0].ID = "changed"
			vm.selected.Clear()
			vm.selected.Add("0a0b0c0d")
			vm.transfer.localPaths = []string{"C:/资料 & demo.txt"}
			vm.transfer.remoteDir = "/sdcard/Download/it's $(x)"
			tester := ui.NewTester(vm.view, size[0], size[1])
			tester.Frame()
			if err := tester.Click("发送文件"); err != nil {
				t.Fatal(err)
			}
			select {
			case req := <-svc.sent:
				if !reflect.DeepEqual(req.IDs, []string{"a1b2c3d4"}) || req.RemoteDir != "/sdcard/Download/it's $(x)" || !reflect.DeepEqual(req.LocalPaths, []string{"C:/资料 & demo.txt"}) {
					t.Fatalf("request=%+v", req)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("send action not reached")
			}
			for vm.transfer.busy {
				select {
				case f := <-posts:
					f()
				case <-time.After(3 * time.Second):
					t.Fatal("UI completion stalled")
				}
			}
			if len(vm.tasks) != 1 || !vm.tasks[0].finished {
				t.Fatal("transfer missing existing task feedback")
			}
		})
	}
}
func TestTransferModalReceivesCommonFolderAndKeepsFooterReachable(t *testing.T) {
	vm := newTestVM(t)
	svc := &transferTestService{Service: vm.svc, received: make(chan string, 1)}
	vm.svc = svc
	vm.post = func(func()) {} // Result posting is asynchronous and irrelevant to request assertion.
	vm.openTransfer([]service.Instance{{ID: "a1b2c3d4", Name: "手机一", State: service.Running}})
	vm.transfer.mode = transferReceiveMode
	vm.transfer.remotePath = "/sdcard/DCIM"
	vm.transfer.localDir = "C:/导出"
	tester := ui.NewTester(vm.view, 610, 480)
	tester.Frame()
	if err := tester.Click("接收文件"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-svc.received:
		if got != "a1b2c3d4|/sdcard/DCIM|C:/导出" {
			t.Fatalf("request=%s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("receive footer unreachable")
	}
}
func TestTransferModalRejectsTargetsNoLongerRunning(t *testing.T) {
	vm := newTestVM(t)
	vm.openTransfer([]service.Instance{{ID: "stopped", Name: "离线", State: service.Stopped}})
	if vm.transfer.show {
		t.Fatal("opened transfer for stopped target")
	}
}

func TestTransferModalDismissalKeepsActiveControlsReachable(t *testing.T) {
	for _, state := range []string{"busy", "picking"} {
		for _, dismiss := range []string{"escape", "backdrop"} {
			t.Run(state+"/"+dismiss, func(t *testing.T) {
				vm := newTestVM(t)
				vm.openTransfer(vm.instances[:1])
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				vm.transfer.busy = state == "busy"
				vm.transfer.picking = state == "picking"
				vm.transfer.cancel = cancel
				tester := ui.NewTester(vm.view, 610, 480)
				tester.Frame()
				if dismiss == "escape" {
					tester.Key(0, ui.KeyEscape)
				} else {
					tester.ClickAt(5, 475)
				}
				tester.Frame()
				if !vm.transfer.show {
					t.Fatal("dismissal hid active transfer controls")
				}
				tester.Frame()
				if state == "busy" {
					if err := tester.Click("取消传输"); err != nil {
						t.Fatal(err)
					}
					if ctx.Err() != context.Canceled {
						t.Fatal("cancel control no longer cancels")
					}
				}
			})
		}
	}
}

func TestRenderTransferModal(t *testing.T) {
	dir := os.Getenv("VIRDECK_SHOT_DIR")
	if dir == "" {
		t.Skip("screenshots not requested")
	}
	for _, mode := range []string{transferSendMode, transferReceiveMode} {
		for _, width := range []int{610, 1180} {
			vm := newTestVM(t)
			vm.openTransfer(vm.instances[:1])
			vm.transfer.mode = mode
			vm.transfer.localPaths = []string{"C:/资料/照片.jpg"}
			vm.transfer.localDir = "C:/Users/用户/Downloads"
			tester := ui.NewTester(vm.view, width, 480)
			tester.Frame()
			suffix := "send"
			if mode == transferReceiveMode {
				suffix = "receive"
			}
			file, err := os.Create(filepath.Join(dir, fmt.Sprintf("transfer_%s_%d.png", suffix, width)))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, tester.Image())
			_ = file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
