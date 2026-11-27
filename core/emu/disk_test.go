package emu

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	if os.Getenv("VIRDECK_TEST_QEMU_IMG") != "1" {
		return
	}
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "info" {
		fmt.Println(`{"backing-filename":"C:\\sdk\\userdata.img"}`)
		os.Exit(0)
	}
	if len(args) >= 3 && args[0] == "convert" {
		if logPath := os.Getenv("VIRDECK_TEST_QEMU_LOG"); logPath != "" {
			_ = os.WriteFile(logPath, []byte(strings.Join(args, " ")), 0o644)
		}
		outPath := args[len(args)-1]
		payload := []byte("mini")
		if os.Getenv("VIRDECK_TEST_QEMU_BLOAT") == "1" {
			payload = bytes.Repeat([]byte("b"), 500)
		}
		if err := os.WriteFile(outPath, payload, 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(1)
}

func TestBackupAndRestoreAVDRoundTripAndZipSlipGuard(t *testing.T) {
	avdHome := filepath.Join(t.TempDir(), "avd")
	srcName := "src_avd"
	srcDir := filepath.Join(avdHome, srcName+".avd")
	if err := os.MkdirAll(filepath.Join(srcDir, "snapshots", "default_boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "config.ini"), []byte("hw.ramSize=4096\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "userdata-qemu.img.qcow2"), []byte("qcow2data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "multiinstance.lock"), []byte("lock"), 0o644); err != nil {
		t.Fatal(err)
	}
	iniContent := "avd.ini.encoding=UTF-8\npath=" + srcDir + "\npath.rel=" + filepath.Join("avd", srcName+".avd") + "\ntarget=android-34\n"
	if err := os.WriteFile(filepath.Join(avdHome, srcName+".ini"), []byte(iniContent), 0o644); err != nil {
		t.Fatal(err)
	}

	destZip := filepath.Join(t.TempDir(), "backup.zip")
	metaIn := AVDBackupMeta{
		Version:      1,
		InstanceName: "测试机",
		SystemImage:  "system-images;android-34;google_apis_playstore;x86_64",
		MemoryMB:     4096,
		Cores:        4,
		Resolution:   "1080p",
		StorageGB:    16,
		Note:         "主控号",
	}
	size, err := BackupAVD(avdHome, srcName, destZip, metaIn)
	if err != nil || size <= 0 {
		t.Fatalf("BackupAVD failed: size=%d err=%v", size, err)
	}

	newName := "restored_avd"
	metaOut, err := RestoreAVD(avdHome, newName, destZip)
	if err != nil {
		t.Fatalf("RestoreAVD failed: %v", err)
	}
	if metaOut.InstanceName != "测试机" || metaOut.Note != "主控号" {
		t.Fatalf("unexpected restored meta: %+v", metaOut)
	}

	restoredDir := filepath.Join(avdHome, newName+".avd")
	if data, err := os.ReadFile(filepath.Join(restoredDir, "userdata-qemu.img.qcow2")); err != nil || string(data) != "qcow2data" {
		t.Fatalf("missing restored disk data: err=%v data=%q", err, string(data))
	}
	if _, err := os.Stat(filepath.Join(restoredDir, "multiinstance.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock file must be excluded from backup/restore")
	}
	iniOut, err := os.ReadFile(filepath.Join(avdHome, newName+".ini"))
	if err != nil {
		t.Fatalf("missing restored ini: %v", err)
	}
	if !strings.Contains(string(iniOut), "path="+restoredDir) || !strings.Contains(string(iniOut), "target=android-34") {
		t.Fatalf("unexpected restored ini:\n%s", string(iniOut))
	}

	// Verify Zip Slip entries are rejected without writing outside avdHome.
	badZip := filepath.Join(t.TempDir(), "evil.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("virdeck-backup.json")
	_, _ = w.Write([]byte(`{"version":1,"instanceName":"evil"}`))
	w2, _ := zw.Create("../escape.txt")
	_, _ = w2.Write([]byte("pwn"))
	_ = zw.Close()
	if err := os.WriteFile(badZip, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreAVD(avdHome, "evil_avd", badZip); err == nil {
		t.Fatal("expected Zip Slip error for ../escape.txt")
	}
}

func TestCompactAVDDiskRemovesCachePreservesBackingAndGuardsSize(t *testing.T) {
	dir := t.TempDir()
	avdHome := filepath.Join(dir, "avd")
	avdName := "compact_avd"
	avdDir := filepath.Join(avdHome, avdName+".avd")
	if err := os.MkdirAll(avdDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(avdDir, "userdata-qemu.img.qcow2"), bytes.Repeat([]byte("u"), 120), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(avdDir, "cache.img.qcow2"), bytes.Repeat([]byte("c"), 50), 0o644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "qemu-img.log")
	t.Setenv("VIRDECK_TEST_QEMU_IMG", "1")
	t.Setenv("VIRDECK_TEST_QEMU_LOG", logFile)

	prevFind := findQemuImgFn
	findQemuImgFn = func(string) string { return exe }
	defer func() { findQemuImgFn = prevFind }()

	before, after, usedQemuImg, err := CompactAVDDisk(context.Background(), avdHome, "", avdName)
	if err != nil {
		t.Fatalf("CompactAVDDisk failed: %v", err)
	}
	if before != 170 || !usedQemuImg || after != 4 {
		t.Fatalf("unexpected compact result: before=%d after=%d used=%v", before, after, usedQemuImg)
	}
	if _, err := os.Stat(filepath.Join(avdDir, "cache.img.qcow2")); !os.IsNotExist(err) {
		t.Fatal("cache.img.qcow2 should have been removed")
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read logFile: %v", err)
	}
	if !strings.Contains(string(logBytes), `-B C:\sdk\userdata.img`) {
		t.Fatalf("expected -B backing flag in qemu-img convert args, got: %s", string(logBytes))
	}

	// Verify size guard refuses replacement when output >= input.
	t.Setenv("VIRDECK_TEST_QEMU_BLOAT", "1")
	before2, after2, used2, err := CompactAVDDisk(context.Background(), avdHome, "", avdName)
	if err != nil {
		t.Fatalf("second CompactAVDDisk failed: %v", err)
	}
	if before2 != 4 || after2 != 4 || !used2 {
		t.Fatalf("size guard should keep 4B disk instead of bloated output: before=%d after=%d used=%v", before2, after2, used2)
	}
}

func TestRestoreAVDRejectsINIInjectionUnsafeQcow2BackingAndADS(t *testing.T) {
	avdHome := filepath.Join(t.TempDir(), "avd")
	writeZip := func(files map[string][]byte) string {
		p := filepath.Join(t.TempDir(), "evil.zip")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for k, v := range files {
			w, _ := zw.Create(k)
			_, _ = w.Write(v)
		}
		_ = zw.Close()
		if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// 1. INI injection via meta.SystemImage when avd.ini is omitted.
	iniInjZip := writeZip(map[string][]byte{
		"virdeck-backup.json": []byte(`{"version":1,"instanceName":"inj","systemImage":"system-images;android-34\npath=C:\\evil;google_apis;x86_64"}`),
	})
	if _, err := RestoreAVD(avdHome, "inj_avd", iniInjZip); err == nil {
		t.Fatal("expected INI injection via SystemImage to be rejected")
	}

	// 2. NTFS ADS ':' in entry path.
	adsZip := writeZip(map[string][]byte{
		"virdeck-backup.json": []byte(`{"version":1,"instanceName":"ads"}`),
		"avd/file.txt:evil":   []byte("ads"),
	})
	if _, err := RestoreAVD(avdHome, "ads_avd", adsZip); err == nil {
		t.Fatal("expected NTFS ADS entry to be rejected")
	}

	// 3. Malicious qcow2 backing-filename pointing to host file.
	qcow2 := make([]byte, 128)
	copy(qcow2[0:4], []byte{'Q', 'F', 'I', 0xfb})
	binary.BigEndian.PutUint32(qcow2[4:8], 3)   // version
	binary.BigEndian.PutUint64(qcow2[8:16], 72) // backing_file_offset
	backing := []byte("/etc/passwd")
	binary.BigEndian.PutUint32(qcow2[16:20], uint32(len(backing)))
	copy(qcow2[72:], backing)
	qcow2Zip := writeZip(map[string][]byte{
		"virdeck-backup.json":           []byte(`{"version":1,"instanceName":"qcow"}`),
		"avd/userdata-qemu.img.qcow2":   qcow2,
	})
	if _, err := RestoreAVD(avdHome, "qcow_avd", qcow2Zip); err == nil {
		t.Fatal("expected malicious qcow2 backing-filename to be rejected")
	}

	// 4. Malicious config.ini .path key.
	cfgZip := writeZip(map[string][]byte{
		"virdeck-backup.json": []byte(`{"version":1,"instanceName":"cfg"}`),
		"avd/config.ini":      []byte("hw.sdCard.path=/etc/shadow\n"),
	})
	if _, err := RestoreAVD(avdHome, "cfg_avd", cfgZip); err == nil {
		t.Fatal("expected malicious config.ini host path to be rejected")
	}
}
