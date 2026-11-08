package service

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"virdeck/core/instance"
)

func TestChineseNameRenameKeepsInternalAVD(t *testing.T) {
	m, fe, fa := newTestManager(t, 2, time.Millisecond)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{NamePrefix: "工作账号", SystemImage: "system-images;android-30;google_apis_playstore;x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	inst := created[0]
	if inst.Name != "工作账号" || inst.AVDName == "" || inst.AVDName == inst.Name {
		t.Fatalf("display and AVD names not separated: %+v", inst)
	}
	if err := m.RenameInstance(ctx, inst.ID, "日常使用"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := instance.NewRegistry(m.cfg.RegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := reloaded.Get(inst.ID)
	if persisted.Name != "日常使用" || persisted.AVDName != inst.AVDName {
		t.Fatalf("rename did not persist independently: %+v", persisted)
	}
	close(fa.bootReady)
	if err := m.Start(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	fe.mu.Lock()
	opts, ok := fe.lastOpts[inst.AVDName]
	fe.mu.Unlock()
	if !ok || opts.AVDName != inst.AVDName {
		t.Fatalf("start used display name: %+v", opts)
	}
	if err := m.Stop(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	clones, err := m.Clone(ctx, inst.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if clones[0].AVDName == inst.AVDName || clones[0].Name == inst.Name {
		t.Fatal("clone reused identity")
	}
	if err := m.Delete(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	fe.mu.Lock()
	deleted := append([]string(nil), fe.deleted...)
	fe.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != inst.AVDName {
		t.Fatalf("delete used display name: %v", deleted)
	}
}

func TestGeneratedBatchNamesRespectDisplayLimit(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Millisecond)
	created, err := m.Create(context.Background(), CreateSpec{NamePrefix: strings.Repeat("中", 64), Count: 2, SystemImage: "system-images;android-30;google_apis_playstore;x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range created {
		if utf8.RuneCountInString(item.Name) > 64 {
			t.Fatalf("generated name too long: %q", item.Name)
		}
	}
	if created[0].Name == created[1].Name {
		t.Fatal("truncated batch names collide")
	}
}
