package mygonative

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"virdeck/core/service"
)

type uiSyncTestSvc struct {
	service.Service
	status service.InputSyncStatus
	keys   chan string
	texts  chan string
	closed bool
}

func (s *uiSyncTestSvc) GetInputSyncStatus() service.InputSyncStatus {
	return s.status
}

func (s *uiSyncTestSvc) StartInputSync(_ context.Context, cfg service.InputSyncConfig) (service.InputSyncStatus, error) {
	slaves := make([]service.InputSyncSlaveStatus, 0, len(cfg.SlaveIDs))
	for _, id := range cfg.SlaveIDs {
		slaves = append(slaves, service.InputSyncSlaveStatus{
			InstanceID: id,
			Name:       "slave_" + id,
			Enabled:    true,
			Connected:  true,
		})
	}
	s.status = service.InputSyncStatus{
		Active:           true,
		CaptureSupported: true,
		MasterID:         cfg.MasterID,
		MasterName:       "account_01",
		JitterPx:         cfg.JitterPx,
		MaxDelayMS:       cfg.MaxDelayMS,
		Slaves:           slaves,
	}
	return s.status, nil
}

func (s *uiSyncTestSvc) StopInputSync(context.Context) error {
	s.closed = true
	s.status = service.InputSyncStatus{Active: false, CaptureSupported: true}
	return nil
}

func (s *uiSyncTestSvc) SetInputSyncPaused(paused bool) (service.InputSyncStatus, error) {
	s.status.Paused = paused
	return s.status, nil
}

func (s *uiSyncTestSvc) SetInputSyncSlaveEnabled(id string, enabled bool) (service.InputSyncStatus, error) {
	for i := range s.status.Slaves {
		if s.status.Slaves[i].InstanceID == id {
			s.status.Slaves[i].Enabled = enabled
		}
	}
	return s.status, nil
}

func (s *uiSyncTestSvc) BroadcastSyncKey(_ context.Context, key string) error {
	s.keys <- key
	return nil
}

func (s *uiSyncTestSvc) BroadcastSyncText(_ context.Context, text string) error {
	s.texts <- text
	return nil
}

func TestSyncModalStartActiveBarPauseKickAndKeys(t *testing.T) {
	vm := newTestVM(t)
	svc := &uiSyncTestSvc{
		Service: vm.svc,
		status:  service.InputSyncStatus{CaptureSupported: true},
		keys:    make(chan string, 4),
		texts:   make(chan string, 4),
	}
	vm.svc = svc
	posts := make(chan func(), 16)
	vm.post = func(fn func()) { posts <- fn }

	tester := ui.NewTester(vm.view, 1180, 740)
	tester.Frame()
	if err := tester.Click("同步操作"); err != nil {
		t.Fatalf("missing header 同步操作 button: %v", err)
	}
	if !vm.syncUI.show || vm.syncUI.masterID != "a1b2c3d4" || !vm.syncUI.slaveIDs["0a0b0c0d"] {
		t.Fatalf("sync modal init mismatch: %+v", vm.syncUI)
	}

	tester.Frame()
	if err := tester.Click("开始同步操作"); err != nil {
		t.Fatalf("click 开始同步操作 failed: %v", err)
	}
	select {
	case fn := <-posts:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("StartInputSync post missing")
	}

	tester.Frame()
	if !vm.syncStatus.Active || !tester.HasText("同步运行中") {
		t.Fatalf("expected active sync state: %+v", vm.syncStatus)
	}

	if err := tester.Click("发送 返回"); err != nil {
		t.Fatal(err)
	}
	select {
	case k := <-svc.keys:
		if k != "back" {
			t.Fatalf("expected back key, got %q", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BroadcastSyncKey not invoked")
	}

	vm.syncUI.textInput = "virdeck"
	tester.Frame()
	if err := tester.Click("同步发送"); err != nil {
		t.Fatal(err)
	}
	select {
	case txt := <-svc.texts:
		if txt != "virdeck" {
			t.Fatalf("expected virdeck text, got %q", txt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BroadcastSyncText not invoked")
	}
	select {
	case fn := <-posts:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("BroadcastSyncText UI post missing")
	}

	// Pause via modal, close modal to check live workbench bar, then stop from bar.
	if err := tester.Click("暂停同步"); err != nil {
		t.Fatal(err)
	}
	if !vm.syncStatus.Paused {
		t.Fatal("expected paused=true")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("已暂停 · 主控 account_01 (1/1 台)") {
		t.Fatalf("missing active workbench sync bar: %v", tester.Texts())
	}

	if err := tester.Click("停止同步"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && vm.syncStatus.Active {
		select {
		case fn := <-posts:
			fn()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !svc.closed || vm.syncStatus.Active || !strings.Contains(vm.syncUI.message, "已停止") {
		t.Fatalf("expected sync stopped cleanly: closed=%v active=%v msg=%q", svc.closed, vm.syncStatus.Active, vm.syncUI.message)
	}
}
