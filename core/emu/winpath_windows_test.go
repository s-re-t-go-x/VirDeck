//go:build windows

package emu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsJunctionStandardUserReadWriteAndSafeUnlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "测试中文 目录 & 符号", "VirDeckData")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(marker, []byte("precious-user-data"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "ascii_links", junctionHashName(target))
	if err := createNTFSJunction(link, target); err != nil {
		t.Fatalf("createNTFSJunction failed without admin: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	if !sameDirectory(link, target) {
		t.Fatalf("junction %q does not match target %q", link, target)
	}
	got, err := os.ReadFile(filepath.Join(link, "keep.txt"))
	if err != nil || string(got) != "precious-user-data" {
		t.Fatalf("read via junction failed: got=%q err=%v", got, err)
	}

	// Idempotent re-creation must succeed cleanly.
	if err := createNTFSJunction(link, target); err != nil {
		t.Fatalf("idempotent createNTFSJunction failed: %v", err)
	}

	// Removing the junction entry must NOT delete the underlying target file.
	if err := os.Remove(link); err != nil {
		t.Fatalf("unlink junction failed: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("target data was lost after removing junction: %v", err)
	}
}

func TestWindowsNonASCIIControllerResolutionAndAVDIni(t *testing.T) {
	base := t.TempDir()
	// Put a real Chinese + space directory under temp; use an isolated pure-ASCII junction base inside temp as well.
	targetData := filepath.Join(base, "测试 模拟器目录", "VirDeckData")
	asciiLinkBase := filepath.Join(base, "links")

	eff, mode, err := resolveWindowsASCIIPathWithBases(targetData, []string{asciiLinkBase})
	if err != nil {
		t.Fatalf("resolveWindowsASCIIPathWithBases failed: %v", err)
	}
	t.Cleanup(func() {
		if mode == PathCompatJunction {
			_ = os.Remove(eff)
		}
	})

	if !IsPureASCII(eff) {
		t.Fatalf("effective path %q is not pure ASCII (mode=%s)", eff, mode)
	}
	if mode != PathCompatShortPath && mode != PathCompatJunction {
		t.Fatalf("unexpected compat mode %q", mode)
	}
	if !sameDirectory(eff, targetData) {
		t.Fatalf("effective %q != target %q", eff, targetData)
	}

	// Seed a fake installed system image inside the Chinese data dir and verify NewControllerWithDataDir
	// exposes pure-ASCII SDKRoot / AVDHome / IsolatedEnv / AVD .ini paths when resolved via shortpath or controller.
	// Point PUBLIC to asciiLinkBase parent only via direct junction verification when 8.3 is off.
	ctrl := NewControllerWithDataDir(targetData, "", "", DefaultADBServerPort)
	t.Cleanup(func() {
		if ctrl.PathCompatMode() == PathCompatJunction {
			_ = os.Remove(ctrl.EffectiveDataDir())
		}
	})

	info := ctrl.InspectEnv(context.Background())
	if !info.PathASCIIOK || !info.PathWritableOK {
		t.Fatalf("controller failed to resolve non-ASCII dataDir transparently: %+v", info)
	}
	if !IsPureASCII(info.EffectiveDataDir) || !IsPureASCII(info.SDKRoot) || !IsPureASCII(info.AVDHome) {
		t.Fatalf("expected all runtime SDK/AVD paths to be pure ASCII, got %+v", info)
	}
	for _, kv := range ctrl.IsolatedEnv() {
		if strings.HasPrefix(kv, "ANDROID_") || strings.HasPrefix(kv, "ADB_VENDOR_KEYS=") {
			_, val, _ := strings.Cut(kv, "=")
			if !IsPureASCII(val) {
				t.Fatalf("IsolatedEnv leaked non-ASCII path: %s", kv)
			}
		}
	}

	// Verify AVD creation + HealAVDIniPaths writes pure-ASCII path= into .ini.
	sysImgDir := filepath.Join(ctrl.ManagedSDKRoot(), "system-images", "android-34", "google_apis_playstore", "x86_64")
	if err := os.MkdirAll(sysImgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysImgDir, "system.img"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.CreateAVD(context.Background(), "avd_winascii", "system-images;android-34;google_apis_playstore;x86_64", 2048, 2); err != nil {
		t.Fatalf("CreateAVD failed: %v", err)
	}
	iniRaw, err := os.ReadFile(filepath.Join(ctrl.AVDHome(), "avd_winascii.ini"))
	if err != nil {
		t.Fatalf("read avd ini: %v", err)
	}
	if !IsPureASCII(string(iniRaw)) {
		t.Fatalf("AVD .ini must remain pure ASCII for emulator.exe, got:\n%s", string(iniRaw))
	}

	// Ensure cleanup of generated test AVD when junction was created in temp.
	_ = ctrl.DeleteAVD(context.Background(), "avd_winascii")
}

func TestWindowsDataDirOverrideAndSuggestASCII(t *testing.T) {
	tmp := t.TempDir()
	badDir := filepath.Join(tmp, "非英文目录")
	ptr := filepath.Join(tmp, "ptr", dataDirPointerFile)
	if _, err := saveDataDirOverrideTo(badDir, "windows", []string{ptr}); err == nil {
		t.Fatal("expected Windows non-ASCII DataDir override to be rejected")
	}

	goodDir := filepath.Join(tmp, "VirDeckASCII")
	written, err := saveDataDirOverrideTo(goodDir, "windows", []string{ptr})
	if err != nil || written != ptr {
		t.Fatalf("saveDataDirOverrideTo failed: written=%q err=%v", written, err)
	}
	got := readDataDirOverride(filepath.Dir(ptr), "")
	if got != goodDir {
		t.Fatalf("readDataDirOverride = %q, want %q", got, goodDir)
	}

	suggested := SuggestPureASCIIDataDir()
	if !IsPureASCII(suggested) || strings.TrimSpace(suggested) == "" {
		t.Fatalf("SuggestPureASCIIDataDir returned invalid path: %q", suggested)
	}
}
