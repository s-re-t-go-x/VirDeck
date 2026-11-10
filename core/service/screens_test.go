package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"virdeck/config"
	"virdeck/core/emu"
	"virdeck/core/screenlayout"
)

func TestScreenLayoutScopeAndPersistence(t *testing.T) {
	m, f, _ := newTestManager(t, 2, time.Second)
	m.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	m.settings = Settings{MaxConcurrent: 3, DownloadProxy: "http://localhost:7890"}
	created, err := m.Create(context.Background(), CreateSpec{NamePrefix: "screen", Count: 2, SystemImage: "system-images;android-30;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	f.displays = []screenlayout.Display{{ID: "primary", Primary: true, Scale: 1, WorkArea: screenlayout.Rect{Width: 1920, Height: 1080}}}
	f.screens = []emu.ProjectionWindow{{InstanceID: created[0].ID}, {InstanceID: "unknown"}, {InstanceID: created[0].ID}}
	f.arrangedCount = 1
	snap, err := m.InspectScreenLayout(context.Background())
	if err != nil || !reflect.DeepEqual(snap.OpenIDs, []string{created[0].ID}) {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
	req := ScreenLayoutRequest{SelectedOnly: true, IDs: []string{created[1].ID, "unknown", created[1].ID}, DisplayID: "primary", Size: "small"}
	n, err := m.ArrangeScreenLayout(context.Background(), req)
	if err != nil || n != 1 {
		t.Fatalf("arrange %d %v", n, err)
	}
	if !reflect.DeepEqual(f.arrangedIDs, []string{created[1].ID}) || f.arrangedHeight != config.Values.ScreenLayout.SmallHeight || f.arrangedGap != config.Values.ScreenLayout.Gap {
		t.Fatalf("unexpected args %+v", f)
	}
	saved := loadSettings(m.settingsPath)
	if saved.ScreenLayout.Size != "small" || saved.ScreenLayout.DisplayID != "primary" || saved.DownloadProxy != "http://localhost:7890" || saved.MaxConcurrent != 3 {
		t.Fatalf("saved=%+v", saved)
	}
	req.SelectedOnly = false
	if _, err := m.ArrangeScreenLayout(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(f.arrangedIDs) != 2 {
		t.Fatalf("all scope=%v", f.arrangedIDs)
	}
}
func TestScreenLayoutRejectsEmptySelectionAndInvalidSize(t *testing.T) {
	m, f, _ := newTestManager(t, 2, time.Second)
	for _, req := range []ScreenLayoutRequest{{SelectedOnly: true, Size: "auto"}, {Size: "giant"}} {
		if _, err := m.ArrangeScreenLayout(context.Background(), req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	if f.arrangedIDs != nil {
		t.Fatal("invalid request reached window backend")
	}
}
func TestScreenLayoutSettingsCompatibilityAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"maxConcurrent":4,"licenseAccepted":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := loadSettings(path)
	if s.ScreenLayout.Size != config.Values.ScreenLayout.Size || !s.LicenseAccepted {
		t.Fatalf("legacy=%+v", s)
	}
	m, f, _ := newTestManager(t, 2, time.Second)
	m.settingsPath = path
	m.settings = s
	f.arrangeErr = errors.New("display disconnected")
	if _, err := m.ArrangeScreenLayout(context.Background(), ScreenLayoutRequest{Size: "large", DisplayID: "gone"}); err == nil {
		t.Fatal("expected failure")
	}
	if got := loadSettings(path); got.ScreenLayout.Size != "auto" {
		t.Fatalf("failed operation saved prefs %+v", got)
	}
}

func TestScreenLayoutSaveRestoreAndClosedWindows(t *testing.T) {
	m, f, _ := newTestManager(t, 2, time.Second)
	m.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	instances, err := m.Create(context.Background(), CreateSpec{NamePrefix: "saved", Count: 2, SystemImage: "system-images;android-30;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	f.displays = []screenlayout.Display{{ID: "main", Primary: true, Scale: 1, WorkArea: screenlayout.Rect{Width: 1000, Height: 900}}}
	f.screens = []emu.ProjectionWindow{{InstanceID: instances[0].ID, Window: screenlayout.Window{Handle: 1, Bounds: screenlayout.Rect{X: 40, Y: 20, Width: 200, Height: 340}, ClientWidth: 180, ClientHeight: 300, Scale: 1}}, {InstanceID: "foreign", Window: screenlayout.Window{Bounds: screenlayout.Rect{Width: 200, Height: 300}}}}
	n, err := m.SaveScreenLayout(context.Background(), ScreenLayoutRequest{SelectedOnly: true, IDs: []string{instances[0].ID}})
	if n != 1 || err != nil {
		t.Fatalf("save %d %v", n, err)
	}
	records, err := m.readScreenLayout()
	if err != nil || len(records) != 1 || records[0].InstanceID != instances[0].ID || records[0].Bounds.X != 40 {
		t.Fatalf("records %+v %v", records, err)
	}
	f.arrangedCount = 1
	if n, err := m.RestoreScreenLayout(context.Background(), ScreenLayoutRequest{}); n != 1 || err != nil {
		t.Fatalf("restore %d %v", n, err)
	}
	if len(f.restored) != 1 || f.restored[0].DisplayID != "main" {
		t.Fatalf("restored %+v", f.restored)
	}
	f.screens = nil
	if _, err := m.SaveScreenLayout(context.Background(), ScreenLayoutRequest{}); err == nil {
		t.Fatal("empty capture overwrote saved layout")
	}
	if got, _ := m.readScreenLayout(); len(got) != 1 {
		t.Fatal("saved layout lost")
	}
}
func TestScreenLayoutAutoFillOnlyForNewProjection(t *testing.T) {
	m, f, _ := newTestManager(t, 2, time.Second)
	instances, err := m.Create(context.Background(), CreateSpec{NamePrefix: "auto", Count: 1, SystemImage: "system-images;android-30;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	id := instances[0].ID
	_, _ = m.registry.Update(id, func(i *Instance) error { i.State = Running; i.Port = 5554; return nil })
	m.settings.ScreenLayout = ScreenLayoutPreferences{Size: "small", DisplayID: "main", AutoFill: true, ClientHeight: 300}
	f.arrangedCount = 1
	f.screenStart = func(id string) {
		if len(f.screens) == 0 {
			f.screens = []emu.ProjectionWindow{{InstanceID: id, Window: screenlayout.Window{Handle: 1, PID: 2}}}
		}
	}
	if err := m.OpenScreen(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if !f.lastScreenOptions.FillOnly || !reflect.DeepEqual(f.arrangedIDs, []string{id}) || f.lastScreenOptions.Height != 300 {
		t.Fatalf("auto fill %+v %v", f.lastScreenOptions, f.arrangedIDs)
	}
	f.arrangedIDs = nil
	if err := m.OpenScreen(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if f.arrangedIDs != nil {
		t.Fatal("existing projection moved on activation")
	}
}

func TestScreenLayoutRemembersHeightInRequestOrder(t *testing.T) {
	m, f, _ := newTestManager(t, 2, time.Second)
	records, err := m.Create(context.Background(), CreateSpec{NamePrefix: "height", Count: 2, SystemImage: "system-images;android-30;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	f.screens = []emu.ProjectionWindow{{InstanceID: records[1].ID, Window: screenlayout.Window{ClientHeight: 600, Scale: 1}}, {InstanceID: records[0].ID, Window: screenlayout.Window{ClientHeight: 300, Scale: 1}}}
	f.arrangedCount = 1
	_, err = m.ArrangeScreenLayout(context.Background(), ScreenLayoutRequest{SelectedOnly: true, IDs: []string{records[0].ID, records[1].ID}, Size: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.GetSettings().ScreenLayout.ClientHeight; got != 300 {
		t.Fatalf("remembered height %d instead of actual first requested window", got)
	}
}
func TestScreenLayoutAutoFillWaitsForFirstLayout(t *testing.T) {
	m, f, _ := newTestManager(t, 2, time.Second)
	m.settings.ScreenLayout = ScreenLayoutPreferences{AutoFill: true, Size: "auto"}
	f.arrangedCount = 1
	f.screens = []emu.ProjectionWindow{{InstanceID: "one", Window: screenlayout.Window{Handle: 1, PID: 2}}}
	if err := m.fillNewScreen(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if f.arrangedIDs != nil {
		t.Fatal("auto-fill acted before a successful layout")
	}
}

type positionedEmu struct {
	*fakeEmu
	bounds screenlayout.Rect
	called bool
}

func (f *positionedEmu) StartProjectionWithBounds(_ context.Context, _ string, _ int, _ string, _ emu.ProjectionSettings, r screenlayout.Rect) (bool, error) {
	f.bounds = r
	f.called = true
	return true, nil
}
func TestOpenScreenAtPassesGeometryAndRejectsInvalidBounds(t *testing.T) {
	m, f, _ := newTestManager(t, 2, time.Second)
	records, err := m.Create(context.Background(), CreateSpec{NamePrefix: "position", Count: 1, SystemImage: "system-images;android-30;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2})
	if err != nil {
		t.Fatal(err)
	}
	id := records[0].ID
	_, _ = m.registry.Update(id, func(i *Instance) error { i.State = Running; i.Port = 5554; return nil })
	backend := &positionedEmu{fakeEmu: f}
	m.emu = backend
	m.settings.ScreenLayout.AutoFill = true
	want := screenlayout.Rect{X: -400, Y: 40, Width: 360, Height: 640}
	if err := m.OpenScreenAt(context.Background(), id, want); err != nil {
		t.Fatal(err)
	}
	if !backend.called || backend.bounds != want {
		t.Fatalf("lost startup geometry: %+v", backend)
	}
	backend.called = false
	if err := m.OpenScreenAt(context.Background(), id, screenlayout.Rect{}); err == nil || backend.called {
		t.Fatal("invalid bounds reached backend")
	}
}
