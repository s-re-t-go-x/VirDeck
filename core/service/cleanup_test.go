package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"virdeck/core/adb"
)

func TestCleanupOnlySelectedCachesAndKeepsUserDisks(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Millisecond)
	m.cfg.DataDir = t.TempDir()
	m.cfg.AVDHome = filepath.Join(m.cfg.DataDir, "avd")
	files := map[string]string{
		"downloads/tool.zip": "archive", "downloads/tool.zip.part": "part", "downloads/tool.zip.meta": "meta", "downloads/keep.txt": "keep",
		"avd/test.avd/cache.img": "cache", "avd/test.avd/cache.img.qcow2": "overlay",
		"avd/test.avd/userdata-qemu.img": "user", "avd/test.avd/userdata-qemu.img.qcow2": "useroverlay", "avd/test.avd/sdcard.img": "sd",
	}
	for rel, content := range files {
		path := filepath.Join(m.cfg.DataDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := m.ScanCleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Downloads.Files != 1 || report.Partials.Files != 2 || report.AVDCache.Files != 2 {
		t.Fatalf("wrong candidates: %+v", report)
	}
	removed, err := m.CleanCaches(context.Background(), CleanupOptions{Downloads: true, AVDCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if removed.Files != 3 || removed.Bytes != 19 {
		t.Fatalf("wrong reclaimed total: %+v", removed)
	}
	for _, rel := range []string{"downloads/tool.zip.part", "downloads/tool.zip.meta", "downloads/keep.txt", "avd/test.avd/userdata-qemu.img", "avd/test.avd/userdata-qemu.img.qcow2", "avd/test.avd/sdcard.img"} {
		if _, err := os.Stat(filepath.Join(m.cfg.DataDir, rel)); err != nil {
			t.Errorf("protected file lost %s: %v", rel, err)
		}
	}
}

func TestCleanupRejectsBusyOperationAndRunningAVD(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Millisecond)
	m.cfg.DataDir = t.TempDir()
	m.maintenance.RLock()
	_, err := m.CleanCaches(context.Background(), CleanupOptions{Downloads: true})
	m.maintenance.RUnlock()
	if err == nil {
		t.Fatal("cleanup accepted active installation")
	}
	m.usedPorts[5554] = "starting"
	_, err = m.CleanCaches(context.Background(), CleanupOptions{AVDCache: true})
	if err == nil {
		t.Fatal("cleanup accepted starting emulator")
	}
}

func TestCleanupADBFaultBlocksAVDOnly(t *testing.T) {
	m, _, _ := newTestManager(t, 2, time.Millisecond)
	m.cfg.DataDir = t.TempDir()
	m.adb = adb.NewClient(filepath.Join(t.TempDir(), "missing-adb.exe"))
	path := filepath.Join(m.cfg.DataDir, "downloads", "tool.zip")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("zip"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := m.ScanCleanup(context.Background())
	if err != nil || !report.AVDBlocked || report.AVDBlockReason == "" {
		t.Fatalf("uncertain AVD state accepted: %+v, %v", report, err)
	}
	if _, err := m.CleanCaches(context.Background(), CleanupOptions{AVDCache: true}); err == nil {
		t.Fatal("AVD cleanup allowed on ADB fault")
	}
	removed, err := m.CleanCaches(context.Background(), CleanupOptions{Downloads: true})
	if err != nil || removed.Files != 1 {
		t.Fatalf("download cleanup incorrectly blocked: %+v, %v", removed, err)
	}
}
