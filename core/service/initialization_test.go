package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstRunCreatesConfigFilesAndKeepsUserChanges(t *testing.T) {
	data := filepath.Join(t.TempDir(), "VirDeckData")
	cfg := Config{DataDir: data, SDKRoot: filepath.Join(data, "sdk"), AVDHome: filepath.Join(data, "avd"), SkipTCPCheck: true}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.json", "instances.json", "images_catalog.json"} {
		raw, err := os.ReadFile(filepath.Join(data, "config", name))
		if err != nil || len(raw) == 0 {
			t.Fatalf("first run did not initialize %s: %v", name, err)
		}
	}
	st := m.GetSettings()
	st.MaxConcurrent = 3
	if err := m.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := m.registry.Add(Instance{ID: "existing", Name: "保留实例", AVDName: "old_avd", State: Stopped, MemoryMB: 2048, Cores: 2}); err != nil {
		t.Fatal(err)
	}
	id := "system-images;android-30;google_apis_playstore;x86_64"
	if err := m.cat.SetFavorite(id, false, ""); err != nil {
		t.Fatal(err)
	}
	before := make(map[string]string)
	for _, name := range []string{"settings.json", "instances.json", "images_catalog.json"} {
		raw, err := os.ReadFile(filepath.Join(data, "config", name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = string(raw)
	}
	again, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if again.GetSettings().MaxConcurrent != 3 {
		t.Fatal("startup replaced saved user settings")
	}
	for name, want := range before {
		raw, err := os.ReadFile(filepath.Join(data, "config", name))
		if err != nil || string(raw) != want {
			t.Fatalf("startup replaced existing %s: %v", name, err)
		}
	}
}
