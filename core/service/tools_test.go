package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"virdeck/core/emu"
	"virdeck/core/instance"
)

type p3bFakeADB struct {
	mu       sync.Mutex
	port     int
	lon, lat float64
	pkgs     []string
	stopped  []string
	cleared  []string
	uninst   []string
	trimmed  bool
}

func (f *p3bFakeADB) SetPath(string)                                        {}
func (f *p3bFakeADB) WaitForBoot(context.Context, int, time.Duration) error { return nil }
func (f *p3bFakeADB) EmuKill(context.Context, int) error                    { return nil }
func (f *p3bFakeADB) BatchInstallAPK(context.Context, map[string]int, string) error {
	return nil
}
func (f *p3bFakeADB) BatchShell(context.Context, map[string]int, string) (map[string]string, error) {
	return nil, nil
}
func (f *p3bFakeADB) DiscoverEmulators(context.Context) map[string]int { return nil }
func (f *p3bFakeADB) HasEmulators(context.Context) (bool, error)       { return false, nil }

func (f *p3bFakeADB) SetLocation(_ context.Context, port int, lon, lat float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.port, f.lon, f.lat = port, lon, lat
	return nil
}

func (f *p3bFakeADB) Screenshot(context.Context, int) ([]byte, error) {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3, 4}, nil
}

func (f *p3bFakeADB) ListPackages(_ context.Context, _ int, _ bool) ([]string, error) {
	return append([]string(nil), f.pkgs...), nil
}

func (f *p3bFakeADB) ForceStopPackage(_ context.Context, _ int, pkg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, pkg)
	return nil
}

func (f *p3bFakeADB) ClearPackageData(_ context.Context, _ int, pkg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, pkg)
	return nil
}

func (f *p3bFakeADB) UninstallPackage(_ context.Context, _ int, pkg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uninst = append(f.uninst, pkg)
	return nil
}

func (f *p3bFakeADB) TrimStorage(context.Context, int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trimmed = true
	return "Trimmed", nil
}

func (f *p3bFakeADB) ResolveLauncherActivity(context.Context, int, string) (string, error) {
	return "com.demo/.Main", nil
}

func (f *p3bFakeADB) LaunchActivity(context.Context, int, string) error { return nil }

func TestP3BToolsAndBackupDiskRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	avdHome := filepath.Join(dataDir, "avd")
	regPath := filepath.Join(dataDir, "config", "instances.json")
	mgr, err := NewManager(Config{
		DataDir:      dataDir,
		RegistryPath: regPath,
		AVDHome:      avdHome,
		SkipTCPCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fakeADB := &p3bFakeADB{pkgs: []string{"com.demo", "org.other"}}
	mgr.adb = fakeADB

	ctx := context.Background()
	runInst := Instance{
		ID: "run1", AVDName: "avd_run1", Name: "运行中设备", State: Running, Port: 5554,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64", MemoryMB: 2048, Cores: 2,
	}
	stopInst := Instance{
		ID: "stop1", AVDName: "avd_stop1", Name: "离线设备", State: Stopped,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64", MemoryMB: 4096, Cores: 4,
		Resolution: "1080p", Note: "备份主号",
	}
	if err := mgr.registry.Add(runInst); err != nil {
		t.Fatal(err)
	}
	if err := mgr.registry.Add(stopInst); err != nil {
		t.Fatal(err)
	}

	// Prepare offline AVD dir.
	stopDir := filepath.Join(avdHome, "avd_stop1.avd")
	if err := os.MkdirAll(stopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stopDir, "userdata-qemu.img.qcow2"), []byte("userdisk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stopDir, "cache.img.qcow2"), []byte("cachejunk"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Virtual GPS.
	if err := mgr.SetLocation(ctx, []string{"run1"}, 39.9042, 116.4074); err != nil {
		t.Fatalf("SetLocation failed: %v", err)
	}
	if fakeADB.port != 5554 || fakeADB.lat != 39.9042 || fakeADB.lon != 116.4074 {
		t.Fatalf("unexpected geo call: %+v", fakeADB)
	}

	// 2. Headless screenshots.
	shots, err := mgr.CaptureScreenshots(ctx, []string{"run1"}, "")
	if err != nil {
		t.Fatalf("CaptureScreenshots failed: %v", err)
	}
	if !strings.HasSuffix(shots["run1"], ".png") {
		t.Fatalf("expected png path, got %v", shots)
	}
	if _, err := os.Stat(shots["run1"]); err != nil {
		t.Fatalf("missing saved screenshot file: %v", err)
	}

	// 3. App manager.
	apps, err := mgr.ListInstalledApps(ctx, "run1", true)
	if err != nil || len(apps) != 2 {
		t.Fatalf("ListInstalledApps failed: apps=%+v err=%v", apps, err)
	}
	for _, op := range []string{"launch", "stop", "clear", "uninstall"} {
		if err := mgr.ManageInstalledApp(ctx, "run1", "com.demo", op); err != nil {
			t.Fatalf("ManageInstalledApp(%s) failed: %v", op, err)
		}
	}
	if len(fakeADB.stopped) != 1 || len(fakeADB.cleared) != 1 || len(fakeADB.uninst) != 1 {
		t.Fatalf("missing app manage calls: stopped=%v cleared=%v uninst=%v", fakeADB.stopped, fakeADB.cleared, fakeADB.uninst)
	}

	// 4. Online disk clean.
	resOnline, err := mgr.CleanInstanceDisk(ctx, "run1")
	if err != nil || resOnline.Mode != "online_fstrim" || !fakeADB.trimmed {
		t.Fatalf("online CleanInstanceDisk failed: %+v err=%v", resOnline, err)
	}

	// 5. Offline disk clean + backup + restore.
	resOffline, err := mgr.CleanInstanceDisk(ctx, "stop1")
	if err != nil || resOffline.Mode != "offline_compact" || resOffline.ReclaimedBytes <= 0 {
		t.Fatalf("offline CleanInstanceDisk failed: %+v err=%v", resOffline, err)
	}
	if _, err := mgr.BackupInstance(ctx, "run1", ""); err == nil {
		t.Fatal("expected error backing up running instance")
	}
	bk, err := mgr.BackupInstance(ctx, "stop1", "")
	if err != nil || bk.SizeBytes <= 0 {
		t.Fatalf("BackupInstance failed: %+v err=%v", bk, err)
	}
	restored, err := mgr.RestoreInstance(ctx, bk.Path, "")
	if err != nil {
		t.Fatalf("RestoreInstance failed: %v", err)
	}
	if restored.State != instance.Stopped || restored.Note != "备份主号" || restored.Cores != 4 || restored.ID == "stop1" {
		t.Fatalf("unexpected restored instance: %+v", restored)
	}
	if _, err := os.Stat(filepath.Join(avdHome, restored.AVDName+".avd", "userdata-qemu.img.qcow2")); err != nil {
		t.Fatalf("restored disk missing: %v", err)
	}
	_ = emu.AVDBackupMeta{}
}

func TestRestoreInstanceSanitizesUntrustedBackupMetadata(t *testing.T) {
	dataDir := t.TempDir()
	avdHome := filepath.Join(dataDir, "avd")
	mgr, err := NewManager(Config{
		DataDir:      dataDir,
		RegistryPath: filepath.Join(dataDir, "config", "instances.json"),
		AVDHome:      avdHome,
		SkipTCPCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a backup zip with extreme/malicious metadata values (control chars in name, bogus gpuMode, out-of-range RAM/cores).
	srcDir := filepath.Join(avdHome, "src.avd")
	if err := os.MkdirAll(srcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "userdata-qemu.img.qcow2"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	badZip := filepath.Join(t.TempDir(), "untrusted.zip")
	if _, err := emu.BackupAVD(avdHome, "src", badZip, emu.AVDBackupMeta{
		Version:      1,
		InstanceName: "bad\x00\nname",
		SystemImage:  "system-images;android-34;google_apis_playstore;x86_64",
		MemoryMB:     9999999,
		Cores:        999,
		StorageGB:    9999,
		Resolution:   "evil_res",
		GPUMode:      "--evil-gpu",
		Proxy:        "-evil-flag",
	}); err != nil {
		t.Fatal(err)
	}

	inst, err := mgr.RestoreInstance(context.Background(), badZip, "")
	if err != nil {
		t.Fatalf("RestoreInstance failed: %v", err)
	}
	if strings.ContainsAny(inst.Name, "\x00\n\r") {
		t.Fatalf("control chars leaked into restored Name: %q", inst.Name)
	}
	if inst.MemoryMB != 16384 || inst.Cores != 8 || inst.StorageGB != 128 {
		t.Fatalf("expected clamped resources, got mem=%d cores=%d disk=%d", inst.MemoryMB, inst.Cores, inst.StorageGB)
	}
	if inst.Resolution != "720p" || inst.GPUMode != "" || inst.Proxy != "" {
		t.Fatalf("expected sanitized res/gpu/proxy, got res=%q gpu=%q proxy=%q", inst.Resolution, inst.GPUMode, inst.Proxy)
	}
}

func TestBackupAndRestoreProxyOptInPolicy(t *testing.T) {
	dataDir := t.TempDir()
	avdHome := filepath.Join(dataDir, "avd")
	mgr, err := NewManager(Config{
		DataDir:      dataDir,
		RegistryPath: filepath.Join(dataDir, "config", "instances.json"),
		AVDHome:      avdHome,
		SkipTCPCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	inst := Instance{
		ID: "prox1", AVDName: "avd_prox1", Name: "代理机", State: Stopped,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
		MemoryMB: 2048, Cores: 2, Proxy: "http://user:secret@127.0.0.1:7890",
	}
	if err := mgr.registry.Add(inst); err != nil {
		t.Fatal(err)
	}
	avdDir := filepath.Join(avdHome, "avd_prox1.avd")
	if err := os.MkdirAll(avdDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(avdDir, "userdata-qemu.img.qcow2"), []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Default BackupInstance must strip Proxy from exported metadata.
	defaultZip := filepath.Join(t.TempDir(), "default.zip")
	if _, err := mgr.BackupInstance(ctx, "prox1", defaultZip); err != nil {
		t.Fatal(err)
	}
	restoredEvenIfOptedIn, err := mgr.RestoreInstanceWithOptions(ctx, defaultZip, "def_restore", RestoreOptions{IncludeProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	if restoredEvenIfOptedIn.Proxy != "" {
		t.Fatalf("default backup must not export proxy, got %q", restoredEvenIfOptedIn.Proxy)
	}

	// Opt-in BackupInstanceWithOptions exports Proxy, and default RestoreInstance still drops it.
	withProxyZip := filepath.Join(t.TempDir(), "withproxy.zip")
	if _, err := mgr.BackupInstanceWithOptions(ctx, "prox1", withProxyZip, BackupOptions{IncludeProxy: true}); err != nil {
		t.Fatal(err)
	}
	restoredDefault, err := mgr.RestoreInstance(ctx, withProxyZip, "no_proxy_restore")
	if err != nil {
		t.Fatal(err)
	}
	if restoredDefault.Proxy != "" {
		t.Fatalf("default restore must ignore backup proxy, got %q", restoredDefault.Proxy)
	}

	// Opt-in RestoreInstanceWithOptions keeps valid Proxy.
	restoredWithProxy, err := mgr.RestoreInstanceWithOptions(ctx, withProxyZip, "with_proxy_restore", RestoreOptions{IncludeProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	if restoredWithProxy.Proxy != "http://user:secret@127.0.0.1:7890" {
		t.Fatalf("expected restored proxy with IncludeProxy=true, got %q", restoredWithProxy.Proxy)
	}

	// Malformed Proxy in archive is still rejected even with IncludeProxy=true.
	badZip := filepath.Join(t.TempDir(), "badproxy.zip")
	if _, err := emu.BackupAVD(avdHome, "avd_prox1", badZip, emu.AVDBackupMeta{
		Version:      1,
		InstanceName: "bad",
		SystemImage:  "system-images;android-34;google_apis_playstore;x86_64",
		Proxy:        "-evil-flag",
	}); err != nil {
		t.Fatal(err)
	}
	badRestored, err := mgr.RestoreInstanceWithOptions(ctx, badZip, "bad_proxy_restore", RestoreOptions{IncludeProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	if badRestored.Proxy != "" {
		t.Fatalf("expected invalid proxy stripped even with IncludeProxy=true, got %q", badRestored.Proxy)
	}
}

type strictLockADB struct {
	*fakeADB
	active  map[string]int
	unknown int
	err     error
	entered chan struct{}
	release chan struct{}
}

func (a *strictLockADB) DiscoverEmulatorsStrict(context.Context) (map[string]int, int, error) {
	if a.entered != nil {
		select {
		case a.entered <- struct{}{}:
		default:
		}
	}
	if a.release != nil {
		<-a.release
	}
	return a.active, a.unknown, a.err
}

type lockCleanFakeEmu struct {
	*fakeEmu
	cleanCalls int
}

func (e *lockCleanFakeEmu) CleanStaleLocks(string) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleanCalls++
	return []string{"multiinstance.lock"}, nil
}

func TestStrictLockCleanupAndLifecycleMutualExclusion(t *testing.T) {
	// (a) ADB error or unknown emulator must prevent CleanStaleLocks.
	m, fe, fa := newTestManager(t, 2, 200*time.Millisecond)
	close(fa.bootReady)
	le := &lockCleanFakeEmu{fakeEmu: fe}
	m.emu = le
	sa := &strictLockADB{fakeADB: fa, unknown: 1}
	m.adb = sa

	ctx := context.Background()
	created, err := m.Create(ctx, CreateSpec{
		NamePrefix:  "lockguard",
		Count:       1,
		SystemImage: "system-images;android-34;google_apis_playstore;x86_64",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0].ID

	if err := m.Start(ctx, id); err != nil {
		t.Fatalf("Start with unknown emulator failed: %v", err)
	}
	if le.cleanCalls != 0 {
		t.Fatalf("expected 0 lock cleanups when unknownCount>0, got %d", le.cleanCalls)
	}
	_ = m.Stop(ctx, id)

	sa.unknown = 0
	sa.err = context.DeadlineExceeded
	if err := m.Start(ctx, id); err != nil {
		t.Fatalf("Start with ADB error failed: %v", err)
	}
	if le.cleanCalls != 0 {
		t.Fatalf("expected 0 lock cleanups when ADB errors, got %d", le.cleanCalls)
	}
	_ = m.Stop(ctx, id)

	// (b) Concurrent Start + Backup / CleanInstanceDisk on the same instance must be mutually exclusive.
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	sa.err = nil
	sa.entered = entered
	sa.release = release

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- m.Start(ctx, id)
	}()
	<-entered

	if _, err := m.BackupInstance(ctx, id, ""); err == nil || !strings.Contains(err.Error(), "启动") {
		t.Fatalf("expected BackupInstance blocked while Start holds instanceOps, got err=%v", err)
	}
	if _, err := m.CleanInstanceDisk(ctx, id); err == nil || !strings.Contains(err.Error(), "启动") {
		t.Fatalf("expected CleanInstanceDisk blocked while Start holds instanceOps, got err=%v", err)
	}
	close(release)
	if err := <-startErrCh; err != nil {
		t.Fatalf("concurrent Start failed: %v", err)
	}
	_ = m.Stop(ctx, id)
}

func TestStartRespectsMaxConcurrentAndCancelledContext(t *testing.T) {
	m, fe, fa := newTestManager(t, 1, 200*time.Millisecond)
	close(fa.bootReady)
	m.emu = &lockCleanFakeEmu{fakeEmu: fe}
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	sa := &strictLockADB{fakeADB: fa, entered: entered, release: release}
	m.adb = sa

	ctx := context.Background()
	c1, err := m.Create(ctx, CreateSpec{NamePrefix: "concA", Count: 1, SystemImage: "system-images;android-34;google_apis_playstore;x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := m.Create(ctx, CreateSpec{NamePrefix: "concB", Count: 1, SystemImage: "system-images;android-34;google_apis_playstore;x86_64"})
	if err != nil {
		t.Fatal(err)
	}

	// (c) Concurrent Start calls must strictly obey MaxConcurrent=1 via m.starting reservation.
	e1 := make(chan error, 1)
	e2 := make(chan error, 1)
	go func() { e1 <- m.Start(ctx, c1[0].ID) }()
	<-entered // First caller holds the single slot inside startOne.
	go func() { e2 <- m.Start(ctx, c2[0].ID) }()

	err2 := <-e2
	if err2 == nil || !strings.Contains(err2.Error(), "上限") {
		t.Fatalf("expected second concurrent Start rejected by MaxConcurrent=1, got %v", err2)
	}
	close(release)
	if err1 := <-e1; err1 != nil {
		t.Fatalf("first Start failed: %v", err1)
	}
	_ = m.Stop(ctx, c1[0].ID)

	// (d) Canceled context before StartProcess must never launch and must release slot/port.
	sa.entered = make(chan struct{}, 2)
	sa.release = make(chan struct{})
	cancelCtx, cancel := context.WithCancel(context.Background())
	eCancel := make(chan error, 1)
	go func() { eCancel <- m.Start(cancelCtx, c2[0].ID) }()
	<-sa.entered
	cancel()
	close(sa.release)
	if err := <-eCancel; err == nil {
		t.Fatal("expected canceled Start error")
	}
	fe.mu.Lock()
	_, launchedCanceled := fe.lastOpts[c2[0].AVDName]
	fe.mu.Unlock()
	if launchedCanceled {
		t.Fatal("canceled Start must not invoke StartProcess")
	}

	// Slot & port must be freed so subsequent Start succeeds cleanly.
	sa.entered = nil
	sa.release = nil
	if err := m.Start(ctx, c2[0].ID); err != nil {
		t.Fatalf("subsequent Start after cancel failed: %v", err)
	}
	_ = m.Stop(ctx, c2[0].ID)
}
