package mygonative

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
	"time"
	"virdeck/core/service"
)

func TestGroupFilteringKeepsBatchScopeVisible(t *testing.T) {
	vm := newTestVM(t)
	vm.groups.snapshot = service.GroupSnapshot{Groups: []service.Group{{ID: "work", Name: "工作"}}, Memberships: map[string]string{"a1b2c3d4": "work"}}
	vm.selected.Add("0a0b0c0d")
	tester := ui.NewTester(vm.view, 610, 780)
	tester.Frame()
	if err := tester.Click("分组筛选"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("工作 (1)"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("全部 1") {
		t.Fatal("all count ignores selected group")
	}
	if len(vm.visible) != 1 || vm.visible[0].ID != "a1b2c3d4" || len(vm.selectedInsts()) != 0 {
		t.Fatalf("group scope %v %v", vm.visible, vm.selectedInsts())
	}
}
func TestGroupManagerCreatesRenamesDeletesMetadata(t *testing.T) {
	vm := newTestVM(t)
	tester := ui.NewTester(vm.view, 610, 480)
	tester.Frame()
	if err := tester.Click("管理分组"); err != nil {
		t.Fatal(err)
	}
	vm.groups.name = "工作"
	tester.Frame()
	if err := tester.Click("创建分组"); err != nil {
		t.Fatal(err)
	}
	snap, err := vm.svc.ListGroups(context.Background())
	if err != nil || len(snap.Groups) != 1 {
		t.Fatalf("create %+v %v", snap, err)
	}
	vm.groups.editID = snap.Groups[0].ID
	vm.groups.editName = "日常"
	tester.Frame()
	if err := tester.Click("保存名称"); err != nil {
		t.Fatal(err)
	}
	snap, _ = vm.svc.ListGroups(context.Background())
	if snap.Groups[0].Name != "日常" {
		t.Fatal("rename failed")
	}
	if err := tester.Click("删除分组"); err != nil {
		t.Fatal(err)
	}
	snap, _ = vm.svc.ListGroups(context.Background())
	if len(snap.Groups) != 0 {
		t.Fatal("delete failed")
	}
}

func TestGroupFilterCountsAreIndependentOfStatusAndSearch(t *testing.T) {
	vm := newTestVM(t)
	vm.groups.snapshot = service.GroupSnapshot{Groups: []service.Group{{ID: "work", Name: "工作"}}, Memberships: map[string]string{"a1b2c3d4": "work", "e5f60718": "work"}}
	vm.query = "account_01"
	vm.filterTab = 1
	tester := ui.NewTester(vm.view, 1180, 780)
	tester.Frame()
	if err := tester.Click("分组筛选"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("工作 (2)") || !tester.HasText("未分组 (2)") || !tester.HasText("全部分组 (4)") {
		t.Fatal("missing complete group counts")
	}
}

type groupingTestSvc struct {
	service.Service
	assigned   chan string
	failAssign bool
	created    []service.Instance
}

func (s *groupingTestSvc) AssignGroup(_ context.Context, ids []string, id string) error {
	if s.failAssign {
		return fmt.Errorf("temporary write error")
	}
	s.assigned <- strings.Join(ids, ",") + "|" + id
	return nil
}
func (s *groupingTestSvc) Create(context.Context, service.CreateSpec) ([]service.Instance, error) {
	return s.created, nil
}
func (s *groupingTestSvc) Clone(context.Context, string, int) ([]service.Instance, error) {
	return s.created, nil
}
func TestNewGroupMoveRetryDoesNotCreateDuplicate(t *testing.T) {
	vm := newTestVM(t)
	svc := &groupingTestSvc{Service: vm.svc, failAssign: true, assigned: make(chan string, 4)}
	vm.svc = svc
	vm.openGroups([]string{"a1b2c3d4"})
	vm.groups.name = "工作"
	vm.createGroupFromForm()
	if vm.groups.pendingGroupID == "" || !strings.Contains(vm.groups.message, "移入失败") {
		t.Fatal("partial failure hidden")
	}
	snap, _ := vm.svc.ListGroups(context.Background())
	if len(snap.Groups) != 1 {
		t.Fatal("new group missing")
	}
	id := snap.Groups[0].ID
	svc.failAssign = false
	vm.createGroupFromForm()
	snap, _ = vm.svc.ListGroups(context.Background())
	if len(snap.Groups) != 1 {
		t.Fatal("retry duplicated group")
	}
	if got := <-svc.assigned; got != "a1b2c3d4|"+id {
		t.Fatalf("wrong frozen move targets %s", got)
	}
	if vm.groups.pendingGroupID != "" || !strings.HasPrefix(vm.groups.message, "✓") {
		t.Fatal("missing move success")
	}
}
func TestCreateInstanceInheritsAndFreezesSelectedGroup(t *testing.T) {
	vm := newTestVM(t)
	g, err := vm.svc.CreateGroup(context.Background(), "工作")
	if err != nil {
		t.Fatal(err)
	}
	vm.refreshGroups()
	vm.groups.active = g.ID
	svc := &groupingTestSvc{Service: vm.svc, created: []service.Instance{{ID: "new-device", Name: "新设备"}}, assigned: make(chan string, 4)}
	vm.svc = svc
	posts := make(chan func(), 32)
	vm.post = func(f func()) { posts <- f }
	tester := ui.NewTester(vm.view, 610, 480)
	tester.Frame()
	if err := tester.Click("新建实例"); err != nil {
		t.Fatal(err)
	}
	if vm.createGroupID != g.ID {
		t.Fatal("create did not inherit current group")
	}
	tester.Frame()
	if err := tester.Click("立即创建"); err != nil {
		t.Fatal(err)
	}
	vm.createGroupID = ""
	vm.groups.active = ""
	select {
	case got := <-svc.assigned:
		if got != "new-device|"+g.ID {
			t.Fatalf("wrong create group %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("created device not assigned")
	}
	drainProjectionTask(t, vm, posts)
}

func TestCloneInstanceInheritsSourceGroup(t *testing.T) {
	vm := newTestVM(t)
	g, err := vm.svc.CreateGroup(context.Background(), "工作")
	if err != nil {
		t.Fatal(err)
	}
	vm.refreshGroups()
	vm.groups.snapshot.Memberships = map[string]string{"0a0b0c0d": g.ID}
	svc := &groupingTestSvc{Service: vm.svc, created: []service.Instance{{ID: "cloned-01", Name: "克隆设备"}}, assigned: make(chan string, 4)}
	vm.svc = svc
	posts := make(chan func(), 32)
	vm.post = func(f func()) { posts <- f }
	for _, inst := range vm.instances {
		if inst.ID == "0a0b0c0d" {
			vm.perform(actClone, []service.Instance{inst})
			break
		}
	}
	if vm.cloneGroupID != g.ID {
		t.Fatalf("clone did not inherit source group: got %q want %q", vm.cloneGroupID, g.ID)
	}
	tester := ui.NewTester(vm.view, 610, 480)
	tester.Frame()
	if err := tester.Click("开始克隆"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-svc.assigned:
		if got != "cloned-01|"+g.ID {
			t.Fatalf("wrong clone group %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cloned device not assigned to group")
	}
	drainProjectionTask(t, vm, posts)
}
