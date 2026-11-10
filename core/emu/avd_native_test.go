package emu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAVDNativeAndHealIniPaths(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "VirDeckData")
	ctrl := NewControllerWithDataDir(dataDir, "", "", DefaultADBServerPort)

	pkgID := "system-images;android-34;google_apis_playstore;x86_64"
	imgDir := filepath.Join(ctrl.SDKRoot(), "system-images", "android-34", "google_apis_playstore", "x86_64")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgDir, "system.img"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.CreateAVDNative("work_01", pkgID, 3072, 2, "720p"); err != nil {
		t.Fatalf("CreateAVDNative failed: %v", err)
	}

	// Verify package.xml, platforms/android-34/build.prop, and platform-tools/ were generated
	// (Android Emulator 37.x requires both platforms/ and platform-tools/ inside ANDROID_SDK_ROOT).
	if !fileExists(filepath.Join(imgDir, "package.xml")) {
		t.Fatalf("expected package.xml to be generated in system image folder")
	}
	if !fileExists(filepath.Join(ctrl.SDKRoot(), "platforms", "android-34", "build.prop")) {
		t.Fatalf("expected platforms/android-34/build.prop placeholder")
	}
	if !dirExists(filepath.Join(ctrl.SDKRoot(), "platform-tools")) {
		t.Fatalf("expected platform-tools directory placeholder inside SDKRoot")
	}

	// Verify config.ini contents.
	cfgBytes, err := os.ReadFile(filepath.Join(ctrl.AVDHome(), "work_01.avd", "config.ini"))
	if err != nil {
		t.Fatalf("read config.ini: %v", err)
	}
	cfgText := string(cfgBytes)
	for _, want := range []string{
		"AvdId=work_01",
		"PlayStore.enabled=true",
		"hw.lcd.width=720",
		"hw.lcd.height=1280",
		"hw.lcd.density=320",
		"hw.ramSize=3072",
		"hw.cpu.ncore=2",
		"fastboot.forceColdBoot=yes",
	} {
		if !strings.Contains(cfgText, want) {
			t.Errorf("config.ini missing %q:\n%s", want, cfgText)
		}
	}

	// Simulate moving the VirDeckData folder by corrupting path= in work_01.ini and healing it.
	iniPath := filepath.Join(ctrl.AVDHome(), "work_01.ini")
	_ = os.WriteFile(iniPath, []byte("avd.ini.encoding=UTF-8\npath=C:\\OldMovedPath\\work_01.avd\npath.rel=avd\\work_01.avd\ntarget=android-34\n"), 0o644)
	if err := ctrl.HealAVDIniPaths(); err != nil {
		t.Fatalf("HealAVDIniPaths: %v", err)
	}
	healed, _ := os.ReadFile(iniPath)
	expectedPath := "path=" + filepath.Join(ctrl.AVDHome(), "work_01.avd")
	if !strings.Contains(string(healed), expectedPath) {
		t.Fatalf("expected healed ini to contain %q, got:\n%s", expectedPath, string(healed))
	}

	// Verify sdkRootForAVD resolves the SDK root containing the AVD's system image.
	if got := ctrl.sdkRootForAVD("work_01"); got != ctrl.SDKRoot() {
		t.Fatalf("sdkRootForAVD(work_01) = %q, want %q", got, ctrl.SDKRoot())
	}

	// Verify sdkRootForAVD resolves the SDK root containing the AVD's system image.
	if got := ctrl.sdkRootForAVD("work_01"); got != ctrl.SDKRoot() {
		t.Fatalf("sdkRootForAVD(work_01) = %q, want %q", got, ctrl.SDKRoot())
	}
}

func TestIsolatedEnvPinsADBAuthenticationKeys(t *testing.T) {
	dir := t.TempDir()
	c := NewControllerWithDataDir(dir, "", "", 5137)
	want := "ADB_VENDOR_KEYS=" + filepath.Join(dir, ".android", "adbkey")
	for _, v := range c.IsolatedEnv() {
		if v == want {
			return
		}
	}
	t.Fatal("isolated emulator and ADB must use the same authentication key")
}

func TestCreateARM64AVDUsesEmulatorCPUArchitecture(t *testing.T) {
	dir := t.TempDir()
	c := NewControllerWithDataDir(dir, filepath.Join(dir, "sdk"), "", DefaultADBServerPort)
	image := filepath.Join(c.SDKRoot(), "system-images", "android-35", "google_apis", "arm64-v8a")
	if err := os.MkdirAll(image, 0755); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateAVDNative("arm_device", "system-images;android-35;google_apis;arm64-v8a", 2048, 2, "720p"); err != nil {
		t.Fatal(err)
	}
	cfg := readKeyValueFile(filepath.Join(c.AVDHome(), "arm_device.avd", "config.ini"))
	if cfg["abi.type"] != "arm64-v8a" || cfg["hw.cpu.arch"] != "arm64" {
		t.Fatalf("incorrect ARM ABI/CPU: %v", cfg)
	}
	ini := readKeyValueFile(filepath.Join(c.AVDHome(), "arm_device.ini"))
	if ini["path.rel"] != filepath.Join("avd", "arm_device.avd") {
		t.Fatalf("nonportable AVD relative path: %q", ini["path.rel"])
	}
}

func TestDefaultDataDirectoryUsesVirDeck(t *testing.T) {
	t.Setenv("VIRDECK_DATA_DIR", "")
	dir := t.TempDir()
	t.Chdir(dir)
	if got := DefaultDataDir(); got != filepath.Join(dir, "VirDeckData") {
		t.Fatalf("default data directory = %q", got)
	}
	override := filepath.Join(dir, "chosen-data")
	t.Setenv("VIRDECK_DATA_DIR", override)
	if got := DefaultDataDir(); got != override {
		t.Fatalf("configured data directory = %q", got)
	}
}
