package service

import (
	"context"
	"testing"
	"time"
)

func TestEditInstancePersistsAndAppliesAtNextStart(t *testing.T) {
	m, fe, fa := newTestManager(t, 2, time.Millisecond)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{NamePrefix: "原名称", SystemImage: "system-images;android-30;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	inst := created[0]
	close(fa.bootReady)
	if err := m.Start(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	spec := InstanceSettings{Name: "工作设备", MemoryMB: 4096, Cores: 4, Proxy: "http://127.0.0.1:7890", Headless: true}
	if err := m.EditInstance(ctx, inst.ID, spec); err != nil {
		t.Fatal(err)
	}
	got, _ := m.registry.Get(inst.ID)
	if got.Name != spec.Name || got.MemoryMB != 4096 || got.Cores != 4 || got.Proxy != spec.Proxy || !got.Headless || got.InternalName() != inst.InternalName() || got.SystemImage != inst.SystemImage {
		t.Fatalf("edit lost settings or changed identity: %+v", got)
	}
	fe.mu.Lock()
	live := fe.lastOpts[inst.InternalName()]
	fe.mu.Unlock()
	if live.MemoryMB != 2048 || live.Cores != 2 {
		t.Fatal("edit changed already-running process options")
	}
	if err := m.Stop(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	fe.mu.Lock()
	next := fe.lastOpts[inst.InternalName()]
	fe.mu.Unlock()
	if next.MemoryMB != 4096 || next.Cores != 4 || next.Proxy != spec.Proxy || !next.Headless {
		t.Fatalf("next start ignored saved settings: %+v", next)
	}
	if err := m.Stop(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
}

func TestEditInstanceRejectsInvalidSettingsWithoutPartialRename(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Millisecond)
	created, err := m.Create(context.Background(), CreateSpec{NamePrefix: "原名", SystemImage: "system-images;android-30;google_apis_playstore;x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []InstanceSettings{{Name: "新名", MemoryMB: 0, Cores: 2}, {Name: "新名", MemoryMB: 2048, Cores: 99}, {Name: "", MemoryMB: 2048, Cores: 2}} {
		if err := m.EditInstance(context.Background(), created[0].ID, bad); err == nil {
			t.Fatalf("accepted invalid settings: %+v", bad)
		}
		got, _ := m.registry.Get(created[0].ID)
		if got.Name != "原名" {
			t.Fatal("invalid edit partially saved name")
		}
	}
}
