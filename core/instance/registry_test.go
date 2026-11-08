package instance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyNameRetainedAfterDisplayRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.json")
	if err := os.WriteFile(path, []byte(`[{"id":"old","name":"account_01","state":"stopped"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	reg, err := NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := reg.Get("old")
	if item.AVDName != "account_01" {
		t.Fatalf("legacy AVD name missing: %+v", item)
	}
	if _, err := reg.Update("old", func(r *Record) error { r.Name = "中文名称"; return nil }); err != nil {
		t.Fatal(err)
	}
	reg, err = NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	item, _ = reg.Get("old")
	if item.Name != "中文名称" || item.InternalName() != "account_01" {
		t.Fatalf("legacy identity moved: %+v", item)
	}
}

func TestUpdateRollsBackDisplayNameWhenSavingFails(t *testing.T) {
	reg, err := NewRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(Record{ID: "id", Name: "原名", AVDName: "internal"}); err != nil {
		t.Fatal(err)
	}
	reg.path = filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(reg.path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Update("id", func(r *Record) error { r.Name = "新名"; return nil }); err == nil {
		t.Fatal("expected persistence error")
	}
	got, _ := reg.Get("id")
	if got.Name != "原名" || got.AVDName != "internal" {
		t.Fatalf("failed rename leaked into memory: %+v", got)
	}
}
