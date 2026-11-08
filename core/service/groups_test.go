package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestGroupsPersistAndDeleteWithoutDeletingInstances(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Second)
	m.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	records, err := m.Create(context.Background(), CreateSpec{NamePrefix: "group", Count: 2, SystemImage: "system-images;android-30;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	group, err := m.CreateGroup(context.Background(), "工作")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AssignGroup(context.Background(), []string{records[0].ID}, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.RenameGroup(context.Background(), group.ID, "日常"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.ListGroups(context.Background())
	if err != nil || len(snapshot.Groups) != 1 || snapshot.Groups[0].Name != "日常" || snapshot.Memberships[records[0].ID] != group.ID {
		t.Fatalf("snapshot %+v %v", snapshot, err)
	}
	if err := m.AssignGroup(context.Background(), []string{records[1].ID, "missing"}, group.ID); err == nil {
		t.Fatal("unknown member accepted")
	}
	snapshot, _ = m.ListGroups(context.Background())
	if snapshot.Memberships[records[1].ID] != "" {
		t.Fatal("invalid batch partially changed membership")
	}
	if _, err := m.CreateGroup(context.Background(), " 日常 "); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := m.DeleteGroup(context.Background(), group.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = m.ListGroups(context.Background())
	if len(snapshot.Groups) != 0 || len(snapshot.Memberships) != 0 || len(m.registry.List()) != 2 {
		t.Fatalf("delete changed instances %+v", snapshot)
	}
}

func TestGroupCreateOptionNameIsReserved(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Second)
	if _, err := m.CreateGroup(context.Background(), "新建分组"); err == nil {
		t.Fatal("management create option label accepted as a group")
	}
}
