package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"virdeck/config"
)

func TestProjectionSettingsInheritanceAndPersistence(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Second)
	m.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	created, err := m.Create(context.Background(), CreateSpec{NamePrefix: "settings", Count: 1, SystemImage: "system-images;android-30;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0].ID
	s := m.GetSettings()
	s.Projection = ProjectionSettings{MaxFPS: 30, MaxSize: 1280, BitRateMbps: 4, Audio: true}
	if err := m.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	inherited, custom, err := m.GetProjectionSettings(id)
	if err != nil || custom || inherited != s.Projection {
		t.Fatalf("inherit %+v %v %v", inherited, custom, err)
	}
	override := ProjectionSettings{MaxFPS: 15, MaxSize: 960, BitRateMbps: 2}
	if err := m.SetProjectionSettings(context.Background(), id, &override); err != nil {
		t.Fatal(err)
	}
	loaded := loadSettings(m.settingsPath)
	if loaded.ProjectionOverrides[id] != override {
		t.Fatalf("saved %+v", loaded)
	}
	snapshot := m.GetSettings()
	snapshot.ProjectionOverrides[id] = s.Projection
	got, custom, _ := m.GetProjectionSettings(id)
	if got != override || !custom {
		t.Fatal("GetSettings map leaked mutable state")
	}
	if err := m.SetProjectionSettings(context.Background(), id, nil); err != nil {
		t.Fatal(err)
	}
	got, custom, _ = m.GetProjectionSettings(id)
	if custom || got != s.Projection {
		t.Fatal("reset inheritance failed")
	}
}
func TestProjectionSettingsRejectInvalidValues(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Second)
	for _, p := range []ProjectionSettings{{MaxFPS: -1, BitRateMbps: 8}, {MaxSize: 1, BitRateMbps: 8}, {BitRateMbps: 101}} {
		s := m.GetSettings()
		s.Projection = p
		if err := m.UpdateSettings(s); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}

func TestInvalidSettingsLoadWarningIsVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("broken JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if s := loadSettings(path); s.LoadWarning == "" {
		t.Fatal("silently ignored invalid configuration")
	}
}

func TestLegacyZeroProjectionSettingsUseNumericDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"projection":{"maxFPS":0,"maxSize":0,"bitRateMbps":3,"audio":true},"projectionOverrides":{"one":{"maxFPS":0,"maxSize":960,"bitRateMbps":2,"audio":true}}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	s := loadSettings(path)
	if s.Projection.MaxFPS != 60 || s.Projection.MaxSize != 1280 || s.Projection.BitRateMbps != 3 || !s.Projection.Audio {
		t.Fatalf("legacy global settings were not migrated without losing preferences: %+v", s.Projection)
	}
	p := s.ProjectionOverrides["one"]
	if p.MaxFPS != 60 || p.MaxSize != 960 || p.BitRateMbps != 2 || !p.Audio {
		t.Fatalf("legacy override was not preserved: %+v", p)
	}
}

func TestProjectionSettingsMinimumValues(t *testing.T) {
	for _, p := range []ProjectionSettings{
		{MaxFPS: 0, MaxSize: 1280, BitRateMbps: 8},
		{MaxFPS: 4, MaxSize: 1280, BitRateMbps: 8},
		{MaxFPS: 60, MaxSize: 0, BitRateMbps: 8},
		{MaxFPS: 60, MaxSize: 319, BitRateMbps: 8},
	} {
		if err := config.ValidateProjection(p); err == nil {
			t.Errorf("accepted below-minimum projection settings: %+v", p)
		}
	}
	if err := config.ValidateProjection(ProjectionSettings{MaxFPS: 5, MaxSize: 320, BitRateMbps: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionSettingsUpperLimits(t *testing.T) {
	for _, p := range []ProjectionSettings{
		{MaxFPS: 241, MaxSize: 1280, BitRateMbps: 8},
		{MaxFPS: 60, MaxSize: 8193, BitRateMbps: 8},
		{MaxFPS: 60, MaxSize: 1280, BitRateMbps: 101},
	} {
		if err := config.ValidateProjection(p); err == nil {
			t.Errorf("accepted above-maximum projection settings: %+v", p)
		}
	}
	if err := config.ValidateProjection(ProjectionSettings{MaxFPS: 240, MaxSize: 8192, BitRateMbps: 100}); err != nil {
		t.Fatal(err)
	}
}
