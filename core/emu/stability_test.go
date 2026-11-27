package emu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseResolutionPresetsAndCustom(t *testing.T) {
	p, err := ParseResolution("tablet")
	if err != nil || p.Width != 1280 || p.Height != 800 || p.Orientation != "landscape" {
		t.Fatalf("unexpected tablet preset: %+v err=%v", p, err)
	}
	custom, err := ParseResolution("1440x900@240")
	if err != nil || custom.Width != 1440 || custom.Height != 900 || custom.Density != 240 || custom.Orientation != "landscape" {
		t.Fatalf("unexpected custom resolution: %+v err=%v", custom, err)
	}
	if _, err := ParseResolution("100x100"); err == nil {
		t.Fatal("expected out-of-range resolution error")
	}
}

func TestCreateAndUpdateAVDConfigWithStorageAndQuickBoot(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "VirDeckData")
	ctrl := NewControllerWithDataDir(dataDir, "", "", DefaultADBServerPort)
	imgDir := filepath.Join(ctrl.SDKRoot(), "system-images", "android-34", "google_apis_playstore", "x86_64")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgDir, "system.img"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.CreateAVDNativeWithOptions("qb_01", "system-images;android-34;google_apis_playstore;x86_64", CreateAVDOptions{
		MemoryMB:   4096,
		Cores:      4,
		Resolution: "tablet",
		StorageGB:  24,
		QuickBoot:  true,
	}); err != nil {
		t.Fatalf("CreateAVDNativeWithOptions: %v", err)
	}

	cfg := readKeyValueFile(filepath.Join(ctrl.AVDHome(), "qb_01.avd", "config.ini"))
	if cfg["disk.dataPartition.size"] != "24G" || cfg["hw.initialOrientation"] != "landscape" || cfg["fastboot.forceFastBoot"] != "yes" || cfg["fastboot.forceColdBoot"] != "no" {
		t.Fatalf("unexpected created config.ini: %+v", cfg)
	}

	if err := ctrl.UpdateAVDConfig("qb_01", 2048, 2, "1080p", false); err != nil {
		t.Fatalf("UpdateAVDConfig: %v", err)
	}
	updated := readKeyValueFile(filepath.Join(ctrl.AVDHome(), "qb_01.avd", "config.ini"))
	if updated["hw.ramSize"] != "2048" || updated["hw.cpu.ncore"] != "2" || updated["hw.lcd.width"] != "1080" || updated["hw.initialOrientation"] != "portrait" || updated["fastboot.forceColdBoot"] != "yes" {
		t.Fatalf("unexpected updated config.ini: %+v", updated)
	}
}

func TestCleanStaleLocksAndGPUFallback(t *testing.T) {
	avdHome := t.TempDir()
	avdDir := filepath.Join(avdHome, "avd_01.avd")
	if err := os.MkdirAll(avdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hardware-qemu.ini.lock", "multiinstance.lock", "userdata-qemu.img.qcow2"} {
		if err := os.WriteFile(filepath.Join(avdDir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := CleanStaleLocks(avdHome, "avd_01")
	if err != nil || len(removed) != 2 {
		t.Fatalf("CleanStaleLocks removed=%v err=%v", removed, err)
	}
	if !fileExists(filepath.Join(avdDir, "userdata-qemu.img.qcow2")) {
		t.Fatal("qcow2 disk file must not be removed")
	}

	if !IsGPUStartupFailure("ERROR: Could not open OpenGL library") {
		t.Fatal("expected GPU failure match")
	}
	if IsGPUStartupFailure("normal boot log") {
		t.Fatal("unexpected GPU failure match")
	}
	if got := FallbackGPUMode("windows", "amd64", "auto"); got != "swiftshader_indirect" {
		t.Fatalf("windows fallback = %q", got)
	}
	if got := FallbackGPUMode("darwin", "arm64", ""); got != "host" {
		t.Fatalf("darwin/arm64 first fallback = %q", got)
	}
	if got := FallbackGPUMode("darwin", "arm64", "host"); got != "swiftshader_indirect" {
		t.Fatalf("darwin/arm64 second fallback = %q", got)
	}
	if got := FallbackGPUMode("windows", "amd64", "swiftshader_indirect"); got != "" {
		t.Fatalf("exhausted fallback = %q", got)
	}
}

func TestBuildStartArgsQuickBootAndGPU(t *testing.T) {
	cold := strings.Join(buildStartArgs(LaunchOptions{AVDName: "a", Port: 5554, NoSnapshot: true}), " ")
	if !strings.Contains(cold, "-no-snapshot") || strings.Contains(cold, "-no-snapshot-load") {
		t.Fatalf("unexpected cold boot args: %s", cold)
	}
	quick := strings.Join(buildStartArgs(LaunchOptions{AVDName: "a", Port: 5554, NoSnapshot: false, GPUMode: "swiftshader_indirect"}), " ")
	if strings.Contains(quick, "-no-snapshot") || !strings.Contains(quick, "-gpu swiftshader_indirect") {
		t.Fatalf("unexpected quick boot args: %s", quick)
	}
	heal := strings.Join(buildStartArgs(LaunchOptions{AVDName: "a", Port: 5554, NoSnapshot: false, NoSnapshotLoad: true}), " ")
	if !strings.Contains(heal, "-no-snapshot-load") {
		t.Fatalf("unexpected self-heal cold load args: %s", heal)
	}
}
