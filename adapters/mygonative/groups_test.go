package mygonative

import (
	"context"
	"github.com/egoist/mygo/ui"
	"testing"
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
	if err := tester.Click("工作"); err != nil {
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
	vm.groups.name = "日常"
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
