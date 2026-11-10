package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"virdeck/core/service"
)

type transferTestService struct {
	service.Service
	sent     chan service.SendFilesRequest
	received chan string
	list     func(context.Context, string, string) ([]service.TransferFile, error)
}

func (s *transferTestService) SendFiles(_ context.Context, r service.SendFilesRequest) (map[string]string, error) {
	s.sent <- r
	return map[string]string{r.IDs[0]: r.RemoteDir + "/batch"}, nil
}
func (s *transferTestService) ReceiveFiles(_ context.Context, id, remote, local string) (string, error) {
	s.received <- id + "|" + remote + "|" + local
	return local + "/unique", nil
}

func (s *transferTestService) ListTransferFiles(ctx context.Context, id, dir string) ([]service.TransferFile, error) {
	if s.list != nil {
		return s.list(ctx, id, dir)
	}
	return []service.TransferFile{{Name: "照片.jpg"}, {Name: "相册", IsDir: true}}, nil
}

func TestTransferBrowseRejectsLateResultsFromChangedPath(t *testing.T) {
	vm := newTestVM(t)
	release := make(chan struct{})
	posts := make(chan func(), 16)
	vm.post = func(f func()) { posts <- f }
	vm.svc = &transferTestService{Service: vm.svc, list: func(context.Context, string, string) ([]service.TransferFile, error) {
		<-release
		return []service.TransferFile{{Name: "old.txt"}}, nil
	}}
	vm.openTransfer(vm.instances[:1])
	vm.transfer.mode = transferReceiveMode
	vm.browseTransfer("/sdcard/Download")
	vm.transfer.remotePath = "/sdcard/DCIM"
	close(release)
	select {
	case f := <-posts:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("listing stalled")
	}
	if len(vm.transfer.entries) != 0 || vm.transfer.listing {
		t.Fatal("stale listing remained active")
	}
}

func TestTransferBrowseSelectsFileWithoutReceivingWholeDirectory(t *testing.T) {
	vm := newTestVM(t)
	posts := make(chan func(), 16)
	vm.post = func(f func()) { posts <- f }
	vm.svc = &transferTestService{Service: vm.svc}
	vm.openTransfer(vm.instances[:1])
	vm.transfer.mode = transferReceiveMode
	vm.browseTransfer("/sdcard/Download")
	select {
	case f := <-posts:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("listing stalled")
	}
	tester := ui.NewTester(vm.view, 1180, 780)
	tester.Frame()
	if err := tester.Click("文件 · 照片.jpg"); err != nil {
		t.Fatal(err)
	}
	if vm.transfer.remotePath != "/sdcard/Download/照片.jpg" {
		t.Fatalf("selection = %q", vm.transfer.remotePath)
	}
	tester.Frame()
	if err := tester.Click("文件 · 照片.jpg"); err != nil {
		t.Fatal(err)
	}
	if vm.transfer.remotePath != "/sdcard/Download" {
		t.Fatalf("toggle off selection = %q", vm.transfer.remotePath)
	}
	tester.Frame()
	if err := tester.Click("文件 · 照片.jpg"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("上一级"); err != nil {
		t.Fatal(err)
	}
	if vm.transfer.remotePath != "/sdcard" {
		t.Fatalf("parent from selected file = %q, want /sdcard", vm.transfer.remotePath)
	}
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
			vm.transfer.listedID = vm.transfer.receiveID
			vm.transfer.listedPath = "/sdcard/Download"
			vm.transfer.listMessage = "2 项 · 点击文件选中，点击文件夹进入"
			vm.transfer.entries = []service.TransferFile{{Name: "相册", IsDir: true}, {Name: "照片.jpg"}}
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

func TestTransferReceiveShowsActualExportAndRemembersDirectory(t *testing.T) {
	vm := newTestVM(t)
	vm.svc = &transferTestService{Service: vm.svc, received: make(chan string, 1)}
	posts := make(chan func(), 16)
	vm.post = func(f func()) { posts <- f }
	vm.openTransfer(vm.instances[:1])
	if vm.transfer.localDir == "" {
		t.Error("missing default receive directory")
	}
	vm.transfer.mode = transferReceiveMode
	vm.transfer.localDir = "C:/exports"
	vm.startTransfer()
	for vm.transfer.busy {
		select {
		case f := <-posts:
			f()
		case <-time.After(3 * time.Second):
			t.Fatal("completion stalled")
		}
	}
	if !strings.Contains(vm.transfer.message, "C:/exports/unique") {
		t.Fatalf("export path missing: %s", vm.transfer.message)
	}
	vm.openTransfer(vm.instances[:1])
	if vm.transfer.localDir != "C:/exports" {
		t.Fatalf("receive directory forgotten: %q", vm.transfer.localDir)
	}
}
